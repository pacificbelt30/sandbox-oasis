package authproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pacificbelt30/codex-dock/pkg/cxd/credentialstore"
)

// Optional official-CLI acceptance: real entrypoint, proxy listeners and local
// OAuth/Responses fixtures. No host auth files, API keys or inference are used.
func TestCodexCLIWorkerCredentialPrivacy(t *testing.T) {
	binary := os.Getenv("CXD_TEST_CODEX_BINARY")
	if binary == "" {
		t.Skip("set CXD_TEST_CODEX_BINARY to the worker image's pinned official CLI")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, refresh := range []bool{false, true} {
		t.Run(fmt.Sprintf("refresh=%t", refresh), func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, ".codex"), 0700); err != nil {
				t.Fatal(err)
			}
			env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "CODEX_HOME=" + filepath.Join(root, ".codex"),
				"XDG_CONFIG_HOME=" + root, "NO_PROXY=127.0.0.1,localhost", "no_proxy=127.0.0.1,localhost", "OTEL_SDK_DISABLED=true"}
			run := func(name string, args ...string) []byte {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, name, args...)
				command.Dir, command.Env = root, env
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("official Codex CLI: %v\n%s", err, output)
				}
				return output
			}
			if version := string(run(binary, "--version")); !strings.HasSuffix(strings.TrimSpace(version), "codex-cli "+pinnedAgentVersion(t, "CODEX_CLI_VERSION")) {
				t.Fatalf("test requires the worker CLI pin, got %q", version)
			}
			id := privacyIDToken(t)
			var refreshed, completed atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/oauth/token" {
					refreshed.Add(1)
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["refresh_token"] != "rt-secret-stays-on-host" {
						t.Error("refresh did not use the host refresh token")
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "private-rotated-access", "refresh_token": "private-rotated-refresh", "id_token": id, "future_token": "private-future-token"})
					return
				}
				if r.Method != http.MethodPost || r.URL.Path != "/codex/responses" {
					_ = json.NewEncoder(w).Encode(map[string]any{"models": []any{}})
					return
				}
				wantAccess := "private-initial-access"
				if refresh {
					wantAccess = "private-rotated-access"
				}
				if r.Header.Get("Authorization") != "Bearer "+wantAccess || r.Header.Get("ChatGPT-Account-Id") != "acct-privacy" {
					t.Error("upstream did not receive the host access token/account ID")
					http.Error(w, "credential mismatch", http.StatusUnauthorized)
					return
				}
				completed.Add(1)
				item := map[string]any{"type": "message", "id": "msg_private", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "CXD_PRIVATE_ID_OK", "annotations": []any{}}}}
				response := map[string]any{"id": "resp_private", "object": "response", "status": "completed", "output": []any{item}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}
				events := []map[string]any{{"type": "response.created", "response": map[string]any{"id": "resp_private", "status": "in_progress", "output": []any{}}}, {"type": "response.output_item.added", "output_index": 0, "item": item}, {"type": "response.output_item.done", "output_index": 0, "item": item}, {"type": "response.completed", "response": response}}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, event := range events {
					encoded, _ := json.Marshal(event)
					_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], encoded)
					w.(http.Flusher).Flush()
				}
			}))
			defer up.Close()
			credentials := &credentialstore.OpenAIOAuth{IDToken: id, AccountID: "acct-privacy", AccessToken: "private-initial-access", RefreshToken: "rt-secret-stays-on-host", LastRefresh: time.Now().UTC().Format(time.RFC3339)}
			if refresh {
				credentials.LastRefresh = "2000-01-01T00:00:00Z"
			}
			store, err := credentialstore.NewFileStore(filepath.Join(t.TempDir(), "profile"))
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Create(context.Background(), "privacy", credentialstore.Credentials{OpenAIOAuth: credentials}); err != nil {
				t.Fatal(err)
			}
			config := Config{CredentialStore: store, ProfileName: "privacy", AdminSecret: strings.Repeat("a", 32), AdminListenAddr: "127.0.0.1:0"}
			p, err := NewProxy(config)
			if err != nil {
				t.Fatal(err)
			}
			p.oauthTokenURL, p.chatgptURL = up.URL+"/oauth/token", up.URL
			if err := p.Start(); err != nil {
				t.Fatal(err)
			}
			defer p.Stop()
			token, err := p.IssueToken("codex-privacy", 120)
			if err != nil {
				t.Fatal(err)
			}
			// Capture verbose proxy logs too, so response/file checks include them.
			logfile, err := os.Create(filepath.Join(root, "proxy.log"))
			if err != nil {
				t.Fatal(err)
			}
			stdout := os.Stdout
			os.Stdout, p.cfg.Verbose = logfile, true
			defer func() { os.Stdout = stdout; _ = logfile.Close() }()
			source, err := os.ReadFile("../../docker/sandbox/entrypoint.sh")
			if err != nil {
				t.Fatal(err)
			}
			entrypoint := filepath.Join(root, "entrypoint.sh")
			if err := os.WriteFile(entrypoint, []byte(strings.Replace(string(source), "cd /workspace", `cd "${HOME}"`, 1)), 0700); err != nil {
				t.Fatal(err)
			}
			env = append(env, "CODEX_TOKEN="+token, "CODEX_AUTH_PROXY_URL="+p.Endpoint(), "CODEX_DOCK_CODEX_HTTP=1", "DOCK_EXEC_CMD=true",
				"CODEX_REFRESH_TOKEN_URL_OVERRIDE="+p.Endpoint()+"/oauth/token?cdx="+token)
			var output bytes.Buffer
			output.Write(run("bash", entrypoint))
			output.Write(run(binary, "login", "status"))
			provider := fmt.Sprintf(`model_providers.privacy={name="privacy",base_url=%q,wire_api="responses",requires_openai_auth=true,supports_websockets=false}`, p.Endpoint()+"/v1")
			output.Write(run(binary, "exec", "--skip-git-repo-check", "--ephemeral", "--json", "--model", "gpt-5.4-mini", "-c", `model_provider="privacy"`, "-c", provider, "Reply with the fixture marker."))
			if !strings.Contains(output.String(), "CXD_PRIVATE_ID_OK") || completed.Load() == 0 || (refresh && refreshed.Load() == 0) || (!refresh && refreshed.Load() != 0) {
				t.Fatalf("missing expected login/refresh/Responses completion: %s", output.String())
			}
			secrets := []string{id, "private-original-signature", "private-initial-access", "private-rotated-access", "private-rotated-refresh", "rt-secret-stays-on-host", "private-future-token", "private-email", "private-nonce", "private-user"}
			assertPrivateTokensAbsent(t, output.String(), secrets...)
			if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
				if err != nil || entry.IsDir() {
					return err
				}
				data, err := os.ReadFile(path)
				if err == nil {
					assertPrivateTokensAbsent(t, string(data), secrets...)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(root, ".codex/auth.json"))
			if err != nil {
				t.Fatal(err)
			}
			var saved map[string]json.RawMessage
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			var tokens map[string]string
			if err := json.Unmarshal(saved["tokens"], &tokens); err != nil {
				t.Fatal(err)
			}
			assertWorkerIDToken(t, tokens["id_token"])
			if tokens["access_token"] != token || tokens["refresh_token"] != "" {
				t.Fatal("official CLI persisted real credentials or lost its worker placeholder")
			}
			if refresh {
				saved, err := store.Load(context.Background(), "privacy")
				if err != nil || saved.OpenAIOAuth.AccessToken != "private-rotated-access" || saved.OpenAIOAuth.RefreshToken != "private-rotated-refresh" || saved.OpenAIOAuth.IDToken != id {
					t.Fatal("real CLI refresh was not durably saved to the independent profile")
				}
				reopened, err := NewProxy(config)
				if err != nil || reopened.oauthCreds.AccessToken != saved.OpenAIOAuth.AccessToken {
					t.Fatal("proxy restart lost the independent OAuth rotation")
				}
			}
		})
	}
}
