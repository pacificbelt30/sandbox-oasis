package authproxy

import (
	"context"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
	"net/http"
	"strings"
	"time"
)

// workerRequest authenticates before any upstream dial or credential injection.
// A lease also bounds WebSocket connections, which can carry multiple turns.
func (p *Proxy) workerRequest(r *http.Request, allowAPIKey bool) (*http.Request, func(), bool) {
	var token string
	values := r.Header.Values("Authorization")
	if len(values) > 1 {
		return nil, nil, false
	}
	if len(values) == 1 {
		fields := strings.Fields(values[0])
		if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") {
			return nil, nil, false
		}
		token = fields[1]
	}
	if allowAPIKey {
		keys := r.Header.Values("X-Api-Key")
		if len(keys) > 1 {
			return nil, nil, false
		}
		if len(keys) == 1 {
			if keys[0] == "" || token != "" && token != keys[0] {
				return nil, nil, false
			}
			token = keys[0]
		}
	}
	if token == "" || !p.transportBound(r, token) {
		return nil, nil, false
	}
	p.mu.RLock()
	var lease context.Context
	var policy *identity.ModelPolicy
	var budget *tokenUsage
	if rec := p.tokensByValue[token]; rec != nil && time.Now().Before(rec.ExpiresAt) && rec.context != nil && rec.context.Err() == nil && p.brokerProviderAllowed(rec.Identity.Provider, r.URL.Path) {
		lease = rec.context
		policy = rec.ModelPolicy
		if rec.UsagePolicy != nil {
			budget = &tokenUsage{key: rec.usageKey, policy: rec.UsagePolicy}
		}
	}
	p.mu.RUnlock()
	if lease == nil {
		return nil, nil, false
	}
	parent := context.WithValue(r.Context(), modelPolicyKey{}, policy)
	if budget != nil {
		parent = context.WithValue(parent, usagePolicyKey{}, budget)
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(lease, cancel)
	return r.WithContext(ctx), func() { stop(); cancel() }, true
}

func providerRouteAllowed(provider, path string) bool {
	switch provider {
	case "", "any":
		return true
	case "openai":
		return strings.HasPrefix(path, "/v1/") || strings.HasPrefix(path, "/chatgpt/")
	case "anthropic":
		return strings.HasPrefix(path, "/anthropic/")
	default:
		return false
	}
}
