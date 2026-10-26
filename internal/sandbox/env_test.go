package sandbox

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/agentpolicy"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

func TestBuildEnvDirectCodexArguments(t *testing.T) {
	args := []string{"exec", "two words", "--json", "quote'and$special"}
	m := newTestManager(&fakeProxy{endpoint: "http://codex-auth-proxy:18080"})
	env, err := m.buildEnv("direct", RunOptions{Agent: AgentCodex, DirectAgent: true, AgentArgs: args, TokenTTL: 60}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := envValue(env, "DOCK_DIRECT_AGENT"); got != "1" {
		t.Errorf("DOCK_DIRECT_AGENT = %q; want 1", got)
	}
	raw, ok := envValue(env, "DOCK_AGENT_ARGS_JSON")
	if !ok {
		t.Fatal("DOCK_AGENT_ARGS_JSON missing")
	}
	var decoded []string
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(args) {
		t.Fatalf("arguments = %q; want %q", decoded, args)
	}
	for i := range args {
		if decoded[i] != args[i] {
			t.Errorf("argument %d = %q; want %q", i, decoded[i], args[i])
		}
	}
}

func TestBuildEnvAgentPolicyIsStructured(t *testing.T) {
	m := newTestManager(&fakeProxy{endpoint: "http://codex-auth-proxy:18080", anthropic: true})
	policy, err := agentpolicy.Resolve(agentpolicy.Empty(), "approve-for-me")
	if err != nil {
		t.Fatal(err)
	}
	env, err := m.buildEnv("policy-worker", RunOptions{Agent: AgentCodex, AgentPolicy: policy, TokenTTL: 60}, "")
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := envValue(env, agentpolicy.EnvProfile); value != "approve-for-me" {
		t.Fatalf("profile = %q", value)
	}
	raw, ok := envValue(env, agentpolicy.EnvCodexConfig)
	if !ok {
		t.Fatal("Codex policy JSON missing")
	}
	var config agentpolicy.CodexConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	if config.ApprovalsReviewer != "auto_review" {
		t.Fatalf("config = %#v", config)
	}
	raw, ok = envValue(env, agentpolicy.EnvClaudeArgs)
	if !ok {
		t.Fatal("Claude policy argv missing")
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		t.Fatal(err)
	}
	if strings.Join(args, " ") != "--permission-mode auto" {
		t.Fatalf("Claude policy argv = %q", args)
	}
}

// fakeProxy implements authproxy.Service for env-building tests.
type fakeProxy struct {
	oauth          bool
	anthropic      bool
	anthropicOAuth bool
	endpoint       string
	issued         []string
}

func (f *fakeProxy) IssueToken(name string, ttlSec int) (string, error) {
	f.issued = append(f.issued, name)
	return "cdx-faketoken", nil
}
func (f *fakeProxy) RevokeToken(name string)    {}
func (f *fakeProxy) IsOAuthMode() bool          { return f.oauth }
func (f *fakeProxy) IsAnthropicMode() bool      { return f.anthropic }
func (f *fakeProxy) IsAnthropicOAuthMode() bool { return f.anthropicOAuth }
func (f *fakeProxy) ContainerEndpoint() string  { return f.endpoint }

// envValue returns the value of key=... in env, or ("", false) if absent.
func envValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			return strings.TrimPrefix(e, prefix), true
		}
	}
	return "", false
}

func newTestManager(p *fakeProxy) *Manager {
	return &Manager{proxy: p}
}

func TestProxyContainerNames(t *testing.T) {
	// Auth proxy only (no http proxy configured).
	m := &Manager{proxy: &fakeProxy{endpoint: "http://codex-auth-proxy:18080"}}
	if got := m.proxyContainerNames(false); len(got) != 1 || got[0] != "codex-auth-proxy" {
		t.Errorf("auth only: got %v; want [codex-auth-proxy]", got)
	}

	// Auth + http proxy → both names, in order, de-duplicated.
	m = &Manager{
		proxy:        &fakeProxy{endpoint: "http://codex-auth-proxy:18080/v1"},
		httpProxyURL: "http://codex-http-proxy:18082",
	}
	got := m.proxyContainerNames(false)
	if len(got) != 2 || got[0] != "codex-auth-proxy" || got[1] != "codex-http-proxy" {
		t.Errorf("auth+http: got %v; want [codex-auth-proxy codex-http-proxy]", got)
	}

	// --no-internet must DROP the egress http proxy so the worker has no network
	// path to it (defends against a worker overriding HTTP(S)_PROXY manually).
	got = m.proxyContainerNames(true)
	if len(got) != 1 || got[0] != "codex-auth-proxy" {
		t.Errorf("no-internet: got %v; want [codex-auth-proxy] only", got)
	}

	// No proxy configured → empty.
	if got := (&Manager{}).proxyContainerNames(false); len(got) != 0 {
		t.Errorf("none: got %v; want empty", got)
	}
}

func TestBuildEnv_NoProxyExcludesProxyHostAndLoopback(t *testing.T) {
	m := newTestManager(&fakeProxy{endpoint: "http://codex-auth-proxy:18080", anthropic: true})
	env, err := m.buildEnv("w1", RunOptions{Agent: AgentNone, TokenTTL: 60}, "")
	if err != nil {
		t.Fatal(err)
	}
	// Both upper- and lower-case variants must be set for tools that read either.
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		v, ok := envValue(env, key)
		if !ok {
			t.Fatalf("%s not set", key)
		}
		for _, want := range []string{"codex-auth-proxy", "localhost", "127.0.0.1"} {
			if !strings.Contains(v, want) {
				t.Errorf("%s = %q; want it to contain %q", key, v, want)
			}
		}
	}
	// The lower-case proxy variants must also be present.
	if v, _ := envValue(env, "https_proxy"); v != "http://codex-auth-proxy:18080" {
		t.Errorf("https_proxy = %q; want http://codex-auth-proxy:18080", v)
	}
}

func TestBuildEnv_AlwaysSetsAgentAndSandbox(t *testing.T) {
	m := newTestManager(&fakeProxy{endpoint: "http://codex-auth-proxy:18080", anthropic: true})
	env, err := m.buildEnv("w1", RunOptions{Agent: AgentCodex, TokenTTL: 60}, "")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := envValue(env, "DOCK_AGENT"); v != "codex" {
		t.Errorf("DOCK_AGENT = %q; want codex", v)
	}
	if _, ok := envValue(env, "CODEX_SANDBOX"); !ok {
		t.Error("CODEX_SANDBOX not set")
	}
}

func TestBuildEnv_ShellDefaultsToBash(t *testing.T) {
	m := newTestManager(&fakeProxy{endpoint: "http://codex-auth-proxy:18080"})
	env, err := m.buildEnv("w1", RunOptions{Agent: AgentNone, TokenTTL: 60}, "")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := envValue(env, "DOCK_SHELL"); v != "bash" {
		t.Errorf("DOCK_SHELL = %q; want bash when ShellType is unset", v)
	}
}

func TestBuildEnv_ShellZsh(t *testing.T) {
	m := newTestManager(&fakeProxy{endpoint: "http://codex-auth-proxy:18080"})
	env, err := m.buildEnv("w1", RunOptions{Agent: AgentNone, TokenTTL: 60, ShellType: ShellZsh}, "")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := envValue(env, "DOCK_SHELL"); v != "zsh" {
		t.Errorf("DOCK_SHELL = %q; want zsh", v)
	}
}

func TestBuildEnv_Codex(t *testing.T) {
	m := newTestManager(&fakeProxy{endpoint: "http://codex-auth-proxy:18080", anthropic: true})
	env, err := m.buildEnv("w1", RunOptions{Agent: AgentCodex, TokenTTL: 60}, "")
	if err != nil {
		t.Fatal(err)
	}

	if v, _ := envValue(env, "CODEX_TOKEN"); v != "cdx-faketoken" {
		t.Errorf("CODEX_TOKEN = %q; want cdx-faketoken", v)
	}
	if v, _ := envValue(env, "OPENAI_BASE_URL"); v != "http://codex-auth-proxy:18080/v1" {
		t.Errorf("OPENAI_BASE_URL = %q", v)
	}
	// General egress is routed through the proxy/router via HTTP(S)_PROXY, with the
	// proxy host excluded from NO_PROXY so API/token traffic reaches it directly.
	if v, _ := envValue(env, "HTTPS_PROXY"); v != "http://codex-auth-proxy:18080" {
		t.Errorf("HTTPS_PROXY = %q; want http://codex-auth-proxy:18080", v)
	}
	if v, _ := envValue(env, "NO_PROXY"); !strings.Contains(v, "codex-auth-proxy") {
		t.Errorf("NO_PROXY = %q; want it to contain codex-auth-proxy", v)
	}
	// Codex agent must not receive Anthropic variables.
	if _, ok := envValue(env, "ANTHROPIC_BASE_URL"); ok {
		t.Error("ANTHROPIC_BASE_URL should not be set for --agent codex")
	}
}

func TestBuildEnv_CodexOAuthRefreshOverride(t *testing.T) {
	m := newTestManager(&fakeProxy{endpoint: "http://codex-auth-proxy:18080", oauth: true})
	env, err := m.buildEnv("w1", RunOptions{Agent: AgentCodex, TokenTTL: 60}, "")
	if err != nil {
		t.Fatal(err)
	}
	v, ok := envValue(env, "CODEX_REFRESH_TOKEN_URL_OVERRIDE")
	if !ok {
		t.Fatal("CODEX_REFRESH_TOKEN_URL_OVERRIDE not set in OAuth mode")
	}
	if !strings.Contains(v, "/oauth/token?cdx=cdx-faketoken") {
		t.Errorf("refresh override = %q; want it to embed the cdx token", v)
	}
}

func TestBuildEnv_Claude(t *testing.T) {
	m := newTestManager(&fakeProxy{endpoint: "http://codex-auth-proxy:18080", anthropic: true})
	env, err := m.buildEnv("w1", RunOptions{Agent: AgentClaude, TokenTTL: 60}, "")
	if err != nil {
		t.Fatal(err)
	}

	if v, _ := envValue(env, "ANTHROPIC_BASE_URL"); v != "http://codex-auth-proxy:18080/anthropic" {
		t.Errorf("ANTHROPIC_BASE_URL = %q", v)
	}
	if v, _ := envValue(env, "ANTHROPIC_API_KEY"); v != "cdx-faketoken" {
		t.Errorf("ANTHROPIC_API_KEY = %q; want placeholder token", v)
	}
	if v, _ := envValue(env, "HTTP_PROXY"); v != "http://codex-auth-proxy:18080" {
		t.Errorf("HTTP_PROXY = %q; want http://codex-auth-proxy:18080", v)
	}
	// Claude agent must not receive Codex variables.
	if _, ok := envValue(env, "CODEX_TOKEN"); ok {
		t.Error("CODEX_TOKEN should not be set for --agent claude")
	}
	if _, ok := envValue(env, "OPENAI_BASE_URL"); ok {
		t.Error("OPENAI_BASE_URL should not be set for --agent claude")
	}
}

func TestBuildEnv_ClaudeWithoutAnthropicCreds(t *testing.T) {
	m := newTestManager(&fakeProxy{endpoint: "http://codex-auth-proxy:18080", anthropic: false})
	env, err := m.buildEnv("w1", RunOptions{Agent: AgentClaude, TokenTTL: 60}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := envValue(env, "ANTHROPIC_BASE_URL"); ok {
		t.Error("ANTHROPIC_BASE_URL should be omitted when the proxy has no Anthropic creds")
	}
}

func TestBuildEnvClaudeSubscriptionAndRestore(t *testing.T) {
	m := newTestManager(&fakeProxy{endpoint: "http://auth:18080", anthropic: true, anthropicOAuth: true})
	env, err := m.buildEnv("claude", RunOptions{Agent: AgentClaude}, "")
	if err != nil {
		t.Fatal(err)
	}
	if key, _ := envValue(env, "ANTHROPIC_API_KEY"); key != "" {
		t.Fatal("subscription selected API billing")
	}
	if token, _ := envValue(env, "CLAUDE_CODE_OAUTH_TOKEN"); token != "cdx-faketoken" {
		t.Fatal("missing subscription placeholder")
	}
	f, manager := newEngineFixture(t)
	manager.proxy = &runtimeProxy{fakeProxy: fakeProxy{endpoint: "http://auth:18080", anthropic: true, anthropicOAuth: true}}
	id, err := manager.Run(RunOptions{Name: "claude-subscription", ProjectDir: t.TempDir(), Agent: AgentClaude, Detach: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := manager.Remove(id, true); err != nil {
			t.Error(err)
		}
	}()
	if workerPlaceholder(*f.worker) == "" {
		t.Fatal("subscription placeholder cannot be restored/revoked")
	}
	connections, err := manager.WorkerConnections(context.Background(), id)
	if err != nil || len(connections) != 1 || connections[0].TokenEnvironment != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Fatalf("subscription connection: %+v %v", connections, err)
	}
}

func TestProviderBoundShellDoesNotFetchAnotherProvidersCredentials(t *testing.T) {
	m := newTestManager(&fakeProxy{endpoint: "http://auth:18080", anthropic: true, anthropicOAuth: true})
	for _, provider := range []string{"openai", "anthropic"} {
		env, err := m.buildEnv("worker", RunOptions{Agent: AgentNone, pendingPlaceholder: "cdx-fixture", TokenIdentity: identity.Identity{Provider: provider}}, "")
		if err != nil {
			t.Fatal(err)
		}
		codex, _ := envValue(env, "CODEX_TOKEN")
		claude, _ := envValue(env, "CLAUDE_CODE_OAUTH_TOKEN")
		if (codex != "") != (provider == "openai") || (claude != "") != (provider == "anthropic") {
			t.Fatalf("provider=%s bootstrap would acquire another provider's credentials", provider)
		}
	}
}

func TestBuildEnv_ShellSetsBothProviders(t *testing.T) {
	m := newTestManager(&fakeProxy{endpoint: "http://codex-auth-proxy:18080", anthropic: true})
	env, err := m.buildEnv("w1", RunOptions{Agent: AgentNone, TokenTTL: 60}, "")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := envValue(env, "DOCK_AGENT"); v != "" {
		t.Errorf("DOCK_AGENT = %q; want empty for shell", v)
	}
	if _, ok := envValue(env, "CODEX_TOKEN"); !ok {
		t.Error("shell mode should configure Codex auth")
	}
	if _, ok := envValue(env, "ANTHROPIC_BASE_URL"); !ok {
		t.Error("shell mode should configure Claude auth when available")
	}
}

func TestBuildEnv_SeparateHTTPProxy(t *testing.T) {
	m := newTestManager(&fakeProxy{endpoint: "http://codex-auth-proxy:18080", anthropic: true})
	m.httpProxyURL = "http://codex-http-proxy:18082"
	env, err := m.buildEnv("w1", RunOptions{Agent: AgentNone, TokenTTL: 60}, "")
	if err != nil {
		t.Fatal(err)
	}
	// General egress → the dedicated egress proxy.
	if v, _ := envValue(env, "HTTP_PROXY"); v != "http://codex-http-proxy:18082" {
		t.Errorf("HTTP_PROXY = %q; want the egress proxy", v)
	}
	if v, _ := envValue(env, "https_proxy"); v != "http://codex-http-proxy:18082" {
		t.Errorf("https_proxy = %q; want the egress proxy", v)
	}
	// API still points at the auth proxy.
	if v, _ := envValue(env, "OPENAI_BASE_URL"); v != "http://codex-auth-proxy:18080/v1" {
		t.Errorf("OPENAI_BASE_URL = %q; want the auth proxy", v)
	}
	// NO_PROXY must exclude BOTH the auth host (so API/token go direct) and the
	// egress host itself (so a direct /health hit doesn't loop back through it).
	v, _ := envValue(env, "NO_PROXY")
	for _, want := range []string{"codex-auth-proxy", "codex-http-proxy", "localhost", "127.0.0.1"} {
		if !strings.Contains(v, want) {
			t.Errorf("NO_PROXY = %q; want it to contain %q", v, want)
		}
	}
}

func TestBuildEnv_KataUsesPinnedProxyAddresses(t *testing.T) {
	m := newTestManager(&fakeProxy{endpoint: "http://codex-auth-proxy:18080", anthropic: true, oauth: true})
	m.httpProxyURL = "http://codex-http-proxy:18082"
	opts := RunOptions{Agent: AgentNone, TokenTTL: 60, proxyHosts: []string{
		"codex-auth-proxy:10.253.2.2", "codex-http-proxy:10.253.2.3",
	}}
	env, err := m.buildEnv("w1", opts, "")
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"CODEX_AUTH_PROXY_URL": "http://10.253.2.2:18080",
		"OPENAI_BASE_URL":      "http://10.253.2.2:18080/v1",
		"ANTHROPIC_BASE_URL":   "http://10.253.2.2:18080/anthropic",
		"HTTP_PROXY":           "http://10.253.2.3:18082",
		"HTTPS_PROXY":          "http://10.253.2.3:18082",
	} {
		if got, _ := envValue(env, key); got != want {
			t.Errorf("%s = %q; want %q", key, got, want)
		}
	}
	if got, _ := envValue(env, "CODEX_REFRESH_TOKEN_URL_OVERRIDE"); !strings.HasPrefix(got, "http://10.253.2.2:18080/oauth/token?cdx=") {
		t.Errorf("refresh endpoint = %q", got)
	}
	if got, _ := envValue(env, "NO_PROXY"); !strings.Contains(got, "10.253.2.2") || !strings.Contains(got, "10.253.2.3") {
		t.Errorf("NO_PROXY = %q", got)
	}
	if _, err := kataProxyEndpoint("http://codex-auth-proxy:18080", []string{"other:10.253.2.2"}); err == nil {
		t.Fatal("unattached proxy name was accepted")
	}
}

func TestBuildEnv_NoInternetSkipsProxyVars(t *testing.T) {
	m := newTestManager(&fakeProxy{endpoint: "http://codex-auth-proxy:18080", anthropic: true})
	env, err := m.buildEnv("w1", RunOptions{Agent: AgentCodex, TokenTTL: 60, NoInternet: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	// API routes still configured...
	if _, ok := envValue(env, "OPENAI_BASE_URL"); !ok {
		t.Error("OPENAI_BASE_URL should still be set with --no-internet (API routes remain)")
	}
	// ...but general egress through the forward proxy is disabled.
	if _, ok := envValue(env, "HTTP_PROXY"); ok {
		t.Error("HTTP_PROXY should not be set when --no-internet is used")
	}
	if _, ok := envValue(env, "HTTPS_PROXY"); ok {
		t.Error("HTTPS_PROXY should not be set when --no-internet is used")
	}
}

func TestBuildEnv_NoProxy(t *testing.T) {
	m := &Manager{}
	env, err := m.buildEnv("w1", RunOptions{Agent: AgentCodex, TokenTTL: 60}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := envValue(env, "CODEX_TOKEN"); ok {
		t.Error("CODEX_TOKEN should not be set without a proxy")
	}
	if v, _ := envValue(env, "DOCK_AGENT"); v != "codex" {
		t.Errorf("DOCK_AGENT = %q; want codex", v)
	}
}

func TestBuildEnv_ExecCmd(t *testing.T) {
	m := newTestManager(&fakeProxy{endpoint: "http://codex-auth-proxy:18080"})
	env, err := m.buildEnv("w1", RunOptions{Agent: AgentNone, TokenTTL: 60, ExecCmd: "ls -la"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := envValue(env, "DOCK_EXEC_CMD"); !ok || v != "ls -la" {
		t.Errorf("DOCK_EXEC_CMD = %q, ok=%v; want \"ls -la\"", v, ok)
	}
}

func TestBuildEnv_ExecCmdOmittedWhenUnset(t *testing.T) {
	m := newTestManager(&fakeProxy{endpoint: "http://codex-auth-proxy:18080"})
	env, err := m.buildEnv("w1", RunOptions{Agent: AgentNone, TokenTTL: 60}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := envValue(env, "DOCK_EXEC_CMD"); ok {
		t.Error("DOCK_EXEC_CMD should not be set when --exec is not used")
	}
}

func TestValidAgent(t *testing.T) {
	for _, a := range []Agent{AgentNone, AgentCodex, AgentClaude} {
		if !ValidAgent(a) {
			t.Errorf("ValidAgent(%q) = false; want true", a)
		}
	}
	if ValidAgent(Agent("bogus")) {
		t.Error("ValidAgent(bogus) = true; want false")
	}
}

func TestWatchPollingIsOptInAndDoesNotEnableEgress(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		m := newTestManager(&fakeProxy{endpoint: "http://auth:18080"})
		env, err := m.buildEnv("polling", RunOptions{WatchPolling: enabled, NoInternet: true}, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"CHOKIDAR_USEPOLLING", "CHOKIDAR_INTERVAL", "WATCHPACK_POLLING"} {
			value, present := envValue(env, key)
			if present != enabled || enabled && value == "" {
				t.Fatalf("%s enabled=%v: %q %v", key, enabled, value, present)
			}
		}
		if _, ok := envValue(env, "HTTP_PROXY"); ok {
			t.Fatal("polling enabled egress")
		}
	}
}
