package sandbox

import (
	"context"
	crand "crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/pacificbelt30/codex-dock/internal/agentconfig"
	"github.com/pacificbelt30/codex-dock/internal/authproxy"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/kataguard"
	"github.com/pacificbelt30/codex-dock/internal/launchtiming"
	"github.com/pacificbelt30/codex-dock/internal/network"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
	"github.com/pacificbelt30/codex-dock/internal/workertransport"
)

const (
	labelPrefix             = "codex-dock."
	labelManaged            = labelPrefix + "managed"
	labelBranch             = labelPrefix + "branch"
	labelTask               = labelPrefix + "task"
	labelRequest            = labelPrefix + "request-key"
	labelRequestFingerprint = labelPrefix + "request-fingerprint"
)

// ManagerConfig holds configuration for the sandbox manager.
type ManagerConfig struct {
	Socket  string
	Proxy   authproxy.Service
	Network *network.Manager
	// HTTPProxyURL is the general-egress (forward) proxy URL workers use for
	// HTTP(S)_PROXY, e.g. http://codex-http-proxy:18082. Empty falls back to the
	// auth proxy's endpoint (single-proxy/back-compat).
	HTTPProxyURL string
	Verbose      bool
	Debug        bool
	// Used by management commands to reconnect without putting the admin
	// secret in worker labels or its VM. Only the URLs are persisted.
	ProxyAdminURL         string
	ProxyConnector        func(adminURL, containerURL string) (authproxy.Service, error)
	ProxyConnectorContext func(context.Context, string, string) (authproxy.Service, error)
	KataConfig            *kataguard.Config
	StateDir              string
}

// Manager handles container lifecycle for codex-dock workers.
type Manager struct {
	cli                   *client.Client
	proxy                 authproxy.Service
	network               *network.Manager
	httpProxyURL          string
	verbose               bool
	debug                 bool
	proxyAdminURL         string
	proxyConnector        func(adminURL, containerURL string) (authproxy.Service, error)
	proxyConnectorContext func(context.Context, string, string) (authproxy.Service, error)
	kataConfig            *kataguard.Config
	guard                 kataNode
	guardMu               sync.Mutex
	attachMu              sync.Mutex
	attachments           map[string]chan struct{}
	stateDir              string
	execProbe             func(context.Context, string, []string) ([]byte, error)
}

// NewManager connects to the dedicated containerd node's compatibility API.
func NewManager(cfg ManagerConfig) (*Manager, error) {
	var cli *client.Client
	var err error
	if cfg.Socket == "" {
		cli, err = engine.NewClient()
	} else {
		cli, err = engine.NewClientAt(cfg.Socket)
	}
	if err != nil {
		return nil, fmt.Errorf("connecting to containerd node: %w", err)
	}
	return &Manager{
		cli:                   cli,
		proxy:                 cfg.Proxy,
		network:               cfg.Network,
		httpProxyURL:          cfg.HTTPProxyURL,
		verbose:               cfg.Verbose,
		debug:                 cfg.Debug,
		proxyAdminURL:         cfg.ProxyAdminURL,
		proxyConnector:        cfg.ProxyConnector,
		proxyConnectorContext: cfg.ProxyConnectorContext,
		kataConfig:            cfg.KataConfig,
		stateDir:              cfg.StateDir,
	}, nil
}

func (m *Manager) Close() error {
	m.guardMu.Lock()
	defer m.guardMu.Unlock()
	var err error
	if closer, ok := m.guard.(io.Closer); ok {
		err = closer.Close()
	}
	return errors.Join(err, m.cli.Close())
}

// Run creates and starts a sandboxed Codex container.
func (m *Manager) Run(opts RunOptions) (containerID string, runErr error) {
	return m.RunContext(context.Background(), opts)
}

// RunContext creates and starts a worker with a caller-controlled deadline.
// Failure cleanup uses its own bounded context so cancellation cannot leave
// the worker running merely because the caller stopped waiting.
func (m *Manager) RunContext(parent context.Context, opts RunOptions) (containerID string, runErr error) {
	launchtiming.Next(parent, "preflight")
	storage, err := workerpolicy.NormalizeTemporaryStorage(opts.TemporaryStorage)
	if err != nil {
		return "", err
	}
	opts.TemporaryStorage = storage
	if err := opts.TokenUsagePolicy.Validate(); err != nil {
		return "", err
	}
	if opts.TokenUsagePolicy != nil {
		issuer, ok := m.proxy.(authproxy.UsageIssuer)
		if !ok || !issuer.SupportsTokenUsagePolicy() {
			return "", fmt.Errorf("execution usage limits require a compatible auth proxy with durable usage storage")
		}
		opts.TokenUsagePolicy = opts.TokenUsagePolicy.Clone()
	}
	if err := opts.Filesystem.Validate(); err != nil {
		return "", err
	}
	if err := opts.Lifecycle.Validate(); err != nil {
		return "", err
	}
	if err := opts.Environment.Validate(); err != nil {
		return "", err
	}
	if opts.Image == "" {
		opts.Image = "codex-dock:latest"
	}
	ctx, cancel := context.WithTimeout(parent, 180*time.Second)
	defer cancel()
	if len(opts.Filesystem.DenyRead)+len(opts.Filesystem.DenyWrite) > 0 || opts.ReadOnlyRootFS || opts.Environment.ImageID != "" || opts.Lifecycle.IdleTimeout > 0 || opts.Lifecycle.ProtectMain || len(storage) > 0 {
		info, err := m.cli.Info(ctx)
		if err != nil {
			return "", err
		}
		if opts.Lifecycle.ProtectMain {
			supported := false
			for _, feature := range info.DriverStatus {
				supported = supported || feature == [2]string{"cxd.main-activity", "1"}
			}
			if !supported {
				return "", fmt.Errorf("main-process idle protection requires an updated Oaiso node")
			}
		}
		supported := false
		idleSafe := opts.Lifecycle.IdleTimeout == 0
		temporarySupported := len(storage) == 0
		for _, feature := range info.DriverStatus {
			if feature == [2]string{"cxd.temporary-storage", "1"} {
				temporarySupported = true
			}
			if feature == [2]string{"cxd.idle-safe", "1"} {
				idleSafe = true
			}
			if feature == [2]string{"cxd.worker-policy", "1"} {
				supported = true
			}
		}
		if !supported || !idleSafe || !temporarySupported {
			return "", fmt.Errorf("worker policy requires a newer Oaiso node; upgrade the node before enabling it")
		}
	}
	if opts.Compatibility.SSHProxy || opts.Compatibility.CABundle != "" {
		info, err := m.cli.ImageInspect(ctx, opts.Image)
		if err != nil {
			return "", err
		}
		if info.Config == nil || info.Config.Labels["io.codex-dock.start-hooks"] != "1" {
			return "", fmt.Errorf("SSH/custom CA compatibility requires a rebuilt worker image")
		}
	}
	if opts.Environment.ImageID != "" {
		info, err := m.cli.ImageInspect(ctx, opts.Image)
		if err != nil {
			return "", err
		}
		if info.ID != opts.Environment.ImageID {
			return "", fmt.Errorf("image lock mismatch: rebuild or explicitly refresh the environment lock")
		}
	}
	isolation, registration, err := ResolveRuntime(opts.Runtime, opts.KataRuntime)
	if err != nil {
		return "", err
	}
	if err := m.CheckRuntime(ctx, opts); err != nil {
		return "", err
	}
	if len(storage) > 0 && isolation != RuntimeKata {
		return "", fmt.Errorf("bounded temporary storage requires Kata isolation")
	}
	if opts.CreateOnly && isolation != RuntimeKata {
		return "", fmt.Errorf("create without initial start requires Kata isolation")
	}
	profile, err := m.resourceProfile(ctx, opts.ResourceProfile)
	if err != nil {
		return "", err
	}
	if m.proxy != nil {
		for _, endpoint := range []string{m.proxyAdminURL, m.proxy.ContainerEndpoint()} {
			if endpoint == "" {
				continue
			}
			u, err := url.Parse(endpoint)
			if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				return "", fmt.Errorf("proxy URLs must not contain credentials, queries or fragments; use the admin-secret setting")
			}
		}
	}
	if isolation == RuntimeKata && m.network == nil {
		m.network = network.NewManagerWithClient(m.cli)
	}

	name := opts.Name
	if name == "" {
		// Pick a name whose container and per-worker network are both free, so two
		// workers never end up sharing one Internal network (which would break
		// isolation) when generated names collide.
		name = pickUniqueName(generateName, m.nameTaken, 12)
	}

	// Determine workspace path
	workspace := opts.ProjectDir
	if opts.WorktreePath != "" {
		workspace = opts.WorktreePath
	}

	absWorkspace, err := absolutePath(workspace)
	if err != nil {
		return "", fmt.Errorf("resolving workspace path: %w", err)
	}

	if err := validateFilesystemTargets(absWorkspace, opts.Filesystem); err != nil {
		return "", err
	}

	// Build package install script
	var allPkgs []Package
	for _, spec := range opts.Packages {
		allPkgs = append(allPkgs, ParsePackage(spec))
	}
	if opts.PkgFile != "" {
		filePkgs, err := LoadPackageFile(opts.PkgFile)
		if err != nil {
			return "", fmt.Errorf("loading package file: %w", err)
		}
		allPkgs = append(allPkgs, filePkgs...)
	}
	installScript := BuildInstallScript(allPkgs)
	if opts.ReadOnlyRootFS && installScript != "" {
		return "", fmt.Errorf("package installation requires a writable rootfs; build packages into the image before using --read-only-rootfs")
	}
	mounts := buildMounts(opts, absWorkspace)
	if opts.Compatibility.CABundle != "" {
		source, err := filepath.Abs(opts.Compatibility.CABundle)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(source)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("CA bundle must be a regular file")
		}
		if info.Size() > 16<<20 {
			return "", fmt.Errorf("CA bundle exceeds 16 MiB")
		}
		bundle, err := os.ReadFile(source)
		if err != nil {
			return "", err
		}
		if !x509.NewCertPool().AppendCertsFromPEM(bundle) {
			return "", fmt.Errorf("CA bundle contains no certificates")
		}
		mounts = append(mounts, mount.Mount{Type: mount.TypeBind, Source: source, Target: "/run/cxd-custom-ca.pem", ReadOnly: true})
	}
	var gitMounts []mount.Mount
	var gitEnv []string
	if !opts.NoGitMetadata {
		gitMounts, gitEnv, err = linkedWorktreeMounts(ctx, absWorkspace, opts.ReadOnly, mounts)
		if err != nil {
			return "", err
		}
	}
	mounts = append(mounts, gitMounts...)
	if isolation == RuntimeKata {
		if err = canonicalizeKataMounts(mounts); err != nil {
			return "", err
		}
		if len(mounts) > 0 && mounts[0].Target == "/workspace" {
			absWorkspace = mounts[0].Source
		}
		if err = validateKataMountSourceIsolation(mounts); err != nil {
			return "", err
		}
		if err = m.protectKataMounts(mounts); err != nil {
			return "", err
		}
	}
	if err := workerpolicy.ValidateTemporaryBudget(storage, profile.GuestMemoryBytes); err != nil {
		return "", err
	}
	denied := append(append([]string(nil), opts.Filesystem.DenyRead...), opts.Filesystem.DenyWrite...)
	if err := validateTemporaryTopology(storage, mounts, denied, opts.ReadOnlyRootFS); err != nil {
		return "", err
	}
	if err := m.checkCleanupState(); err != nil {
		return "", fmt.Errorf("preparing cleanup state: %w", err)
	}

	var createdID, createdNetwork string
	var allocation string
	tokenIssued := false
	proxies := m.proxyContainerNames(opts.NoInternet)
	defer func() {
		if runErr == nil {
			return
		}
		launchtiming.Next(ctx, "worker_cleanup")
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cleanupCancel()
		if tokenIssued && m.proxy != nil {
			m.proxy.RevokeToken(name)
		}
		if createdID != "" {
			if err := m.Remove(createdID, true); err != nil {
				containerID = createdID // Caller must retain its worktree for recovery.
				runErr = errors.Join(runErr, fmt.Errorf("cleanup container %s: %w", createdID, err))
				return // Do not detach the network while a VM may still be alive.
			}
			return // Durable removal also recovered the network/allocation/worktree.
		}
		if createdNetwork != "" {
			if err := m.network.RemoveKataWorkerNetwork(cleanupCtx, createdNetwork, proxies); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("cleanup network %s: %w", createdNetwork, err))
			}
		}
		if allocation != "" {
			if err := m.releaseKataAllocation(cleanupCtx, allocation, createdID); err != nil {
				runErr = errors.Join(runErr, err)
			}
		}
	}()

	// Note: auth.json is NOT bind-mounted even in OAuth mode.
	// The Auth Proxy provides only a placeholder token to the container via
	// the /token endpoint, keeping the refresh_token on the host (F-AUTH-01).

	// Container labels for identification
	labels := map[string]string{
		labelManaged:                     "true",
		labelBranch:                      opts.Branch,
		labelTask:                        opts.Task,
		labelIsolation:                   isolation,
		labelRuntime:                     registration,
		labelPrefix + "resource-profile": profile.Name,
	}
	if opts.AgentPolicy.Name != "" {
		labels[labelPrefix+"agent-policy"] = opts.AgentPolicy.Name
	}
	if opts.RequestKey != "" {
		if len(opts.RequestKey) != 64 || len(opts.RequestFingerprint) != 64 || len(opts.AllocationID) != 32 {
			return "", fmt.Errorf("invalid external request identity")
		}
		if _, err := hex.DecodeString(opts.RequestKey + opts.RequestFingerprint + opts.AllocationID); err != nil {
			return "", fmt.Errorf("invalid external request identity: %w", err)
		}
		labels[labelRequest] = opts.RequestKey
		labels[labelRequestFingerprint] = opts.RequestFingerprint
	}
	if opts.UseWorktree && opts.WorktreePath != "" {
		labels[labelPrefix+"worktree"] = absWorkspace
	}
	if m.proxy != nil {
		if p, ok := m.proxy.(authproxy.ProfileService); ok && p.AuthProfile() != "" {
			labels[labelPrefix+"auth-profile"] = p.AuthProfile()
			labels[labelPrefix+"auth-profile-connection"] = p.ProfileConnectionVersion()
		}
		labels[labelPrefix+"proxy-container-url"] = m.proxy.ContainerEndpoint()
		labels[labelPrefix+"proxy-admin-url"] = m.proxyAdminURL
		labels[labelPrefix+"token-ttl"] = strconv.Itoa(opts.TokenTTL)
		if opts.TokenModelPolicy != nil {
			if err := opts.TokenModelPolicy.Validate(); err != nil {
				return "", err
			}
			encoded, err := json.Marshal(opts.TokenModelPolicy)
			if err != nil {
				return "", err
			}
			if len(encoded)+len(labelPrefix+"token-model-policy") > 4096 {
				return "", fmt.Errorf("worker model policy exceeds container label limit")
			}
			labels[labelPrefix+"token-model-policy"] = string(encoded)
		}
		if opts.TokenUsagePolicy != nil {
			encoded, err := json.Marshal(opts.TokenUsagePolicy)
			if err != nil {
				return "", err
			}
			labels[labelPrefix+"token-usage-policy"] = string(encoded)
		}
		if !opts.TokenIdentity.Empty() {
			encoded, err := json.Marshal(opts.TokenIdentity)
			if err != nil {
				return "", err
			}
			labels[labelPrefix+"token-identity"] = string(encoded)
		}
	}
	if opts.Environment.ImageID != "" {
		labels[labelPrefix+"image-id"] = opts.Environment.ImageID
	}
	if opts.ReadOnlyRootFS {
		labels[labelPrefix+"read-only-rootfs"] = "true"
	}

	if err := addWorkerPolicyLabels(labels, opts); err != nil {
		return "", err
	}

	// Per-worker Internal network: create it and attach the proxy so the worker
	// can reach the proxy/router but nothing else (no other worker, host, or
	// direct internet). Enforced entirely by Docker — no iptables/sudo.
	launchtiming.Next(ctx, "network_setup")
	netName := network.WorkerNetworkName(name)
	if m.network != nil {
		if isolation == RuntimeKata {
			createdNetwork, err = m.network.CreateKataWorkerNetwork(ctx, name)
			if err != nil {
				return "", err
			}
			labels[labelPrefix+"network-id"] = createdNetwork
			encoded, err := json.Marshal(proxies)
			if err != nil {
				return "", err
			}
			labels[labelProxyHosts] = string(encoded)
		} else {
			if err := m.network.EnsureWorkerNetwork(name); err != nil {
				return "", fmt.Errorf("creating worker network: %w", err)
			}
		}
		// Attach the proxies the worker needs to this worker's Internal network.
		// The credential-injecting auth proxy is always attached; the
		// general-egress http proxy is attached only when general egress is
		// allowed. With --no-internet we deliberately leave the egress proxy
		// OFF the worker net so the worker has no path to it at all — not just a
		// missing HTTP(S)_PROXY env (which a worker could otherwise override).
		for _, proxyName := range proxies {
			if err := m.network.ConnectProxy(name, proxyName); err != nil {
				return "", fmt.Errorf("connecting proxy %q to worker network: %w", proxyName, err)
			}
		}
	}

	// Security: drop all capabilities, non-root user, isolated Internal network.
	launchtiming.Next(ctx, "worker_configuration")
	hostConfig := buildHostConfig(mounts, netName)
	hostConfig.MaskedPaths = append(hostConfig.MaskedPaths, opts.Filesystem.DenyRead...)
	hostConfig.ReadonlyPaths = append(hostConfig.ReadonlyPaths, opts.Filesystem.DenyWrite...)
	if opts.ReadOnlyRootFS {
		hostConfig.ReadonlyRootfs = true
		hostConfig.Tmpfs = map[string]string{
			"/tmp":     "rw,nosuid,nodev,size=268435456,mode=1777",
			"/var/tmp": "rw,nosuid,nodev,size=268435456,mode=1777",
		}
	}
	var proxyHosts []string
	if isolation == RuntimeKata {
		proxyHosts, err = m.network.KataProxyHosts(ctx, createdNetwork, "", proxies)
		if err != nil {
			return "", err
		}
		opts.proxyHosts = proxyHosts
	}
	applyRuntime(hostConfig, isolation, registration, proxyHosts)
	// Reserve and validate the network before issuing credentials. A name
	// collision must not replace or revoke another worker's token.
	tokenIssued = m.proxy != nil && isolation != RuntimeKata
	if isolation == RuntimeKata && m.proxy != nil {
		if opts.TokenUsagePolicy != nil {
			issuer, ok := m.proxy.(authproxy.UsageIssuer)
			if !ok {
				return "", fmt.Errorf("auth proxy does not support usage preparation")
			}
			if err := issuer.PrepareUsage(name, opts.TokenIdentity, opts.TokenUsagePolicy); err != nil {
				return "", err
			}
		}
		token, err := randomID(32)
		if err != nil {
			return "", err
		}
		opts.pendingPlaceholder = "cdx-" + token
	}
	env, err := m.buildEnv(name, opts, installScript)
	if err != nil {
		return "", err
	}
	env = append(env, gitEnv...)
	var environmentReady string
	if len(opts.Environment.StartHooks) > 0 || len(opts.Environment.Readiness) > 0 {
		if opts.ShellMode {
			return "", fmt.Errorf("start hooks/readiness cannot be combined with raw --shell")
		}
		info, err := m.cli.ImageInspect(ctx, opts.Image)
		if err != nil {
			return "", err
		}
		if info.Config == nil || info.Config.Labels["io.codex-dock.start-hooks"] != "1" {
			return "", fmt.Errorf("image does not support start hooks; rebuild the worker image")
		}
		payload, err := json.Marshal(opts.Environment)
		if err != nil {
			return "", err
		}
		nonce, err := randomID(16)
		if err != nil {
			return "", err
		}
		environmentReady = "/var/tmp/cxd-ready-" + nonce
		env = append(env, "DOCK_ENVIRONMENT_JSON="+string(payload), "DOCK_ENVIRONMENT_READY="+environmentReady)
	}

	for _, spec := range storage {
		if hostConfig.Tmpfs == nil {
			hostConfig.Tmpfs = make(map[string]string)
		}
		hostConfig.Tmpfs[spec.Target] = spec.MountOptions()
	}
	// Build codex command
	codexArgs := buildCodexArgs(opts)

	containerConfig := &container.Config{
		Image:        opts.Image,
		Env:          env,
		WorkingDir:   "/workspace",
		Labels:       labels,
		Tty:          !opts.Detach,
		OpenStdin:    !opts.Detach,
		AttachStdin:  !opts.Detach,
		AttachStdout: !opts.Detach,
		AttachStderr: !opts.Detach,
		User:         opts.ContainerUser,
	}
	if opts.authenticationSession {
		if err := configureAuthenticationSession(containerConfig, hostConfig, opts, m.httpProxyURL); err != nil {
			return "", err
		}
	}
	if opts.ShellMode {
		// Override the Dockerfile ENTRYPOINT so Docker runs the chosen shell
		// directly instead of "/entrypoint.sh".
		containerConfig.Entrypoint = []string{shellBinaryPath(opts.ShellType)}
	} else if !opts.authenticationSession {
		containerConfig.Cmd = []string{"/entrypoint.sh"}
	}
	_ = codexArgs // passed via env
	launchtiming.Next(ctx, "capacity_reservation")
	if isolation == RuntimeKata {
		allocation = opts.AllocationID
		if allocation == "" {
			allocation, err = randomID(16)
			if err != nil {
				return "", err
			}
		}
		if err = m.prepareKataBootstrap(ctx, containerConfig, hostConfig, allocation, profile); err != nil {
			return "", err
		}
	}

	launchtiming.Next(ctx, "container_create")
	resp, err := m.cli.ContainerCreate(ctx, containerConfig, hostConfig, nil, nil, name)
	if err != nil {
		return "", fmt.Errorf("creating container %s: %w", name, err)
	}
	createdID = resp.ID
	launchtiming.Next(ctx, "created_worker_verification")
	unlock, err := m.lockCleanup(ctx, resp.ID)
	if err != nil {
		return "", err
	}
	defer unlock()
	if isolation == RuntimeKata {
		if err := m.validateKataWorker(ctx, resp.ID); err != nil {
			return "", err
		}
	}

	if m.verbose {
		fmt.Printf("Container created: %s (%s)\n", name, resp.ID[:12])
	}
	if opts.CreateOnly {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return resp.ID, nil
	}

	launchtiming.Next(ctx, "vm_start")
	if err := m.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("starting container %s: %w", name, err)
	}
	if isolation == RuntimeKata {
		launchtiming.Next(ctx, "host_guest_verification")
		if err := m.validateKataWorker(ctx, resp.ID); err != nil {
			return "", err
		}
		if err = m.verifyKataGuest(ctx, resp.ID, true); err != nil {
			return "", err
		}
		v, err := m.cli.ContainerInspect(ctx, resp.ID)
		if err != nil {
			return "", err
		}
		launchtiming.Next(ctx, "token_activation")
		tokenIssued = m.proxy != nil
		if err = m.restoreWorkerToken(v); err != nil {
			return "", err
		}
		launchtiming.Next(ctx, "bootstrap_release")
		if !opts.Detach {
			if width, height, ok := localTerminalSize(os.Stdin); ok {
				if err := m.cli.ContainerResize(ctx, resp.ID, container.ResizeOptions{Width: width, Height: height}); err != nil {
					return "", fmt.Errorf("setting initial worker terminal size: %w", err)
				}
			}
		}
		if _, err = m.execTrusted(ctx, resp.ID, []string{guestGuard, "release", allocation}); err != nil {
			return "", err
		}
	}

	launchtiming.Next(ctx, "environment_readiness")
	if len(opts.Environment.StartHooks) > 0 || len(opts.Environment.Readiness) > 0 {
		if err := m.waitEnvironment(ctx, resp.ID, environmentReady, opts.Environment.ReadinessTimeout); err != nil {
			return "", err
		}
	}
	if !opts.Detach {
		// Keep final output alive until Wait returns, including fast commands.
		done := make(chan struct{})
		m.attachMu.Lock()
		if m.attachments == nil {
			m.attachments = make(map[string]chan struct{})
		}
		m.attachments[resp.ID] = done
		m.attachMu.Unlock()
		go func() {
			m.attachIO(context.Background(), resp.ID)
			close(done)
			m.attachMu.Lock()
			if m.attachments[resp.ID] == done {
				delete(m.attachments, resp.ID)
			}
			m.attachMu.Unlock()
		}()
	}

	return resp.ID, nil
}

// Start (re)starts a stopped container by ID. Used by the TUI to restart exited workers (F-UI-02).
func (m *Manager) Start(containerID string) (startErr error) {
	return m.StartContext(context.Background(), containerID)
}

// StartContext starts a created or stopped worker and honors the caller's deadline.
func (m *Manager) StartContext(parent context.Context, containerID string) (startErr error) {
	ctx, cancel := context.WithTimeout(parent, 180*time.Second)
	defer cancel()
	v, err := m.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return err
	}
	if v.ContainerJSONBase == nil || v.Config == nil || v.HostConfig == nil || v.Config.Labels[labelManaged] != "true" {
		return fmt.Errorf("worker is missing its managed configuration")
	}
	if v.Config.Labels[labelAuthenticationSession] == "true" {
		return fmt.Errorf("authentication sessions cannot be resumed; start a new login")
	}
	unlock, err := m.lockCleanup(ctx, v.ID)
	if err != nil {
		return err
	}
	defer unlock()
	// Quarantine or removal may have completed while this start waited for
	// the lifecycle lock. Never restore credentials from a stale inspection.
	v, err = m.cli.ContainerInspect(ctx, v.ID)
	if err != nil {
		return err
	}
	if v.ContainerJSONBase == nil || v.Config == nil || v.HostConfig == nil || v.Config.Labels[labelManaged] != "true" {
		return fmt.Errorf("worker is missing its managed configuration")
	}
	if _, err := m.readCleanup(v.ID); err == nil {
		return fmt.Errorf("worker removal is pending; retry rm before creating another worker")
	} else if !os.IsNotExist(err) {
		return err
	}
	if v.Config.Labels[workerpolicy.QuarantinedLabel] == "true" {
		return fmt.Errorf("worker is quarantined; create a new worker to apply the new policy")
	}
	kata := v.Config.Labels[labelIsolation] == RuntimeKata
	if kata {
		if err = m.CheckRuntime(ctx, RunOptions{Runtime: RuntimeKata, KataRuntime: v.Config.Labels[labelRuntime]}); err != nil {
			return err
		}
		if err = m.validateKataWorker(ctx, v.ID); err != nil {
			return err
		}
	}
	if v.State != nil && v.State.Running {
		if kata {
			return m.verifyKataGuest(ctx, v.ID, false)
		}
		return nil
	}
	// Parse retained readiness before releasing user code.
	var readyPath string
	var environment workerpolicy.Environment
	if kata {
		for _, value := range v.Config.Env {
			if strings.HasPrefix(value, "DOCK_ENVIRONMENT_READY=") {
				readyPath = strings.TrimPrefix(value, "DOCK_ENVIRONMENT_READY=")
			}
			if strings.HasPrefix(value, "DOCK_ENVIRONMENT_JSON=") {
				if err = json.Unmarshal([]byte(strings.TrimPrefix(value, "DOCK_ENVIRONMENT_JSON=")), &environment); err != nil {
					return fmt.Errorf("invalid retained environment policy: %w", err)
				}
			}
		}
		if readyPath != "" && (filepath.Dir(readyPath) != "/var/tmp" || !strings.HasPrefix(filepath.Base(readyPath), "cxd-ready-")) {
			return fmt.Errorf("invalid retained readiness path")
		}
	}
	if kata {
		g, err := m.kataNode()
		if err != nil {
			return err
		}
		profile, err := m.resourceProfile(ctx, v.Config.Labels[labelPrefix+"resource-profile"])
		if err != nil {
			return err
		}
		if profile.Name == "standard" {
			_, err = g.Reserve(ctx, v.Config.Labels[labelAllocation])
		} else if reserver, ok := g.(kataProfileReserver); ok {
			_, err = reserver.ReserveProfile(ctx, v.Config.Labels[labelAllocation], profile.Name)
		} else {
			return fmt.Errorf("kata node does not support %s resource profile", profile.Name)
		}
		if err != nil {
			return err
		}
	} else if err = m.restoreWorkerToken(v); err != nil {
		return err
	}
	failed := true
	defer func() {
		if failed && kata {
			m.revokeWorkerToken(v)
			timeout := 5
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			stopErr := m.cli.ContainerStop(cleanupCtx, v.ID, container.StopOptions{Timeout: &timeout})
			releaseErr := m.releaseKataAllocation(cleanupCtx, v.Config.Labels[labelAllocation], v.ID)
			startErr = errors.Join(startErr, stopErr, releaseErr)
		}
	}()
	if err = m.cli.ContainerStart(ctx, v.ID, container.StartOptions{}); err != nil {
		m.revokeWorkerToken(v)
		return err
	}
	if kata {
		if err = m.validateKataWorker(ctx, v.ID); err != nil {
			return err
		}
		if err = m.verifyKataGuest(ctx, v.ID, true); err != nil {
			return err
		}
		if err = m.restoreWorkerToken(v); err != nil {
			return err
		}
		if readyPath != "" {
			if _, err = m.execTrusted(ctx, v.ID, []string{"rm", "-f", "--", readyPath}); err != nil {
				return err
			}
		}
		if _, err = m.execTrusted(ctx, v.ID, []string{guestGuard, "release", v.Config.Labels[labelAllocation]}); err != nil {
			return err
		}
	}
	if readyPath != "" {
		if err = m.waitEnvironment(ctx, v.ID, readyPath, environment.ReadinessTimeout); err != nil {
			return err
		}
	}
	failed = false
	return nil
}

// StartByName resolves a retained worker by its name or ID prefix and starts it.
func (m *Manager) StartByName(name string) error {
	id, err := m.resolveID(name)
	if err != nil {
		return err
	}
	return m.Start(id)
}

// Wait returns a channel that receives nil when the container exits cleanly, or an error.
func (m *Manager) Wait(containerID string) <-chan error {
	ch := make(chan error, 1)
	m.attachMu.Lock()
	outputDone := m.attachments[containerID]
	m.attachMu.Unlock()
	go func() {
		statusCh, errCh := m.cli.ContainerWait(context.Background(), containerID, container.WaitConditionNotRunning)
		var waitErr error
		select {
		case err := <-errCh:
			waitErr = err
		case status := <-statusCh:
			if status.StatusCode != 0 {
				waitErr = fmt.Errorf("container exited with code %d", status.StatusCode)
			}
		}
		if outputDone != nil {
			select {
			case <-outputDone:
			case <-time.After(10 * time.Second):
				waitErr = errors.Join(waitErr, fmt.Errorf("timed out draining worker output"))
			}
		}
		ch <- waitErr
	}()
	return ch
}

// Stop gracefully stops a container by ID and revokes its auth token (F-AUTH-04).
func (m *Manager) Stop(containerID string, timeoutSec int) error {
	return m.StopContext(context.Background(), containerID, timeoutSec)
}

// StopContext stops a managed worker and revokes its proxy token.
func (m *Manager) StopContext(parent context.Context, containerID string, timeoutSec int) error {
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()
	v, err := m.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return err
	}
	unlock, err := m.lockCleanup(ctx, v.ID)
	if err != nil {
		return err
	}
	defer unlock()
	revokeErr := m.revokeWorkerTokenChecked(v)
	if err = m.cli.ContainerStop(ctx, v.ID, container.StopOptions{Timeout: &timeoutSec}); err != nil {
		return err
	}
	if v.Config != nil && v.Config.Labels[labelIsolation] == RuntimeKata && v.Config.Labels[labelAllocation] != "" {
		return errors.Join(revokeErr, m.releaseKataAllocation(ctx, v.Config.Labels[labelAllocation], v.ID))
	}
	return revokeErr
}

// StopByName stops a container by name and revokes its auth token (F-AUTH-04).
func (m *Manager) StopByName(name string, timeoutSec int) error {
	id, err := m.resolveID(name)
	if err != nil {
		return err
	}
	return m.Stop(id, timeoutSec)
}

// RevokeToken revokes the auth token for a container identified by ID or name.
// This should be called when a container exits naturally (without Stop being called).
func (m *Manager) RevokeToken(containerID string) {
	if m.proxy == nil {
		return
	}
	ctx := context.Background()
	if name, err := m.resolveNameByID(ctx, containerID); err == nil {
		m.proxy.RevokeToken(name)
	}
}

// shellBinaryPath returns the absolute in-container path of the requested
// shell binary, defaulting to bash.
func shellBinaryPath(t ShellType) string {
	if t == ShellZsh {
		return "/bin/zsh"
	}
	return "/bin/bash"
}

// ImageExists reports whether the named image tag is present in the worker namespace.
func (m *Manager) ImageExists(tag string) (bool, error) {
	return m.ImageExistsContext(context.Background(), tag)
}

func (m *Manager) ImageExistsContext(ctx context.Context, tag string) (bool, error) {
	_, err := m.cli.ImageInspect(ctx, tag)
	if err != nil {
		if errdefs.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// ImageCreatedAt returns the creation timestamp recorded in the named image's
// metadata (the same value "docker inspect" reports as .Created).
func (m *Manager) ImageCreatedAt(tag string) (time.Time, error) {
	return m.ImageCreatedAtContext(context.Background(), tag)
}

func (m *Manager) ImageCreatedAtContext(ctx context.Context, tag string) (time.Time, error) {
	info, err := m.cli.ImageInspect(ctx, tag)
	if err != nil {
		return time.Time{}, err
	}
	if info.Created == "" {
		return time.Time{}, fmt.Errorf("image %s has no creation timestamp", tag)
	}
	return time.Parse(time.RFC3339Nano, info.Created)
}

// Remove removes a container by ID and tears down its per-worker network.
func (m *Manager) Remove(containerID string, force bool) error {
	return m.RemoveContext(context.Background(), containerID, force)
}

// RemoveContext durably records cleanup before deleting a managed worker.
func (m *Manager) RemoveContext(ctx context.Context, containerID string, force bool) error {
	return m.removeDurably(ctx, containerID, force)
}

// RemoveByName also finds an interrupted cleanup after its Docker object is gone.
func (m *Manager) RemoveByName(name string, force bool) error {
	id, err := m.resolveID(name)
	if err != nil {
		record, recoveryErr := m.findCleanup(name)
		if recoveryErr != nil {
			return errors.Join(err, recoveryErr)
		}
		id = record.ID
	}
	return m.Remove(id, force)
}

// List returns all codex-dock managed workers.
func (m *Manager) List(all bool) ([]Worker, error) {
	return m.ListContext(context.Background(), all)
}

// InspectContext returns one managed worker by its full ID or exact name.
// An unmanaged node object is never returned through the worker API.
func (m *Manager) InspectContext(ctx context.Context, reference string) (Worker, error) {
	if reference == "" {
		return Worker{}, fmt.Errorf("worker ID or name is required")
	}
	v, err := m.cli.ContainerInspect(ctx, reference)
	if err != nil {
		return Worker{}, err
	}
	if v.ContainerJSONBase == nil || v.Config == nil || v.Config.Labels[labelManaged] != "true" {
		return Worker{}, fmt.Errorf("worker %q is not managed", reference)
	}
	w := Worker{ID: v.ID, Name: strings.TrimPrefix(v.Name, "/"), Image: v.Config.Image, Branch: v.Config.Labels[labelBranch], Task: v.Config.Labels[labelTask], AgentPolicy: v.Config.Labels[labelPrefix+"agent-policy"]}
	if v.State != nil {
		w.Status = v.State.Status
		if t, err := time.Parse(time.RFC3339Nano, v.State.StartedAt); err == nil && !t.IsZero() {
			w.StartedAt = &t
		}
		if t, err := time.Parse(time.RFC3339Nano, v.State.FinishedAt); err == nil && !t.IsZero() {
			w.FinishedAt = &t
		}
	}
	return w, nil
}

// ListContext lists workers with a caller-supplied deadline. Shell completion
// uses a short deadline so a stalled node cannot hang an interactive prompt.
func (m *Manager) ListContext(ctx context.Context, all bool) ([]Worker, error) {
	f := filters.NewArgs()
	f.Add("label", labelManaged+"=true")

	containers, err := m.cli.ContainerList(ctx, container.ListOptions{
		All:     all,
		Filters: f,
	})
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}

	workers := make([]Worker, 0, len(containers))
	for _, c := range containers {
		w := Worker{
			ID:          c.ID,
			Name:        strings.TrimPrefix(c.Names[0], "/"),
			Status:      c.State,
			Image:       c.Image,
			Branch:      c.Labels[labelBranch],
			Task:        c.Labels[labelTask],
			AgentPolicy: c.Labels[labelPrefix+"agent-policy"],
		}
		if c.Created > 0 {
			t := time.Unix(c.Created, 0)
			w.StartedAt = &t
		}
		workers = append(workers, w)
	}
	return workers, nil
}

// Logs streams container logs to opts.Output (defaults to os.Stdout).
func (m *Manager) Logs(opts LogOptions) error {
	ctx := context.Background()
	id, err := m.resolveID(opts.Name)
	if err != nil {
		return err
	}

	tail := strconv.Itoa(opts.Tail)
	rc, err := m.cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       tail,
		Follow:     opts.Follow,
		Timestamps: false,
	})
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()

	out := opts.Output
	if out == nil {
		out = os.Stdout
	}
	_, err = io.Copy(out, rc)
	return err
}

// resolveNameByID returns the container name (without leading '/') for a given ID.
func (m *Manager) resolveNameByID(ctx context.Context, containerID string) (string, error) {
	info, err := m.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(info.Name, "/"), nil
}

func (m *Manager) resolveID(nameOrID string) (string, error) {
	ctx := context.Background()
	containers, err := m.cli.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: filters.NewArgs(filters.Arg("label", labelManaged+"=true")),
	})
	if err != nil {
		return "", err
	}
	for _, c := range containers {
		if strings.HasPrefix(c.ID, nameOrID) {
			return c.ID, nil
		}
		for _, n := range c.Names {
			if strings.TrimPrefix(n, "/") == nameOrID {
				return c.ID, nil
			}
		}
	}
	return "", fmt.Errorf("container %q: %w", nameOrID, errdefs.ErrNotFound)
}

func (m *Manager) attachIO(ctx context.Context, containerID string) {
	resp, err := m.cli.ContainerAttach(ctx, containerID, container.AttachOptions{
		Stdin:  true,
		Stdout: true,
		Stderr: true,
		Stream: true,
	})
	if err != nil {
		return
	}
	defer resp.Close()

	localTTY, err := openLocalTerminal(os.Stdin, true, func(width, height uint) error {
		return m.cli.ContainerResize(ctx, containerID, container.ResizeOptions{Width: width, Height: height})
	})
	if err != nil {
		return
	}
	stop := context.AfterFunc(ctx, resp.Close)
	defer stop()
	if localTTY != nil {
		inputDone := make(chan struct{})
		go func() {
			defer close(inputDone)
			_ = localTTY.copyTo(ctx, resp.Conn)
		}()
		defer func() {
			_ = localTTY.Close()
			resp.Close()
			<-inputDone
		}()
	} else {
		go func() { _, _ = io.Copy(resp.Conn, os.Stdin) }()
	}

	// Pass stdin bytes to the container as-is.  Ctrl+Z (0x1a) is forwarded
	// to the container's PTY where the in-container bash handles job control:
	// codex is suspended and the bash prompt appears inside the container.
	_, _ = io.Copy(os.Stdout, resp.Reader)
}

// buildEnv assembles the container environment, issuing a short-lived auth token
// from the proxy when one is configured. The agent (codex/claude/shell) controls
// which provider's proxy variables are injected:
//   - codex / shell : OpenAI/Codex variables (CODEX_*, OPENAI_BASE_URL)
//   - claude / shell: Anthropic variables (ANTHROPIC_*) when the proxy can serve them
//
// Containers always receive only a placeholder token; the proxy injects the real
// credential on every outbound request.
func (m *Manager) buildEnv(name string, opts RunOptions, installScript string) ([]string, error) {
	shellType := opts.ShellType
	if shellType == "" {
		shellType = ShellBash
	}
	env := []string{
		"CODEX_DOCK_VERIFIED_KATA=0",
		"CODEX_SANDBOX=1",
		"TERM=xterm-256color",
		"DOCK_AGENT=" + string(opts.Agent),
		"DOCK_SHELL=" + string(shellType),
	}

	if m.proxy != nil {
		token := opts.pendingPlaceholder
		if token == "" {
			var err error
			if opts.TokenUsagePolicy != nil {
				issuer, ok := m.proxy.(authproxy.UsageIssuer)
				if !ok || !issuer.SupportsTokenUsagePolicy() {
					return nil, fmt.Errorf("auth proxy does not support execution usage limits")
				}
				token, err = issuer.IssueTokenWithUsage(name, opts.TokenTTL, opts.TokenIdentity, opts.TokenModelPolicy, opts.TokenUsagePolicy)
			} else if opts.TokenModelPolicy != nil {
				issuer, ok := m.proxy.(authproxy.PolicyIssuer)
				if !ok {
					return nil, fmt.Errorf("auth proxy does not support token model policy")
				}
				token, err = issuer.IssueTokenWithPolicy(name, opts.TokenTTL, opts.TokenIdentity, opts.TokenModelPolicy)
			} else if opts.TokenIdentity.Empty() {
				token, err = m.proxy.IssueToken(name, opts.TokenTTL)
			} else if issuer, ok := m.proxy.(authproxy.IdentityIssuer); ok {
				token, err = issuer.IssueTokenWithIdentity(name, opts.TokenTTL, opts.TokenIdentity)
			} else {
				return nil, fmt.Errorf("auth proxy does not support token identity")
			}
			if err != nil {
				return nil, fmt.Errorf("issuing auth token: %w", err)
			}
		}
		// ContainerEndpoint() identifies the auth proxy on the worker network.
		// Kata uses its verified front IP because guest /etc/hosts injection is
		// not reliable across all supported node setups.
		containerProxyURL, err := kataProxyEndpoint(m.proxy.ContainerEndpoint(), opts.proxyHosts)
		if err != nil {
			return nil, err
		}

		if secure, ok := m.proxy.(authproxy.WorkerTransportService); ok && secure.WorkerTransportEnabled() {
			if opts.pendingPlaceholder == "" {
				return nil, fmt.Errorf("worker TLS requires a Kata worker")
			}
			if opts.TokenTTL > workertransport.MaxTTL {
				return nil, fmt.Errorf("worker TLS token TTL must be at most 3600 seconds")
			}
			containerProxyURL = workertransport.LocalURL
			env = append(env, "CODEX_DOCK_WORKER_MTLS=1")
		}
		wantCodex := (opts.Agent == AgentCodex || opts.Agent == AgentNone) && (opts.TokenIdentity.Provider == "" || opts.TokenIdentity.Provider == "openai")
		wantClaude := (opts.Agent == AgentClaude || opts.Agent == AgentNone) && (opts.TokenIdentity.Provider == "" || opts.TokenIdentity.Provider == "anthropic")

		if wantCodex {
			restricted := opts.TokenModelPolicy != nil || opts.TokenUsagePolicy != nil
			if source, ok := m.proxy.(interface{ ModelPolicyActive() bool }); ok {
				restricted = restricted || source.ModelPolicyActive()
			}
			if restricted {
				env = append(env, "CODEX_DOCK_CODEX_HTTP=1")
			}
			env = append(env,
				"CODEX_AUTH_PROXY_URL="+containerProxyURL,
				"CODEX_TOKEN="+token,
				// NOTE: Codex CLI does NOT read OPENAI_BASE_URL — it is deprecated and
				// ignored (openai/codex#16719). The Responses API endpoint is redirected
				// through the proxy by the openai_base_url key that entrypoint.sh writes
				// to ~/.codex/config.toml, not by this env var. We still export it as a
				// convenience for other OpenAI-SDK-based tools in the interactive shell,
				// which honour it and would otherwise reach api.openai.com directly.
				"OPENAI_BASE_URL="+containerProxyURL+"/v1",
			)
			// In OAuth mode, redirect Codex CLI's token refresh calls to the proxy.
			// The proxy substitutes the host's real refresh_token so it never reaches
			// the container. The short-lived token is embedded as ?cdx= for authentication
			// because Codex CLI does not add custom headers to refresh requests.
			if m.proxy.IsOAuthMode() {
				env = append(env,
					"CODEX_REFRESH_TOKEN_URL_OVERRIDE="+containerProxyURL+"/oauth/token?cdx="+token,
				)
			}
		}

		// Preserve the official CLI's distinct subscription and API-key paths.
		if wantClaude && m.proxy.IsAnthropicMode() {
			oauth := false
			if mode, ok := m.proxy.(interface{ IsAnthropicOAuthMode() bool }); ok {
				oauth = mode.IsAnthropicOAuthMode()
			}
			claudeEnv, err := agentconfig.ClaudeEnv(containerProxyURL+"/anthropic", token, oauth)
			if err != nil {
				return nil, err
			}
			env = append(env, claudeEnv...)
		}

		// Route general egress (git/npm/pip/curl) through the dedicated egress proxy
		// (codex-http-proxy) via the standard HTTP(S)_PROXY vars. NO_PROXY excludes
		// the AUTH proxy host so the API base URLs and token fetches reach it
		// directly (origin-form → reverse routes) instead of being tunneled. Skipped
		// when --no-internet is set, leaving only the auth proxy's API routes.
		if !opts.NoInternet {
			egressProxyURL := m.httpProxyURL
			if egressProxyURL == "" {
				egressProxyURL = containerProxyURL // back-compat: single proxy
			} else {
				egressProxyURL, err = kataProxyEndpoint(egressProxyURL, opts.proxyHosts)
				if err != nil {
					return nil, err
				}
			}
			authHost := proxyEndpointHost(containerProxyURL)
			// NO_PROXY excludes the auth proxy (API/token traffic must reach it
			// directly as origin-form → reverse routes) and the http proxy's own
			// host (so a direct hit on its /health doesn't loop back through
			// itself and get refused). De-duplicated; empty hosts dropped.
			noProxyHosts := dedupeNonEmpty(authHost, proxyEndpointHost(egressProxyURL), "localhost", "127.0.0.1")
			noProxy := strings.Join(noProxyHosts, ",")
			env = append(env,
				"HTTP_PROXY="+egressProxyURL,
				"HTTPS_PROXY="+egressProxyURL,
				"http_proxy="+egressProxyURL,
				"https_proxy="+egressProxyURL,
				"NO_PROXY="+noProxy,
				"no_proxy="+noProxy,
			)
		}

		if m.debug {
			fmt.Fprintf(os.Stderr, "debug: issued token for %s (TTL=%ds, agent=%q)\n", name, opts.TokenTTL, opts.Agent)
		}
	}

	if opts.WatchPolling {
		env = append(env, "CHOKIDAR_USEPOLLING=true", "CHOKIDAR_INTERVAL=1000", "WATCHPACK_POLLING=1000")
	}
	if opts.ExecCmd != "" {
		env = append(env, "DOCK_EXEC_CMD="+opts.ExecCmd)
	}
	if opts.Task != "" {
		env = append(env, "CODEX_TASK="+opts.Task)
	}
	if opts.DirectAgent {
		env = append(env, "DOCK_DIRECT_AGENT=1")
	}
	if len(opts.AgentArgs) != 0 {
		argsJSON, err := json.Marshal(opts.AgentArgs)
		if err != nil {
			return nil, fmt.Errorf("encoding agent arguments: %w", err)
		}
		env = append(env, "DOCK_AGENT_ARGS_JSON="+string(argsJSON))
	}
	if opts.Model != "" {
		env = append(env, "CODEX_MODEL="+opts.Model)
	}
	if opts.ApprovalMode != "" && opts.ApprovalMode != ApprovalModeSuggest {
		env = append(env, "CODEX_APPROVAL_MODE="+string(opts.ApprovalMode))
	}
	if opts.AgentPolicy.Name != "" {
		policyEnv, err := opts.AgentPolicy.Environment()
		if err != nil {
			return nil, err
		}
		env = append(env, policyEnv...)
	}
	if opts.AgentsMD != "" {
		env = append(env, "CODEX_AGENTS_MD="+opts.AgentsMD)
	}
	if installScript != "" {
		env = append(env, "CODEX_INSTALL_SCRIPT="+installScript)
	}

	// When a custom container user is specified, the uid may not exist in the
	// image's /etc/passwd, causing Docker to set HOME="/".
	// Use a writable non-workspace home to avoid permission errors and avoid creating extra directories under /workspace.
	if opts.ContainerUser != "" || opts.ReadOnlyRootFS {
		env = append(env, "HOME=/var/tmp/codex-home")
	}
	if opts.ReadOnlyRootFS {
		env = append(env, "XDG_CACHE_HOME=/var/tmp/codex-home/.cache")
	}

	return compatibilityEnv(opts, env)
}

// proxyContainerNames returns the container names of the proxies that must be
// attached to each worker's Internal network: the credential-injecting auth
// proxy (from the proxy endpoint, e.g. "codex-auth-proxy") and, when general
// egress is allowed, the general-egress http proxy (from HTTPProxyURL, e.g.
// "codex-http-proxy"). When noInternet is true the http proxy is omitted so the
// worker has no network path to it. De-duplicated; empty entries dropped.
func (m *Manager) proxyContainerNames(noInternet bool) []string {
	seen := map[string]struct{}{}
	var names []string
	add := func(host string) {
		if host == "" {
			return
		}
		if _, ok := seen[host]; ok {
			return
		}
		seen[host] = struct{}{}
		names = append(names, host)
	}
	if m.proxy != nil {
		add(proxyEndpointHost(m.proxy.ContainerEndpoint()))
	}
	if !noInternet {
		add(proxyEndpointHost(m.httpProxyURL))
	}
	return names
}

// dedupeNonEmpty returns the inputs with empty strings and duplicates removed,
// preserving first-seen order.
func dedupeNonEmpty(items ...string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, it := range items {
		if it == "" {
			continue
		}
		if _, ok := seen[it]; ok {
			continue
		}
		seen[it] = struct{}{}
		out = append(out, it)
	}
	return out
}

// proxyEndpointHost extracts the hostname from a proxy endpoint URL.
func proxyEndpointHost(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// Kata's guest may not receive the host-generated /etc/hosts bind mount.
// Use the already validated, per-worker proxy address for product endpoints.
func kataProxyEndpoint(endpoint string, hosts []string) (string, error) {
	if len(hosts) == 0 {
		return endpoint, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" {
		return "", fmt.Errorf("invalid proxy endpoint %q", endpoint)
	}
	for _, entry := range hosts {
		name, ip, ok := strings.Cut(entry, ":")
		if ok && name == u.Hostname() {
			if u.Port() == "" {
				u.Host = ip
			} else {
				u.Host = net.JoinHostPort(ip, u.Port())
			}
			return u.String(), nil
		}
	}
	return "", fmt.Errorf("proxy %q is not attached to the Kata worker network", u.Hostname())
}

// nameTaken reports whether a worker name is already in use by an existing
// container or by an existing per-worker network. Errors are treated as
// "available" so transient lookup failures don't block name selection.
func (m *Manager) nameTaken(name string) bool {
	if _, err := m.resolveID(name); err == nil {
		return true
	}
	if m.network != nil {
		if exists, err := m.network.WorkerNetworkExists(name); err == nil && exists {
			return true
		}
	}
	return false
}

// pickUniqueName returns the first name produced by gen that is not reported
// taken. After maxAttempts collisions it appends a short random suffix, which is
// itself re-checked so the fallback name is verified free too.
func pickUniqueName(gen func() string, taken func(string) bool, maxAttempts int) string {
	for i := 0; i < maxAttempts; i++ {
		n := gen()
		if !taken(n) {
			return n
		}
	}
	base := gen()
	for i := 0; i < maxAttempts; i++ {
		n := base + "-" + randomSuffix()
		if !taken(n) {
			return n
		}
	}
	// Extremely unlikely; return a suffixed name regardless.
	return base + "-" + randomSuffix()
}

// randomSuffix returns a short random hex string for disambiguating names.
func randomSuffix() string {
	b := make([]byte, 4)
	if _, err := crand.Read(b); err != nil {
		// crypto/rand should not fail; fall back to a timestamp-based value.
		return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
	}
	return hex.EncodeToString(b)
}

func buildCodexArgs(opts RunOptions) []string {
	args := []string{"codex"}
	switch opts.ApprovalMode {
	case ApprovalModeAutoEdit:
		args = append(args, "--ask-for-approval", "on-request")
	case ApprovalModeFullAuto:
		args = append(args, "--ask-for-approval", "never")
	case ApprovalModeDanger:
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.Task != "" {
		args = append(args, opts.Task)
	}
	return args
}

// buildMounts assembles the container bind mounts: the project workspace at
// /workspace (read-only when opts.ReadOnly is set), followed by any
// additional user-requested mounts from opts.Mounts, each independently
// read-only or read-write.
func buildMounts(opts RunOptions, absWorkspace string) []mount.Mount {
	if opts.authenticationSession {
		return nil
	}
	mounts := []mount.Mount{
		{
			Type:     mount.TypeBind,
			Source:   absWorkspace,
			Target:   "/workspace",
			ReadOnly: opts.ReadOnly,
			BindOptions: &mount.BindOptions{
				Propagation: mount.PropagationRPrivate,
			},
		},
	}
	for _, ms := range opts.Mounts {
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   ms.Source,
			Target:   ms.Target,
			ReadOnly: ms.ReadOnly,
			BindOptions: &mount.BindOptions{
				Propagation: mount.PropagationRPrivate,
			},
		})
	}
	return mounts
}

func absolutePath(path string) (string, error) {
	if len(path) > 0 && path[0] == '/' {
		return path, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return wd + "/" + path, nil
}

// buildHostConfig constructs the container HostConfig with security defaults.
// netName is the per-worker Internal network the container is attached to; the
// worker reaches the proxy over this network. Kata pins its endpoint to the
// validated front IP; no host-gateway alias is needed.
func buildHostConfig(mounts []mount.Mount, netName string) *container.HostConfig {
	return &container.HostConfig{
		NetworkMode: container.NetworkMode(netName),
		Mounts:      mounts,
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges:true"},
		Resources: container.Resources{
			PidsLimit: int64ptr(512),
		},
	}
}

func int64ptr(i int64) *int64 { return &i }
