package authproxy

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
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
	stopConnection := closeThrottledConnectionOnLeaseEnd(ctx, lease)
	return r.WithContext(ctx), func() { stop(); stopConnection(); cancel() }, true
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

// The bandwidth limiter runs below TLS. Closing the raw socket interrupts its
// wait without taking tls.Conn's write lock, which the blocked writer owns.
type workerConnectionKey struct{}

func (p *Proxy) workerConnectionContext(ctx context.Context, conn net.Conn) context.Context {
	if p.cfg.NetworkResources.BytesPerSecond == 0 && !p.cfg.isEgress() {
		return ctx
	}
	if secure, ok := conn.(*tls.Conn); ok {
		conn = secure.NetConn()
	}
	return context.WithValue(ctx, workerConnectionKey{}, conn)
}

func closeThrottledConnectionOnLeaseEnd(ctx, lease context.Context) func() {
	conn, ok := ctx.Value(workerConnectionKey{}).(net.Conn)
	if !ok {
		return func() {}
	}
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(finished)
		// An ordinary request finishing or one HTTP/2 stream being canceled must
		// not tear down another valid request on the same connection.
		if lease.Err() != nil || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			_ = conn.Close()
		}
	})
	return func() {
		// Finish any running callback before HTTP/1 can reuse this connection for
		// a subsequent request. Unregister before the normal request cancel.
		if !stop() {
			<-finished
		}
	}
}
