package authproxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func issuedTestToken(t *testing.T, p *Proxy) string {
	t.Helper()
	name := "fixture-" + t.Name()
	token, err := p.IssueToken(name, 60)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.RevokeToken(name) })
	return token
}

func TestRemoteProxyRejectsCachedLegacyBinary(t *testing.T) {
	for _, flag := range []any{nil, false, true} {
		t.Run(fmt.Sprint(flag), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mode := map[string]any{"oauth_mode": true}
				if flag != nil {
					mode["worker_token_auth"] = flag
				}
				_ = json.NewEncoder(w).Encode(mode)
			}))
			defer server.Close()
			_, err := NewRemoteProxy(server.URL, "http://auth:18080", "")
			if flag == true && err != nil {
				t.Fatal(err)
			}
			if flag != true && (err == nil || !strings.Contains(err.Error(), "rebuild")) {
				t.Fatal("legacy image did not fail closed with upgrade guidance")
			}
		})
	}
}

func authorizeTestRequest(t *testing.T, p *Proxy, r *http.Request) {
	t.Helper()
	token := issuedTestToken(t, p)
	if strings.HasPrefix(r.URL.Path, "/anthropic/") && r.Header.Get("X-Api-Key") != "" {
		r.Header.Set("X-Api-Key", token)
		if r.Header.Get("Authorization") != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
	} else {
		r.Header.Set("Authorization", "Bearer "+token)
	}
}

func TestWorkerAPIRoutesRejectInvalidCredentialsBeforeUpstream(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(200) }))
	defer upstream.Close()
	p := newTestProxy(t)
	p.apiUpstreamURL, p.chatgptURL, p.anthropicUpstreamURL = upstream.URL, upstream.URL, upstream.URL
	p.anthropicAPIKey = "host-anthropic-fixture"
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	valid := issuedTestToken(t, p)
	revoked, err := p.IssueToken("revoked", 60)
	if err != nil {
		t.Fatal(err)
	}
	p.RevokeToken("revoked")
	expired, err := p.IssueToken("expired", 60)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.tokens["expired"].ExpiresAt = time.Now().Add(-time.Second)
	p.mu.Unlock()
	for _, path := range []string{"/v1/models", "/chatgpt/codex/models", "/anthropic/v1/messages"} {
		for _, upgrade := range []bool{false, true} {
			for _, tc := range []struct {
				name      string
				auth, api []string
			}{
				{"missing", nil, nil},
				{"unissued", []string{"Bearer cdx-" + strings.Repeat("0", 64)}, nil},
				{"expired", []string{"Bearer " + expired}, nil},
				{"revoked", []string{"Bearer " + revoked}, nil},
				{"host-key", []string{"Bearer sk-test-key-12345"}, nil},
				{"duplicate-bearer", []string{"Bearer " + valid, "Bearer " + valid}, nil},
				{"basic", []string{"Basic " + valid}, nil},
				{"wrong-key", nil, []string{"invalid"}},
				{"duplicate-key", nil, []string{valid, valid}},
				{"conflicting", []string{"Bearer " + revoked}, []string{valid}},
			} {
				t.Run(fmt.Sprintf("%s/ws=%t/%s", path, upgrade, tc.name), func(t *testing.T) {
					r, err := http.NewRequest(http.MethodGet, p.Endpoint()+path, nil)
					if err != nil {
						t.Fatal(err)
					}
					for _, v := range tc.auth {
						r.Header.Add("Authorization", v)
					}
					for _, v := range tc.api {
						r.Header.Add("X-Api-Key", v)
					}
					if upgrade {
						r.Header.Set("Connection", "Upgrade")
						r.Header.Set("Upgrade", "websocket")
					}
					resp, err := http.DefaultClient.Do(r)
					if err != nil {
						t.Fatal(err)
					}
					defer resp.Body.Close()
					if resp.StatusCode != 401 {
						t.Fatalf("status=%d; expected unauthorized", resp.StatusCode)
					}
				})
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid requests reached upstream: %d", calls.Load())
	}
	for _, path := range []string{"/v1/models", "/chatgpt/codex/models", "/anthropic/v1/messages"} {
		r, _ := http.NewRequest(http.MethodGet, p.Endpoint()+path, nil)
		r.Header.Set("Authorization", "bEaReR "+valid)
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("valid token denied: %s", path)
		}
	}
	if err := p.RestoreToken("revoked", revoked, 60); err != nil {
		t.Fatal(err)
	}
	r, _ := http.NewRequest(http.MethodGet, p.Endpoint()+"/v1/models", nil)
	r.Header.Set("Authorization", "Bearer "+revoked)
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("restored token rejected")
	}
}

func TestWorkerWebSocketLeaseEndsOnRevocationAndExpiry(t *testing.T) {
	for _, reason := range []string{"revoke", "expire", "replace", "restore", "stop"} {
		t.Run(reason, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, b, err := w.(http.Hijacker).Hijack()
				if err != nil {
					return
				}
				defer c.Close()
				_, _ = b.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
				_ = b.Flush()
				_, _ = io.Copy(io.Discard, c)
			}))
			defer upstream.Close()
			p := newTestProxy(t)
			p.apiUpstreamURL = upstream.URL
			if err := p.Start(); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			ttl := 60
			if reason == "expire" {
				ttl = 1
			}
			token, err := p.IssueToken("worker", ttl)
			if err != nil {
				t.Fatal(err)
			}
			c, err := net.Dial("tcp", strings.TrimPrefix(p.Endpoint(), "http://"))
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(4 * time.Second))
			_, _ = fmt.Fprintf(c, "GET /v1/responses HTTP/1.1\r\nHost: proxy\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nAuthorization: Bearer %s\r\n\r\n", token)
			response, err := http.ReadResponse(bufio.NewReader(c), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != 101 {
				t.Fatalf("upgrade=%d", response.StatusCode)
			}
			switch reason {
			case "revoke":
				p.RevokeToken("worker")
			case "replace":
				_, err = p.IssueToken("worker", 60)
			case "restore":
				err = p.RestoreToken("worker", token, 60)
			case "stop":
				p.Stop()
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Read(make([]byte, 1))
			if err == nil {
				t.Fatal("unauthorized tunnel remained open")
			}
			if e, ok := err.(net.Error); ok && e.Timeout() {
				t.Fatal("tunnel stayed open after lease ended")
			}
		})
	}
}
