package authproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// handleAnthropicProxy proxies /anthropic/* to https://api.anthropic.com/*.
// Containers set ANTHROPIC_BASE_URL=http://<proxy>/anthropic so Claude Code
// routes all Messages API traffic through the proxy. The proxy injects the
// host's real Anthropic credential (API key or OAuth bearer) on every request,
// replacing whatever placeholder the container sent.
func (p *Proxy) handleAnthropicProxy(w http.ResponseWriter, r *http.Request) {
	p.reverseProxy(w, r, "/anthropic", p.anthropicUpstreamURL, p.injectAnthropicCredentials)
}

// injectAnthropicCredentials overwrites the Anthropic auth headers in h with the
// host's real credentials, replacing any placeholder the container sent.
//   - API-key mode: sets x-api-key, removes Authorization.
//   - OAuth mode:  sets Authorization: Bearer <access_token>, removes x-api-key,
//     and adds the required anthropic-beta OAuth header.
//
// The authenticated reverse route refreshes OAuth before reaching this injector.
func (p *Proxy) injectAnthropicCredentials(h http.Header) {
	// Anthropic requires anthropic-version; supply a default if the client omits it.
	if h.Get("anthropic-version") == "" {
		h.Set("anthropic-version", defaultAnthropicVersion)
	}

	if p.anthropicOAuth != nil {
		p.mu.RLock()
		accessToken := p.anthropicOAuth.AccessToken
		p.mu.RUnlock()
		h.Del("x-api-key")
		h.Set("Authorization", "Bearer "+accessToken)
		mergeBetaHeader(h, anthropicOAuthBetaHeader)
		return
	}
	if p.anthropicAPIKey != "" {
		h.Del("Authorization")
		h.Set("x-api-key", p.anthropicAPIKey)
	}
}

// mergeBetaHeader ensures value is present in the anthropic-beta header without
// dropping any beta flags the client already requested.
func mergeBetaHeader(h http.Header, value string) {
	existing := h.Get("anthropic-beta")
	if existing == "" {
		h.Set("anthropic-beta", value)
		return
	}
	for _, part := range strings.Split(existing, ",") {
		if strings.EqualFold(strings.TrimSpace(part), value) {
			return
		}
	}
	h.Set("anthropic-beta", existing+","+value)
}

// refreshAnthropicOAuthIfNeeded refreshes the host's Anthropic OAuth access token
// within 60s of expiring. Unknown expiry (setup-token) is used as supplied.
// The refresh token never leaves the proxy. Failures prevent upstream inference.
func (p *Proxy) refreshAnthropicOAuthIfNeeded(ctx context.Context) error {
	// Provider mode is fixed at proxy construction. API-key requests must not
	// wait behind an unrelated OpenAI OAuth refresh on the shared refresh lock.
	if p.anthropicOAuth == nil {
		return nil
	}
	p.anthropicRefreshMu.Lock()
	defer p.anthropicRefreshMu.Unlock()
	p.mu.RLock()
	if p.anthropicOAuth == nil {
		p.mu.RUnlock()
		return nil
	}
	expiresAt := p.anthropicOAuth.ExpiresAt
	refreshToken := p.anthropicOAuth.RefreshToken
	p.mu.RUnlock()

	// expiresAt == 0 means unknown; in that case do not attempt a refresh
	// (the current token is assumed valid).
	if expiresAt == 0 {
		return nil
	}
	if time.Now().UnixMilli() < expiresAt-60_000 {
		return nil
	}
	if refreshToken == "" {
		if time.Now().UnixMilli() >= expiresAt {
			return fmt.Errorf("anthropic OAuth credential expired without refresh token")
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	body, _ := json.Marshal(map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     p.anthropicOAuthClientID,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.anthropicOAuthTokenURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating Anthropic refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		if p.cfg.Verbose {
			fmt.Printf("Anthropic OAuth refresh failed: %v\n", err)
		}
		return fmt.Errorf("anthropic refresh request failed")
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil || resp.StatusCode != http.StatusOK {
		if p.cfg.Verbose {
			fmt.Printf("Anthropic OAuth refresh returned status %d\n", resp.StatusCode)
		}
		return fmt.Errorf("anthropic refresh returned status %d", resp.StatusCode)
	}

	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if len(respBody) > 1<<20 {
		return fmt.Errorf("anthropic refresh response too large")
	}
	if err := json.Unmarshal(respBody, &tr); err != nil || tr.AccessToken == "" || tr.ExpiresIn <= 0 || tr.ExpiresIn > 365*24*3600 {
		return fmt.Errorf("invalid Anthropic refresh response")
	}

	p.mu.RLock()
	next := *p.anthropicOAuth
	p.mu.RUnlock()
	next.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		next.RefreshToken = tr.RefreshToken
	}
	if tr.ExpiresIn > 0 {
		next.ExpiresAt = time.Now().UnixMilli() + tr.ExpiresIn*1000
	}
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	if err := p.persistOAuthState("anthropic", &next); err != nil {
		if p.cfg.Verbose {
			fmt.Printf("Anthropic OAuth refresh state save failed: %v\n", err)
		}
		return fmt.Errorf("saving Anthropic refresh state: %w", err)
	}
	p.mu.Lock()
	*p.anthropicOAuth = next
	p.mu.Unlock()
	if p.cfg.Verbose {
		fmt.Println("Anthropic OAuth access token refreshed")
	}
	return nil
}
