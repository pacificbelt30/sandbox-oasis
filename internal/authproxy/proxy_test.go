package authproxy

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestProxy(t *testing.T) *Proxy {
	t.Helper()
	p, err := NewProxy(Config{TokenTTL: 60, Verbose: false})
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	p.apiKey = "sk-test-key-12345"
	return p
}

func TestIssueToken(t *testing.T) {
	p := newTestProxy(t)

	token, err := p.IssueToken("worker-1", 60)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if token == "" {
		t.Fatal("IssueToken returned empty token")
	}
	if !strings.HasPrefix(token, "cdx-") {
		t.Errorf("token %q does not start with 'cdx-'", token)
	}

	p.mu.RLock()
	rec, ok := p.tokens["worker-1"]
	p.mu.RUnlock()
	if !ok {
		t.Fatal("token not stored in proxy")
	}
	if rec.Token != token {
		t.Errorf("stored token %q != issued token %q", rec.Token, token)
	}
}

func TestIssueToken_Unique(t *testing.T) {
	p := newTestProxy(t)

	t1, _ := p.IssueToken("w1", 60)
	t2, _ := p.IssueToken("w2", 60)
	if t1 == t2 {
		t.Error("two IssueToken calls returned the same token")
	}
}

func TestRevokeToken(t *testing.T) {
	p := newTestProxy(t)

	_, err := p.IssueToken("worker-rev", 60)
	if err != nil {
		t.Fatal(err)
	}

	p.RevokeToken("worker-rev")

	p.mu.RLock()
	_, ok := p.tokens["worker-rev"]
	p.mu.RUnlock()
	if ok {
		t.Error("token still present after RevokeToken")
	}
}

func TestHandleToken_Valid(t *testing.T) {
	p := newTestProxy(t)
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	token, err := p.IssueToken("ctr-ok", 60)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/token", nil)
	req.Header.Set("X-Codex-Token", token)
	w := httptest.NewRecorder()
	p.handleToken(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	var m map[string]string
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	// api_key must be a placeholder (the container's CODEX_TOKEN), not the real key.
	if m["api_key"] == "sk-test-key-12345" {
		t.Error("api_key must not be the real API key; containers receive a placeholder only")
	}
	if !strings.HasPrefix(m["api_key"], "cdx-") {
		t.Errorf("api_key placeholder = %q; want cdx- prefix", m["api_key"])
	}
	if m["container_name"] != "ctr-ok" {
		t.Errorf("container_name = %q; want ctr-ok", m["container_name"])
	}
}

func TestHandleToken_MissingHeader(t *testing.T) {
	p := newTestProxy(t)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req := httptest.NewRequest(http.MethodGet, "/token", nil)
	w := httptest.NewRecorder()
	p.handleToken(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleToken_InvalidToken(t *testing.T) {
	p := newTestProxy(t)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req := httptest.NewRequest(http.MethodGet, "/token", nil)
	req.Header.Set("X-Codex-Token", "cdx-not-a-real-token")
	w := httptest.NewRecorder()
	p.handleToken(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleToken_WrongMethod(t *testing.T) {
	p := newTestProxy(t)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req := httptest.NewRequest(http.MethodPost, "/token", nil)
	w := httptest.NewRecorder()
	p.handleToken(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleToken_ExpiredToken(t *testing.T) {
	p := newTestProxy(t)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	token, _ := p.IssueToken("ctr-exp", 60)

	// Manually force expiry
	p.mu.Lock()
	p.tokens["ctr-exp"].ExpiresAt = time.Now().Add(-1 * time.Second)
	p.mu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/token", nil)
	req.Header.Set("X-Codex-Token", token)
	w := httptest.NewRecorder()
	p.handleToken(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for expired token, got %d", w.Code)
	}
}

func TestHandleHealth(t *testing.T) {
	p := newTestProxy(t)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	_, _ = p.IssueToken("w1", 60)
	_, _ = p.IssueToken("w2", 60)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	p.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var m map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m["status"] != "ok" {
		t.Errorf("status = %v; want ok", m["status"])
	}
	if int(m["active_tokens"].(float64)) != 2 {
		t.Errorf("active_tokens = %v; want 2", m["active_tokens"])
	}
}

func TestHandleRevoke(t *testing.T) {
	p := newTestProxy(t)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	_, _ = p.IssueToken("ctr-revoke", 60)

	req := httptest.NewRequest(http.MethodPost, "/revoke?container=ctr-revoke", nil)
	w := httptest.NewRecorder()
	p.handleRevoke(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	p.mu.RLock()
	_, exists := p.tokens["ctr-revoke"]
	p.mu.RUnlock()
	if exists {
		t.Error("token still exists after revoke endpoint")
	}
}

func TestHandleRevoke_MissingContainer(t *testing.T) {
	p := newTestProxy(t)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req := httptest.NewRequest(http.MethodPost, "/revoke", nil)
	w := httptest.NewRecorder()
	p.handleRevoke(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestExpireLoop(t *testing.T) {
	p := newTestProxy(t)

	token, _ := p.IssueToken("expire-me", 1)
	_ = token

	// Force immediate expiry
	p.mu.Lock()
	p.tokens["expire-me"].ExpiresAt = time.Now().Add(-2 * time.Second)
	p.mu.Unlock()

	// Exercise the same cleanup used by the ticker.
	p.expireTokens(time.Now())

	p.mu.RLock()
	_, ok := p.tokens["expire-me"]
	p.mu.RUnlock()
	if ok {
		t.Error("expired token was not cleaned up")
	}
}

func TestStartStop(t *testing.T) {
	p := newTestProxy(t)
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if p.Endpoint() == "" {
		t.Error("Endpoint() is empty after Start")
	}
	if !strings.HasPrefix(p.Endpoint(), "http://127.0.0.1:") {
		t.Errorf("Endpoint() = %q; expected http://127.0.0.1:...", p.Endpoint())
	}

	// Issue a token and verify it's cleared on Stop
	_, _ = p.IssueToken("w1", 60)
	p.Stop()

	p.mu.RLock()
	count := len(p.tokens)
	p.mu.RUnlock()
	if count != 0 {
		t.Errorf("tokens not cleared on Stop: %d remaining", count)
	}
}

func TestGenerateToken(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 50; i++ {
		tok, err := generateToken()
		if err != nil {
			t.Fatalf("generateToken: %v", err)
		}
		if !strings.HasPrefix(tok, "cdx-") {
			t.Errorf("token %q missing 'cdx-' prefix", tok)
		}
		if len(tok) != 68 { // "cdx-" (4) + 64 hex chars (32 bytes)
			t.Errorf("token len = %d; want 68", len(tok))
		}
		if _, dup := seen[tok]; dup {
			t.Error("generateToken produced duplicate token")
		}
		seen[tok] = struct{}{}
	}
}

// ── OAuth mode proxy tests ───────────────────────────────────────────────────

// newOAuthTestProxy creates a Proxy configured for OAuth mode, bypassing
// IsOAuthAuth() file detection by directly setting oauthCreds.
func newOAuthTestProxy(t *testing.T, accessToken string) *Proxy {
	t.Helper()
	p, err := NewProxy(Config{TokenTTL: 60, Verbose: false})
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	p.oauthCreds = &OAuthCredentials{
		AccessToken:  accessToken,
		RefreshToken: "rt-secret-stays-on-host",
		TokenType:    "Bearer",
	}
	p.apiKey = "" // ensure API key mode is not active
	return p
}

func TestIsOAuthMode_False(t *testing.T) {
	p := newTestProxy(t)
	if p.IsOAuthMode() {
		t.Error("IsOAuthMode() = true for API key proxy; want false")
	}
}

func TestIsOAuthMode_True(t *testing.T) {
	p := newOAuthTestProxy(t, "at-test-access")
	if !p.IsOAuthMode() {
		t.Error("IsOAuthMode() = false for OAuth proxy; want true")
	}
}

func TestHandleToken_OAuthMode_ReturnsAccessToken(t *testing.T) {
	p := newOAuthTestProxy(t, "at-container-visible")
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	token, err := p.IssueToken("ctr-oauth", 60)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/token", nil)
	req.Header.Set("X-Codex-Token", token)
	w := httptest.NewRecorder()
	p.handleToken(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var m map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// oauth_access_token must be a placeholder (the CODEX_TOKEN), not the real access token.
	if m["oauth_access_token"] == "at-container-visible" {
		t.Error("oauth_access_token must not be the real access token; containers receive a placeholder only")
	}
	if !strings.HasPrefix(m["oauth_access_token"], "cdx-") {
		t.Errorf("oauth_access_token placeholder = %q; want cdx- prefix", m["oauth_access_token"])
	}
	// container_name must match
	if m["container_name"] != "ctr-oauth" {
		t.Errorf("container_name = %q; want ctr-oauth", m["container_name"])
	}
}

func TestHandleToken_OAuthMode_RefreshTokenNotLeaked(t *testing.T) {
	// OAuth mode must NOT pass refresh_token to containers.
	// Containers refresh tokens via CODEX_REFRESH_TOKEN_URL_OVERRIDE → proxy /oauth/token,
	// which substitutes the host's real refresh_token. The container never sees it.
	p := newOAuthTestProxy(t, "at-no-leak")
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	token, _ := p.IssueToken("ctr-noleak", 60)

	req := httptest.NewRequest(http.MethodGet, "/token", nil)
	req.Header.Set("X-Codex-Token", token)
	w := httptest.NewRecorder()
	p.handleToken(w, req)

	var m map[string]string
	if err := json.NewDecoder(w.Result().Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// oauth_access_token must be a placeholder, not the real token.
	if m["oauth_access_token"] == "at-no-leak" {
		t.Error("oauth_access_token must not be the real access token (security: real token never leaves proxy)")
	}
	if !strings.HasPrefix(m["oauth_access_token"], "cdx-") {
		t.Errorf("oauth_access_token = %q; want cdx- placeholder", m["oauth_access_token"])
	}
	// refresh_token must never be returned to containers.
	if _, ok := m["oauth_refresh_token"]; ok {
		t.Error("oauth_refresh_token must not be present in /token response (security: token stays on host)")
	}
}

func TestHandleToken_OAuthMode_NoAPIKey(t *testing.T) {
	p := newOAuthTestProxy(t, "at-oauth")
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	token, _ := p.IssueToken("ctr-no-apikey", 60)

	req := httptest.NewRequest(http.MethodGet, "/token", nil)
	req.Header.Set("X-Codex-Token", token)
	w := httptest.NewRecorder()
	p.handleToken(w, req)

	var m map[string]string
	json.NewDecoder(w.Result().Body).Decode(&m)

	// api_key must not be present in OAuth response
	if _, hasKey := m["api_key"]; hasKey {
		t.Error("api_key should not be present in OAuth mode response")
	}
}

func TestNewProxy_OAuthMode(t *testing.T) {
	home, cleanup := withTempHome(t)
	defer cleanup()

	// Write an OAuth auth.json
	writeAuthJSONForProxy(t, home, map[string]interface{}{
		"access_token":  "at-proxy-test",
		"refresh_token": "rt-proxy-test",
		"token_type":    "Bearer",
	})

	p, err := NewProxy(Config{Verbose: false})
	if err != nil {
		t.Fatalf("NewProxy in OAuth mode: %v", err)
	}

	if !p.IsOAuthMode() {
		t.Error("proxy should be in OAuth mode when auth.json has refresh_token")
	}
	if p.oauthCreds.AccessToken != "at-proxy-test" {
		t.Errorf("oauthCreds.AccessToken = %q; want at-proxy-test", p.oauthCreds.AccessToken)
	}
	// RefreshToken must be held in proxy (not nil) but should NOT be forwarded
	if p.oauthCreds.RefreshToken == "" {
		t.Error("proxy should hold refresh_token internally (for future refresh support)")
	}
	// apiKey must be empty in OAuth mode
	if p.apiKey != "" {
		t.Errorf("apiKey should be empty in OAuth mode, got %q", p.apiKey)
	}
}

// writeAuthJSONForProxy is a test helper that writes auth.json to the fake HOME.
func writeAuthJSONForProxy(t *testing.T, home string, data map[string]interface{}) {
	t.Helper()
	codexDir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(codexDir, 0700); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexDir, "auth.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}

// ── ListenAddr tests ─────────────────────────────────────────────────────────

func TestStart_DefaultListenAddr(t *testing.T) {
	p := newTestProxy(t)
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	// Endpoint() always returns 127.0.0.1 for host-side access regardless of
	// the actual bind address (default is 0.0.0.0 to allow container access).
	if !strings.HasPrefix(p.Endpoint(), "http://127.0.0.1:") {
		t.Errorf("default Endpoint() = %q; expected http://127.0.0.1:...", p.Endpoint())
	}
}

func TestStart_AdminListenerSplit(t *testing.T) {
	p, err := NewProxy(Config{
		TokenTTL:        60,
		ListenAddr:      "127.0.0.1:0",
		AdminListenAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	workerBase := "http://" + p.addr
	adminBase := "http://" + p.adminAddr
	if p.adminAddr == "" || p.adminAddr == p.addr {
		t.Fatalf("admin listener not separated: worker=%q admin=%q", p.addr, p.adminAddr)
	}

	// /admin/mode must NOT be reachable on the worker-facing listener.
	resp, err := http.Get(workerBase + "/admin/mode") //nolint:noctx
	if err != nil {
		t.Fatalf("GET worker /admin/mode: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Errorf("worker listener served /admin/mode (status %d); admin must be isolated", resp.StatusCode)
	}

	// /admin/mode IS reachable on the dedicated admin listener.
	resp2, err := http.Get(adminBase + "/admin/mode") //nolint:noctx
	if err != nil {
		t.Fatalf("GET admin /admin/mode: %v", err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("admin listener /admin/mode status = %d; want 200", resp2.StatusCode)
	}
}

func TestProxy_Port(t *testing.T) {
	p := newTestProxy(t)
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	port := p.Port()
	if port == "" {
		t.Fatal("Port() is empty after Start")
	}
	// Port must be a valid decimal number.
	for _, c := range port {
		if c < '0' || c > '9' {
			t.Errorf("Port() = %q; contains non-digit character %q", port, c)
		}
	}
}

func TestProxy_ContainerEndpoint(t *testing.T) {
	p := newTestProxy(t)
	if err := p.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop()

	ce := p.ContainerEndpoint()
	if !strings.HasPrefix(ce, "http://host.docker.internal:") {
		t.Errorf("ContainerEndpoint() = %q; expected http://host.docker.internal:...", ce)
	}
	// Port in ContainerEndpoint must match Port().
	wantSuffix := ":" + p.Port()
	if !strings.HasSuffix(ce, wantSuffix) {
		t.Errorf("ContainerEndpoint() = %q; should end with %q", ce, wantSuffix)
	}
}

func TestStart_CustomListenAddr(t *testing.T) {
	p, err := NewProxy(Config{
		TokenTTL:   60,
		Verbose:    false,
		ListenAddr: "127.0.0.1:0", // Still loopback but using explicit config path.
	})
	if err != nil {
		t.Fatalf("NewProxy: %v", err)
	}
	p.apiKey = "sk-test"

	if err := p.Start(); err != nil {
		t.Fatalf("Start with ListenAddr: %v", err)
	}
	defer p.Stop()

	if !strings.HasPrefix(p.Endpoint(), "http://127.0.0.1:") {
		t.Errorf("Endpoint() = %q; expected http://127.0.0.1:...", p.Endpoint())
	}
}

func TestConfig_ListenAddrDefault(t *testing.T) {
	cfg := Config{TokenTTL: 60}
	if cfg.ListenAddr != "" {
		t.Errorf("ListenAddr default should be empty string, got %q", cfg.ListenAddr)
	}
}

// ── handleOAuthTokenRefresh tests ────────────────────────────────────────────

// newOAuthProxyWithFakeOAuth creates an OAuth-mode proxy whose /oauth/token
// upstream is redirected to fakeServer.URL so tests never hit the real internet.
func newOAuthProxyWithFakeOAuth(t *testing.T, fakeServer *httptest.Server) *Proxy {
	t.Helper()
	p := newOAuthTestProxy(t, "at-initial")
	p.oauthTokenURL = fakeServer.URL + "/oauth/token"
	return p
}

// fakeOAuthServer starts a test HTTP server that simulates auth.openai.com/oauth/token.
// handler is called for every POST /oauth/token.
func fakeOAuthServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/token", handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

type blockingRefreshBody struct {
	started     chan struct{}
	closed      chan struct{}
	startedOnce sync.Once
	closedOnce  sync.Once
}

func newBlockingRefreshBody() *blockingRefreshBody {
	return &blockingRefreshBody{started: make(chan struct{}), closed: make(chan struct{})}
}

func (b *blockingRefreshBody) Read([]byte) (int, error) {
	b.startedOnce.Do(func() { close(b.started) })
	<-b.closed
	return 0, io.ErrClosedPipe
}

func (b *blockingRefreshBody) Close() error {
	b.closedOnce.Do(func() { close(b.closed) })
	return nil
}

func TestHandleOAuthTokenRefresh_HappyPath(t *testing.T) {
	var gotBody map[string]interface{}
	var gotContentType string
	fake := fakeOAuthServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		gotContentType = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "at-new",
			"id_token":      "id-new",
			"refresh_token": "rt-new", // server rotates refresh_token
		})
	})

	p := newOAuthProxyWithFakeOAuth(t, fake)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	cdx, _ := p.IssueToken("ctr-refresh", 60)

	// Simulate what Codex CLI actually sends: JSON with empty refresh_token.
	// client_id and grant_type are added by Codex CLI itself, not the proxy.
	reqPayload := `{"client_id":"app_EMoamEEZ73f0CkXaXp7hrann","grant_type":"refresh_token","refresh_token":""}`
	req := httptest.NewRequest(http.MethodPost, "/oauth/token?cdx="+cdx,
		strings.NewReader(reqPayload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	p.handleOAuthTokenRefresh(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Upstream must receive application/json (Codex CLI's actual format).
	if gotContentType != "application/json" {
		t.Errorf("upstream Content-Type = %q; want application/json", gotContentType)
	}

	// Only refresh_token is replaced; all other fields pass through as-is from Codex CLI.
	if gotBody["refresh_token"] != "rt-secret-stays-on-host" {
		t.Errorf("upstream refresh_token = %v; want rt-secret-stays-on-host (host's real token)", gotBody["refresh_token"])
	}
	// client_id is passed through from Codex CLI unchanged — proxy does NOT inject it.
	if gotBody["client_id"] != "app_EMoamEEZ73f0CkXaXp7hrann" {
		t.Errorf("upstream client_id = %v; want app_EMoamEEZ73f0CkXaXp7hrann (Codex CLI adds this)", gotBody["client_id"])
	}
	if gotBody["grant_type"] != "refresh_token" {
		t.Errorf("upstream grant_type = %v; want refresh_token", gotBody["grant_type"])
	}

	// Response to container must NOT include refresh_token.
	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := resp["refresh_token"]; ok {
		t.Error("refresh_token must not be returned to container")
	}
	// access_token returned to container must be a placeholder, not the real new token.
	at, _ := resp["access_token"].(string)
	if at == "at-new" {
		t.Error("access_token must not be the real new token; container receives placeholder only")
	}
	if !strings.HasPrefix(at, "cdx-") {
		t.Errorf("access_token placeholder = %q; want cdx- prefix", at)
	}
}

func TestHandleOAuthTokenRefresh_UpdatesHostCreds(t *testing.T) {
	fake := fakeOAuthServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token":  "at-rotated",
			"id_token":      "id-rotated",
			"refresh_token": "rt-rotated",
		})
	})

	p := newOAuthProxyWithFakeOAuth(t, fake)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	cdx, _ := p.IssueToken("ctr-update", 60)

	req := httptest.NewRequest(http.MethodPost, "/oauth/token?cdx="+cdx, strings.NewReader(""))
	w := httptest.NewRecorder()
	p.handleOAuthTokenRefresh(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// Host's cached credentials must be updated
	p.mu.RLock()
	at := p.oauthCreds.AccessToken
	rt := p.oauthCreds.RefreshToken
	id := p.oauthCreds.IDToken
	p.mu.RUnlock()

	if at != "at-rotated" {
		t.Errorf("host AccessToken = %q; want at-rotated", at)
	}
	if rt != "rt-rotated" {
		t.Errorf("host RefreshToken = %q; want rt-rotated (rotation)", rt)
	}
	if id != "id-rotated" {
		t.Errorf("host IDToken = %q; want id-rotated", id)
	}
}

func TestHandleOAuthTokenRefresh_MissingCdxParam(t *testing.T) {
	p := newOAuthTestProxy(t, "at-test")
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(""))
	w := httptest.NewRecorder()
	p.handleOAuthTokenRefresh(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleOAuthTokenRefresh_InvalidCdxToken(t *testing.T) {
	p := newOAuthTestProxy(t, "at-test")
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req := httptest.NewRequest(http.MethodPost, "/oauth/token?cdx=cdx-not-valid", strings.NewReader(""))
	w := httptest.NewRecorder()
	p.handleOAuthTokenRefresh(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestHandleOAuthTokenRefresh_ExpiredCdxToken(t *testing.T) {
	p := newOAuthTestProxy(t, "at-test")
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	cdx, _ := p.IssueToken("ctr-exp-refresh", 60)
	p.mu.Lock()
	p.tokens["ctr-exp-refresh"].ExpiresAt = time.Now().Add(-1 * time.Second)
	p.mu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/oauth/token?cdx="+cdx, strings.NewReader(""))
	w := httptest.NewRecorder()
	p.handleOAuthTokenRefresh(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for expired token, got %d", w.Code)
	}
}

func TestHandleOAuthTokenRefresh_WrongMethod(t *testing.T) {
	p := newOAuthTestProxy(t, "at-test")
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req := httptest.NewRequest(http.MethodGet, "/oauth/token", nil)
	w := httptest.NewRecorder()
	p.handleOAuthTokenRefresh(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestHandleOAuthTokenRefresh_NonOAuthMode(t *testing.T) {
	p := newTestProxy(t) // API key mode, not OAuth
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	cdx, _ := p.IssueToken("ctr-apikey", 60)

	req := httptest.NewRequest(http.MethodPost, "/oauth/token?cdx="+cdx, strings.NewReader(""))
	w := httptest.NewRecorder()
	p.handleOAuthTokenRefresh(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for non-OAuth mode, got %d", w.Code)
	}
}

func TestHandleOAuthTokenRefresh_UpstreamError(t *testing.T) {
	fake := fakeOAuthServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	})

	p := newOAuthProxyWithFakeOAuth(t, fake)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	cdx, _ := p.IssueToken("ctr-upstream-err", 60)

	req := httptest.NewRequest(http.MethodPost, "/oauth/token?cdx="+cdx, strings.NewReader(""))
	w := httptest.NewRecorder()
	p.handleOAuthTokenRefresh(w, req)

	// Relay the upstream error status
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 relayed from upstream, got %d", w.Code)
	}
}

func TestHandleOAuthTokenRefresh_EmptyBodyStillInjectsHostToken(t *testing.T) {
	// Edge case: container sends empty body (not expected in practice, but handled).
	var gotBody map[string]interface{}
	fake := fakeOAuthServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"access_token": "at-ok"})
	})

	p := newOAuthProxyWithFakeOAuth(t, fake)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	cdx, _ := p.IssueToken("ctr-empty-body", 60)

	req := httptest.NewRequest(http.MethodPost, "/oauth/token?cdx="+cdx, strings.NewReader(""))
	w := httptest.NewRecorder()
	p.handleOAuthTokenRefresh(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// Even with empty body, proxy must inject the host's real refresh_token.
	if gotBody["refresh_token"] != "rt-secret-stays-on-host" {
		t.Errorf("upstream refresh_token = %v; want rt-secret-stays-on-host", gotBody["refresh_token"])
	}
}

func TestHandleOAuthTokenRefresh_BoundsBodyBeforeProviderLock(t *testing.T) {
	upstreamCalls := 0
	fake := fakeOAuthServer(t, func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls++
		w.WriteHeader(http.StatusInternalServerError)
	})
	p := newOAuthProxyWithFakeOAuth(t, fake)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	cdx, _ := p.IssueToken("ctr-body-limit", 60)
	payload := `{"padding":"` + strings.Repeat("x", int(openAIRefreshBodyLimit)) + `"}`
	w := httptest.NewRecorder()
	p.handleOAuthTokenRefresh(w, httptest.NewRequest(http.MethodPost, "/oauth/token?cdx="+cdx, strings.NewReader(payload)))
	if w.Code != http.StatusRequestEntityTooLarge || upstreamCalls != 0 {
		t.Fatalf("oversized body code=%d upstream=%d", w.Code, upstreamCalls)
	}
}

func TestHandleOAuthTokenRefresh_TimesOutSlowBodyOnRealConnection(t *testing.T) {
	p := newOAuthTestProxy(t, "at-test")
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	cdx, err := p.IssueToken("ctr-slow-body", 60)
	if err != nil {
		t.Fatal(err)
	}
	upstreamCalls := 0
	p.httpClient = &http.Client{Transport: privacyTransport(func(*http.Request) (*http.Response, error) {
		upstreamCalls++
		return nil, fmt.Errorf("unexpected upstream request")
	})}
	srv := httptest.NewServer(http.HandlerFunc(p.handleOAuthTokenRefresh))
	defer srv.Close()
	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err = conn.SetDeadline(time.Now().Add(openAIRefreshBodyTimeout + 3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = fmt.Fprintf(conn, "POST /oauth/token?cdx=%s HTTP/1.1\r\nHost: fixture\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{", cdx); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodPost})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestTimeout || upstreamCalls != 0 {
		t.Fatalf("slow body status=%d upstream=%d", resp.StatusCode, upstreamCalls)
	}
}

func TestHandleOAuthTokenRefresh_RevocationClosesBlockedBodyWithoutHoldingRefreshLock(t *testing.T) {
	p := newOAuthTestProxy(t, "at-test")
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	cdx, _ := p.IssueToken("ctr-blocked-body", 60)
	body := newBlockingRefreshBody()
	req := httptest.NewRequest(http.MethodPost, "/oauth/token?cdx="+cdx, nil)
	req.Body = body
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		p.handleOAuthTokenRefresh(w, req)
		close(done)
	}()
	select {
	case <-body.started:
	case <-time.After(time.Second):
		t.Fatal("handler did not start reading the request body")
	}
	for name, lock := range map[string]*sync.Mutex{
		"provider": &p.openAIRefreshMu,
		"store":    &p.refreshMu,
	} {
		lockAvailable := make(chan struct{})
		go func() {
			lock.Lock()
			lockAvailable <- struct{}{}
			lock.Unlock()
		}()
		select {
		case <-lockAvailable:
		case <-time.After(time.Second):
			t.Fatalf("request body read held the %s refresh lock", name)
		}
	}
	p.RevokeToken("ctr-blocked-body")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("revocation did not unblock the request body")
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked body request code=%d", w.Code)
	}
}

func TestHandleOAuthTokenRefresh_RevocationCancelsUpstream(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	p := newOAuthTestProxy(t, "at-initial")
	p.httpClient = &http.Client{Transport: privacyTransport(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		close(canceled)
		return nil, r.Context().Err()
	})}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	cdx, _ := p.IssueToken("ctr-blocked-upstream", 60)
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		p.handleOAuthTokenRefresh(w, httptest.NewRequest(http.MethodPost, "/oauth/token?cdx="+cdx, strings.NewReader(`{}`)))
		close(done)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("upstream refresh did not start")
	}
	p.RevokeToken("ctr-blocked-upstream")
	for name, signal := range map[string]<-chan struct{}{"upstream cancellation": canceled, "handler completion": done} {
		select {
		case <-signal:
		case <-time.After(time.Second):
			t.Fatalf("revocation did not cause %s", name)
		}
	}
	if w.Code == http.StatusOK {
		t.Fatal("revoked refresh returned a successful worker response")
	}
}

func TestHandleOAuthTokenRefresh_BoundsIssuerResponse(t *testing.T) {
	fake := fakeOAuthServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", int(openAIRefreshResponseLimit)+1)))
	})
	p := newOAuthProxyWithFakeOAuth(t, fake)
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	cdx, _ := p.IssueToken("ctr-response-limit", 60)
	w := httptest.NewRecorder()
	p.handleOAuthTokenRefresh(w, httptest.NewRequest(http.MethodPost, "/oauth/token?cdx="+cdx, strings.NewReader(`{}`)))
	if w.Code != http.StatusBadGateway {
		t.Fatalf("oversized issuer response code=%d", w.Code)
	}
}

// ── handleAPIProxy tests ──────────────────────────────────────────────────────

// newFakeUpstream starts a test server and returns it.
// handler receives every request forwarded by the proxy.
func newFakeUpstream(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestHandleAPIProxy_APIKeyMode_ForwardsToAPIUpstream(t *testing.T) {
	var gotPath string
	var gotAuth string
	upstream := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"object":"list"}`))
	})

	p := newTestProxy(t)
	p.apiUpstreamURL = upstream.URL
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer sk-test-key-12345")
	w := httptest.NewRecorder()
	authorizeTestRequest(t, p, req)
	p.handleAPIProxy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotPath != "/models" {
		t.Errorf("upstream path = %q; want /models", gotPath)
	}
	// Authorization header must be injected with the proxy's real API key,
	// regardless of whatever the container sent.
	if gotAuth != "Bearer sk-test-key-12345" {
		t.Errorf("upstream Authorization = %q; want Bearer sk-test-key-12345", gotAuth)
	}
}

func TestHandleAPIProxy_OAuthMode_ForwardsToChatGPTCodex(t *testing.T) {
	var gotPath string
	upstream := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"resp-1"}`))
	})

	p := newOAuthTestProxy(t, "at-oauth-access")
	// chatgptURL is the base; handler appends /codex
	p.chatgptURL = upstream.URL
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req := httptest.NewRequest(http.MethodPost, "/v1/responses",
		strings.NewReader(`{"model":"codex-mini"}`))
	req.Header.Set("Authorization", "Bearer at-oauth-access")
	w := httptest.NewRecorder()
	authorizeTestRequest(t, p, req)
	p.handleAPIProxy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	// In OAuth mode the path becomes /codex/responses
	if gotPath != "/codex/responses" {
		t.Errorf("upstream path = %q; want /codex/responses", gotPath)
	}
}

func TestHandleAPIProxy_QueryStringPreserved(t *testing.T) {
	var gotQuery string
	upstream := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	})

	p := newTestProxy(t)
	p.apiUpstreamURL = upstream.URL
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req := httptest.NewRequest(http.MethodGet, "/v1/models?limit=10&order=asc", nil)
	w := httptest.NewRecorder()
	authorizeTestRequest(t, p, req)
	p.handleAPIProxy(w, req)

	if gotQuery != "limit=10&order=asc" {
		t.Errorf("upstream query = %q; want limit=10&order=asc", gotQuery)
	}
}

func TestHandleAPIProxy_ResponseBodyRelayed(t *testing.T) {
	upstream := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"result":"ok"}`))
	})

	p := newTestProxy(t)
	p.apiUpstreamURL = upstream.URL
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	authorizeTestRequest(t, p, req)
	p.handleAPIProxy(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected 201, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q; want application/json", ct)
	}
	if body := w.Body.String(); body != `{"result":"ok"}` {
		t.Errorf("body = %q; want {\"result\":\"ok\"}", body)
	}
}

func TestHandleAPIProxy_HopByHopHeadersNotForwarded(t *testing.T) {
	var gotConnection string
	upstream := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotConnection = r.Header.Get("Connection")
		w.WriteHeader(http.StatusOK)
	})

	p := newTestProxy(t)
	p.apiUpstreamURL = upstream.URL
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Authorization", "Bearer sk-test")
	w := httptest.NewRecorder()
	authorizeTestRequest(t, p, req)
	p.handleAPIProxy(w, req)

	if gotConnection != "" {
		t.Errorf("Connection hop-by-hop header must not be forwarded, got %q", gotConnection)
	}
}

func TestHandleAPIProxy_NonHopByHopHeadersForwarded(t *testing.T) {
	var gotVersion string
	upstream := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotVersion = r.Header.Get("Version")
		w.WriteHeader(http.StatusOK)
	})

	p := newTestProxy(t)
	p.apiUpstreamURL = upstream.URL
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	req.Header.Set("Version", "0.110.0") // Codex CLI version header
	req.Header.Set("Authorization", "Bearer sk-test")
	w := httptest.NewRecorder()
	authorizeTestRequest(t, p, req)
	p.handleAPIProxy(w, req)

	if gotVersion != "0.110.0" {
		t.Errorf("Version header = %q; want 0.110.0", gotVersion)
	}
}

// ── handleChatGPTProxy tests ──────────────────────────────────────────────────

func TestHandleChatGPTProxy_ForwardsToChatGPTBackend(t *testing.T) {
	var gotPath string
	upstream := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"limits":{}}`))
	})

	p := newTestProxy(t)
	p.chatgptURL = upstream.URL
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req := httptest.NewRequest(http.MethodGet, "/chatgpt/public_api/conversation_limit", nil)
	w := httptest.NewRecorder()
	authorizeTestRequest(t, p, req)
	p.handleChatGPTProxy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if gotPath != "/public_api/conversation_limit" {
		t.Errorf("upstream path = %q; want /public_api/conversation_limit", gotPath)
	}
}

func TestHandleChatGPTProxy_QueryStringPreserved(t *testing.T) {
	var gotQuery string
	upstream := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	})

	p := newTestProxy(t)
	p.chatgptURL = upstream.URL
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req := httptest.NewRequest(http.MethodGet, "/chatgpt/some/path?foo=bar", nil)
	w := httptest.NewRecorder()
	authorizeTestRequest(t, p, req)
	p.handleChatGPTProxy(w, req)

	if gotQuery != "foo=bar" {
		t.Errorf("upstream query = %q; want foo=bar", gotQuery)
	}
}

func TestHandleChatGPTProxy_ResponseStatusRelayed(t *testing.T) {
	upstream := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	})

	p := newTestProxy(t)
	p.chatgptURL = upstream.URL
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req := httptest.NewRequest(http.MethodGet, "/chatgpt/limits", nil)
	w := httptest.NewRecorder()
	authorizeTestRequest(t, p, req)
	p.handleChatGPTProxy(w, req)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429 relayed from upstream, got %d", w.Code)
	}
}

func TestHandleChatGPTProxy_HopByHopHeadersNotForwarded(t *testing.T) {
	var gotUpgrade string
	upstream := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotUpgrade = r.Header.Get("Upgrade")
		w.WriteHeader(http.StatusOK)
	})

	p := newTestProxy(t)
	p.chatgptURL = upstream.URL
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req := httptest.NewRequest(http.MethodGet, "/chatgpt/limits", nil)
	req.Header.Set("Upgrade", "websocket")
	w := httptest.NewRecorder()
	authorizeTestRequest(t, p, req)
	p.handleChatGPTProxy(w, req)

	if gotUpgrade != "" {
		t.Errorf("Upgrade hop-by-hop header must not be forwarded, got %q", gotUpgrade)
	}
}

// ── reverseProxy integration: end-to-end via httptest.Server ─────────────────

func TestReverseProxy_EndToEnd_APIKeyMode(t *testing.T) {
	// Verifies that a full HTTP request through the running proxy reaches upstream.
	upstream := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})

	p := newTestProxy(t)
	p.apiUpstreamURL = upstream.URL
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, p.Endpoint()+"/v1/models", nil)
	authorizeTestRequest(t, p, req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q; want {\"ok\":true}", body)
	}
}

func TestReverseProxy_EndToEnd_ChatGPTProxy(t *testing.T) {
	upstream := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"limits":{"daily":100}}`))
	})

	p := newTestProxy(t)
	p.chatgptURL = upstream.URL
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, p.Endpoint()+"/chatgpt/public_api/limits", nil)
	authorizeTestRequest(t, p, req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /chatgpt/public_api/limits: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// ── WebSocket tunneling tests ─────────────────────────────────────────────────

// TestIsWebSocketRequest verifies detection of WebSocket upgrade requests.
func TestIsWebSocketRequest(t *testing.T) {
	cases := []struct {
		upgrade    string
		connection string
		want       bool
	}{
		{"websocket", "Upgrade", true},
		{"WebSocket", "upgrade", true},
		{"WEBSOCKET", "keep-alive, Upgrade", true},
		{"", "Upgrade", false},
		{"websocket", "", false},
		{"websocket", "keep-alive", false},
	}
	for _, tc := range cases {
		r, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
		if tc.upgrade != "" {
			r.Header.Set("Upgrade", tc.upgrade)
		}
		if tc.connection != "" {
			r.Header.Set("Connection", tc.connection)
		}
		if got := isWebSocketRequest(r); got != tc.want {
			t.Errorf("upgrade=%q connection=%q: isWebSocketRequest()=%v, want %v",
				tc.upgrade, tc.connection, got, tc.want)
		}
	}
}

// TestWebSocketProxy_Tunnel verifies end-to-end WebSocket tunneling through the proxy.
// It spins up a bare TCP server that echoes WebSocket frames (after a 101 handshake)
// and checks that data flows bidirectionally through handleWebSocketProxy.
func TestWebSocketProxy_Tunnel(t *testing.T) {
	// Minimal WebSocket echo server: accepts the HTTP upgrade, then echoes bytes.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close() //nolint:errcheck

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close() //nolint:errcheck

		// Read the HTTP upgrade request.
		br := bufio.NewReader(conn)
		for {
			line, err := br.ReadString('\n')
			if err != nil || line == "\r\n" {
				break
			}
		}
		// Send 101 Switching Protocols.
		_, _ = fmt.Fprint(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")

		// Echo everything back.
		buf := make([]byte, 64)
		n, _ := br.Read(buf)
		_, _ = conn.Write(buf[:n])
	}()

	// Start the auth proxy and point its API upstream at our echo server.
	p := newTestProxy(t)
	p.apiUpstreamURL = "http://" + ln.Addr().String()
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	// Connect to the proxy with a WebSocket upgrade request.
	proxyAddr := strings.TrimPrefix(p.Endpoint(), "http://")
	clientConn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer clientConn.Close() //nolint:errcheck

	// Send the WebSocket HTTP upgrade request.
	_, err = fmt.Fprintf(clientConn,
		"GET /v1/responses HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nAuthorization: Bearer %s\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n",
		proxyAddr, issuedTestToken(t, p))
	if err != nil {
		t.Fatalf("writing upgrade request: %v", err)
	}

	// Read and verify the 101 response from the proxy.
	br2 := bufio.NewReader(clientConn)
	statusLine, err := br2.ReadString('\n')
	if err != nil {
		t.Fatalf("reading status line: %v", err)
	}
	if !strings.Contains(statusLine, "101") {
		t.Fatalf("expected 101 Switching Protocols, got: %q", statusLine)
	}
	// Drain remaining headers.
	for {
		line, err := br2.ReadString('\n')
		if err != nil || line == "\r\n" {
			break
		}
	}

	// Send a payload and verify the echo server returns it.
	payload := []byte("hello-ws")
	if _, err := clientConn.Write(payload); err != nil {
		t.Fatalf("writing payload: %v", err)
	}

	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(br2, buf); err != nil {
		t.Fatalf("reading echo: %v", err)
	}
	if string(buf) != string(payload) {
		t.Errorf("echo = %q; want %q", buf, payload)
	}

	<-serverDone
}

// ── injectCredentials tests ───────────────────────────────────────────────────

func TestInjectCredentials_APIKeyMode(t *testing.T) {
	p := newTestProxy(t)
	h := http.Header{}
	h.Set("Authorization", "Bearer cdx-placeholder-token")
	p.injectCredentials(h)
	if h.Get("Authorization") != "Bearer sk-test-key-12345" {
		t.Errorf("Authorization = %q; want Bearer sk-test-key-12345", h.Get("Authorization"))
	}
}

func TestInjectCredentials_OAuthMode(t *testing.T) {
	p := newOAuthTestProxy(t, "at-real-access")
	p.oauthCreds.AccountID = "acc-123"
	h := http.Header{}
	h.Set("Authorization", "Bearer cdx-placeholder")
	p.injectCredentials(h)
	if h.Get("Authorization") != "Bearer at-real-access" {
		t.Errorf("Authorization = %q; want Bearer at-real-access", h.Get("Authorization"))
	}
	if h.Get("ChatGPT-Account-Id") != "acc-123" {
		t.Errorf("ChatGPT-Account-Id = %q; want acc-123", h.Get("ChatGPT-Account-Id"))
	}
}

func TestInjectCredentials_OAuthMode_NoAccountID(t *testing.T) {
	p := newOAuthTestProxy(t, "at-real")
	p.oauthCreds.AccountID = ""
	h := http.Header{}
	p.injectCredentials(h)
	if h.Get("Authorization") != "Bearer at-real" {
		t.Errorf("Authorization = %q; want Bearer at-real", h.Get("Authorization"))
	}
	if v := h.Get("ChatGPT-Account-Id"); v != "" {
		t.Errorf("ChatGPT-Account-Id should not be set when AccountID is empty, got %q", v)
	}
}

// TestHandleAPIProxy_PlaceholderReplacedWithRealKey verifies that even when the
// container sends a dummy placeholder token, the upstream receives the real API key.
func TestHandleAPIProxy_PlaceholderReplacedWithRealKey(t *testing.T) {
	var gotAuth string
	upstream := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	})

	p := newTestProxy(t) // apiKey = "sk-test-key-12345"
	p.apiUpstreamURL = upstream.URL
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	// Container sends a placeholder cdx- token, not the real key.
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer cdx-placeholder-fake-token")
	w := httptest.NewRecorder()
	authorizeTestRequest(t, p, req)
	p.handleAPIProxy(w, req)

	if gotAuth != "Bearer sk-test-key-12345" {
		t.Errorf("upstream Authorization = %q; want real key Bearer sk-test-key-12345", gotAuth)
	}
}

// TestHandleAPIProxy_PlaceholderReplacedWithRealToken_OAuthMode verifies that in
// OAuth mode the upstream receives the real access_token, not the container's placeholder.
func TestHandleAPIProxy_PlaceholderReplacedWithRealToken_OAuthMode(t *testing.T) {
	var gotAuth string
	var gotAccountID string
	upstream := newFakeUpstream(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccountID = r.Header.Get("ChatGPT-Account-Id")
		w.WriteHeader(http.StatusOK)
	})

	p := newOAuthTestProxy(t, "at-real-oauth-token")
	p.oauthCreds.AccountID = "acc-real-456"
	p.chatgptURL = upstream.URL
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	// Container sends a placeholder cdx- token.
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer cdx-placeholder")
	req.Header.Set("ChatGPT-Account-Id", "acc-wrong")
	w := httptest.NewRecorder()
	authorizeTestRequest(t, p, req)
	p.handleAPIProxy(w, req)

	if gotAuth != "Bearer at-real-oauth-token" {
		t.Errorf("upstream Authorization = %q; want Bearer at-real-oauth-token", gotAuth)
	}
	if gotAccountID != "acc-real-456" {
		t.Errorf("upstream ChatGPT-Account-Id = %q; want acc-real-456", gotAccountID)
	}
}

// TestWebSocketProxy_InjectsRealCredentials verifies that the WebSocket tunnel
// sends the real host credentials to the upstream, not the placeholder the container sent.
func TestWebSocketProxy_InjectsRealCredentials(t *testing.T) {
	var gotAuth string
	var gotAccountID string

	// Minimal WebSocket server that captures headers from the HTTP upgrade request.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close() //nolint:errcheck

	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close() //nolint:errcheck

		br := bufio.NewReader(conn)
		// Read request line.
		_, _ = br.ReadString('\n')
		// Read headers.
		for {
			line, err := br.ReadString('\n')
			if err != nil || line == "\r\n" {
				break
			}
			line = strings.TrimRight(line, "\r\n")
			if strings.HasPrefix(strings.ToLower(line), "authorization:") {
				gotAuth = strings.TrimSpace(line[len("authorization:"):])
			}
			if strings.HasPrefix(strings.ToLower(line), "chatgpt-account-id:") {
				gotAccountID = strings.TrimSpace(line[len("chatgpt-account-id:"):])
			}
		}
		// Send 101 and immediately close; we only need to verify headers were received.
		_, _ = fmt.Fprint(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	}()

	p := newOAuthTestProxy(t, "at-ws-real")
	p.oauthCreds.AccountID = "acc-ws-real"
	// In OAuth mode, /v1/* proxies to chatgptURL+"/codex/*".
	p.chatgptURL = "http://" + ln.Addr().String()
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	defer p.Stop()

	proxyAddr := strings.TrimPrefix(p.Endpoint(), "http://")
	clientConn, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer clientConn.Close() //nolint:errcheck

	// Send WebSocket upgrade with placeholder credentials.
	_, err = fmt.Fprintf(clientConn,
		"GET /v1/responses HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Authorization: Bearer %s\r\nChatGPT-Account-Id: acc-wrong\r\n"+
			"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n",
		proxyAddr, issuedTestToken(t, p))
	if err != nil {
		t.Fatalf("writing upgrade request: %v", err)
	}

	// Read the 101 response.
	br2 := bufio.NewReader(clientConn)
	statusLine, err := br2.ReadString('\n')
	if err != nil {
		t.Fatalf("reading status line: %v", err)
	}
	if !strings.Contains(statusLine, "101") {
		t.Fatalf("expected 101 Switching Protocols, got: %q", statusLine)
	}
	for {
		line, err := br2.ReadString('\n')
		if err != nil || line == "\r\n" {
			break
		}
	}

	<-serverDone

	if gotAuth != "Bearer at-ws-real" {
		t.Errorf("WebSocket upstream Authorization = %q; want Bearer at-ws-real", gotAuth)
	}
	if gotAccountID != "acc-ws-real" {
		t.Errorf("WebSocket upstream ChatGPT-Account-Id = %q; want acc-ws-real", gotAccountID)
	}
}

func TestHandleAdminIssue(t *testing.T) {
	p := newTestProxy(t)
	p.cfg.AdminSecret = "secret"
	req := httptest.NewRequest(http.MethodPost, "/admin/issue", strings.NewReader(`{"container":"worker-1","ttl":60}`))
	req.Header.Set("X-Proxy-Admin-Secret", "secret")
	w := httptest.NewRecorder()
	p.handleAdminIssue(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var out map[string]string
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(out["token"], "cdx-") {
		t.Fatalf("unexpected token: %q", out["token"])
	}
}

func TestRemoteProxy_IssueToken(t *testing.T) {
	p := newTestProxy(t)
	p.cfg.AdminSecret = "secret"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/mode":
			p.handleAdminMode(w, r)
		case "/admin/issue":
			p.handleAdminIssue(w, r)
		case "/admin/revoke":
			p.handleAdminRevoke(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	r, err := NewRemoteProxy(ts.URL, "http://codex-auth-proxy:18080", "secret")
	if err != nil {
		t.Fatalf("NewRemoteProxy: %v", err)
	}
	token, err := r.IssueToken("worker-r1", 60)
	if err != nil {
		t.Fatalf("IssueToken: %v", err)
	}
	if !strings.HasPrefix(token, "cdx-") {
		t.Fatalf("unexpected token: %s", token)
	}
	r.RevokeToken("worker-r1")
}

func TestRemoteProxy_ReportsAnthropicOAuthMode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/mode" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{
			"worker_token_auth":    true,
			"oauth_mode":           true,
			"anthropic_available":  true,
			"anthropic_oauth_mode": true,
		})
	}))
	defer ts.Close()

	r, err := NewRemoteProxy(ts.URL, "http://codex-auth-proxy:18080", "")
	if err != nil {
		t.Fatalf("NewRemoteProxy: %v", err)
	}
	if !r.IsAnthropicOAuthMode() {
		t.Fatal("IsAnthropicOAuthMode() = false; want true")
	}
}

func TestConnectionHint_ConnectionRefused(t *testing.T) {
	err := &net.OpError{Err: fmt.Errorf("connect: connection refused")}
	hint := connectionHint(err)
	if !strings.Contains(hint, "auth proxy is not running") {
		t.Fatalf("hint = %q; want startup guidance", hint)
	}
}

func TestConnectionHint_NonConnectionRefused(t *testing.T) {
	err := &net.OpError{Err: fmt.Errorf("i/o timeout")}
	hint := connectionHint(err)
	if hint != "" {
		t.Fatalf("hint = %q; want empty", hint)
	}
}
