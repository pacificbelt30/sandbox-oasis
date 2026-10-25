package cxd

import (
	"context"

	"github.com/pacificbelt30/codex-dock/internal/authproxy"
)

// Capabilities describes the validated node connected by Open. Features are
// additive strings; callers must check a feature before relying on it.
type Capabilities struct {
	BuildVersion    string
	ProtocolVersion int
	Features        []string
}

func (c *Client) Capabilities() Capabilities {
	version, features := c.node.Capabilities()
	return Capabilities{BuildVersion: version, ProtocolVersion: c.node.ProtocolVersion(), Features: features}
}

type ProxyCapabilities struct {
	ModelAuthorization bool
	TokenModelPolicy   bool
	TokenUsagePolicy   bool
	ModelPolicyActive  bool
	CaseProxy          bool
	CredentialBroker   bool
	TokenIdentity      bool
	OpenAIOAuth        bool
	Anthropic          bool
	AnthropicOAuth     bool
}

// ProxyCapabilities probes the configured auth proxy. Node capabilities alone
// cannot establish which proxy image or credential mode is running.
func (c *Client) ProxyCapabilities(ctx context.Context) (ProxyCapabilities, error) {
	p, err := authproxy.NewRemoteProxyContext(ctx, c.opts.ProxyAdminURL, c.opts.ProxyContainerURL, c.opts.ProxyAdminSecret)
	if err != nil {
		return ProxyCapabilities{}, err
	}
	return ProxyCapabilities{TokenUsagePolicy: p.SupportsTokenUsagePolicy(), TokenModelPolicy: p.SupportsTokenModelPolicy(), ModelAuthorization: p.SupportsModelAuthorization(), ModelPolicyActive: p.ModelPolicyActive(), CredentialBroker: p.SupportsCredentialBroker(), CaseProxy: p.IsCaseProxy(), TokenIdentity: p.SupportsTokenIdentity(), OpenAIOAuth: p.IsOAuthMode(), Anthropic: p.IsAnthropicMode(), AnthropicOAuth: p.IsAnthropicOAuthMode()}, nil
}
