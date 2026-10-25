package authproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	openAIRefreshBodyLimit       int64 = 64 << 10
	openAIRefreshResponseLimit   int64 = 1 << 20
	openAIRefreshBodyTimeout           = 10 * time.Second
	openAIRefreshUpstreamTimeout       = 20 * time.Second
)

// handleOAuthTokenRefresh acts as an OAuth token refresh proxy for containers.
// Codex CLI calls this endpoint via CODEX_REFRESH_TOKEN_URL_OVERRIDE.
// The container authenticates via ?cdx=<short-lived-token> query param.
// The proxy substitutes the host's real refresh_token, calls OpenAI, and returns
// worker-only access/ID placeholders. Real credentials stay on the host.
//
// Codex CLI sends JSON:
//
//	{"client_id":"app_EMoamEEZ73f0CkXaXp7hrann","grant_type":"refresh_token","refresh_token":""}
//
// The proxy replaces only the "refresh_token" field; all other fields (grant_type,
// client_id, etc.) are passed through as-is from what Codex CLI sent.
//
// Monitored fields:
//   - request  body["refresh_token"]: replaced with host's real refresh_token
//   - response: fresh projection; no real tokens or arbitrary issuer fields
func (p *Proxy) handleOAuthTokenRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Authenticate the container via the short-lived token embedded in the URL.
	// Codex CLI does not add custom headers when calling the refresh endpoint,
	// so the token is passed as a query param when setting CODEX_REFRESH_TOKEN_URL_OVERRIDE.
	cdxToken := r.URL.Query().Get("cdx")
	if cdxToken == "" || !p.transportBound(r, cdxToken) {
		http.Error(w, "missing cdx param", http.StatusUnauthorized)
		return
	}

	p.mu.RLock()
	found := p.tokensByValue[cdxToken]
	oauthMode := p.oauthCreds != nil
	var lease context.Context
	if found != nil {
		lease = found.context
	}
	p.mu.RUnlock()

	if found == nil || lease == nil || lease.Err() != nil || time.Now().After(found.ExpiresAt) || found.Identity.Provider == "anthropic" {
		http.Error(w, "token invalid or expired", http.StatusUnauthorized)
		return
	}
	if !oauthMode {
		http.Error(w, "not in OAuth mode", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	leaseCallbackDone := make(chan struct{})
	stopLease := context.AfterFunc(lease, func() {
		cancel()
		_ = r.Body.Close()
		close(leaseCallbackDone)
	})
	defer func() {
		if !stopLease() {
			<-leaseCallbackDone
		}
		cancel()
	}()
	r = r.WithContext(ctx)

	// Parse the JSON body that Codex CLI sent.
	// Only "refresh_token" is replaced; all other fields (grant_type, client_id, …)
	// are kept as-is. The container's auth.json has refresh_token="" so Codex CLI
	// sends an empty string here; the proxy substitutes the host's real token.
	controller := http.NewResponseController(w)
	deadlineSet := controller.SetReadDeadline(time.Now().Add(openAIRefreshBodyTimeout)) == nil
	defer func() {
		if deadlineSet {
			_ = controller.SetReadDeadline(time.Time{})
		}
	}()
	body, err := io.ReadAll(io.LimitReader(r.Body, openAIRefreshBodyLimit+1))
	if deadlineSet {
		_ = controller.SetReadDeadline(time.Time{})
		deadlineSet = false
	}
	if err != nil {
		if lease.Err() != nil {
			http.Error(w, "token invalid or expired", http.StatusUnauthorized)
			return
		}
		if r.Context().Err() != nil {
			http.Error(w, "request body timeout", http.StatusRequestTimeout)
			return
		}
		http.Error(w, "reading request body", http.StatusBadRequest)
		return
	}
	if int64(len(body)) > openAIRefreshBodyLimit {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	var reqBody map[string]interface{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &reqBody); err != nil {
			http.Error(w, "parsing request body", http.StatusBadRequest)
			return
		}
	} else {
		reqBody = map[string]interface{}{}
	}
	if reqBody == nil {
		http.Error(w, "request body must be a JSON object", http.StatusBadRequest)
		return
	}
	if r.Context().Err() != nil {
		http.Error(w, "token invalid or expired", http.StatusUnauthorized)
		return
	}

	p.openAIRefreshMu.Lock()
	defer p.openAIRefreshMu.Unlock()
	// A refresh queued behind another request must recheck the exact lease and
	// use the most recently rotated host refresh token.
	p.mu.RLock()
	current := p.tokensByValue[cdxToken]
	valid := current == found && lease.Err() == nil && time.Now().Before(found.ExpiresAt) && p.oauthCreds != nil
	var refreshToken string
	if valid {
		refreshToken = p.oauthCreds.RefreshToken
	}
	p.mu.RUnlock()
	if !valid {
		http.Error(w, "token invalid or expired", http.StatusUnauthorized)
		return
	}
	// Replace only the refresh_token; leave grant_type, client_id, etc. unchanged.
	reqBody["refresh_token"] = refreshToken

	newBody, err := json.Marshal(reqBody)
	if err != nil {
		http.Error(w, "encoding request body", http.StatusInternalServerError)
		return
	}

	// Forward to the real OpenAI OAuth endpoint with Content-Type: application/json.
	upstreamCtx, stopUpstream := context.WithTimeout(r.Context(), openAIRefreshUpstreamTimeout)
	defer stopUpstream()
	req, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost, p.oauthTokenURL,
		bytes.NewReader(newBody))
	if err != nil {
		http.Error(w, "creating refresh request", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		http.Error(w, "calling OAuth endpoint failed", http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, openAIRefreshResponseLimit+1))
	if err != nil {
		http.Error(w, "reading OAuth response", http.StatusInternalServerError)
		return
	}
	if int64(len(respBody)) > openAIRefreshResponseLimit {
		http.Error(w, "OAuth response too large", http.StatusBadGateway)
		return
	}
	if lease.Err() != nil || r.Context().Err() != nil {
		http.Error(w, "token invalid or expired", http.StatusUnauthorized)
		return
	}

	if resp.StatusCode != http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(resp.StatusCode)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": workerRefreshErrorCode(respBody)})
		return
	}

	var tokenResp map[string]interface{}
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		http.Error(w, "parsing OAuth response", http.StatusInternalServerError)
		return
	}
	if access, ok := tokenResp["access_token"].(string); !ok || access == "" {
		http.Error(w, "OAuth response missing access token", http.StatusBadGateway)
		return
	}
	// Persist the next generation before exposing it to other requests.
	p.mu.RLock()
	next := *p.oauthCreds
	p.mu.RUnlock()
	if newAccess, ok := tokenResp["access_token"].(string); ok && newAccess != "" {
		next.AccessToken = newAccess
	}
	if newID, ok := tokenResp["id_token"].(string); ok && newID != "" {
		next.IDToken = newID
	}
	// Rotate the host's refresh_token if the server issued a new one (RFC 6749 §6).
	if newRefresh, ok := tokenResp["refresh_token"].(string); ok && newRefresh != "" {
		next.RefreshToken = newRefresh
	}
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	if lease.Err() != nil || r.Context().Err() != nil {
		http.Error(w, "token invalid or expired", http.StatusUnauthorized)
		return
	}
	if err := p.persistOAuthState("openai", &next); err != nil {
		http.Error(w, "saving OAuth refresh failed", http.StatusBadGateway)
		return
	}
	p.mu.Lock()
	*p.oauthCreds = next
	p.mu.Unlock()
	if lease.Err() != nil || r.Context().Err() != nil {
		http.Error(w, "token invalid or expired", http.StatusUnauthorized)
		return
	}

	// Construct a fresh response: deleting known secret fields would still leak
	// future token fields or nested diagnostics returned by the issuer.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"access_token": found.Token,
		"id_token":     workerIDToken(next),
	})

	if p.cfg.Verbose {
		fmt.Printf("OAuth token refreshed for container %s\n", found.ContainerName)
	}
}

// handleAPIProxy proxies /v1/* to the real Responses API backend.
// In API key mode: forwards to https://api.openai.com/v1/
// In OAuth/ChatGPT mode: forwards to https://chatgpt.com/backend-api/codex/
//
// Containers should set OPENAI_BASE_URL=http://<proxy>/v1 so Codex CLI routes
// all Responses API traffic through the proxy.
func (p *Proxy) handleAPIProxy(w http.ResponseWriter, r *http.Request) {
	var base string
	if p.oauthCreds != nil {
		base = p.chatgptURL + "/codex"
	} else {
		base = p.apiUpstreamURL
	}
	p.reverseProxy(w, r, "/v1", base, p.injectCredentials)
}

// handleChatGPTProxy proxies /chatgpt/* to https://chatgpt.com/backend-api/*.
// Containers in ChatGPT auth mode set chatgpt_base_url=http://<proxy>/chatgpt/
// in their Codex CLI config so backend-api calls (rate limits, account info, etc.)
// flow through the proxy.
func (p *Proxy) handleChatGPTProxy(w http.ResponseWriter, r *http.Request) {
	p.reverseProxy(w, r, "/chatgpt", p.chatgptURL, p.injectCredentials)
}

// injectCredentials overwrites Authorization (and ChatGPT-Account-Id in OAuth mode)
// in h with the real host credentials, replacing any placeholder value the container sent.
// Safe to call concurrently.
func (p *Proxy) injectCredentials(h http.Header) {
	if p.oauthCreds != nil {
		p.mu.RLock()
		accessToken := p.oauthCreds.AccessToken
		accountID := p.oauthCreds.AccountID
		p.mu.RUnlock()
		h.Set("Authorization", "Bearer "+accessToken)
		if accountID != "" {
			h.Set("ChatGPT-Account-Id", accountID)
		}
		return
	}
	if p.apiKey != "" {
		h.Set("Authorization", "Bearer "+p.apiKey)
	}
}
