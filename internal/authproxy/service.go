package authproxy

import "github.com/pacificbelt30/codex-dock/pkg/cxd/identity"

// Service describes the minimal functionality required by sandbox manager.
// Implementations can be in-process (Proxy) or external (RemoteProxy).
type Service interface {
	IssueToken(containerName string, ttlSec int) (string, error)
	RevokeToken(containerName string)
	IsOAuthMode() bool
	// IsAnthropicMode reports whether the proxy can serve Anthropic (Claude Code)
	// requests, in either API-key or OAuth mode.
	IsAnthropicMode() bool
	ContainerEndpoint() string
}

// ProfileService binds a retained worker to its original independent login.
// OAuth refresh changes the credential version, not this connection version.
type ProfileService interface {
	AuthProfile() string
	ProfileConnectionVersion() string
}

// IdentityIssuer is optional so legacy CLI launches retain their original
// token contract. A caller requesting identity must never silently downgrade.
type IdentityIssuer interface {
	IssueTokenWithIdentity(string, int, identity.Identity) (string, error)
}

// TokenRestorer is an admin-only capability for restarting a retained worker.
// Docker cannot replace a saved container's environment. The same placeholder
// is reauthorized explicitly on restart, and remains revoked while stopped.
type TokenRestorer interface {
	RestoreToken(containerName, token string, ttlSec int) error
}

type IdentityRestorer interface {
	RestoreTokenWithIdentity(containerName, token string, ttlSec int, id identity.Identity) error
}

// TokenRevoker reports revocation failures so cleanup can retain its journal.
type TokenRevoker interface {
	RevokeTokenChecked(containerName string) error
}

// PolicyIssuer and PolicyRestorer are optional, fail-closed capabilities.
// A nil policy retains the legacy proxy-wide authorization contract.
type PolicyIssuer interface {
	IssueTokenWithPolicy(string, int, identity.Identity, *identity.ModelPolicy) (string, error)
}
type PolicyRestorer interface {
	RestoreTokenWithPolicy(string, string, int, identity.Identity, *identity.ModelPolicy) error
}

type UsageIssuer interface {
	PrepareUsage(string, identity.Identity, *identity.UsagePolicy) error
	IssueTokenWithUsage(string, int, identity.Identity, *identity.ModelPolicy, *identity.UsagePolicy) (string, error)
	SupportsTokenUsagePolicy() bool
}
type UsageRestorer interface {
	RestoreTokenWithUsage(string, string, int, identity.Identity, *identity.ModelPolicy, *identity.UsagePolicy) error
}
