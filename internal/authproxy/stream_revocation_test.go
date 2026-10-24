package authproxy

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkerSSELeaseEndsOnRevocationAndExpiry(t *testing.T) {
	for _, route := range []string{"/v1/responses", "/chatgpt/codex/responses", "/anthropic/v1/messages"} {
		for _, reason := range []string{"revoke", "expire", "replace", "restore", "stop"} {
			t.Run(route+"/"+reason, func(t *testing.T) {
				ended := make(chan struct{})
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: ready\n\n")
					w.(http.Flusher).Flush()
					<-r.Context().Done()
					close(ended)
				}))
				defer upstream.Close()
				p := newTestProxy(t)
				p.apiUpstreamURL, p.chatgptURL, p.anthropicUpstreamURL = upstream.URL, upstream.URL, upstream.URL
				p.anthropicAPIKey = "host-anthropic-fixture"
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
				req, _ := http.NewRequest(http.MethodPost, p.Endpoint()+route, strings.NewReader(`{"stream":true}`))
				req.Header.Set("Authorization", "Bearer "+token)
				client := &http.Client{Timeout: 5 * time.Second}
				response, err := client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				reader := bufio.NewReader(response.Body)
				if line, err := reader.ReadString('\n'); err != nil || response.StatusCode != 200 || line != "data: ready\n" {
					t.Fatalf("stream not established: status=%d line=%q err=%v", response.StatusCode, line, err)
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
				_, err = io.ReadAll(reader)
				if err != nil && strings.Contains(err.Error(), "Client.Timeout") {
					t.Fatalf("stream remained open until client timeout: %v", err)
				}
				select {
				case <-ended:
				case <-time.After(time.Second):
					t.Fatal("lease ended without closing upstream stream")
				}
			})
		}
	}
}
