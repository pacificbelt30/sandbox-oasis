package authproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/networkpolicy"
)

func compileForwardPolicy(cfg Config) (networkpolicy.Policy, error) {
	rules := cfg.ForwardPolicy
	if len(cfg.ForwardAllowDomains) != 0 {
		if len(rules.AllowDomains) != 0 {
			return nil, fmt.Errorf("set only one forward domain allowlist")
		}
		rules.AllowDomains = cfg.ForwardAllowDomains
	}
	rules.BlockPrivate = rules.BlockPrivate || cfg.BlockPrivate
	policy, err := networkpolicy.Compile(rules)
	if err != nil {
		return nil, err
	}
	return networkpolicy.All(policy, cfg.EgressPolicy), nil
}

func (p *Proxy) forwardPolicy() (networkpolicy.Policy, error) {
	if p.egressPolicy != nil {
		return p.egressPolicy, nil
	}
	return compileForwardPolicy(p.cfg)
}

func newProxyTransport(dial func(context.Context, string, string) (net.Conn, error)) *http.Transport {
	return &http.Transport{
		// Egress must reach the policy-checked destination directly. An ambient
		// HTTP_PROXY would instead resolve/dial another proxy and bypass IP rules.
		DialContext: dial, ForceAttemptHTTP2: true, MaxIdleConns: 100,
		IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

type policyTransport struct {
	base   http.RoundTripper
	policy networkpolicy.Policy
}

func (t policyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.User != nil {
		return nil, fmt.Errorf("%w: URL credentials are not supported", networkpolicy.ErrDenied)
	}
	port := ""
	switch r.URL.Scheme {
	case "http":
		port = "80"
	case "https":
		port = "443"
	default:
		return nil, fmt.Errorf("%w: unsupported URL scheme", networkpolicy.ErrDenied)
	}
	dst, err := networkpolicy.ParseDestination(r.URL.Host, port)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid URL authority", networkpolicy.ErrDenied)
	}
	if err := t.policy.CheckDestination(r.Context(), dst); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(r)
}

// Auth upstreams keep their independent, fixed routes and private-IP guard.
// Forward domain/port rules never accidentally block the AI credential routes.
func (p *Proxy) guardedDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	policy := p.privatePolicy
	if policy == nil {
		var err error
		policy, err = networkpolicy.Compile(networkpolicy.Rules{BlockPrivate: p.cfg.BlockPrivate})
		if err != nil {
			return nil, err
		}
	}
	return (networkpolicy.Dialer{Policy: policy, Connect: p.connectVerified}).DialContext(ctx, network, addr)
}

func isBlockedDestErr(err error) bool { return errors.Is(err, networkpolicy.ErrDenied) }

func (p *Proxy) requestForwardPolicy(r *http.Request) (networkpolicy.Policy, error) {
	if policy, ok := r.Context().Value(egressPolicyKey{}).(*egressEpoch); ok {
		return policy.policy, nil
	}
	return p.forwardPolicy()
}
