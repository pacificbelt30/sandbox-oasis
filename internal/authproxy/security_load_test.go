package authproxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConcurrentStreamsCloseOnRevokeWithoutNewUpstreamWork(t *testing.T) {
	var hits, active atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		active.Add(1)
		defer active.Add(-1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: start\n\n")
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	p := newTestProxy(t)
	p.apiUpstreamURL = upstream.URL
	p.httpClient = &http.Client{Transport: &http.Transport{MaxConnsPerHost: 96, MaxIdleConnsPerHost: 96}}
	defer p.httpClient.CloseIdleConnections()
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	token, err := p.IssueToken("burst-worker", 60)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ready := make(chan error, 96)
	var readers sync.WaitGroup
	for range 96 {
		readers.Go(func() {
			r, _ := http.NewRequestWithContext(ctx, "POST", p.Endpoint()+"/v1/responses", strings.NewReader(`{"stream":true}`))
			r.Header.Set("Authorization", "Bearer "+token)
			resp, err := http.DefaultClient.Do(r)
			if err != nil {
				ready <- err
				return
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != 200 {
				ready <- io.ErrUnexpectedEOF
				return
			}
			ready <- nil
			_, _ = io.Copy(io.Discard, resp.Body)
		})
	}
	for range 96 {
		select {
		case err := <-ready:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("stream burst did not become ready")
		}
	}
	p.RevokeToken("burst-worker")
	done := make(chan struct{})
	go func() { readers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("revoked streams remained open")
	}
	for attempt := 0; active.Load() != 0 && attempt < 100; attempt++ {
		time.Sleep(time.Millisecond * 10)
	}
	if active.Load() != 0 || hits.Load() != 96 {
		t.Fatalf("upstream leak: active=%d total=%d", active.Load(), hits.Load())
	}
	r, _ := http.NewRequestWithContext(ctx, "POST", p.Endpoint()+"/v1/responses", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 401 || hits.Load() != 96 {
		t.Fatal("revocation permitted new upstream work")
	}
}

func TestStoppedProxyCannotReissueOrLeakExpiryLoop(t *testing.T) {
	p := newTestProxy(t)
	token, err := p.IssueToken("worker", 60)
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() { p.expireLoop(); close(ended) }()
	p.Stop()
	p.Stop()
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("stopped proxy retained expiry goroutine")
	}
	if _, err = p.IssueToken("new", 60); err == nil {
		t.Fatal("stopped proxy reissued authority")
	}
	if err = p.RestoreToken("worker", token, 60); err == nil {
		t.Fatal("stopped proxy restored authority")
	}
	if err = p.Start(); err == nil {
		p.Stop()
		t.Fatal("stopped proxy restarted without new instance")
	}
}

func TestConcurrentIssueAndStopNeverLeavesLiveAuthority(t *testing.T) {
	p := newTestProxy(t)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 32; i++ {
		wg.Go(func() { <-start; _, _ = p.IssueToken("racing-worker", 60) })
	}
	wg.Go(func() { <-start; p.Stop() })
	close(start)
	wg.Wait()
	p.mu.RLock()
	count := len(p.tokensByValue)
	stopped := p.stopped
	p.mu.RUnlock()
	if count != 0 || !stopped {
		t.Fatal("issuance raced past stop")
	}
}

func TestAmbiguousAdminJSONCannotReplaceLiveAuthority(t *testing.T) {
	p := newTestProxy(t)
	token, err := p.IssueToken("victim", 60)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	for _, body := range []string{`{"container":"victim","container":"other","ttl":60}`, `{"container":"victim","Container":"other","ttl":60}`, `{"container":"victim","ttl":60} {}`, `{"container":"victim","ttl":60,"unknown":true}`, `{"container":"victim","ttl":9223372036854775807}`} {
		r := httptest.NewRequest("POST", "/admin/issue", strings.NewReader(body))
		w := httptest.NewRecorder()
		p.handleAdminIssue(w, r)
		if w.Code < 400 {
			t.Fatal("invalid admin JSON granted authority")
		}
		r = httptest.NewRequest("POST", "/v1/responses", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		_, done, ok := p.workerRequest(r, false)
		if !ok {
			t.Fatal("invalid request changed existing authority")
		}
		done()
	}
}
