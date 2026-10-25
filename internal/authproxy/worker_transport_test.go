package authproxy

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/workertransport"
)

func TestWorkerMTLSRejectsCopiedTokensAndRetiredKeys(t *testing.T) {
	p, err := NewProxy(Config{WorkerMTLS: true, AdminSecret: strings.Repeat("a", 32), AdminListenAddr: "127.0.0.1:0", ListenAddr: "127.0.0.1:0", TokenTTL: 60})
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); w.WriteHeader(204) }))
	defer up.Close()
	p.apiUpstreamURL = up.URL
	if err = p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	token, err := p.IssueToken("first", 60)
	if err != nil {
		t.Fatal(err)
	}
	other, err := p.IssueToken("second", 60)
	if err != nil {
		t.Fatal(err)
	}
	first, err := p.WorkerTransport(context.Background(), token, 60)
	if err != nil {
		t.Fatal(err)
	}
	second, err := p.WorkerTransport(context.Background(), other, 60)
	if err != nil {
		t.Fatal(err)
	}
	client := func(config workertransport.Config) *http.Client {
		config.Endpoint = p.Endpoint()
		tc, e := config.TLSConfig()
		if e != nil {
			t.Fatal(e)
		}
		tr := &http.Transport{TLSClientConfig: tc}
		t.Cleanup(tr.CloseIdleConnections)
		return &http.Client{Transport: tr}
	}
	firstClient, secondClient := client(first), client(second)
	request := func(c *http.Client, credential string, want int) {
		t.Helper()
		r, _ := http.NewRequest("POST", p.Endpoint()+"/v1/responses", strings.NewReader(`{"model":"fixture"}`))
		r.Header.Set("Authorization", "Bearer "+credential)
		resp, e := c.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("status %d want %d", resp.StatusCode, want)
		}
	}
	request(firstClient, token, 204)
	request(secondClient, token, 401)
	// A live connection and the old certificate cannot reuse a restored token.
	p.RevokeToken("first")
	request(firstClient, token, 401)
	if err = p.RestoreToken("first", token, 60); err != nil {
		t.Fatal(err)
	}
	fresh, err := p.WorkerTransport(context.Background(), token, 60)
	if err != nil {
		t.Fatal(err)
	}
	request(firstClient, token, 401)
	request(client(fresh), token, 204)
	if hits.Load() != 2 {
		t.Fatalf("unexpected upstream hits %d", hits.Load())
	}
	// TLS without a client certificate fails before any HTTP authorization.
	first.Endpoint = p.Endpoint()
	tc, _ := first.TLSConfig()
	tc.Certificates = nil
	tr := &http.Transport{TLSClientConfig: tc}
	defer tr.CloseIdleConnections()
	resp, e := (&http.Client{Transport: tr}).Get(p.Endpoint() + "/health")
	if e == nil {
		_ = resp.Body.Close()
		t.Fatal("client certificate missing accepted")
	}
	// Plaintext cannot be used as a fallback on the secure listener.
	resp, e = http.Get(strings.Replace(p.Endpoint(), "https:", "http:", 1) + "/health")
	if e == nil {
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == 200 {
			t.Fatalf("plaintext accepted: %q", b)
		}
	}
	if p.transportCA.TLSConfig().MinVersion != tls.VersionTLS13 {
		t.Fatal("unexpected TLS minimum")
	}
}

func TestWorkerCertificateExpiryCancelsStreamBeforeTokenExpiry(t *testing.T) {
	p, err := NewProxy(Config{WorkerMTLS: true, AdminSecret: strings.Repeat("a", 32), AdminListenAddr: "127.0.0.1:0", ListenAddr: "127.0.0.1:0", TokenTTL: 60})
	if err != nil {
		t.Fatal(err)
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: ready\n\n")
		_ = http.NewResponseController(w).Flush()
		<-r.Context().Done()
	}))
	defer up.Close()
	p.apiUpstreamURL = up.URL
	if err = p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	token, err := p.IssueToken("short-cert", 60)
	if err != nil {
		t.Fatal(err)
	}
	config, err := p.WorkerTransport(context.Background(), token, 2)
	if err != nil {
		t.Fatal(err)
	}
	config.Endpoint = p.Endpoint()
	tc, err := config.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{TLSClientConfig: tc}
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", p.Endpoint()+"/v1/responses", strings.NewReader(`{"stream":true}`))
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := (&http.Client{Transport: transport}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.ReadAll(response.Body)
	if ctx.Err() != nil {
		t.Fatal("certificate expiry failed to cancel stream")
	}
	p.mu.RLock()
	record := p.tokensByValue[token]
	stillLive := record != nil && time.Now().Before(record.ExpiresAt)
	p.mu.RUnlock()
	if !stillLive {
		t.Fatal("test did not isolate certificate expiry from token expiry")
	}
}
