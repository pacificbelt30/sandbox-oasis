package authproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

func meteredProxy(t *testing.T) *Proxy {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	p, err := NewProxy(Config{UsageStateDir: dir, TokenTTL: 3600, AdminSecret: strings.Repeat("s", 32), AdminListenAddr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	p.apiKey = "fake-upstream-key"
	t.Cleanup(p.Stop)
	return p
}

func meteredRequest(t *testing.T, p *Proxy, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "http://proxy/v1/responses", strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	p.handleAPIProxy(w, r)
	return w
}

func TestUsageCapsRejectBeforeUpstreamAndSurviveReissue(t *testing.T) {
	p := meteredProxy(t)
	var hits atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "Bearer fake-upstream-key" {
			t.Error("credential injection changed")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`)
	}))
	defer up.Close()
	p.apiUpstreamURL = up.URL
	limits := &identity.UsagePolicy{MaxRequests: 2, MaxConcurrent: 1, MaxBytes: 10000}
	token, err := p.IssueTokenWithUsage("worker", 3600, identity.Identity{}, nil, limits)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if w := meteredRequest(t, p, token, `{}`); w.Code != 200 {
			t.Fatalf("valid request denied: %d", w.Code)
		}
	}
	if w := meteredRequest(t, p, token, `{}`); w.Code != 429 || hits.Load() != 2 {
		t.Fatalf("quota reached upstream: status=%d hits=%d", w.Code, hits.Load())
	}
	// Even an older admin caller omitting limits inherits the durable policy.
	token, err = p.IssueToken("worker", 3600)
	if err != nil {
		t.Fatal(err)
	}
	if w := meteredRequest(t, p, token, `{}`); w.Code != 429 || hits.Load() != 2 {
		t.Fatal("reissue reset the request counter")
	}
	s, err := p.Usage("worker")
	if err != nil || s.Requests != 2 || s.Active != 0 || s.Tokens != 10 || s.UnmeteredRequests != 0 {
		t.Fatalf("bad counters: %+v %v", s, err)
	}
	var report bytes.Buffer
	if err := json.NewEncoder(&report).Encode(s); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(report.String(), token) || strings.Contains(report.String(), "fake-upstream-key") {
		t.Fatal("usage report leaked credentials")
	}
}

func TestObservedTokenBudgetAndMissingUsageFailClosed(t *testing.T) {
	for _, body := range []string{`{"usage":{"total_tokens":12}}`, `{"no_usage":true}`} {
		p := meteredProxy(t)
		var hits atomic.Int64
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, body)
		}))
		p.apiUpstreamURL = up.URL
		token, err := p.IssueTokenWithUsage("worker", 3600, identity.Identity{}, nil, &identity.UsagePolicy{MaxTokens: 10})
		if err != nil {
			t.Fatal(err)
		}
		if w := meteredRequest(t, p, token, `{}`); w.Code != 200 {
			t.Fatal("initial inference was not forwarded")
		}
		w := meteredRequest(t, p, token, `{}`)
		if hits.Load() != 1 || w.Code != 429 && w.Code != 503 {
			t.Fatalf("missing/exhausted observed usage was ignored: %d hits=%d", w.Code, hits.Load())
		}
		up.Close()
	}
}

func TestUsageByteLimitNeverWritesBeyondCombinedBudget(t *testing.T) {
	p := meteredProxy(t)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(w, strings.Repeat("x", 100))
	}))
	defer up.Close()
	p.apiUpstreamURL = up.URL
	token, err := p.IssueTokenWithUsage("worker", 3600, identity.Identity{}, nil, &identity.UsagePolicy{MaxBytes: 20})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	func() {
		defer func() {
			if recovered := recover(); recovered != nil && recovered != http.ErrAbortHandler {
				panic(recovered)
			}
		}()
		r := httptest.NewRequest("POST", "http://proxy/v1/responses", strings.NewReader("12345"))
		r.Header.Set("Authorization", "Bearer "+token)
		p.handleAPIProxy(w, r)
	}()
	s, err := p.Usage("worker")
	if err != nil || s.Bytes != 20 || w.Body.Len() > 15 {
		t.Fatalf("combined body limit exceeded: %+v forwarded=%d err=%v", s, w.Body.Len(), err)
	}
}

func TestUsagePreparationDoesNotActivateTokensAndMissingHistoryRejectsRestore(t *testing.T) {
	p := meteredProxy(t)
	limits := &identity.UsagePolicy{MaxRequests: 2}
	if err := p.PrepareUsage("worker", identity.Identity{}, limits); err != nil {
		t.Fatal(err)
	}
	if len(p.tokens) != 0 {
		t.Fatal("preparation activated credentials before Kata validation")
	}
	token, err := generateToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.RestoreTokenWithUsage("worker", token, 60, identity.Identity{}, nil, limits); err != nil {
		t.Fatal(err)
	}
	p.RevokeToken("worker")
	if err := p.RestoreTokenWithUsage("another", token, 60, identity.Identity{}, nil, limits); err == nil {
		t.Fatal("missing history reset an existing execution")
	}
	if _, err := p.UsageContext(context.Background(), "worker"); err != nil {
		t.Fatal(err)
	}
}
