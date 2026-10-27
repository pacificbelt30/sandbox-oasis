package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/authproxy"
	"github.com/pacificbelt30/codex-dock/internal/environmentlock"
	"github.com/pacificbelt30/codex-dock/internal/network"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/template"
	"github.com/pacificbelt30/codex-dock/internal/workerapp"
	"github.com/pacificbelt30/codex-dock/internal/workeregress"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/term"
)

// userMode is the raw value of --user before resolution.
var userMode string

// approvalModeFlag holds the raw value of --approval-mode before validation.
var approvalModeFlag string

// agentFlag holds the raw value of --agent before validation.
var agentFlag string

// fullAutoFlag is a deprecated alias for --approval-mode full-auto.
var fullAutoFlag bool

// runTemplate holds the raw value of --template before resolution to an image tag.
var runTemplate string

// shellTypeFlag holds the raw value of --shell-type before validation.
var shellTypeFlag string

var runEnvironmentLock string
var runStartHooks []string
var runReadiness string
var runOpts sandbox.RunOptions
var (
	proxyAdminURL           string
	proxyContainerURL       string
	httpProxyURL            string
	runProxyAdminSecret     string
	runProxyAdminSecretFile string
	runKeep                 bool
	forceRecreateProxy      bool
	mountFlags              []string
	runEgressAllow          []string
	runPolicyPreset         string
)

var runCmd = &cobra.Command{
	Use:   "run [flags] [IMAGE [COMMAND...]]",
	Short: "Start an isolated coding-agent worker or shell",
	Long: `Start a Kata VM-isolated worker managed through containerd and Dragonball (Linux/KVM required).
Use --runtime runc for explicit compatibility without VM isolation.
Auth credentials are injected via the Auth Proxy instead of being directly mounted.

A positional IMAGE and optional COMMAND work like "docker run IMAGE COMMAND".
Without an image, use "-- COMMAND..." to run a command in the default image.
Both forms are equivalent to --exec without quoting the whole command:

  oaiso run -d --agent codex codex-dock:latest
  oaiso run codex-dock:latest npm test -- --watch=false
  oaiso run -- npm test -- --watch=false`,
	Args: cobra.ArbitraryArgs,
	RunE: runWorker,
}

func init() {
	rootCmd.AddCommand(runCmd)

	f := runCmd.Flags()
	bindUsageFlags(runCmd)
	f.StringVar(&runObserveDir, "observe-dir", "", "Save secret-free launch timings to a private local diagnostic directory")
	f.SetInterspersed(false) // After IMAGE, flags belong to COMMAND as in docker run.
	f.StringVar(&runOpts.Runtime, "runtime", sandbox.RuntimeKata, "Worker isolation: kata (default, requires Linux/KVM) or explicit runc compatibility mode; never falls back automatically")
	f.StringVar(&runOpts.KataRuntime, "kata-runtime", sandbox.DefaultKataRuntime, "Kata v2 runtime registration on the dedicated containerd node")
	f.StringVarP(&runOpts.Image, "image", "i", "codex-dock:latest", "OCI image for the sandbox")
	f.StringVar(&runOpts.ResourceProfile, "profile-size", "standard", "Kata resource profile: standard, small, compact or a node-registered name")
	f.StringVarP(&runTemplate, "template", "T", "", `Sandbox image template (e.g. "plain", "pwn"). Resolved to the template's image tag; an explicit --image always takes precedence. Use "oaiso build --template list" to see available templates.`)
	f.StringArrayVarP(&runOpts.Packages, "pkg", "p", nil, "Additional packages to install (apt:<pkg>, pip:<pkg>, npm:<pkg>)")
	f.StringVar(&runOpts.PkgFile, "pkg-file", "", "Path to package definition file (packages.dock)")
	f.StringVar(&runOpts.ProjectDir, "project", ".", "Project directory to mount as /workspace")
	f.BoolVarP(&runOpts.UseWorktree, "worktree", "w", false, "Use git worktree for isolation")
	f.StringVarP(&runOpts.Branch, "branch", "b", "", "Branch to checkout (requires --worktree)")
	f.BoolVarP(&runOpts.NewBranch, "new-branch", "B", false, "Create new branch (requires --worktree and --branch)")
	f.StringVarP(&runOpts.Name, "name", "n", "", "Container name (auto-generated if omitted)")
	f.StringVar(&agentFlag, "agent", "", `AI agent to launch inside the sandbox.
  shell    interactive shell with auth configured (built-in default)
  codex    launch OpenAI Codex CLI
  claude   launch Anthropic Claude Code
  Configure the default with "oaiso default codex" or "oaiso default shell".`)
	f.StringVarP(&runOpts.Task, "task", "t", "", "Initial task prompt for the agent")
	f.StringVarP(&runOpts.ExecCmd, "exec", "e", "", `Run this command instead of an interactive shell. An explicit --agent codex/claude takes priority; the configured default agent does not apply.
  Mutually exclusive with a "-- command..." trailing on the command line (see the "run" command's usage), which is usually easier for multi-word commands since it avoids having to quote the whole thing as one string.`)
	f.StringVar(&approvalModeFlag, "approval-mode", "suggest", `Agent approval mode.
  suggest   use the agent CLI's default approval behavior
  auto-edit Codex: --ask-for-approval on-request; Claude: acceptEdits
  full-auto Codex: --ask-for-approval never; Claude: skip permissions
  danger    bypass agent approvals and Codex's inner sandbox
            The worker runtime and host restrictions remain enforced.`)
	f.StringVar(&agentPolicyName, "agent-policy", "", "Host-managed agent policy (default: selected policy, normally interactive)")
	f.BoolVar(&fullAutoFlag, "full-auto", false, "Deprecated: use --approval-mode full-auto")
	if err := f.MarkDeprecated("full-auto", "use --approval-mode full-auto instead"); err != nil {
		panic(err)
	}
	f.StringVarP(&runOpts.Model, "model", "m", "", "Model name to pass to the selected agent (not a model authorization rule)")
	f.BoolVar(&runOpts.WatchPolling, "watch-polling", false, "Enable 1s polling for Chokidar/Watchpack to observe host edits across the VM boundary")
	f.BoolVar(&runOpts.Compatibility.SSHProxy, "ssh-proxy", false, "Configure Git SSH through the policy-checked HTTP CONNECT proxy")
	f.BoolVar(&runOpts.Compatibility.JVMProxy, "jvm-proxy", false, "Configure JVM HTTP(S) proxy properties")
	f.StringVar(&runOpts.Compatibility.CABundle, "ca-bundle", "", "Additional PEM CA certificates mounted read-only in the worker")
	f.StringArrayVar(&runOpts.Filesystem.DenyRead, "deny-read", nil, "Mask a guest path below /workspace (repeatable, opt-in)")
	f.StringArrayVar(&runOpts.Filesystem.DenyWrite, "deny-write", nil, "Make a guest path below /workspace read-only (repeatable, opt-in)")
	f.DurationVar(&runOpts.Lifecycle.MaxLifetime, "max-lifetime", 0, "Opt-in maximum lifetime enforced by oaiso reap")
	f.DurationVar(&runOpts.Lifecycle.IdleTimeout, "idle-timeout", 0, "Opt-in idle timeout since last oaiso exec; enforced by oaiso reap")
	f.BoolVar(&runOpts.Lifecycle.ProtectMain, "protect-main", false, "Prevent idle reaping while the primary process runs (maximum lifetime still applies)")
	f.DurationVar(&runOpts.Lifecycle.Retention, "retention", 0, "Opt-in retention after exit; oaiso reap removes expired workers")
	f.StringVar(&runEnvironmentLock, "environment-lock", "", "Validate a lock file and require its image digest")
	f.StringArrayVar(&runStartHooks, "start-hook", nil, "Guest shell command before agent startup (repeatable)")
	f.StringVar(&runReadiness, "readiness", "", "Guest shell probe that must succeed before startup completes")
	f.DurationVar(&runOpts.Environment.ReadinessTimeout, "readiness-timeout", 0, "Hook/readiness deadline (default 60s)")
	f.StringVar(&runOpts.Environment.ImageID, "image-id", "", "Require this exact image digest (sha256:...)")
	f.StringVar(&runOpts.CloneDir, "clone-dir", "", "Create an independent local clone at a new absolute directory; retained for recovery")
	f.BoolVar(&runOpts.ReadOnly, "read-only", false, "Mount project as read-only")
	f.BoolVar(&runOpts.ReadOnlyRootFS, "read-only-rootfs", false, "Make the image rootfs read-only; use tmpfs for temporary files and agent HOME")
	f.StringVar(&runOpts.AgentStateSize, "agent-state-size", "", "Private Codex SQLite disk for new writable Kata workers: auto (512MiB), off, or 64MiB–8GiB")
	f.BoolVar(&runOpts.NoGitMetadata, "no-git-metadata", false, "Do not automatically share linked worktree Git metadata with the worker")
	f.StringArrayVar(&mountFlags, "mount", nil, `Additional host directory to bind-mount into the container (repeatable).
  Format: <host-path>:<container-path>[:ro|rw]
  <container-path> must be absolute and cannot be /workspace (use --project/--read-only for that).
  Trailing ":ro" mounts it read-only; ":rw" (default) mounts it read-write.`)
	f.BoolVar(&runOpts.NoInternet, "no-internet", false, "Disable general egress for the worker: do not set HTTP(S)_PROXY, so only the auth proxy's API routes are reachable (no git/npm/pip egress through the router)")
	f.StringVar(&runPolicyPreset, "policy-preset", "", "Opt-in egress preset: offline, test, dependencies, api")
	f.StringArrayVar(&runEgressAllow, "egress-allow-domain", nil, "Allow general egress to domain and subdomains; prefix = for exact match (repeatable; per-worker)")
	f.IntVar(&runOpts.TokenTTL, "token-ttl", 3600, "Token TTL in seconds")
	f.StringVar(&runOpts.AgentsMD, "agents-md", "", "Path to additional AGENTS.md")
	f.StringVar(&proxyAdminURL, "proxy-admin-url", "http://127.0.0.1:18081", "External auth proxy admin URL (host-published admin port)")
	f.StringVar(&proxyContainerURL, "proxy-container-url", "http://codex-auth-proxy:18080", "Auth proxy URL reachable from worker containers (API reverse routes)")
	f.StringVar(&httpProxyURL, "http-proxy-url", "http://codex-http-proxy:18082", "General-egress (forward) proxy URL workers use for HTTP(S)_PROXY")
	f.StringVar(&runProxyAdminSecret, "proxy-admin-secret", "", "Admin secret for external auth proxy")
	f.StringVar(&runProxyAdminSecretFile, "proxy-admin-secret-file", "", "Owner-only (0600) file with the auth proxy admin secret")
	f.BoolVarP(&runOpts.Detach, "detach", "d", false, "Run container in background")
	f.BoolVarP(&runOpts.Detach, "detach-legacy", "D", false, "Deprecated alias for --detach")
	if err := f.MarkHidden("detach-legacy"); err != nil {
		panic(err)
	}
	f.BoolVar(&runKeep, "keep", false, "Keep the container and its per-worker network after a foreground run exits (default: remove them so networks don't accumulate)")
	f.BoolVar(&forceRecreateProxy, "force-recreate-proxy", false, "Recreate an auth proxy whose credential state differs from the host even when workers are running (existing workers will lose authentication)")
	f.IntVarP(&runOpts.Parallel, "parallel", "P", 1, "Number of parallel workers")
	f.BoolVarP(&runOpts.ShellMode, "shell", "s", false, "Start a raw shell, bypassing entrypoint auth setup (debugging). The default (no --agent) already provides an auth-configured shell.")
	f.StringVar(&shellTypeFlag, "shell-type", "", `Interactive shell to launch (the no-agent shell, and --shell debug mode).
  bash  (default, and the fallback when the requested shell isn't installed in the image)
  zsh   requires zsh in the image (the built-in "plain" and "pwn" templates include it)
  When omitted (and not set via config), this is auto-detected from the host's $SHELL environment variable — except for --shell, which always defaults to bash since it bypasses entrypoint.sh and so cannot fall back if the auto-detected shell is missing.`)
	f.StringVar(&userMode, "user", "current", `User to run as inside the container.
	  "current" current command user (uid:gid, default)
	  "codex"   codex user in image (uid:1001)
	  ""        image default user
	  "dir"     project directory owner (uid:gid)
	  "uid"     explicit uid (e.g. "1000" or "1000:1000")`)
	registerCompletion(runCmd, "agent", cobra.FixedCompletions([]cobra.Completion{"shell", "codex", "claude"}, cobra.ShellCompDirectiveNoFileComp))
	registerCompletion(runCmd, "runtime", cobra.FixedCompletions([]cobra.Completion{"kata", "runc"}, cobra.ShellCompDirectiveNoFileComp))
	registerCompletion(runCmd, "approval-mode", cobra.FixedCompletions([]cobra.Completion{"suggest", "auto-edit", "full-auto", "danger"}, cobra.ShellCompDirectiveNoFileComp))
	registerCompletion(runCmd, "agent-policy", completeAgentPolicies)
	registerCompletion(runCmd, "shell-type", cobra.FixedCompletions([]cobra.Completion{"bash", "zsh"}, cobra.ShellCompDirectiveNoFileComp))
	registerCompletion(runCmd, "user", cobra.FixedCompletions([]cobra.Completion{"current", "codex", "dir"}, cobra.ShellCompDirectiveNoFileComp))
	registerCompletion(runCmd, "template", completeTemplates)
	registerCompletion(runCmd, "image", completeImageTags)
	registerCompletion(runCmd, "profile-size", completeResourceProfiles)
}

func runWorker(cmd *cobra.Command, args []string) (result error) {
	stage := "configuration"
	var buildCfg *ProjectConfig
	defer func() { result = startupFailure(stage, result, buildCfg) }()
	projectDir, err := resolveProjectDir(runOpts.ProjectDir)
	if err != nil {
		return fmt.Errorf("resolving project directory: %w", err)
	}
	projectCfg, err := loadProjectConfig(projectDir)
	if err != nil {
		return err
	}
	stage = "agent policy"
	if err := applyAgentPolicyDefaults(cmd); err != nil {
		return err
	}
	stage = "configuration"
	applyRunConfigDefaults(cmd)
	if err := applyOptionalRunDefaults(cmd); err != nil {
		return err
	}
	applyProjectRunConfig(cmd, projectCfg)
	if runOpts.CloneDir != "" && runOpts.Parallel > 1 {
		return fmt.Errorf("--clone-dir requires a distinct directory per worker; launch workers separately")
	}
	rules, offline, policyErr := workeregress.ResolvePreset(configuredPolicy().Rules(), runPolicyPreset, runEgressAllow)
	if policyErr != nil {
		return policyErr
	}
	if offline {
		runOpts.NoInternet = true
	}
	if runPolicyPreset != "" && !offline {
		runEgressAllow = rules.AllowDomains
	}
	// An explicit template selects both the image tag and its build source.
	// A project build with the same tag must not replace that template.
	buildCfg = projectBuildConfigForRun(cmd, projectCfg)
	if runOpts.DirectAgent {
		if agentFlag == string(sandbox.AgentClaude) {
			if codexOptionPresent(runOpts.AgentArgs, "--model") {
				runOpts.Model = ""
			}
			if codexOptionPresent(runOpts.AgentArgs, "--permission-mode", "--dangerously-skip-permissions") {
				approvalModeFlag = "suggest"
			}
		} else {
			if codexOptionPresent(runOpts.AgentArgs, "--model", "-m", "--profile", "-p") || codexConfigOverridePresent(runOpts.AgentArgs, "model") {
				runOpts.Model = ""
			}
			if codexOptionPresent(runOpts.AgentArgs, "--ask-for-approval", "-a", "--full-auto", "--dangerously-bypass-approvals-and-sandbox", "--profile", "-p") || codexConfigOverridePresent(runOpts.AgentArgs, "approval_policy") {
				approvalModeFlag = "suggest"
			}
		}
	}
	image, commandArgs := resolveRunPositionals(args, cmd.ArgsLenAtDash(), cmd.Flags().Changed("image") || cmd.Flags().Changed("template"))
	if image != "" {
		runOpts.Image = image
	}
	if selected, _ := cmd.Flags().GetString("checkpoint"); selected != "" && (image != "" || runEnvironmentLock != "") {
		return fmt.Errorf("--checkpoint cannot be combined with a positional image or --environment-lock")
	}
	if err := applyCheckpointRun(cmd); err != nil {
		return err
	}
	if _, _, err := sandbox.ResolveRuntime(runOpts.Runtime, runOpts.KataRuntime); err != nil {
		return err
	}
	if runEnvironmentLock != "" {
		l, err := environmentlock.Load(runEnvironmentLock, projectDir, runOpts.Image)
		if err != nil {
			return err
		}
		runOpts.Environment.ImageID = l.ImageID
	}
	// Check before proxy startup, token issuance, worktrees or image builds.
	stage = "node"
	preflight, err := sandbox.NewManager(managementConfig())
	if err != nil {
		return err
	}
	defer func() { _ = preflight.Close() }()
	stage = "runtime"
	if err := preflight.CheckRuntime(cmd.Context(), runOpts); err != nil {
		return err
	}
	stage = "configuration"

	// A positional command is equivalent to --exec, but as separate argv
	// entries rather than one pre-quoted string.
	execCmd, err := resolveExecCmd(runOpts.ExecCmd, commandArgs)
	if err != nil {
		return err
	}
	runOpts.ExecCmd = execCmd

	if runOpts.WatchPolling {
		if _, err := fmt.Fprintln(cmd.ErrOrStderr(), "Host edit polling enabled for Chokidar/Watchpack (1s). Other watchers require their own polling settings."); err != nil {
			return err
		}
	}
	// Resolve project directory
	runOpts.ProjectDir = projectDir

	// Resolve --user into uid[:gid] for the container
	containerUser, err := resolveContainerUser(userMode, runOpts.ProjectDir)
	if err != nil {
		return fmt.Errorf("resolving --user: %w", err)
	}
	runOpts.ContainerUser = containerUser

	// Resolve --mount into additional bind mounts.
	mounts, err := parseMountSpecs(mountFlags)
	if err != nil {
		return fmt.Errorf("resolving --mount: %w", err)
	}
	runOpts.Mounts = mounts
	if err := ensureProfilesOutsideWorkerShares(runOpts.ProjectDir, runOpts.CloneDir, mounts); err != nil {
		return err
	}

	// Resolve approval mode: --full-auto (deprecated) maps to "full-auto"
	// when --approval-mode has not been explicitly set from its default.
	mode := sandbox.ApprovalMode(approvalModeFlag)
	if fullAutoFlag && approvalModeFlag == "suggest" {
		mode = sandbox.ApprovalModeFullAuto
	}
	if !sandbox.ValidApprovalMode(mode) {
		return fmt.Errorf("invalid --approval-mode %q; valid values: suggest, auto-edit, full-auto, danger", approvalModeFlag)
	}
	runOpts.ApprovalMode = mode

	// An implicit agent default applies only to an interactive run. Explicit
	// commands and the native `oaiso codex` entry point keep their own behavior.
	agent, err := resolveSelectedAgent(cmd, runOpts.ExecCmd)
	if err != nil {
		return err
	}
	runOpts.Agent = agent

	if runOpts.ExecCmd != "" && runOpts.ShellMode {
		return fmt.Errorf("--exec and --shell are mutually exclusive (--shell bypasses entrypoint.sh entirely, so --exec would never run)")
	}

	// Resolve interactive shell type.
	shellType, err := resolveShellType(shellTypeFlag, runOpts.ShellMode)
	if err != nil {
		return err
	}
	runOpts.ShellType = shellType

	// Network manager handles per-worker Internal networks. Each worker gets its
	// own Internal bridge shared only with the proxy (created in sandbox.Run), so
	// isolation is enforced by the containerd node without any iptables/sudo.
	netMgr, err := network.NewManager()
	if err != nil {
		return fmt.Errorf("creating network manager: %w", err)
	}
	defer func() { _ = netMgr.Close() }()

	stage = "authentication"
	adminSecret, err := effectiveRunProxySecret()
	if err != nil {
		return err
	}
	stage = "auth proxy"
	proxy, err := authproxy.NewRemoteProxy(proxyAdminURL, proxyContainerURL, adminSecret)
	if err != nil {
		// If the proxy simply isn't running and we're on a terminal with the
		// default proxy URLs, offer to build/start it now instead of failing.
		proxy, err = offerToStartProxy(cmd, err)
		if err != nil {
			return fmt.Errorf("connecting external auth proxy: %w", err)
		}
	}

	stage = "authentication"
	proxy, err = reconcileProxyAuthState(cmd.Context(), proxy, forceRecreateProxy)
	if err != nil {
		return err
	}

	if runOpts.Agent == sandbox.AgentClaude && !proxy.IsAnthropicMode() {
		return fmt.Errorf("--agent claude requires Anthropic credentials on the auth proxy; " +
			"set ANTHROPIC_API_KEY / CLAUDE_CODE_OAUTH_TOKEN or run `claude auth login` (~/.claude/.credentials.json) before starting the proxy")
	}
	if (len(runEgressAllow) > 0 || configuredPolicy().DefaultDeny) && !runOpts.NoInternet {
		stage = "configuration"
		if cmd.Flags().Changed("http-proxy-url") || httpProxyURL != "http://codex-http-proxy:18082" {
			return fmt.Errorf("--egress-allow-domain requires the managed egress proxy")
		}
		policyURL, err := ensureWorkerEgressProxy(cmd, runEgressAllow)
		if err != nil {
			return err
		}
		httpProxyURL = policyURL
	}

	// Load packages.dock if present and no --pkg-file given
	if runOpts.PkgFile == "" {
		autoFile := runOpts.ProjectDir + "/packages.dock"
		if _, err := os.Stat(autoFile); err == nil {
			runOpts.PkgFile = autoFile
		}
	}

	// Setup signal handling for graceful shutdown
	stage = "node"
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	managerCfg := managementConfig()
	managerCfg.Proxy = proxy
	managerCfg.Network = netMgr
	managerCfg.HTTPProxyURL = httpProxyURL
	managerCfg.Debug = debug
	managerCfg.ProxyAdminURL = proxyAdminURL
	sbMgr, err := sandbox.NewManager(managerCfg)
	if err != nil {
		return fmt.Errorf("creating sandbox manager: %w", err)
	}
	defer func() { _ = sbMgr.Close() }()

	// Auto-build the sandbox image when it is not present locally, or when it
	// is stale relative to its template's Dockerfile (see imageNeedsBuild).
	stage = "image"
	needsBuild, reason := false, ""
	if selected, _ := cmd.Flags().GetString("checkpoint"); selected == "" {
		needsBuild, reason, err = imageNeedsBuild(sbMgr, runOpts.Image, buildCfg)
	}
	if err != nil {
		return fmt.Errorf("checking image %s: %w", runOpts.Image, err)
	}
	if needsBuild {
		fmt.Fprintln(cmd.ErrOrStderr(), startupImageProgress(runOpts.Image, reason)) //nolint:errcheck
		if buildCfg != nil && buildCfg.Build.Dockerfile != "" && buildCfg.Build.Tag == runOpts.Image {
			if err := buildProjectImage(cmd.Context(), buildCfg, runOpts.Image); err != nil {
				return fmt.Errorf("auto-build project image: %w", err)
			}
		} else if buildCfg != nil && buildCfg.Build.Template != "" && buildCfg.Build.Tag == runOpts.Image {
			if err := buildFromTemplate(cmd.Context(), buildCfg.Build.Template, runOpts.Image); err != nil {
				return fmt.Errorf("auto-build project template: %w", err)
			}
		} else if tmplName := template.MatchTag(runOpts.Image); tmplName != "" {
			if err := buildFromTemplate(cmd.Context(), tmplName, runOpts.Image); err != nil {
				return fmt.Errorf("auto-build template %q: %w", tmplName, err)
			}
		} else {
			dockerfile, buildCtx, err := resolveDockerfile("")
			if err != nil {
				return fmt.Errorf("auto-build: %w", err)
			}
			if err := executeBuild(cmd.Context(), runOpts.Image, dockerfile, buildCtx); err != nil {
				return fmt.Errorf("auto-build: %w", err)
			}
		}
		fmt.Println("Build finished, starting container...")
	}

	stage = "configuration"
	if runOpts.Parallel > 1 {
		return runParallel(sbMgr, sigCh)
	}

	return runSingle(sbMgr, sigCh)
}

func applyAgentPolicyDefaults(cmd *cobra.Command) error {
	effective, err := resolveHostAgentPolicy()
	if err != nil {
		return fmt.Errorf("loading host agent policy: %w", err)
	}
	runOpts.AgentPolicy = effective
	flags := cmd.Flags()
	if !runOpts.DirectAgent && !flags.Changed("agent") && effective.Agent != "" {
		agentFlag = effective.Agent
	}
	if !flags.Changed("model") && effective.Model != "" {
		runOpts.Model = effective.Model
	}
	if !flags.Changed("approval-mode") && effective.ApprovalMode != "" {
		approvalModeFlag = effective.ApprovalMode
	}
	return nil
}

func projectBuildConfigForRun(cmd *cobra.Command, cfg *ProjectConfig) *ProjectConfig {
	if cmd.Flags().Changed("template") {
		return nil
	}
	return cfg
}

// resolveRunPositionals keeps the explicit --image/--template forms and the
// existing "-- COMMAND" form while allowing Docker's IMAGE COMMAND form.
func resolveRunPositionals(args []string, atDash int, explicitImage bool) (string, []string) {
	if len(args) == 0 {
		return "", nil
	}
	if atDash == 0 || explicitImage {
		return "", args
	}
	command := args[1:]
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}
	return args[0], command
}

type proxyAuthState struct {
	openAIOAuth        bool
	anthropicAvailable bool
	anthropicOAuth     bool
}

func hostProxyAuthState() proxyAuthState {
	return proxyAuthState{
		openAIOAuth:        authproxy.IsOAuthAuth(),
		anthropicAvailable: authproxy.HasAnthropicCredentials(),
		anthropicOAuth:     authproxy.IsAnthropicOAuth(),
	}
}

func remoteProxyAuthState(proxy *authproxy.RemoteProxy) proxyAuthState {
	return proxyAuthState{
		openAIOAuth:        proxy.IsOAuthMode(),
		anthropicAvailable: proxy.IsAnthropicMode(),
		anthropicOAuth:     proxy.IsAnthropicOAuthMode(),
	}
}

func proxyAuthStateMismatch(host, remote proxyAuthState) bool {
	return host != remote
}

func runningWorkersBlockProxyRecreation(workerCount int, force bool) bool {
	return workerCount > 0 && !force
}

// reconcileProxyAuthState recreates only the credential-holding auth proxy
// when its loaded auth modes differ from the host. Recreating that proxy drops
// its in-memory worker tokens, so an automatic recreation is safe only when no
// workers are running unless the caller explicitly forces it.
func reconcileProxyAuthState(ctx context.Context, proxy *authproxy.RemoteProxy, force bool) (*authproxy.RemoteProxy, error) {
	if profile := selectedAuthProfile(); profile != "" || proxy.AuthProfile() != "" {
		if profile == "" || profile != proxy.AuthProfile() {
			return nil, fmt.Errorf("selected auth profile does not match the proxy; select the same profile and explicitly recreate a different proxy after draining workers")
		}
		state, connection, err := profileProxyState(ctx, profile)
		if err != nil {
			return nil, fmt.Errorf("loading selected auth profile: %w", err)
		}
		if connection != proxy.ProfileConnectionVersion() || proxyAuthStateMismatch(state, remoteProxyAuthState(proxy)) {
			return nil, fmt.Errorf("auth profile generation differs from proxy; drain workers and explicitly recreate the proxy")
		}
		return proxy, nil
	}
	hostState := hostProxyAuthState()
	if !proxyAuthStateMismatch(hostState, remoteProxyAuthState(proxy)) {
		return proxy, nil
	}

	mgr, err := sandbox.NewManager(sandbox.ManagerConfig{})
	if err != nil {
		return nil, fmt.Errorf("checking workers before auth proxy recreation: %w", err)
	}
	workers, err := mgr.List(false)
	if err != nil {
		return nil, fmt.Errorf("checking workers before auth proxy recreation: %w", err)
	}
	if runningWorkersBlockProxyRecreation(len(workers), force) {
		names := make([]string, 0, len(workers))
		for _, worker := range workers {
			names = append(names, worker.Name)
		}
		return nil, fmt.Errorf(
			"host and auth proxy credentials differ, but %d worker(s) are running (%s); stop and restart them, or rerun with --force-recreate-proxy (running workers will lose authentication)",
			len(workers), strings.Join(names, ", "))
	}

	if len(workers) > 0 {
		fmt.Printf("Host and auth proxy credentials differ; force-recreating auth proxy with %d running worker(s)...\n", len(workers))
	} else {
		fmt.Println("Host and auth proxy credentials differ; recreating auth proxy...")
	}
	if err := removeProxy(ctx, proxyRunName, true); err != nil {
		return nil, fmt.Errorf("removing stale auth proxy container %q: %w", proxyRunName, err)
	}

	// `oaiso run` has its own admin-secret flag; propagate it to the shared
	// proxy startup path used by `oaiso proxy run`.
	proxyAdminSecret = runProxyAdminSecret
	proxyAdminSecretFile = runProxyAdminSecretFile
	if err := startProxyContainer(ctx); err != nil {
		return nil, fmt.Errorf("recreating auth proxy: %w", err)
	}

	adminSecret, err := effectiveRunProxySecret()
	if err != nil {
		return nil, err
	}
	refreshed, err := connectProxyWithRetry(proxyAdminURL, proxyContainerURL, adminSecret, 30)
	if err != nil {
		return nil, fmt.Errorf("reconnecting recreated auth proxy: %w", err)
	}
	if proxyAuthStateMismatch(hostState, remoteProxyAuthState(refreshed)) {
		return nil, fmt.Errorf("recreated auth proxy still does not match host credentials")
	}
	return refreshed, nil
}

// imageNeedsBuild reports whether image should be (re)built before starting the
// container, and why. It always returns true when the image doesn't exist
// locally. When auto-build is enabled (see autoBuildEnabled) and the image
// matches a known template, it also rebuilds when the template's on-disk
// Dockerfile has been modified since the image was last built. Errors from the
// (best-effort) staleness check are swallowed — only the initial existence
// check can fail this function, so a transient node API hiccup while
// checking staleness never blocks a run that would otherwise succeed.
func imageNeedsBuild(sbMgr *sandbox.Manager, image string, projectConfigs ...*ProjectConfig) (bool, string, error) {
	exists, err := sbMgr.ImageExists(image)
	if err != nil {
		return false, "", err
	}
	if !exists {
		return true, fmt.Sprintf("Image %s not found locally", image), nil
	}

	if !autoBuildEnabled() {
		return false, "", nil
	}
	if len(projectConfigs) != 0 {
		cfg := projectConfigs[0]
		if cfg != nil && cfg.Build.Dockerfile != "" && cfg.Build.Tag == image {
			createdAt, err := sbMgr.ImageCreatedAt(image)
			if err == nil {
				path, _, _ := projectDockerfile(cfg)
				if info, statErr := os.Stat(path); statErr == nil && info.ModTime().After(createdAt) {
					return true, fmt.Sprintf("Project Dockerfile for %s has changed since it was last built", image), nil
				}
			}
			return false, "", nil
		}
	}

	tmplName := template.MatchTag(image)
	if tmplName == "" {
		return false, "", nil
	}
	tmpl, err := template.Get(tmplName)
	if err != nil {
		return false, "", nil
	}
	createdAt, err := sbMgr.ImageCreatedAt(image)
	if err != nil {
		return false, "", nil
	}
	stale, err := tmpl.StaleAgainst(createdAt)
	if err != nil || !stale {
		return false, "", nil
	}
	return true, fmt.Sprintf("Template for %s has changed since it was last built", image), nil
}

// autoBuildEnabled reports whether codex-dock should rebuild an existing
// template-derived image when its Dockerfile has changed on disk since the
// image was built. Controlled via the "auto_build" (global) / "run.auto_build"
// (section) config keys; defaults to enabled when unset.
func autoBuildEnabled() bool {
	if viper.IsSet("run.auto_build") {
		return viper.GetBool("run.auto_build")
	}
	if viper.IsSet("auto_build") {
		return viper.GetBool("auto_build")
	}
	return true
}

// offerToStartProxy is called when connecting to the auth proxy failed. When the
// failure looks like "proxy not running", the proxy URLs are at their defaults,
// and stdin is a terminal, it prompts the user to build/start the proxy container
// and, on yes, starts it and retries the connection. Otherwise it returns the
// original error unchanged.
func offerToStartProxy(cmd *cobra.Command, connErr error) (*authproxy.RemoteProxy, error) {
	urlsDefault := !cmd.Flags().Changed("proxy-admin-url") && !cmd.Flags().Changed("proxy-container-url")
	if !isProxyUnreachable(connErr) || !urlsDefault || !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil, connErr
	}

	fmt.Println("Auth Proxy is not running.")
	if !confirmYesNo(os.Stdin, "Build/start the auth proxy container now? [y/N]: ") {
		return nil, connErr
	}

	applyForwardPolicyDefaults(cmd)
	if selectedAuthProfile() != "" || runProxyAdminSecretFile != "" {
		proxyAdminSecret = runProxyAdminSecret
		proxyAdminSecretFile = runProxyAdminSecretFile
	}
	if err := startProxyContainer(cmd.Context()); err != nil {
		return nil, fmt.Errorf("starting auth proxy: %w", err)
	}

	// The container needs a moment to bind its listeners; retry briefly.
	adminSecret, err := effectiveRunProxySecret()
	if err != nil {
		return nil, err
	}
	proxy, err := connectProxyWithRetry(proxyAdminURL, proxyContainerURL, adminSecret, 30)
	if err != nil {
		return nil, fmt.Errorf("auth proxy started but did not become ready: %w", err)
	}
	return proxy, nil
}

// isProxyUnreachable reports whether err indicates the proxy is simply not
// listening (as opposed to, say, an auth/secret rejection that starting it
// wouldn't fix).
func isProxyUnreachable(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "connection refused") ||
		strings.Contains(s, "no such host") ||
		strings.Contains(s, "no route to host") ||
		strings.Contains(s, "i/o timeout") ||
		strings.Contains(s, "connect: ")
}

// confirmYesNo reads a line from r and returns true only for an affirmative
// answer. An empty line (just Enter) defaults to no.
func confirmYesNo(r io.Reader, prompt string) bool {
	fmt.Print(prompt)
	line, _ := bufio.NewReader(r).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

// connectProxyWithRetry retries NewRemoteProxy a few times so a just-started
// proxy container has time to bind its listeners.
func connectProxyWithRetry(adminURL, containerURL, secret string, attempts int) (*authproxy.RemoteProxy, error) {
	var lastErr error
	for i := 0; i < attempts; i++ {
		p, err := authproxy.NewRemoteProxy(adminURL, containerURL, secret)
		if err == nil {
			return p, nil
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	return nil, lastErr
}

func applyRunConfigDefaults(cmd *cobra.Command) {
	proxyAllowedModels = viper.GetStringSlice("proxy.allowed_models")
	if !cmd.Flags().Changed("watch-polling") {
		runOpts.WatchPolling = viper.GetBool("run.watch_polling")
	}
	flags := cmd.Flags()
	if !flags.Changed("policy-preset") {
		runPolicyPreset = viper.GetString("run.policy_preset")
	}
	applyRuntimeDefaults(cmd)
	if !runOpts.DirectAgent && !flags.Changed("agent") && viper.IsSet("run.agent") {
		agentFlag = viper.GetString("run.agent")
	}
	if !flags.Changed("model") && viper.IsSet("run.model") {
		runOpts.Model = viper.GetString("run.model")
	}
	if !flags.Changed("profile-size") && viper.IsSet("run.profile_size") {
		runOpts.ResourceProfile = viper.GetString("run.profile_size")
	}
	if !flags.Changed("read-only") && viper.IsSet("run.read_only") {
		runOpts.ReadOnly = viper.GetBool("run.read_only")
	}
	if !flags.Changed("read-only-rootfs") && viper.IsSet("run.read_only_rootfs") {
		runOpts.ReadOnlyRootFS = viper.GetBool("run.read_only_rootfs")
	}
	if !flags.Changed("agent-state-size") && viper.IsSet("run.agent_state_size") {
		runOpts.AgentStateSize = viper.GetString("run.agent_state_size")
	}
	if !flags.Changed("no-git-metadata") && viper.IsSet("run.no_git_metadata") {
		runOpts.NoGitMetadata = viper.GetBool("run.no_git_metadata")
	}
	if !flags.Changed("no-internet") && viper.IsSet("run.no_internet") {
		runOpts.NoInternet = viper.GetBool("run.no_internet")
	}
	if !flags.Changed("egress-allow-domain") && viper.IsSet("run.egress_allow_domains") {
		runEgressAllow = viper.GetStringSlice("run.egress_allow_domains")
	}

	if !flags.Changed("image") {
		if flags.Changed("template") {
			// An explicit --template flag always wins over config-file
			// settings, matching the CLI flags > config.toml priority used
			// throughout codex-dock.
			if tmpl, err := template.Get(runTemplate); err == nil {
				runOpts.Image = tmpl.Tag("")
			}
		} else {
			if v := viper.GetString("run.image"); v != "" {
				runOpts.Image = v
			} else if v := viper.GetString("default_image"); v != "" {
				runOpts.Image = v
			}

			// When a default template is configured and --image was not explicitly
			// set, resolve the template's tag as the image name. This allows
			// `default_template = "pwn"` to automatically use `codex-dock:pwn`.
			tmplName := viper.GetString("run.template")
			if tmplName == "" {
				tmplName = viper.GetString("default_template")
			}
			if tmplName != "" {
				if tmpl, err := template.Get(tmplName); err == nil {
					runOpts.Image = tmpl.Tag("")
				}
			}
		}
	}

	if !flags.Changed("token-ttl") {
		if viper.IsSet("run.token_ttl") {
			runOpts.TokenTTL = viper.GetInt("run.token_ttl")
		} else if viper.IsSet("default_token_ttl") {
			runOpts.TokenTTL = viper.GetInt("default_token_ttl")
		}
	}

	if !flags.Changed("approval-mode") && viper.IsSet("run.approval_mode") {
		approvalModeFlag = viper.GetString("run.approval_mode")
	}

	if !flags.Changed("user") && viper.IsSet("run.user") {
		userMode = viper.GetString("run.user")
	}

	if !flags.Changed("shell-type") {
		if v := viper.GetString("run.shell_type"); v != "" {
			shellTypeFlag = v
		} else if v := viper.GetString("default_shell_type"); v != "" {
			shellTypeFlag = v
		}
	}

	if !flags.Changed("mount") && viper.IsSet("run.mounts") {
		mountFlags = viper.GetStringSlice("run.mounts")
	}

	if !flags.Changed("proxy-container-url") {
		if v := viper.GetString("proxy.container_url"); v != "" {
			proxyContainerURL = v
		} else if v := viper.GetString("firewall.proxy_container_url"); v != "" {
			// Backwards compatibility with the old [firewall] config section.
			proxyContainerURL = v
		}
	}
	if !flags.Changed("proxy-admin-url") {
		if value := viper.GetString("proxy.admin_url"); value != "" {
			proxyAdminURL = value
		}
	}
	if !flags.Changed("proxy-admin-secret-file") {
		runProxyAdminSecretFile = ""
		if !flags.Changed("proxy-admin-secret") {
			runProxyAdminSecretFile = viper.GetString("proxy.admin_secret_file")
		}
	}
	if !flags.Changed("proxy-admin-secret") {
		runProxyAdminSecret = ""
		if flags.Changed("proxy-admin-secret-file") {
			return
		}
		runProxyAdminSecret = viper.GetString("proxy.admin_secret")
		if value, ok := os.LookupEnv("CODEX_DOCK_PROXY_ADMIN_SECRET"); ok {
			runProxyAdminSecret = value
		}
	}
}

func runSingle(mgr *sandbox.Manager, sigCh <-chan os.Signal) error {
	opts := runOpts
	containerID, err := workerapp.Launch(context.Background(), diagnosticRunner{runner: mgr, directory: runObserveDir}, opts)
	if err != nil {
		return startupFailure("worker launch", fmt.Errorf("starting container: %w", err))
	}

	if opts.Detach {
		fmt.Printf("Container started: %s\n", containerID[:12])
		return nil
	}

	// Wait for signal or container exit
	exitCh := mgr.Wait(containerID)
	var runErr error
	select {
	case <-sigCh:
		fmt.Println("\nStopping container...")
		runErr = mgr.Stop(containerID, 10) // Stop() revokes token (F-AUTH-04)
	case err := <-exitCh:
		// Container exited on its own — revoke its token (F-AUTH-04)
		mgr.RevokeToken(containerID)
		runErr = err
	}

	// Foreground cleanup: remove the container and its per-worker Internal network
	// so they don't accumulate across runs (use --keep to retain them).
	if !runKeep {
		if err := mgr.Remove(containerID, true); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("cleaning up container/network: %w", err))
		}
	}
	return runErr
}

func runParallel(mgr *sandbox.Manager, sigCh <-chan os.Signal) error {
	opts := runOpts
	n := opts.Parallel
	fmt.Printf("Starting %d parallel workers...\n", n)

	containerIDs := make([]string, 0, n)
	cleanupFailedBatch := func() {
		for _, id := range containerIDs {
			if err := mgr.Remove(id, true); err != nil {
				fmt.Fprintf(os.Stderr, "warning: cleaning failed batch worker %s: %v\n", id, err)
			}
		}
	}

	for i := 1; i <= n; i++ {
		o := opts
		branch := opts.Branch
		if branch == "" {
			branch = fmt.Sprintf("worker-%d", i)
		} else {
			branch = fmt.Sprintf("%s-%d", branch, i)
		}
		o.Branch = branch
		o.Name = fmt.Sprintf("%s-%d", opts.Name, i)

		if opts.UseWorktree {
			o.NewBranch = true
		}
		containerID, err := workerapp.Launch(context.Background(), diagnosticRunner{runner: mgr, directory: runObserveDir}, o)
		if err != nil {
			if containerID != "" {
				containerIDs = append(containerIDs, containerID)
			}
			cleanupFailedBatch()
			return startupFailure("worker launch", fmt.Errorf("starting worker %d: %w", i, err))
		}
		containerIDs = append(containerIDs, containerID)
		fmt.Printf("  Worker %d started: %s\n", i, containerID[:12])
	}

	if opts.Detach {
		return nil
	}

	<-sigCh
	fmt.Println("\nStopping all workers...")
	var cleanupErr error
	for _, id := range containerIDs {
		if err := mgr.Stop(id, 10); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("stopping %s: %w", id, err))
		}
		// Remove the container and its per-worker network so they don't accumulate.
		if !runKeep {
			if err := mgr.Remove(id, true); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("cleaning up %s: %w", id, err))
			}
		}
	}
	return cleanupErr
}

// parseMountSpecs parses --mount flag values of the form
// "<host-path>:<container-path>[:ro|rw]" into sandbox.MountSpec values.
// The host path is resolved to an absolute path; the container path must
// already be absolute and may not be "/workspace" (reserved for --project).
func parseMountSpecs(raw []string) ([]sandbox.MountSpec, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	mounts := make([]sandbox.MountSpec, 0, len(raw))
	targets := make(map[string]bool, len(raw))
	for _, spec := range raw {
		parts := strings.Split(spec, ":")
		if len(parts) < 2 || len(parts) > 3 {
			return nil, fmt.Errorf("invalid --mount %q; expected <host-path>:<container-path>[:ro|rw]", spec)
		}

		hostPath, containerPath := parts[0], parts[1]
		if hostPath == "" || containerPath == "" {
			return nil, fmt.Errorf("invalid --mount %q; host and container paths must not be empty", spec)
		}
		if !filepath.IsAbs(containerPath) {
			return nil, fmt.Errorf("invalid --mount %q; container path must be absolute", spec)
		}
		if containerPath == "/workspace" {
			return nil, fmt.Errorf("invalid --mount %q; /workspace is reserved for --project (use --read-only for that mount)", spec)
		}
		if targets[containerPath] {
			return nil, fmt.Errorf("invalid --mount %q; container path %q is mounted more than once", spec, containerPath)
		}

		readOnly := false
		if len(parts) == 3 {
			switch parts[2] {
			case "ro":
				readOnly = true
			case "rw":
				readOnly = false
			default:
				return nil, fmt.Errorf("invalid --mount %q; mode must be \"ro\" or \"rw\"", spec)
			}
		}

		absHost, err := filepath.Abs(hostPath)
		if err != nil {
			return nil, fmt.Errorf("resolving host path %q: %w", hostPath, err)
		}

		targets[containerPath] = true
		mounts = append(mounts, sandbox.MountSpec{
			Source:   absHost,
			Target:   containerPath,
			ReadOnly: readOnly,
		})
	}
	return mounts, nil
}

// resolveShellType resolves --shell-type (and its config-file defaults,
// already folded into flagValue by applyRunConfigDefaults) into a
// sandbox.ShellType. When flagValue is empty, it auto-detects from the
// host's $SHELL so zsh users get a zsh shell with no extra configuration —
// except in shellMode (--shell), which overrides the container Entrypoint
// directly to the shell binary, bypassing entrypoint.sh entirely. That path
// has no way to fall back to bash if the auto-detected shell isn't installed
// in the image (Docker just fails to start the container), so it defaults to
// bash instead and only honors an explicit --shell-type.
func resolveShellType(flagValue string, shellMode bool) (sandbox.ShellType, error) {
	shellType := sandbox.ShellType(flagValue)
	if shellType == "" {
		if shellMode {
			shellType = sandbox.ShellBash
		} else {
			shellType = detectShellTypeFromEnv()
		}
	}
	if !sandbox.ValidShellType(shellType) {
		return "", fmt.Errorf("invalid --shell-type %q; valid values: bash, zsh", flagValue)
	}
	return shellType, nil
}

// detectShellTypeFromEnv picks a sandbox.ShellType from the host's $SHELL
// environment variable, falling back to ShellBash when $SHELL is unset or
// names a shell codex-dock doesn't support inside the container.
func detectShellTypeFromEnv() sandbox.ShellType {
	switch filepath.Base(os.Getenv("SHELL")) {
	case "zsh":
		return sandbox.ShellZsh
	default:
		return sandbox.ShellBash
	}
}

// resolveExecCmd merges --exec with a trailing "-- command..." into the single
// DOCK_EXEC_CMD string the container runs. The two are mutually exclusive.
func resolveExecCmd(execFlag string, trailingArgs []string) (string, error) {
	if len(trailingArgs) == 0 {
		return execFlag, nil
	}
	if execFlag != "" {
		return "", fmt.Errorf("--exec and a trailing \"-- command...\" are mutually exclusive")
	}
	return shellJoin(trailingArgs), nil
}

// shellJoin re-quotes argv so that, when passed to `sh -c`, it reproduces the
// same arguments verbatim. Used for a trailing "-- command..." on the command
// line so multi-word commands don't need to be hand-quoted into one string
// the way --exec requires.
func shellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shellQuote(a)
	}
	return strings.Join(parts, " ")
}

// shellQuote wraps s in single quotes if it contains characters that would
// otherwise be interpreted by the shell, escaping any embedded single quotes.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\\\"'`$&|;<>(){}[]*?!~#") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func resolveProjectDir(dir string) (string, error) {
	if dir == "." {
		return os.Getwd()
	}
	return dir, nil
}

// resolveContainerUser converts a --user mode string into a "uid:gid" value
// suitable for container.Config.User.
//
//	""        → "" (use image default)
//	"current" → "<uid>:<gid>" of the process running this command
//	"codex"   → "1001:1001" (codex user used by the default image)
//	"dir"     → "<uid>:<gid>" of the owner of projectDir
//	anything else is returned as-is (e.g. "1000", "1000:1000")
func resolveContainerUser(mode, projectDir string) (string, error) {
	switch mode {
	case "":
		return "", nil
	case "current":
		uid := syscall.Getuid()
		gid := syscall.Getgid()
		return fmt.Sprintf("%d:%d", uid, gid), nil
	case "codex":
		return "1001:1001", nil
	case "dir":
		info, err := os.Stat(projectDir)
		if err != nil {
			return "", fmt.Errorf("stat %s: %w", projectDir, err)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return "", fmt.Errorf("cannot read uid/gid from %s on this platform", projectDir)
		}
		return fmt.Sprintf("%d:%d", stat.Uid, stat.Gid), nil
	default:
		return mode, nil
	}
}
