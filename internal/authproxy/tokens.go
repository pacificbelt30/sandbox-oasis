package authproxy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

// tokenRecord holds a single issued token and its metadata.
type tokenRecord struct {
	Token            string
	ContainerName    string
	ExpiresAt        time.Time
	IssuedAt         time.Time
	Identity         identity.Identity
	ModelPolicy      *identity.ModelPolicy
	UsagePolicy      *identity.UsagePolicy
	transportKeyHash string
	usageKey         string
	context          context.Context
	cancel           context.CancelFunc
}

func newTokenRecord(name, token string, ttl int) *tokenRecord {
	now := time.Now()
	expires := now.Add(time.Duration(ttl) * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), expires)
	return &tokenRecord{Token: token, ContainerName: name, ExpiresAt: expires, IssuedAt: now, context: ctx, cancel: cancel}
}

// IssueToken creates and stores a short-lived token for a container.
func (p *Proxy) IssueToken(containerName string, ttlSec int) (string, error) {
	return p.IssueTokenWithIdentity(containerName, ttlSec, identity.Identity{})
}

func (p *Proxy) IssueTokenWithIdentity(containerName string, ttlSec int, id identity.Identity) (string, error) {
	return p.IssueTokenWithPolicy(containerName, ttlSec, id, nil)
}

func (p *Proxy) IssueTokenWithPolicy(containerName string, ttlSec int, id identity.Identity, policy *identity.ModelPolicy) (string, error) {
	return p.IssueTokenWithUsage(containerName, ttlSec, id, policy, nil)
}

func (p *Proxy) IssueTokenWithUsage(containerName string, ttlSec int, id identity.Identity, policy *identity.ModelPolicy, limits *identity.UsagePolicy) (tokenValue string, resultErr error) {
	if containerName == "" || len(containerName) > 255 || strings.ContainsAny(containerName, "\r\n\x00") {
		return "", fmt.Errorf("invalid worker name")
	}
	if err := p.validateLeaseTTL(ttlSec); err != nil {
		return "", err
	}
	action := "issue"
	if policy != nil || limits != nil {
		action = "permission_change"
	}
	finish, auditErr := p.auditBegin(action, containerName)
	if auditErr != nil {
		return "", auditErr
	}
	defer func() {
		auditResult(finish, &resultErr)
		if resultErr != nil && tokenValue != "" {
			p.RevokeToken(containerName)
			tokenValue = ""
		}
	}()
	if err := policy.Validate(); err != nil {
		return "", err
	}
	if err := p.checkCaseGeneration(); err != nil {
		return "", err
	}
	if err := p.checkTokenIdentity(id); err != nil {
		return "", err
	}
	token, err := generateToken()
	if err != nil {
		return "", err
	}

	rec := newTokenRecord(containerName, token, ttlSec)
	rec.Identity = id
	rec.ModelPolicy = policy.Clone()
	if err := p.bindTokenUsage(rec, limits); err != nil {
		rec.cancel()
		return "", err
	}

	p.mu.Lock()
	if p.stopped || p.caseRevoked || p.revokedRuns[id] {
		p.mu.Unlock()
		rec.cancel()
		return "", fmt.Errorf("credential case or run was revoked")
	}
	p.replaceTokenLocked(rec)
	if rec.UsagePolicy != nil {
		if p.usageBindings == nil {
			p.usageBindings = make(map[string]string)
		}
		p.usageBindings[containerName] = rec.usageKey
	}
	p.mu.Unlock()

	if p.cfg.Verbose {
		fmt.Printf("Token issued for %s (TTL=%ds)\n", containerName, ttlSec)
	}
	return token, nil
}

func (p *Proxy) checkTokenIdentity(id identity.Identity) error {
	if p.cfg.CaseID != "" || p.cfg.ProfileName != "" && !id.Empty() {
		if err := id.Validate(); err != nil || id.CaseID != p.credentialScope() || id.ConnectionVersion != p.caseConnectionVersion {
			return fmt.Errorf("token identity does not match credential scope")
		}
	} else if !id.Empty() {
		return id.Validate()
	}
	return nil
}

// RevokeToken immediately revokes the token for a container.
func (p *Proxy) RevokeToken(containerName string) {
	finish := p.auditRestrictive("revoke", containerName)
	defer finish(nil)
	p.mu.Lock()
	p.removeTokenLocked(containerName)
	p.mu.Unlock()
	if p.cfg.Verbose {
		fmt.Printf("Token revoked for %s\n", containerName)
	}
}

// handleToken responds to a container requesting the API key (verified by token).
func (p *Proxy) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := r.Header.Get("X-Codex-Token")
	if token == "" || !p.transportBound(r, token) {
		http.Error(w, "missing X-Codex-Token header", http.StatusUnauthorized)
		return
	}

	p.mu.RLock()
	found := p.tokensByValue[token]
	p.mu.RUnlock()

	if found == nil || time.Now().After(found.ExpiresAt) {
		http.Error(w, "token invalid or expired", http.StatusUnauthorized)
		return
	}

	if p.cfg.Verbose {
		fmt.Printf("Credentials served to container %s\n", found.ContainerName)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if p.oauthCreds != nil {
		// OAuth mode: return a placeholder access_token (the container's CODEX_TOKEN).
		// The real access_token never leaves the proxy; it is injected by
		// reverseProxy/handleWebSocketProxy on every outbound API request.
		// id_token is a fresh dummy JWT containing selected account/plan metadata.
		// The real ID token, including its signature, stays on the host too.
		// refresh_token is intentionally withheld.
		p.mu.RLock()
		creds := *p.oauthCreds
		p.mu.RUnlock()
		_ = json.NewEncoder(w).Encode(map[string]string{
			"oauth_access_token": found.Token, // placeholder; real token injected by proxy
			"oauth_id_token":     workerIDToken(creds),
			"oauth_account_id":   creds.AccountID,
			"oauth_last_refresh": creds.LastRefresh,
			"container_name":     found.ContainerName,
			// oauth_refresh_token intentionally omitted
		})
	} else {
		// API key mode: return a placeholder api_key (the container's CODEX_TOKEN).
		// The real API key is injected by reverseProxy on every outbound request.
		_ = json.NewEncoder(w).Encode(map[string]string{
			"api_key":        found.Token, // placeholder; real key injected by proxy
			"container_name": found.ContainerName,
		})
	}
}

func (p *Proxy) handleHealth(w http.ResponseWriter, r *http.Request) {
	p.mu.RLock()
	count := len(p.tokens)
	p.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"status":        "ok",
		"active_tokens": count,
	})
}

func (p *Proxy) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	containerName := r.URL.Query().Get("container")
	if containerName == "" {
		http.Error(w, "missing container param", http.StatusBadRequest)
		return
	}
	p.RevokeToken(containerName)
	w.WriteHeader(http.StatusOK)
}

func (p *Proxy) expireLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case now := <-ticker.C:
			p.expireTokens(now)
		}
	}
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "cdx-" + hex.EncodeToString(b), nil
}

// RevokeRun invalidates the matching identity and cancels all active streams.
func (p *Proxy) RevokeRun(ctx context.Context, in identity.Identity) (resultErr error) {
	finish := p.auditRestrictive("revoke", in.RunID)
	defer func() { finish(resultErr) }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := in.Validate(); err != nil {
		return err
	}
	if p.cfg.CaseID != "" && in.CaseID != p.cfg.CaseID {
		return fmt.Errorf("run belongs to another case")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.revokedRuns == nil {
		p.revokedRuns = make(map[identity.Identity]bool)
	}
	p.revokedRuns[in] = true
	for name, rec := range p.tokens {
		if rec.Identity == in {
			p.removeTokenLocked(name)
		}
	}
	return nil
}

// Both indexes and lease cancellation change under the same lock. In
// particular, replacing or restoring a worker must remove its previous value.
func (p *Proxy) replaceTokenLocked(rec *tokenRecord) {
	p.removeTokenLocked(rec.ContainerName)
	p.tokens[rec.ContainerName] = rec
	p.tokensByValue[rec.Token] = rec
}

func (p *Proxy) removeTokenLocked(name string) {
	if rec := p.tokens[name]; rec != nil {
		if rec.cancel != nil {
			rec.cancel()
		}
		delete(p.tokensByValue, rec.Token)
		delete(p.tokens, name)
	}
}

func (p *Proxy) expireTokens(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for name, rec := range p.tokens {
		if now.After(rec.ExpiresAt) {
			p.removeTokenLocked(name)
			if p.cfg.Verbose {
				fmt.Printf("Token expired for %s\n", name)
			}
		}
	}
}
