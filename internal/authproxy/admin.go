package authproxy

import (
	"encoding/json"
	"github.com/pacificbelt30/codex-dock/internal/strictjson"
	"net/http"

	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

func (p *Proxy) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if p.cfg.AdminSecret == "" {
		return true
	}
	if r.Header.Get("X-Proxy-Admin-Secret") != p.cfg.AdminSecret {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func (p *Proxy) handleAdminIssue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !p.requireAdmin(w, r) {
		return
	}
	var req struct {
		Container   string                `json:"container"`
		TTL         int                   `json:"ttl"`
		Identity    identity.Identity     `json:"identity"`
		ModelPolicy *identity.ModelPolicy `json:"model_policy,omitempty"`
		UsagePolicy *identity.UsagePolicy `json:"usage_policy,omitempty"`
	}
	if err := strictjson.Decode(r.Body, 96<<10, &req); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Container == "" {
		http.Error(w, "missing container", http.StatusBadRequest)
		return
	}
	if req.TTL == 0 {
		req.TTL = p.cfg.TokenTTL
	}
	if err := p.validateLeaseTTL(req.TTL); err != nil {
		http.Error(w, "invalid token TTL", http.StatusBadRequest)
		return
	}
	t, err := p.IssueTokenWithUsage(req.Container, req.TTL, req.Identity, req.ModelPolicy, req.UsagePolicy)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"token": t})
}

func (p *Proxy) handleAdminRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !p.requireAdmin(w, r) {
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

func (p *Proxy) handleAdminRevokeRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !p.requireAdmin(w, r) {
		return
	}
	var in identity.Identity
	if err := strictjson.Decode(r.Body, 8192, &in); err != nil || in.Validate() != nil || p.cfg.CaseID != "" && in.CaseID != p.cfg.CaseID {
		http.Error(w, "invalid run identity", http.StatusBadRequest)
		return
	}
	if err := p.RevokeRun(r.Context(), in); err != nil {
		http.Error(w, "invalid run identity", 400)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (p *Proxy) handleAdminRevokeCase(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !p.requireAdmin(w, r) {
		return
	}
	if p.credentialScope() == "" {
		http.Error(w, "not a case proxy", http.StatusBadRequest)
		return
	}
	p.revokeCaseTokens()
	w.WriteHeader(http.StatusNoContent)
}

func (p *Proxy) handleAdminMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !p.requireAdmin(w, r) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if p.cfg.ProfileName != "" {
		w.Header().Set("X-Cxd-Auth-Profile", p.cfg.ProfileName)
		w.Header().Set("X-Cxd-Connection-Version", p.caseConnectionVersion)
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{
		"worker_token_auth":    true,
		"worker_mtls":          p.WorkerTransportEnabled(),
		"token_usage_policy":   p.usageLedger != nil,
		"model_authorization":  true,
		"model_policy_active":  len(p.cfg.AllowedModels) != 0,
		"credential_broker":    true,
		"openai_available":     p.apiKey != "" || p.IsOAuthMode(),
		"oauth_mode":           p.IsOAuthMode(),
		"anthropic_available":  p.IsAnthropicMode(),
		"anthropic_oauth_mode": p.IsAnthropicOAuthMode(),
		"token_identity":       true,
		"token_model_policy":   true,
		"case_proxy":           p.cfg.CaseID != "",
		"independent_profile":  p.cfg.ProfileName != "",
	})
}
