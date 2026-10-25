package authproxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"testing"
)

func FuzzUnissuedWorkerAuthorization(f *testing.F) {
	for _, seed := range []string{"", "Bearer arbitrary", "Bearer cdx-" + strings.Repeat("a", 64), "Bearer a, Bearer b", "Basic secret", "Bearer\t\tbad"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, header string) {
		if len(header) > 4096 {
			t.Skip()
		}
		p := newTestProxy(t)
		p.httpClient = &http.Client{Transport: privacyTransport(func(*http.Request) (*http.Response, error) {
			t.Fatal("unissued credential reached upstream")
			return nil, nil
		})}
		r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"fixture"}`))
		r.Header.Set("Authorization", header)
		r.Header.Set("X-Api-Key", header)
		w := httptest.NewRecorder()
		p.reverseProxy(w, r, "/v1", "https://upstream.invalid", func(http.Header) { t.Fatal("unissued credential injected") })
		if w.Code != 401 {
			t.Fatalf("unissued credential status %d", w.Code)
		}
	})
}
func FuzzModelAuthorization(f *testing.F) {
	for _, seed := range []string{`{"model":"allowed"}`, `{"model":"allowed","model":"forbidden"}`, `{"model":"allowed","Model":"forbidden"}`, `{"model":"forbidden"}`, `{"model":"allowed"} {}`, `{"model":null}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 128<<10 {
			t.Skip()
		}
		p := newTestProxy(t)
		p.cfg.AllowedModels = []string{"allowed"}
		r := httptest.NewRequest("POST", "/v1/responses", strings.NewReader(input))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		if p.authorizeModel(w, r) {
			body, err := io.ReadAll(r.Body)
			if err != nil || string(body) != input {
				t.Fatal("authorized body changed")
			}
			var parsed struct {
				Model string `json:"model"`
			}
			if json.Unmarshal([]byte(input), &parsed) != nil || parsed.Model != "allowed" {
				t.Fatal("model authorization without allowed model")
			}
		}
	})
}

func FuzzBrokerURLScope(f *testing.F) {
	for _, path := range []string{"/allowed/item", "/allowed/../outside", "/allowed/%2e%2e/outside", "/allowed//item", "/allowed\\outside", "//other.example/allowed", "/allowed/item?target=https://other.example"} {
		f.Add(path, "GET")
	}
	f.Fuzz(func(t *testing.T, suffix, method string) {
		if len(suffix) > 4096 || len(method) > 32 {
			t.Skip()
		}
		parsed, err := url.ParseRequestURI("/broker/service" + suffix)
		if err != nil {
			return
		}
		p := newTestProxy(t)
		p.cfg.CredentialRoutes = []CredentialRoute{{Name: "service", Upstream: "https://trusted.invalid", Paths: []string{"/allowed"}, Methods: []string{"GET"}, Header: "Authorization", Prefix: "Bearer ", Secret: func(context.Context) (string, error) { return "fixture-broker-secret", nil }}}
		p.httpClient = &http.Client{Transport: privacyTransport(func(r *http.Request) (*http.Response, error) {
			if r.Method != "GET" || r.URL.Scheme != "https" || r.URL.Host != "trusted.invalid" || r.URL.User != nil || (r.URL.Path != "/allowed" && !strings.HasPrefix(r.URL.Path, "/allowed/")) || path.Clean(r.URL.Path) != r.URL.Path {
				t.Fatal("credential scope widened")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
		})}
		token, err := p.IssueToken("fixture", 60)
		if err != nil {
			t.Fatal(err)
		}
		defer p.RevokeToken("fixture")
		r := httptest.NewRequest("GET", "/", nil)
		r.URL = parsed
		r.Method = method
		r.Header.Set("Authorization", "Bearer "+token)
		p.handleBroker(httptest.NewRecorder(), r)
	})
}
