package authproxy

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/networkpolicy"
	"github.com/pacificbelt30/codex-dock/internal/policyevents"
	"github.com/pacificbelt30/codex-dock/internal/usage"
	"github.com/pacificbelt30/codex-dock/internal/workertransport"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/credentialstore"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

// Proxy is the Auth Proxy server that issues short-lived tokens to containers.
type Proxy struct {
	transportCA           *workertransport.Authority
	usageLedger           *usage.Ledger
	usageBindings         map[string]string
	rootCAs               *x509.CertPool
	upstreamProxy         *url.URL
	cfg                   Config
	events                *policyevents.Store
	privatePolicy         networkpolicy.Policy
	apiKey                string
	oauthCreds            *OAuthCredentials // non-nil when operating in OAuth mode
	listener              net.Listener
	server                *http.Server
	mu                    sync.RWMutex
	refreshMu             sync.Mutex                  // serialize refreshes across both providers
	oauthSource           map[string]string           // hash of read-only login refresh token
	storeCreds            credentialstore.Credentials // protected by refreshMu
	storeVersion          string                      // protected by refreshMu
	caseConnectionVersion string                      // immutable after initialization
	tokens                map[string]*tokenRecord     // containerName -> record
	tokensByValue         map[string]*tokenRecord     // token -> same record, protected by mu
	revokedRuns           map[identity.Identity]bool
	caseRevoked           bool
	addr                  string

	// Admin listener (only used when Config.AdminListenAddr is set).
	adminListener net.Listener
	adminServer   *http.Server
	adminAddr     string

	// Anthropic (Claude Code) credentials. Populated independently of the
	// OpenAI credentials above so a single proxy can serve both agents.
	anthropicAPIKey string                     // non-empty in Anthropic API-key mode
	anthropicOAuth  *AnthropicOAuthCredentials // non-nil in Claude subscription (OAuth) mode

	// Upstream endpoints; overridable for testing.
	httpClient     *http.Client
	egressPolicy   networkpolicy.Policy
	oauthTokenURL  string // default: "https://auth.openai.com/oauth/token"
	apiUpstreamURL string // default: "https://api.openai.com/v1" (API key mode)
	chatgptURL     string // default: "https://chatgpt.com/backend-api" (OAuth mode + /chatgpt/)

	// Anthropic upstream + OAuth refresh endpoint; overridable for testing.
	anthropicUpstreamURL   string // default: "https://api.anthropic.com"
	anthropicOAuthTokenURL string // default: "https://platform.claude.com/v1/oauth/token"
	anthropicOAuthClientID string // Claude Code public OAuth client id
}

// AdminBindEgress is a sentinel host for AdminListenAddr meaning "bind the admin
// listener to the container's egress (primary) IPv4 address". This keeps /admin/*
// reachable from the host (via the published port, which DNATs to that IP) while
// making it unreachable from worker containers, which only know the proxy's IP on
// their own Internal network. Falls back to all interfaces if detection fails.
const AdminBindEgress = "egress"

// anthropicOAuthBetaHeader is required by the Anthropic API when authenticating
// with a Claude subscription OAuth bearer token instead of an API key.
const anthropicOAuthBetaHeader = "oauth-2025-04-20"

// defaultAnthropicVersion is sent when the container omits anthropic-version.
const defaultAnthropicVersion = "2023-06-01"

func (p *Proxy) AuthProfile() string              { return p.cfg.ProfileName }
func (p *Proxy) ProfileConnectionVersion() string { return p.caseConnectionVersion }

// NewProxy creates a new Auth Proxy.
// In OAuth mode (detected via IsOAuthAuth), credentials are loaded from
// ~/.codex/auth.json. In API key mode, the API key is loaded as usual.
func NewProxy(cfg Config) (result *Proxy, initErr error) {
	if cfg.WorkerMTLS && (cfg.isEgress() || len(cfg.AdminSecret) < 32 || cfg.AdminListenAddr == "") {
		return nil, fmt.Errorf("worker TLS requires an auth proxy and separate protected admin listener")
	}
	if cfg.isEgress() && cfg.UsageStateDir != "" {
		return nil, fmt.Errorf("execution usage limits belong on the auth proxy")
	}
	if cfg.UsageStateDir != "" && (cfg.AdminSecret == "" || cfg.AdminListenAddr == "") {
		return nil, fmt.Errorf("usage limits require a separate protected admin listener")
	}
	defer func() {
		if result != nil && cfg.UsageStateDir != "" {
			result.usageLedger, initErr = usage.Open(cfg.UsageStateDir)
			if initErr != nil {
				result.httpClient.CloseIdleConnections()
				result = nil
			}
		}
	}()
	if cfg.ProfileName != "" && (cfg.CredentialStore == nil || cfg.BrokerOnly || cfg.isEgress()) {
		return nil, fmt.Errorf("independent profile requires an auth-role credential store")
	}
	if err := ValidateAllowedModels(cfg.AllowedModels); err != nil {
		return nil, err
	}
	if cfg.isEgress() && len(cfg.AllowedModels) != 0 {
		return nil, fmt.Errorf("model authorization belongs on the auth proxy")
	}
	cfg.AllowedModels = append([]string(nil), cfg.AllowedModels...)
	if cfg.isEgress() && (len(cfg.CredentialRoutes) > 0 || cfg.CredentialRoutesFile != "") {
		return nil, fmt.Errorf("credential routes cannot run in the credential-free egress proxy")
	}
	if cfg.CredentialRoutesFile != "" {
		routes, err := LoadCredentialRoutes(cfg.CredentialRoutesFile)
		if err != nil {
			return nil, err
		}
		cfg.CredentialRoutes = append(cfg.CredentialRoutes, routes...)
	}
	cfg.CredentialRoutes = cloneRoutes(cfg.CredentialRoutes)
	if err := validateRoutes(cfg.CredentialRoutes); err != nil {
		return nil, err
	}
	if cfg.isEgress() && len(cfg.CredentialRoutes) > 0 {
		return nil, fmt.Errorf("credential routes cannot run in the credential-free egress proxy")
	}
	if cfg.Role != "" && cfg.Role != RoleAuth && cfg.Role != RoleEgress {
		return nil, fmt.Errorf("unknown proxy role %q", cfg.Role)
	}
	if cfg.CredentialStore != nil && (cfg.isEgress() || len(cfg.AdminSecret) < 32 || cfg.AdminListenAddr == "") {
		return nil, fmt.Errorf("external credential store requires an auth proxy with a separate protected admin listener")
	}
	p := &Proxy{
		cfg:                    cfg,
		oauthSource:            make(map[string]string),
		tokens:                 make(map[string]*tokenRecord),
		tokensByValue:          make(map[string]*tokenRecord),
		revokedRuns:            make(map[identity.Identity]bool),
		httpClient:             http.DefaultClient,
		oauthTokenURL:          "https://auth.openai.com/oauth/token",
		apiUpstreamURL:         "https://api.openai.com/v1",
		chatgptURL:             "https://chatgpt.com/backend-api",
		anthropicUpstreamURL:   "https://api.anthropic.com",
		anthropicOAuthTokenURL: "https://platform.claude.com/v1/oauth/token",
		anthropicOAuthClientID: "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
	}

	if cfg.WorkerMTLS {
		var err error
		p.transportCA, err = workertransport.NewAuthority()
		if err != nil {
			return nil, err
		}
	}
	if err := p.configureUpstream(); err != nil {
		return nil, err
	}
	p.events = policyevents.New(512, cfg.EventWriter)
	p.privatePolicy, _ = networkpolicy.Compile(networkpolicy.Rules{BlockPrivate: cfg.BlockPrivate})

	// When configured to block private destinations, route every outbound dial
	// (forward proxy and reverse upstreams) through a guard that rejects
	// private/loopback/link-local addresses. Off by default so tests keep using
	// 127.0.0.1 upstreams via http.DefaultClient.
	if cfg.BlockPrivate || cfg.UpstreamProxyURL != "" || len(cfg.CABundlePEM) > 0 {
		p.httpClient = &http.Client{Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           p.guardedDialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}}
	}

	if transport, ok := p.httpClient.Transport.(*http.Transport); ok {
		transport.TLSClientConfig = &tls.Config{RootCAs: p.rootCAs, MinVersion: tls.VersionTLS12}
		if p.upstreamProxy != nil {
			transport.Proxy = nil
		}
	}

	// The egress (forward-only) instance holds no credentials.
	if cfg.isEgress() {
		policy, err := compileForwardPolicy(cfg)
		if err != nil {
			return nil, err
		}
		p.egressPolicy = policy
		p.httpClient = &http.Client{Transport: newProxyTransport((networkpolicy.Dialer{Policy: policy, Connect: p.connectVerified}).DialContext)}
		if t, ok := p.httpClient.Transport.(*http.Transport); ok {
			t.TLSClientConfig = &tls.Config{RootCAs: p.rootCAs, MinVersion: tls.VersionTLS12}
		}
		return p, nil
	}
	if cfg.BrokerOnly {
		if len(cfg.CredentialRoutes) == 0 {
			return nil, fmt.Errorf("broker requires routes")
		}
		return p, nil
	}
	if cfg.CredentialStore != nil {
		if (cfg.CaseID == "") == (cfg.ProfileName == "") || cfg.OAuthStateDir != "" {
			return nil, fmt.Errorf("credential store requires exactly one case or profile and forbids legacy OAuth state")
		}
		if cfg.ProfileName != "" {
			if err := credentialstore.ValidateProfileName(cfg.ProfileName); err != nil {
				return nil, err
			}
		}
		loaded, err := cfg.CredentialStore.Load(context.Background(), p.credentialScope())
		if err != nil {
			return nil, fmt.Errorf("loading case credentials: %w", err)
		}
		if loaded.Version == "" || loaded.ConnectionVersion == "" || loaded.OpenAIAPIKey != "" && loaded.OpenAIOAuth != nil || loaded.AnthropicAPIKey != "" && loaded.AnthropicOAuth != nil {
			return nil, fmt.Errorf("invalid external credential generation")
		}
		if loaded.OpenAIAPIKey == "" && loaded.OpenAIOAuth == nil && loaded.AnthropicAPIKey == "" && loaded.AnthropicOAuth == nil {
			return nil, fmt.Errorf("external credential generation has no enabled provider")
		}
		if loaded.OpenAIOAuth != nil && (loaded.OpenAIOAuth.AccessToken == "" || loaded.OpenAIOAuth.RefreshToken == "") || loaded.AnthropicOAuth != nil && loaded.AnthropicOAuth.AccessToken == "" {
			return nil, fmt.Errorf("external OAuth credential is incomplete")
		}
		p.storeCreds, p.storeVersion, p.caseConnectionVersion = loaded, loaded.Version, loaded.ConnectionVersion
		p.apiKey, p.anthropicAPIKey = loaded.OpenAIAPIKey, loaded.AnthropicAPIKey
		if v := loaded.OpenAIOAuth; v != nil {
			p.oauthCreds = &OAuthCredentials{IDToken: v.IDToken, AccessToken: v.AccessToken, RefreshToken: v.RefreshToken, AccountID: v.AccountID, LastRefresh: v.LastRefresh, ExpiresAt: v.ExpiresAt, TokenType: v.TokenType}
		}
		if v := loaded.AnthropicOAuth; v != nil {
			p.anthropicOAuth = &AnthropicOAuthCredentials{AccessToken: v.AccessToken, RefreshToken: v.RefreshToken, ExpiresAt: v.ExpiresAt, SubscriptionType: v.SubscriptionType, Scopes: append([]string(nil), v.Scopes...)}
		}
		return p, nil
	}
	if cfg.ProfileName != "" {
		return nil, fmt.Errorf("independent profile requires a credential store")
	}

	if IsOAuthAuth() {
		creds, err := LoadOAuthCredentials()
		if err != nil {
			return nil, fmt.Errorf("loading OAuth credentials: %w", err)
		}
		p.oauthCreds = creds
		p.oauthSource["openai"] = oauthSourceHash(creds.RefreshToken)
		if err := p.restoreOAuthState("openai", creds.RefreshToken, creds); err != nil {
			return nil, err
		}
		if cfg.Verbose {
			fmt.Fprintln(os.Stderr, "Auth Proxy: OpenAI OAuth mode (placeholder tokens issued to containers; proxy injects real credentials on outbound requests)")
		}
	} else {
		apiKey := loadAPIKey()
		if apiKey == "" && cfg.Verbose {
			fmt.Fprintln(os.Stderr, "warning: no OpenAI API key found; Codex containers will not receive auth tokens")
		}
		p.apiKey = apiKey
	}

	// Anthropic (Claude Code) credentials are loaded independently so the proxy
	// can serve both agents from a single endpoint.
	if IsAnthropicOAuth() {
		creds, err := LoadAnthropicOAuthCredentials()
		if err != nil {
			return nil, fmt.Errorf("loading Anthropic OAuth credentials: %w", err)
		}
		p.anthropicOAuth = creds
		p.oauthSource["anthropic"] = oauthSourceHash(creds.RefreshToken)
		if err := p.restoreOAuthState("anthropic", creds.RefreshToken, creds); err != nil {
			return nil, err
		}
		if cfg.Verbose {
			fmt.Fprintln(os.Stderr, "Auth Proxy: Anthropic OAuth mode (Claude subscription; proxy injects real bearer token on outbound requests)")
		}
	} else if key := loadAnthropicAPIKey(); key != "" {
		p.anthropicAPIKey = key
		if cfg.Verbose {
			fmt.Fprintln(os.Stderr, "Auth Proxy: Anthropic API-key mode (proxy injects real x-api-key on outbound requests)")
		}
	}

	return p, nil
}

// IsAnthropicMode returns true when the proxy can serve Anthropic (Claude Code)
// requests, in either API-key or OAuth mode.
func (p *Proxy) IsAnthropicMode() bool {
	return p.anthropicOAuth != nil || p.anthropicAPIKey != ""
}

// IsAnthropicOAuthMode returns true when the proxy serves Anthropic requests
// using a Claude subscription OAuth token rather than an API key.
func (p *Proxy) IsAnthropicOAuthMode() bool {
	return p.anthropicOAuth != nil
}

// IsOAuthMode returns true when the proxy is operating in OAuth mode.
func (p *Proxy) IsOAuthMode() bool {
	return p.oauthCreds != nil
}

// Start begins listening on a random port on the configured address.
// If ListenAddr is empty it defaults to "0.0.0.0:0" (all interfaces).
// Binding to all interfaces allows worker containers to reach the proxy
// via host.docker.internal (resolves to the Docker bridge gateway IP).
func (p *Proxy) Start() error {
	addr := p.cfg.ListenAddr
	if addr == "" {
		addr = "0.0.0.0:0"
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	if err != nil {
		return fmt.Errorf("starting auth proxy: %w", err)
	}
	if p.transportCA != nil {
		ln = tls.NewListener(ln, p.transportCA.TLSConfig())
	}
	p.listener = ln
	p.addr = ln.Addr().String()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", p.handleHealth)

	// The egress (forward-only) instance serves no reverse routes, no token
	// endpoints, and no admin: just /health plus the forward proxy below.
	if p.cfg.isEgress() {
		p.server = &http.Server{Handler: p.workerFacingHandler(mux), ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 64 << 10}
		go func() {
			if err := p.server.Serve(ln); err != nil && err != http.ErrServerClosed {
				log.Printf("egress proxy error: %v", err)
			}
		}()
		go p.expireLoop()
		if p.cfg.Verbose {
			fmt.Printf("Egress proxy listening on %s (forward-only, no credentials)\n", p.addr)
		}
		return nil
	}

	// Data-plane mux (RoleAuth): routes the worker is allowed to use. The /admin/*
	// routes are registered here only when no separate admin listener is configured.
	mux.HandleFunc("/token", p.handleToken)
	if p.cfg.AdminListenAddr == "" {
		mux.HandleFunc("/revoke", p.handleRevoke)
	}
	// OAuth token refresh: Codex CLI calls this via CODEX_REFRESH_TOKEN_URL_OVERRIDE.
	// The proxy substitutes the host's real refresh_token so it never reaches containers.
	mux.HandleFunc("/oauth/token", p.handleOAuthTokenRefresh)
	// Responses API reverse proxy: containers set OPENAI_BASE_URL=http://proxy/v1.
	// Forwards to api.openai.com/v1 (API key mode) or chatgpt.com/backend-api/codex (OAuth mode).
	mux.HandleFunc("/v1/", p.handleAPIProxy)
	// ChatGPT backend-api reverse proxy: containers use chatgpt_base_url=http://proxy/chatgpt/.
	mux.HandleFunc("/chatgpt/", p.handleChatGPTProxy)
	// Anthropic (Claude Code) reverse proxy: containers set ANTHROPIC_BASE_URL=http://proxy/anthropic.
	// Forwards to api.anthropic.com, injecting the host's real API key or OAuth bearer token.
	mux.HandleFunc("/anthropic/", p.handleAnthropicProxy)
	if len(p.cfg.CredentialRoutes) > 0 {
		mux.HandleFunc("/broker/", p.handleBroker)
	}

	adminMux := http.NewServeMux()
	adminMux.HandleFunc("/admin/transport", p.handleAdminTransport)
	adminMux.HandleFunc("/admin/issue", p.handleAdminIssue)
	adminMux.HandleFunc("/admin/restore", p.handleAdminRestore)
	adminMux.HandleFunc("/admin/revoke", p.handleAdminRevoke)
	adminMux.HandleFunc("/admin/revoke-run", p.handleAdminRevokeRun)
	adminMux.HandleFunc("/admin/revoke-case", p.handleAdminRevokeCase)
	adminMux.HandleFunc("/admin/mode", p.handleAdminMode)
	adminMux.HandleFunc("/admin/usage", p.handleAdminUsage)
	adminMux.HandleFunc("/admin/usage/prepare", p.handleAdminPrepareUsage)

	if p.cfg.AdminListenAddr != "" {
		// Serve /admin/* on a dedicated listener so workers cannot reach token
		// issuance. With AdminBindEgress this binds to the egress IP only, so the
		// admin port is unreachable from the per-worker Internal networks.
		adminListen := resolveAdminListenAddr(p.cfg.AdminListenAddr)
		aln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", adminListen)
		if err != nil {
			_ = ln.Close()
			return fmt.Errorf("starting auth proxy admin listener: %w", err)
		}
		p.adminListener = aln
		p.adminAddr = aln.Addr().String()
		p.adminServer = &http.Server{Handler: adminMux}
		go func() {
			if err := p.adminServer.Serve(aln); err != nil && err != http.ErrServerClosed {
				log.Printf("auth proxy admin error: %v", err)
			}
		}()
		if p.cfg.AdminSecret == "" {
			log.Printf("warning: auth proxy admin listener has no --admin-secret; /admin/* is unauthenticated")
		}
	} else {
		// Single-listener mode (in-process/tests): admin routes share the main mux.
		mux.HandleFunc("/admin/issue", p.handleAdminIssue)
		mux.HandleFunc("/admin/restore", p.handleAdminRestore)
		mux.HandleFunc("/admin/revoke", p.handleAdminRevoke)
		mux.HandleFunc("/admin/revoke-run", p.handleAdminRevokeRun)
		mux.HandleFunc("/admin/revoke-case", p.handleAdminRevokeCase)
		mux.HandleFunc("/admin/mode", p.handleAdminMode)
		mux.HandleFunc("/admin/usage", p.handleAdminUsage)
		mux.HandleFunc("/admin/usage/prepare", p.handleAdminPrepareUsage)
	}

	// Worker-facing handler: CONNECT/absolute-form requests are handled by the
	// forward proxy (router); everything else falls through to the data-plane mux.
	p.server = &http.Server{Handler: p.workerFacingHandler(mux), ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 64 << 10}
	go func() {
		if err := p.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("auth proxy error: %v", err)
		}
	}()

	// Background goroutine to expire tokens
	go p.expireLoop()

	if p.cfg.Verbose {
		fmt.Printf("Auth Proxy listening on %s\n", p.addr)
		if p.adminAddr != "" {
			fmt.Printf("Auth Proxy admin listening on %s\n", p.adminAddr)
		}
	}
	return nil
}

// resolveAdminListenAddr maps the AdminBindEgress sentinel host to the container's
// egress (primary) IPv4 so the admin port is unreachable from worker Internal
// networks. Any other host is returned unchanged.
func resolveAdminListenAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host != AdminBindEgress {
		return addr
	}
	if ip := primaryNonLoopbackIPv4(); ip != "" {
		return net.JoinHostPort(ip, port)
	}
	// Detection failed: fall back to all interfaces so the proxy still starts.
	log.Printf("warning: could not determine egress IP for admin listener; binding all interfaces")
	return net.JoinHostPort("0.0.0.0", port)
}

// primaryNonLoopbackIPv4 returns the first global-unicast IPv4 address on a
// non-loopback interface. At proxy startup the container is attached only to the
// egress network, so this is the egress IP; the per-worker networks are connected
// later by the sandbox manager.
func primaryNonLoopbackIPv4() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipnet, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			return ip.String()
		}
	}
	return ""
}

// Stop shuts down the proxy and revokes all tokens.
func (p *Proxy) Stop() {
	// Hijacked WebSockets are not closed by http.Server.Shutdown. Cancel each
	// worker lease first, including active HTTP streams and upgraded sockets.
	p.mu.Lock()
	for _, rec := range p.tokens {
		if rec.cancel != nil {
			rec.cancel()
		}
	}
	p.tokens = make(map[string]*tokenRecord)
	p.tokensByValue = make(map[string]*tokenRecord)
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if p.server != nil {
		_ = p.server.Shutdown(ctx)
	}
	if p.adminServer != nil {
		_ = p.adminServer.Shutdown(ctx)
	}
	if p.usageLedger != nil {
		_ = p.usageLedger.Close()
	}
}

// Port returns the TCP port the proxy is listening on.
// Only valid after Start() has been called.
func (p *Proxy) Port() string {
	_, port, _ := net.SplitHostPort(p.addr)
	return port
}

// Endpoint returns the proxy URL for host-side access (e.g. health checks, tests).
// Always uses 127.0.0.1 regardless of bind address.
func (p *Proxy) Endpoint() string {
	scheme := "http"
	if p.WorkerTransportEnabled() {
		scheme = "https"
	}
	return scheme + "://127.0.0.1:" + p.Port()
}

// ContainerEndpoint returns the proxy URL that worker containers should use.
// Containers reach the proxy via host.docker.internal which Docker resolves
// to the host's bridge gateway IP at container creation time.
// Requires Docker Engine >= 20.10 and --add-host=host.docker.internal:host-gateway
// to be set on the worker container (manager.go handles this automatically).
func (p *Proxy) ContainerEndpoint() string {
	scheme := "http"
	if p.WorkerTransportEnabled() {
		scheme = "https"
	}
	return scheme + "://host.docker.internal:" + p.Port()
}
