package authproxy

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/usage"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

func (p *Proxy) RestoreToken(name, token string, ttl int) error {
	return p.RestoreTokenWithIdentity(name, token, ttl, identity.Identity{})
}

func (p *Proxy) RestoreTokenWithIdentity(name, token string, ttl int, id identity.Identity) error {
	return p.RestoreTokenWithPolicy(name, token, ttl, id, nil)
}

func (p *Proxy) RestoreTokenWithPolicy(name, token string, ttl int, id identity.Identity, policy *identity.ModelPolicy) error {
	return p.RestoreTokenWithUsage(name, token, ttl, id, policy, nil)
}

func (p *Proxy) RestoreTokenWithUsage(name, token string, ttl int, id identity.Identity, policy *identity.ModelPolicy, limits *identity.UsagePolicy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	if err := p.checkCaseGeneration(); err != nil {
		return err
	}
	if err := p.checkTokenIdentity(id); err != nil {
		return err
	}
	if name == "" || len(name) > 255 || strings.ContainsAny(name, "\r\n\x00") || !strings.HasPrefix(token, "cdx-") || len(token) != 68 {
		return fmt.Errorf("invalid worker placeholder")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(token, "cdx-")); err != nil {
		return fmt.Errorf("invalid worker placeholder")
	}
	if ttl <= 0 {
		ttl = p.cfg.TokenTTL
	}
	if ttl <= 0 || ttl > 365*24*3600 {
		return fmt.Errorf("invalid token TTL")
	}
	rec := newTokenRecord(name, token, ttl)
	rec.Identity = id
	rec.ModelPolicy = policy.Clone()
	if limits != nil {
		if p.usageLedger == nil {
			rec.cancel()
			return fmt.Errorf("durable usage storage required")
		}
		if _, err := p.usageLedger.Inspect(usage.Key(name, id)); err != nil {
			rec.cancel()
			return fmt.Errorf("usage history missing; restore refused")
		}
	}
	if err := p.bindTokenUsage(rec, limits); err != nil {
		rec.cancel()
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.caseRevoked || p.revokedRuns[id] {
		rec.cancel()
		return fmt.Errorf("credential case or run was revoked")
	}
	if existing := p.tokensByValue[token]; existing != nil && existing.ContainerName != name {
		// The new lease has not been installed; do not alter the existing token.
		rec.cancel()
		return fmt.Errorf("placeholder already belongs to another worker")
	}
	p.replaceTokenLocked(rec)
	if rec.UsagePolicy != nil {
		if p.usageBindings == nil {
			p.usageBindings = make(map[string]string)
		}
		p.usageBindings[name] = rec.usageKey
	}
	return nil
}

func (p *Proxy) handleAdminRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !p.requireAdmin(w, r) {
		return
	}
	var req struct {
		Container   string                `json:"container"`
		Token       string                `json:"token"`
		TTL         int                   `json:"ttl"`
		Identity    identity.Identity     `json:"identity"`
		ModelPolicy *identity.ModelPolicy `json:"model_policy,omitempty"`
		UsagePolicy *identity.UsagePolicy `json:"usage_policy,omitempty"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 96<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid json", 400)
		return
	}
	if err := p.RestoreTokenWithUsage(req.Container, req.Token, req.TTL, req.Identity, req.ModelPolicy, req.UsagePolicy); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *RemoteProxy) RestoreToken(name, token string, ttl int) error {
	return r.RestoreTokenWithIdentity(name, token, ttl, identity.Identity{})
}

func (r *RemoteProxy) RestoreTokenWithIdentity(name, token string, ttl int, id identity.Identity) error {
	return r.RestoreTokenWithPolicy(name, token, ttl, id, nil)
}
func (r *RemoteProxy) RestoreTokenWithPolicy(name, token string, ttl int, id identity.Identity, policy *identity.ModelPolicy) error {
	return r.RestoreTokenWithUsage(name, token, ttl, id, policy, nil)
}

func (r *RemoteProxy) RestoreTokenWithUsage(name, token string, ttl int, id identity.Identity, policy *identity.ModelPolicy, limits *identity.UsagePolicy) error {
	if err := limits.Validate(); err != nil {
		return err
	}
	if limits != nil && !r.tokenUsagePolicy {
		return fmt.Errorf("auth proxy does not support durable execution usage limits")
	}
	if err := policy.Validate(); err != nil {
		return err
	}
	if policy != nil && !r.tokenModelPolicy {
		return fmt.Errorf("auth proxy does not support token model policy")
	}
	if !id.Empty() && !r.identityMode {
		return fmt.Errorf("auth proxy does not support token identity")
	}
	body, err := json.Marshal(map[string]any{"container": name, "token": token, "ttl": ttl, "identity": id, "model_policy": policy, "usage_policy": limits})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, r.adminURL+"/admin/restore", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if r.adminSecret != "" {
		req.Header.Set(r.adminSecretHeader, r.adminSecret)
	}
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("restoring worker token failed (status %d); rebuild/restart the auth proxy with this version", resp.StatusCode)
	}
	return nil
}
