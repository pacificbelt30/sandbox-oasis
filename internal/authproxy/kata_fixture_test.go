package authproxy

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/pacificbelt30/codex-dock/pkg/cxd/credentialstore"
)

// This entrypoint exists only in the opt-in test executable, never in cxd.
// The fixture image replaces /codex-dock with `go test -c ./internal/authproxy`.
// Production proxy handlers and credential-store calls remain unchanged; only
// fixed upstream endpoints are redirected to synthetic loopback HTTP servers.
func runKataFixtureProxy() int {
	if len(os.Args) < 3 || os.Args[1] != "proxy" || os.Args[2] != "serve" {
		return 2
	}
	f := flag.NewFlagSet("fixture-proxy", flag.ContinueOnError)
	var cfg Config
	f.BoolVar(&cfg.WorkerMTLS, "worker-mtls", false, "")
	cfg.EventWriter = os.Stdout
	var socket, role string
	var block bool
	f.StringVar(&cfg.ListenAddr, "listen", "", "")
	f.StringVar(&cfg.AdminListenAddr, "admin-listen", "", "")
	f.StringVar(&cfg.AdminSecret, "admin-secret", "", "")
	f.StringVar(&cfg.CaseID, "case-id", "", "")
	f.StringVar(&cfg.CredentialRoutesFile, "credential-routes", "", "")
	f.StringVar(&cfg.UsageStateDir, "usage-state-dir", "", "")
	f.StringVar(&socket, "credential-store-socket", "", "")
	f.StringVar(&role, "role", "auth", "")
	f.BoolVar(&block, "block-private", false, "")
	f.BoolVar(&cfg.ForwardPolicy.DefaultDeny, "forward-default-deny", true, "")
	f.Func("forward-allow-cidr", "", func(v string) error {
		cfg.ForwardPolicy.AllowCIDRs = append(cfg.ForwardPolicy.AllowCIDRs, v)
		return nil
	})
	f.Func("forward-allow-port", "", func(v string) error {
		n, err := strconv.Atoi(v)
		cfg.ForwardPolicy.AllowPorts = append(cfg.ForwardPolicy.AllowPorts, n)
		return err
	})
	f.Func("allow-model", "", func(v string) error { cfg.AllowedModels = append(cfg.AllowedModels, v); return nil })
	if err := f.Parse(os.Args[3:]); err != nil {
		return 2
	}
	if role == RoleEgress {
		cfg.Role, cfg.BlockPrivate = RoleEgress, block
		return runKataEgressFixture(cfg)
	}
	if socket != "" {
		store, err := credentialstore.NewUnixClient(socket)
		if err != nil {
			return 1
		}
		cfg.CredentialStore = store
	}
	connections, closeObserver, err := startKataConnectionObserver()
	if err != nil {
		return 1
	}
	defer closeObserver()
	p, err := NewProxy(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "fixture proxy initialization:", err)
		return 1
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isWebSocketRequest(r) {
			conn, rw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			defer connections.track()()
			_ = conn.SetDeadline(time.Now().Add(3 * time.Minute))
			_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
			_ = rw.Flush()
			_, _ = io.Copy(conn, rw)
			return
		}
		if r.URL.Path == "/oauth/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"fixture-access-rotated","refresh_token":"fixture-refresh-rotated"}`)
			return
		}
		if r.URL.Path == "/anthropic/oauth/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"fixture-anthropic-access","refresh_token":"fixture-anthropic-refresh-rotated","expires_in":3600}`)
			return
		}
		if r.URL.Path == "/v1/messages" || r.URL.Path == "/v1/messages/count_tokens" {
			serveClaudeFixture(w, r, p.IsAnthropicOAuthMode())
			return
		}
		var body struct {
			Stream bool            `json:"stream"`
			Input  json.RawMessage `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if string(body.Input) == `"usage-budget"` {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`)
			return
		}
		if body.Stream && len(body.Input) > 0 {
			if r.Method != http.MethodPost || (r.URL.Path != "/codex/responses" || r.Header.Get("Authorization") != "Bearer fixture-access-initial") && (r.URL.Path != "/v1/responses" || r.Header.Get("Authorization") != "Bearer sk-network-fixture-invalid") {
				http.Error(w, "fixture transport/auth mismatch", 400)
				return
			}
			item := map[string]any{"type": "message", "id": "msg_http", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "CXD_HTTP_SSE_OK", "annotations": []any{}}}}
			response := map[string]any{"id": "resp_http", "object": "response", "status": "completed", "output": []any{item}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}
			events := []map[string]any{{"type": "response.created", "response": map[string]any{"id": "resp_http", "object": "response", "status": "in_progress", "output": []any{}}}, {"type": "response.output_item.added", "output_index": 0, "item": item}, {"type": "response.output_item.done", "output_index": 0, "item": item}, {"type": "response.completed", "response": response}}
			w.Header().Set("Content-Type", "text/event-stream")
			for _, event := range events {
				encoded, _ := json.Marshal(event)
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], encoded)
				w.(http.Flusher).Flush()
			}
			return
		}
		if body.Stream {
			defer connections.track()()
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"fixture\":true}\n\n")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer up.Close()
	brokerUp := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-broker-secret" {
			http.Error(w, "credential missing", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, "BROKER_SCOPE_OK")
	}))
	defer brokerUp.Close()
	for i := range p.cfg.CredentialRoutes {
		if p.cfg.CredentialRoutes[i].Upstream == "https://broker.fixture" {
			p.cfg.CredentialRoutes[i].Upstream = brokerUp.URL
		}
	}
	p.httpClient.Transport = brokerUp.Client().Transport
	p.oauthTokenURL = up.URL + "/oauth/token"
	p.anthropicOAuthTokenURL = up.URL + "/anthropic/oauth/token"
	p.apiUpstreamURL, p.chatgptURL, p.anthropicUpstreamURL = up.URL+"/v1", up.URL, up.URL
	if err := p.Start(); err != nil {
		return 1
	}
	defer p.Stop()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	<-ctx.Done()
	return 0
}
