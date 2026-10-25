package authproxy

import (
	"github.com/pacificbelt30/codex-dock/internal/networkpolicy"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/credentialstore"
	"io"
)

// Proxy roles. The credential-injecting reverse proxy and the general-traffic
// forward proxy run as two separate containers so the one holding real
// credentials never forwards arbitrary traffic.
const (
	// RoleAuth serves the credential-injecting reverse routes (/v1, /chatgpt,
	// /anthropic), the token endpoints, and admin. It does NOT forward general
	// traffic. This is the default (also used by the in-process proxy and tests).
	RoleAuth = "auth"
	// RoleEgress serves only the HTTP/CONNECT forward proxy for general traffic
	// (git/npm/pip). It holds no credentials and exposes no admin/token routes.
	RoleEgress = "egress"
)

// Config configures the Auth Proxy.
type Config struct {
	// AuditDirectory stores bounded operation evidence. Managed proxies default to UsageStateDir/audit.
	AuditDirectory string
	// UsageStateDir is an exclusive owner-private durable execution ledger.
	// Limits are unavailable without it; never place this directory in workers.
	UsageStateDir string
	// AllowedModels restricts fixed AI routes to exact model IDs. Empty keeps
	// legacy forwarding. Unsupported endpoints and WebSockets are denied.
	AllowedModels        []string
	CredentialRoutes     []CredentialRoute
	CredentialRoutesFile string
	BrokerOnly           bool
	UpstreamProxyURL     string
	CABundlePEM          []byte
	// EventWriter receives credential-free JSON denial records. Nil keeps recording in memory only.
	EventWriter io.Writer
	PolicyID    string

	// CredentialStore and CaseID opt into credentials supplied by a host product.
	// Each case must run in a distinct auth proxy instance. The default nil
	// store retains the existing host-file behavior.
	CredentialStore credentialstore.Store
	CaseID          string
	// ProfileName selects an independent local profile through CredentialStore.
	// Unlike a multi-user case proxy, it retains the CLI's unscoped token API.
	// It is mutually exclusive with CaseID and never enables host fallback.
	ProfileName string
	// OAuthStateDir stores rotated credentials outside the read-only login files.
	// Empty keeps the previous in-memory behavior (mainly useful in tests).
	OAuthStateDir string
	TokenTTL      int
	Verbose       bool
	ListenAddr    string // worker-facing TCP address. Defaults to "0.0.0.0:0".
	AdminSecret   string

	// Role selects what this instance serves: RoleAuth (reverse + token + admin,
	// no forwarding) or RoleEgress (forward proxy only, no credentials). Empty is
	// treated as RoleAuth for backward compatibility.
	Role string

	// AdminListenAddr, when non-empty, serves the /admin/* routes on a separate
	// listener so they are not reachable on the worker-facing data-plane port.
	// When empty, /admin/* is served on the main listener (in-process/test use).
	// Only meaningful for RoleAuth.
	AdminListenAddr string

	// ForwardAllowDomains optionally restricts the forward proxy (RoleEgress) to
	// the listed domains (and their subdomains). Empty means allow all hosts.
	ForwardAllowDomains []string

	// ForwardPolicy restricts general egress, independently of fixed AI routes.
	// EgressPolicy can add custom policy decisions without weakening these rules.
	ForwardPolicy networkpolicy.Rules
	EgressPolicy  networkpolicy.Policy

	// BlockPrivate, when true, makes the forward proxy and all upstream dials
	// refuse destinations that resolve to private/loopback/link-local ranges
	// (RFC1918, 127/8, 169.254/16 incl. cloud metadata, ULA, CGNAT). This stops a
	// worker from pivoting into the host LAN. Off by default so in-process/test
	// proxies can reach 127.0.0.1 upstreams.
	BlockPrivate bool
}

// isEgress reports whether the proxy is the forward-only egress instance.
func (c Config) isEgress() bool { return c.Role == RoleEgress }
