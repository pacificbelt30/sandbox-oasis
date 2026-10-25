package authproxy

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/netquota"
)

func TestBandwidthWaitDoesNotDelayWorkerRevocation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "fixture-resource-key-never-valid")
	for _, secure := range []bool{false, true} {
		modes := []string{"revoke", "expire", "replace", "restore"}
		if secure {
			modes = append(modes, "certificate")
		}
		for _, mode := range modes {
			name := "plain/" + mode
			if secure {
				name = "tls/" + mode
			}
			t.Run(name, func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("X-Fixture-Stream") != "hold" {
						w.WriteHeader(http.StatusNoContent)
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: ready\n\n")
					w.(http.Flusher).Flush()
					_, _ = io.WriteString(w, strings.Repeat("x", 128<<10))
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}))
				defer upstream.Close()
				p, err := NewProxy(Config{TokenTTL: 60, WorkerMTLS: secure, AdminSecret: strings.Repeat("s", 32), AdminListenAddr: "127.0.0.1:0", ListenAddr: "127.0.0.1:0", NetworkResources: netquota.Limits{MaxConnections: 4, BytesPerSecond: 1024}})
				if err != nil {
					t.Fatal(err)
				}
				p.apiUpstreamURL = upstream.URL
				if err := p.Start(); err != nil {
					t.Fatal(err)
				}
				defer p.Stop()
				ttl := 60
				if mode == "expire" {
					ttl = 1
				}
				token, err := p.IssueToken("throttled-stream", ttl)
				if err != nil {
					t.Fatal(err)
				}
				other, err := p.IssueToken("other-worker", 60)
				if err != nil {
					t.Fatal(err)
				}
				client := func(token, source string, certificateTTL int) *http.Client {
					transport := &http.Transport{DialContext: (&net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(source)}}).DialContext}
					if secure {
						config, err := p.WorkerTransport(t.Context(), token, certificateTTL)
						if err != nil {
							t.Fatal(err)
						}
						config.Endpoint = p.Endpoint()
						transport.TLSClientConfig, err = config.TLSConfig()
						if err != nil {
							t.Fatal(err)
						}
					}
					t.Cleanup(transport.CloseIdleConnections)
					return &http.Client{Transport: transport, Timeout: 5 * time.Second}
				}
				certificateTTL := 30
				if mode == "certificate" {
					certificateTTL = 2
				}
				streamClient := client(token, "127.0.0.1", certificateTTL)
				otherClient := client(other, "127.0.0.2", 30)
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint()+"/v1/responses", strings.NewReader(`{"stream":true}`))
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set("Authorization", "Bearer "+token)
				request.Header.Set("X-Fixture-Stream", "hold")
				response, err := streamClient.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				reader := bufio.NewReader(response.Body)
				if line, err := reader.ReadString('\n'); err != nil || line != "data: ready\n" {
					t.Fatalf("stream not started: %q %v", line, err)
				}
				ended := make(chan struct{})
				go func() { _, _ = io.Copy(io.Discard, reader); close(ended) }()
				// Let the large response exhaust the initial burst and block its writer.
				time.Sleep(300 * time.Millisecond)
				switch mode {
				case "revoke":
					p.RevokeToken("throttled-stream")
				case "replace":
					_, err = p.IssueToken("throttled-stream", 60)
				case "restore":
					err = p.RestoreToken("throttled-stream", token, 60)
				}
				if err != nil {
					t.Fatal(err)
				}
				select {
				case <-ended:
				case <-time.After(3 * time.Second):
					t.Fatal("expired/revoked stream remains open while waiting for bandwidth")
				}
				// A separate execution/source remains usable, and normal completion keeps
				// its TCP connection available for the following request.
				for i := 0; i < 2; i++ {
					var reused atomic.Bool
					trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused.Store(info.Reused) }}
					req, err := http.NewRequestWithContext(httptrace.WithClientTrace(t.Context(), trace), http.MethodPost, p.Endpoint()+"/v1/responses", strings.NewReader(`{}`))
					if err != nil {
						t.Fatal(err)
					}
					req.Header.Set("Authorization", "Bearer "+other)
					resp, err := otherClient.Do(req)
					if err != nil {
						t.Fatal("other execution interrupted:", err)
					}
					_ = resp.Body.Close()
					if resp.StatusCode != http.StatusNoContent || i == 1 && !reused.Load() {
						t.Fatalf("valid connection not preserved: status=%d reused=%t", resp.StatusCode, reused.Load())
					}
				}
			})
		}
	}
}
