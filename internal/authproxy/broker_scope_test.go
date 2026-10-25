package authproxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrokerExactPathsPinOriginAndMethodBeforeReadingSecret(t *testing.T) {
	var hits, secrets atomic.Int32
	route := CredentialRoute{Name: "service", Upstream: "https://trusted.example", ExactPaths: []string{"/v1/read"}, Methods: []string{"GET"}, Header: "Authorization", Prefix: "Bearer ", Secret: func(context.Context) (string, error) { secrets.Add(1); return "fixture-host-secret", nil }}
	p, err := NewProxy(Config{BrokerOnly: true, CredentialRoutes: []CredentialRoute{route}})
	if err != nil {
		t.Fatal(err)
	}
	p.httpClient = &http.Client{Transport: privacyTransport(func(r *http.Request) (*http.Response, error) {
		hits.Add(1)
		if r.URL.Scheme != "https" || r.URL.Host != "trusted.example" || r.Host != "trusted.example" || r.URL.Path != "/v1/read" || r.Method != "GET" {
			t.Errorf("scope changed: %s %s host=%s", r.Method, r.URL, r.Host)
		}
		if r.Header.Get("Authorization") != "Bearer fixture-host-secret" || r.Header.Get("X-HTTP-Method-Override") != "" || r.Header.Get("X-Forwarded-Host") != "" {
			t.Error("credential/override headers not constrained")
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}
	token, err := p.IssueToken("worker", 60)
	if err != nil {
		t.Fatal(err)
	}
	// Both lists must be owned by the proxy after configuration.
	route.ExactPaths[0] = "/"
	route.Methods[0] = "POST"
	for _, tt := range []struct {
		path, method string
		status       int
	}{
		{"/v1/read", "GET", 200}, {"/v1/read?target=https://attacker.invalid/x", "GET", 200},
		{"/v1/read/child", "GET", 403}, {"/v1/read-more", "GET", 403}, {"/v1/read", "POST", 403},
		{"/v1/read/../outside", "GET", 403}, {"/v1/%72ead", "GET", 403}, {"/v1/read%2fchild", "GET", 403},
		{"/v1/read%00", "GET", 403}, {"/v1/read%3foutside", "GET", 403}, {"/v1/read%252fchild", "GET", 403},
	} {
		before := secrets.Load()
		r := httptest.NewRequest(tt.method, "/broker/service"+tt.path, nil)
		r.Host = "attacker.invalid"
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-HTTP-Method-Override", "DELETE")
		r.Header.Set("X-Forwarded-Host", "attacker.invalid")
		w := httptest.NewRecorder()
		p.handleBroker(w, r)
		if w.Code != tt.status {
			t.Errorf("%s %s: %d", tt.method, tt.path, w.Code)
		}
		if tt.status != 200 && secrets.Load() != before {
			t.Errorf("denied request read the secret: %s", tt.path)
		}
	}
	if hits.Load() != 2 || secrets.Load() != 2 {
		t.Fatalf("unexpected injection: hits=%d secrets=%d", hits.Load(), secrets.Load())
	}
}

func TestBrokerMixedScopesAndCanonicalConfiguration(t *testing.T) {
	base := CredentialRoute{Name: "service", Upstream: "https://trusted.example", Paths: []string{"/tree"}, ExactPaths: []string{"/single"}, Methods: []string{"GET"}, Header: "Authorization", Secret: func(context.Context) (string, error) { return "fixture", nil }}
	for _, origin := range []string{"https://trusted.example/?", "https://trusted.example/%2f", "https://trusted.example?", "https://trusted.example/#x"} {
		r := base
		r.Upstream = origin
		if err := ValidateCredentialRoutes([]CredentialRoute{r}); err == nil {
			t.Errorf("ambiguous origin accepted: %q", origin)
		}
	}
	for _, scope := range []string{"relative", "//other", "/x/../y", "/x/", "/x%2f", "/x?query", "/x#fragment", "/x\\y", "/x\x00", "/x\x7f", string([]byte{'/', 0xff})} {
		r := base
		r.ExactPaths = []string{scope}
		if err := ValidateCredentialRoutes([]CredentialRoute{r}); err == nil {
			t.Errorf("ambiguous exact path accepted: %q", scope)
		}
	}
	p, err := NewProxy(Config{BrokerOnly: true, CredentialRoutes: []CredentialRoute{base}})
	if err != nil {
		t.Fatal(err)
	}
	p.httpClient = &http.Client{Transport: privacyTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}
	token, err := p.IssueToken("worker", 60)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		path   string
		status int
	}{{"/tree", 200}, {"/tree/child", 200}, {"/tree-more", 403}, {"/single", 200}, {"/single/child", 403}, {"/tree/a%00", 403}, {"/tree/%2e%2e/outside", 403}, {"/tree/a%7f", 403}} {
		r := httptest.NewRequest("GET", "/broker/service"+tt.path, nil)
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		p.handleBroker(w, r)
		if w.Code != tt.status {
			t.Errorf("%s: %d", tt.path, w.Code)
		}
	}
}

func TestBrokerExactStreamRevocationCancelsUpstream(t *testing.T) {
	stopped := make(chan struct{})
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/events" || r.Header.Get("Authorization") != "Bearer fixture-host-secret" {
			http.Error(w, "bad fixture request", 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: ready\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(stopped)
	}))
	defer upstream.Close()
	p, err := NewProxy(Config{BrokerOnly: true, CredentialRoutes: []CredentialRoute{{Name: "service", Upstream: upstream.URL, ExactPaths: []string{"/events"}, Methods: []string{"GET"}, Header: "Authorization", Prefix: "Bearer ", Secret: func(context.Context) (string, error) { return "fixture-host-secret", nil }}}})
	if err != nil {
		t.Fatal(err)
	}
	p.httpClient = upstream.Client()
	proxy := httptest.NewServer(http.HandlerFunc(p.handleBroker))
	defer proxy.Close()
	token, err := p.IssueToken("worker", 60)
	if err != nil {
		t.Fatal(err)
	}
	defer p.RevokeToken("worker")
	req, err := http.NewRequest("GET", proxy.URL+"/broker/service/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("stream denied", resp.StatusCode)
	}
	first := make([]byte, len("data: ready\n\n"))
	if _, err = io.ReadFull(resp.Body, first); err != nil {
		t.Fatal(err)
	}
	p.RevokeToken("worker")
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("revoked broker stream kept upstream alive")
	}
}
