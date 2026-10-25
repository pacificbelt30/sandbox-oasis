package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"os"
	"path/filepath"
	"strconv"

	dockerdefaults "github.com/pacificbelt30/codex-dock/docker"
	"github.com/pacificbelt30/codex-dock/internal/authproxy"
	"github.com/pacificbelt30/codex-dock/internal/buildinfo"
	"github.com/pacificbelt30/codex-dock/internal/netquota"
	"github.com/pacificbelt30/codex-dock/internal/network"
	"github.com/pacificbelt30/codex-dock/internal/networkpolicy"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/credentialstore"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	defaultProxyContainerName     = "codex-auth-proxy"
	defaultHTTPProxyContainerName = "codex-http-proxy"
	defaultAuthDataPort           = 18080
	defaultAuthAdminPort          = 18081
	defaultHTTPProxyPort          = 18082
	proxyLayoutLabel              = "io.codex-dock.proxy-layout"
	proxyLayoutVersion            = "split-v4-worker-mtls"
	proxyImageProtocolLabel       = "io.codex-dock.proxy-protocol"
	proxyImageProtocolVersion     = "external-store-v1"
	proxyImageBuildLabel          = "io.codex-dock.proxy-build"
	proxyCaseLabel                = "io.codex-dock.credential-case"
)

var (
	proxyListenAddr       string
	proxyAdminListenAddr  string
	proxyAdminSecret      string
	proxyAdminSecretFile  string
	proxyForwardAllow     []string
	proxyRole             string
	proxyBlockPrivate     bool
	proxyOAuthStateDir    string
	proxyUsageStateDir    string
	proxyAuditDirectory   string
	proxyWorkerMTLS       bool
	proxyCredentialSocket string
	proxyCaseID           string
	proxyAllowedModels    []string
	proxyProfileStoreDir  string

	proxyBuildTag        string
	proxyBuildDockerfile string

	proxyRunName      string
	proxyRunPort      int
	proxyRunAdminPort int
	proxyRunHTTPName  string
	proxyRunHTTPPort  int
	proxyRunNetwork   string

	proxyStopName string

	proxyRmName  string
	proxyRmForce bool
)

// managedProxyNames returns the default proxy container names codex-dock manages.
func managedProxyNames() []string {
	return []string{defaultProxyContainerName, defaultHTTPProxyContainerName}
}

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Manage auth proxy service and container",
}

// proxy serve ---------------------------------------------------------------

var proxyServeCmd = &cobra.Command{
	Use:    "serve",
	Short:  "Run auth proxy server (in-process)",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		applyForwardPolicyDefaults(cmd)
		resources, err := proxyNetworkResourceLimits(cmd)
		if err != nil {
			return err
		}
		var store credentialstore.Store
		proxyProfileKeyStdin, err := cmd.Flags().GetBool("profile-key-stdin")
		if err != nil {
			return err
		}
		adminSecret, _, err := proxyServerSecret()
		if err != nil {
			return err
		}
		profile := selectedAuthProfile()
		if profile != "" {
			if proxyCredentialSocket != "" || proxyCaseID != "" || proxyRole != authproxy.RoleAuth || proxyOAuthStateDir != "" {
				return fmt.Errorf("independent profiles cannot be combined with case/socket credentials, egress role or legacy OAuth state")
			}
			var err error
			if proxyProfileStoreDir != "" {
				store, err = credentialstore.OpenProfile(proxyProfileStoreDir, profile)
			} else {
				store, err = openAuthProfile(profile)
			}
			if err != nil {
				return err
			}
		} else if proxyProfileStoreDir != "" {
			return fmt.Errorf("--profile-store-dir requires --auth-profile")
		}
		if encrypted, ok := store.(*credentialstore.EncryptedStore); ok {
			defer func() { _ = encrypted.Close() }()
			if !proxyProfileKeyStdin {
				return fmt.Errorf("encrypted proxy requires --profile-key-stdin")
			}
			info, err := encrypted.Info(cmd.Context())
			if err != nil {
				return err
			}
			if info.Format != "age" {
				return credentialstore.ErrMigrationRequired
			}
			key, err := credentialstore.ReadUnlockKey(cmd.InOrStdin())
			if err != nil {
				return fmt.Errorf("encrypted proxy key input failed")
			}
			defer clear(key)
			if err := encrypted.UnlockWithKey(cmd.Context(), key); err != nil {
				return err
			}
		} else if proxyProfileKeyStdin {
			return fmt.Errorf("--profile-key-stdin requires an encrypted profile")
		}

		if proxyCredentialSocket != "" {
			var err error
			store, err = credentialstore.NewUnixClient(proxyCredentialSocket)
			if err != nil {
				return err
			}
		}
		var bundle []byte
		if proxyCAFile != "" {
			var err error
			bundle, err = os.ReadFile(proxyCAFile)
			if err != nil {
				return err
			}
		}
		p, err := authproxy.NewProxy(authproxy.Config{
			UpstreamProxyURL: proxyUpstreamURL, CABundlePEM: bundle,
			NetworkResources:     resources,
			CredentialRoutesFile: proxyBrokerFile,
			AllowedModels:        proxyAllowedModels,
			EventWriter:          os.Stdout,
			CredentialStore:      store,
			CaseID:               proxyCaseID,
			ProfileName:          profile,
			TokenTTL:             3600,
			Verbose:              verbose,
			Role:                 proxyRole,
			ListenAddr:           proxyListenAddr,
			AdminListenAddr:      proxyAdminListenAddr,
			AdminSecret:          adminSecret,
			ForwardPolicy:        forwardPolicyRules(),
			BlockPrivate:         proxyBlockPrivate,
			OAuthStateDir:        proxyOAuthStateDir,
			UsageStateDir:        proxyUsageStateDir,
			AuditDirectory:       proxyAuditDirectory,
			WorkerMTLS:           proxyWorkerMTLS,
		})
		if err != nil {
			return err
		}
		if err := p.Start(); err != nil {
			return err
		}
		fmt.Printf("auth proxy running at %s\n", p.Endpoint())
		select {}
	},
}

// proxy build ---------------------------------------------------------------

var proxyBuildCmd = &cobra.Command{
	Use:   "build",
	Short: "Build the auth proxy OCI image with BuildKit",
	RunE: func(cmd *cobra.Command, args []string) error {
		dockerfile, buildCtx, err := resolveProxyDockerfile(proxyBuildDockerfile)
		if err != nil {
			return err
		}
		tag := viper.GetString("proxy_image")
		if proxyBuildTag != "" {
			tag = proxyBuildTag
		}
		return executeProxyBuild(cmd.Context(), tag, dockerfile, buildCtx)
	},
}

// proxy run -----------------------------------------------------------------

var proxyRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the auth proxy on containerd",
	Long: `Run separate auth and general-egress proxies as detached containerd tasks.

Select an independent credential profile with --auth-profile NAME or global
auth_profile. Only that profile is mounted for OAuth refresh; host CLI logins
and authentication environment variables are not used as fallback.

Without profile selection, legacy mode reads configured OpenAI and Anthropic
API keys or OAuth credentials from the invoking user's environment and files.
Only the auth proxy receives real credentials; the egress proxy does not.

Configure credentials before starting. Prefer --admin-secret-file for admin
authority. General egress requires domain or CIDR grants by default; changing
settings does not update existing proxies or workers.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		applyForwardPolicyDefaults(cmd)
		resources, err := proxyNetworkResourceLimits(cmd)
		if err != nil {
			return err
		}
		return startProxyContainerWithResources(cmd.Context(), resources)
	},
}

// startProxyContainer ensures BOTH proxy containers are running:
//   - codex-auth-proxy (RoleAuth): credential-injecting reverse routes + token +
//     admin. Data-plane internal; admin published to host loopback (egress IP).
//   - codex-http-proxy (RoleEgress): general-egress forward proxy, no credentials,
//     private/LAN blocked.
//
// It builds the proxy image / egress network when missing and restarts existing
// (stopped) containers. Shared by `proxy run` and `oaiso run`'s auto-start.
func startProxyContainer(ctx context.Context) error {
	limits, err := proxyNetworkResourceLimits(nil)
	if err != nil {
		return err
	}
	return startProxyContainerWithResources(ctx, limits)
}

func startProxyContainerWithResources(ctx context.Context, resources netquota.Limits) error {
	if err := resources.Validate(); err != nil {
		return err
	}
	adminSecret, adminSecretFile, err := proxyServerSecret()
	if err != nil {
		return err
	}
	if adminSecret == "" && adminSecretFile == "" {
		adminSecret, adminSecretFile, err = prepareManagedProxyAdminSecret()
		if err != nil {
			return err
		}
	}
	if len(adminSecret) < 32 {
		return fmt.Errorf("managed worker TLS requires an admin secret of at least 32 bytes; configure --admin-secret-file")
	}
	if err := authproxy.ValidateAllowedModels(proxyAllowedModels); err != nil {
		return err
	}
	profile := selectedAuthProfile()
	var profileDirectory, profileConnection, profileFingerprint string
	var profileKey []byte
	defer func() { clear(profileKey) }()
	if profile != "" {
		if proxyCredentialSocket != "" || proxyCaseID != "" || len(adminSecret) < 32 {
			return fmt.Errorf("independent profile requires an admin secret of at least 32 bytes and cannot be combined with case/socket credentials")
		}
		store, err := openAuthProfile(profile)
		if err != nil {
			return err
		}
		profileDirectory = store.Directory()
		if encrypted, ok := store.(*credentialstore.EncryptedStore); ok {
			info, err := encrypted.Info(ctx)
			if err != nil {
				return err
			}
			if info.Format != "age" {
				return credentialstore.ErrMigrationRequired
			}
			profileFingerprint = info.Fingerprint
			if _, connection, ok := runningEncryptedProfile(ctx, profile); ok {
				profileConnection = connection
			} else {
				store, err = unlockedAuthProfile(ctx, profile)
				if err != nil {
					return err
				}
				value, err := store.Load(ctx, profile)
				if err != nil {
					return err
				}
				profileConnection = value.ConnectionVersion
				unlocked, ok := store.(*credentialstore.EncryptedStore)
				if !ok {
					return fmt.Errorf("encrypted profile storage changed while unlocking")
				}
				profileKey, err = unlocked.UnlockedKey()
				if err != nil {
					return err
				}
			}
		} else {
			loaded, err := store.Load(ctx, profile)
			if err != nil {
				return fmt.Errorf("loading independent profile: %w", err)
			}
			profileConnection = loaded.ConnectionVersion
		}
	}
	rules := forwardPolicyRules()
	rules.BlockPrivate = true
	if _, err := networkpolicy.Compile(rules); err != nil {
		return fmt.Errorf("invalid egress policy: %w", err)
	}
	image := viper.GetString("proxy_image")
	if err := ensureBridgeNetwork(ctx, proxyRunNetwork, network.ProxyBridgeName); err != nil {
		return err
	}
	if err := ensureProxyImageForStore(ctx, image, profileFingerprint != ""); err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	stateDir := ""
	if proxyCredentialSocket == "" && profile == "" {
		stateDir = filepath.Join(home, ".local", "state", "codex-dock", "oauth")
		if err := os.MkdirAll(stateDir, 0700); err != nil {
			return err
		}
		if info, err := os.Lstat(stateDir); err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return fmt.Errorf("OAuth state directory %s must be private (0700)", stateDir)
		}
	} else if profile == "" && proxyCaseID == "" {
		return fmt.Errorf("--credential-store-socket requires --case-id")
	} else if len(adminSecret) < 32 {
		return fmt.Errorf("external credential store requires --admin-secret with at least 32 bytes")
	}

	// 1) Auth proxy: holds credentials, admin published, no forwarding.
	auth := proxyRunArgs{networkResources: resources, allowedModels: proxyAllowedModels, brokerFile: proxyBrokerFile, upstreamProxy: proxyUpstreamURL, caFile: proxyCAFile,
		name:               proxyRunName,
		role:               authproxy.RoleAuth,
		networkName:        proxyRunNetwork,
		image:              image,
		listenAddr:         fmt.Sprintf("0.0.0.0:%d", proxyRunPort),
		adminPort:          proxyRunAdminPort,
		adminListenAddr:    fmt.Sprintf("%s:%d", authproxy.AdminBindEgress, proxyRunAdminPort),
		adminSecret:        adminSecret,
		adminSecretFile:    adminSecretFile,
		blockPrivate:       true, // defense-in-depth on its fixed upstreams
		mountCreds:         proxyCredentialSocket == "" && profile == "",
		usageStateDir:      managedUsageDirectory(home, proxyRunName, adminSecret),
		oauthStateDir:      stateDir,
		credentialSocket:   proxyCredentialSocket,
		caseID:             proxyCaseID,
		profileName:        profile,
		profileDirectory:   profileDirectory,
		profileConnection:  profileConnection,
		profileFingerprint: profileFingerprint, profileKey: profileKey,
	}
	if auth.mountCreds {
		auth.apiKeyEnv = os.Getenv("OPENAI_API_KEY")
		auth.anthropicKeyEnv = os.Getenv("ANTHROPIC_API_KEY")
		auth.claudeOAuthEnv = os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")
		auth.storedKeyPath = filepath.Join(home, ".config", "codex-dock", "apikey")
		auth.oauthJSONPath = filepath.Join(home, ".codex", "auth.json")
		auth.anthropicKeyPath = filepath.Join(home, ".config", "codex-dock", "anthropic-apikey")
		auth.claudeCredsPath = filepath.Join(home, ".claude", ".credentials.json")
	}
	if err := ensureProxyContainer(ctx, auth); err != nil {
		return err
	}
	if profileFingerprint != "" {
		clear(profileKey)
		forgetUnlockedProfile(ctx, profile)
	}

	// 2) Egress proxy: forward-only, no credentials, LAN blocked.
	egress := proxyRunArgs{networkResources: resources, upstreamProxy: proxyUpstreamURL, caFile: proxyCAFile,
		name:          proxyRunHTTPName,
		role:          authproxy.RoleEgress,
		networkName:   proxyRunNetwork,
		image:         image,
		listenAddr:    fmt.Sprintf("0.0.0.0:%d", proxyRunHTTPPort),
		blockPrivate:  true,
		forwardAllow:  proxyForwardAllow,
		forwardPolicy: proxyForwardRules,
	}
	return ensureProxyContainer(ctx, egress)
}

// ensureProxyImage builds the proxy image when it is not present locally.
func ensureProxyImage(ctx context.Context, image string) error {
	return ensureProxyImageForStore(ctx, image, false)
}

func ensureProxyImageForStore(ctx context.Context, image string, encrypted bool) error {
	c, err := engine.NewClient()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if inspected, err := c.ImageInspect(ctx, image); err == nil {
		if inspected.Config != nil && proxyImageCompatible(inspected.Config.Labels) && (!encrypted || inspected.Config.Labels[proxyVaultProtocolLabel] == proxyVaultProtocolVersion) {
			return nil
		}
		if image != "codex-dock-proxy:latest" {
			return fmt.Errorf("proxy image %s does not match CLI version/protocol; rebuild it with the current proxy Dockerfile", image)
		}
	} else if !errdefs.IsNotFound(err) {
		return fmt.Errorf("inspecting proxy image %s: %w", image, err)
	}
	fmt.Printf("Proxy image %s not found or outdated; building...\n", image)
	if built, err := buildProxyImageFromExecutable(ctx, image); built || err != nil {
		return err
	}
	dockerfile, buildCtx, err := resolveProxyDockerfile("")
	if err != nil {
		return err
	}
	return executeProxyBuild(ctx, image, dockerfile, buildCtx)
}

func proxyImageCompatible(labels map[string]string) bool {
	return labels[proxyImageProtocolLabel] == proxyImageProtocolVersion && labels[proxyImageBuildLabel] == buildinfo.Version
}

// ensureProxyContainer (re)starts one proxy container from its spec: a running
// container is left alone, a stopped one is started, and a missing one is created.
func ensureProxyContainer(ctx context.Context, spec proxyRunArgs) (result error) {
	if err := spec.networkResources.Validate(); err != nil {
		return err
	}
	// A cached image can still contain an old binary after the host CLI is
	// upgraded. Labels describe its layout, not its authentication behavior.
	defer func() {
		if result == nil && spec.role == authproxy.RoleAuth {
			_, result = connectProxyWithRetry(fmt.Sprintf("http://127.0.0.1:%d", spec.adminPort), fmt.Sprintf("http://%s:%d", spec.name, defaultAuthDataPort), spec.adminSecret, 20)
		}
	}()
	c, err := engine.NewClient()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	v, err := c.ContainerInspect(ctx, spec.name)
	if err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	if err == nil {
		if v.Config == nil || v.State == nil {
			return fmt.Errorf("incomplete proxy record")
		}
		if spec.profileFingerprint != "" && v.Config.Labels[proxyVaultProtocolLabel] != proxyVaultProtocolVersion {
			return fmt.Errorf("encrypted proxy key protocol changed; drain workers and remove the old authentication proxy before restarting")
		}
		if v.Config.Labels[proxyVaultLabel] != spec.profileFingerprint {
			return fmt.Errorf("encrypted profile key changed; drain workers and remove the old proxy before restarting")
		}
		if spec.profileFingerprint != "" && !v.State.Running && (len(spec.profileKey) == 0 || len(spec.profileKey) > credentialstore.UnlockKeyMaxSize) {
			return credentialstore.ErrLocked
		}
		if v.Config.Labels[netquota.Label] != spec.networkResources.ID() {
			return fmt.Errorf("proxy network resource limits changed; drain workers before explicitly recreating this proxy")
		}
		if v.Config.Labels[proxyTransportLabel] != proxyTransportID(spec) {
			return fmt.Errorf("proxy transport settings changed; drain workers before explicitly recreating this proxy")
		}
		if v.Config.Labels[proxyProfileLabel] != profileProxyID(spec) {
			return fmt.Errorf("proxy credential profile changed; drain workers and explicitly recreate the proxy")
		}
		if spec.profileName != "" && (v.HostConfig == nil || !hasProfileMount(v.HostConfig, spec.profileDirectory)) {
			return fmt.Errorf("proxy profile mount differs; drain workers and explicitly recreate the proxy")
		}
		if v.Config.Labels[proxyLayoutLabel] != proxyLayoutVersion {
			workers, listErr := c.ContainerList(ctx, container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", "codex-dock.managed=true"))})
			if listErr != nil {
				return fmt.Errorf("checking workers before proxy replacement: %w", listErr)
			}
			if len(workers) != 0 {
				return fmt.Errorf("proxy %q uses an older layout; stop and remove %d retained workers before replacing it, because their proxy IPs and tokens are pinned", spec.name, len(workers))
			}
			if err = c.ContainerRemove(ctx, spec.name, container.RemoveOptions{Force: true}); err != nil {
				return err
			}
		} else {
			if spec.role == authproxy.RoleAuth && v.Config.Labels[proxyCaseLabel] != caseDigest(spec.caseID) {
				return fmt.Errorf("proxy %q belongs to another credential case; use a separate proxy name", spec.name)
			}
			if spec.credentialSocket != "" {
				mounted := false
				if v.HostConfig != nil {
					for _, mount := range v.HostConfig.Mounts {
						if mount.Source == spec.credentialSocket && mount.Target == "/run/cxd-credential-store.sock" && mount.ReadOnly {
							mounted = true
						}
					}
				}
				if !mounted {
					return fmt.Errorf("proxy %q uses another credential store socket; drain workers before replacing it", spec.name)
				}
			}
			if spec.role == authproxy.RoleEgress {
				if err = checkProxyPolicy(v.Config.Labels[proxyPolicyLabel], spec); err != nil {
					return err
				}
			}
			if v.State.Running {
				return nil
			}
			if err := c.ContainerStart(ctx, spec.name, container.StartOptions{}); err != nil {
				return err
			}
			return deliverProxyKey(ctx, c, spec.name, spec)
		}
	}
	if spec.profileFingerprint != "" {
		image, err := c.ImageInspect(ctx, spec.image)
		if err != nil {
			return err
		}
		if image.Config == nil || image.Config.Labels[proxyVaultProtocolLabel] != proxyVaultProtocolVersion {
			return fmt.Errorf("proxy image does not support encrypted profiles; rebuild it with the current CLI before starting")
		}
	}
	cfg, host, err := proxyConfiguration(spec)
	if err != nil {
		return err
	}
	created, err := c.ContainerCreate(ctx, cfg, host, nil, nil, spec.name)
	if err != nil {
		return err
	}
	if err := c.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return err
	}
	return deliverProxyKey(ctx, c, created.ID, spec)
}

// proxy stop ----------------------------------------------------------------

var proxyStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the proxy containers (auth + egress)",
	RunE: func(cmd *cobra.Command, args []string) error {
		names := managedProxyNames()
		if cmd.Flags().Changed("name") {
			names = []string{proxyStopName}
		}
		for _, name := range names {
			fmt.Printf("Stopping proxy container %q...\n", name)
			if err := stopProxy(cmd.Context(), name); err != nil {
				fmt.Printf("  error: %v\n", err)
			}
		}
		return nil
	},
}

// proxy rm ------------------------------------------------------------------

var proxyRmCmd = &cobra.Command{
	Use:   "rm",
	Short: "Remove the proxy containers (auth + egress)",
	RunE: func(cmd *cobra.Command, args []string) error {
		names := managedProxyNames()
		if cmd.Flags().Changed("name") {
			names = []string{proxyRmName}
		}
		for _, name := range names {
			fmt.Printf("Removing proxy container %q...\n", name)

			if err := removeProxy(cmd.Context(), name, proxyRmForce); err != nil {
				fmt.Printf("  error: %v\n", err)
			}
		}
		return nil
	},
}

// ---------------------------------------------------------------------------

func init() {
	rootCmd.AddCommand(proxyCmd)
	proxyCmd.AddCommand(proxyServeCmd)
	proxyCmd.AddCommand(proxyBuildCmd)
	proxyCmd.AddCommand(proxyRunCmd)
	proxyCmd.AddCommand(proxyStopCmd)
	proxyCmd.AddCommand(proxyRmCmd)

	proxyServeCmd.Flags().StringArrayVar(&proxyAllowedModels, "allow-model", nil, "Allow exact upstream model ID (repeatable; disables unsupported AI routes and WebSockets)")
	proxyRunCmd.Flags().StringArrayVar(&proxyAllowedModels, "allow-model", nil, "Allow exact upstream model ID (repeatable; recreate proxy to change)")
	// serve flags
	proxyServeCmd.Flags().StringVar(&proxyRole, "role", authproxy.RoleAuth, "Proxy role: \"auth\" (reverse routes + token + admin, no forwarding) or \"egress\" (forward proxy only, no credentials)")
	proxyServeCmd.Flags().StringVar(&proxyListenAddr, "listen", "0.0.0.0:18080", "worker-facing listen address")
	proxyServeCmd.Flags().StringVar(&proxyAdminListenAddr, "admin-listen", "", "separate listen address for /admin/* endpoints (auth role). Use host \"egress\" (e.g. egress:18081) to bind the container's egress IP so workers cannot reach it")
	proxyServeCmd.Flags().StringVar(&proxyAdminSecret, "admin-secret", "", "admin secret for /admin/* endpoints")
	proxyServeCmd.Flags().StringVar(&proxyAdminSecretFile, "admin-secret-file", "", "Owner-only (0600) file containing the admin secret")
	bindForwardPolicyFlags(proxyServeCmd)
	bindNetworkResourceFlags(proxyServeCmd)
	proxyServeCmd.Flags().StringVar(&proxyBrokerFile, "credential-routes", "", "Private JSON file of fixed credential routes")
	proxyServeCmd.Flags().BoolVar(&proxyBlockPrivate, "block-private", false, "Refuse outbound connections to private/LAN/link-local addresses (RFC1918, 127/8, 169.254/16, ULA, CGNAT)")
	proxyServeCmd.Flags().StringVar(&proxyOAuthStateDir, "oauth-state-dir", "", "Writable private directory for rotated OAuth credentials")
	proxyServeCmd.Flags().BoolVar(&proxyWorkerMTLS, "worker-mtls", false, "Require token-bound TLS 1.3 client certificates on worker data connections")
	proxyServeCmd.Flags().StringVar(&proxyAuditDirectory, "audit-dir", "", "Private durable audit directory (default: usage-state-dir/audit)")
	proxyServeCmd.Flags().StringVar(&proxyUsageStateDir, "usage-state-dir", "", "Exclusive owner-private directory for durable execution usage limits")
	proxyServeCmd.Flags().StringVar(&proxyCredentialSocket, "credential-store-socket", "", "Private Unix socket for a host-managed credential store")
	proxyServeCmd.Flags().StringVar(&proxyCaseID, "case-id", "", "Credential case selected for this auth proxy instance")
	proxyServeCmd.Flags().StringVar(&proxyProfileStoreDir, "profile-store-dir", "", "Private independent profile directory (auth proxy container only)")

	proxyServeCmd.Flags().Bool("profile-key-stdin", false, "Read an encrypted profile data key from private container stdin")

	// build flags
	proxyBuildCmd.Flags().StringVarP(&proxyBuildTag, "tag", "t", "", "Image tag (default: proxy_image from config)")
	proxyBuildCmd.Flags().StringVarP(&proxyBuildDockerfile, "dockerfile", "f", "", "Path to auth-proxy.Dockerfile")

	// run flags
	proxyRunCmd.Flags().StringVar(&proxyRunName, "name", defaultProxyContainerName, "Auth proxy container name")
	proxyRunCmd.Flags().IntVarP(&proxyRunPort, "port", "p", defaultAuthDataPort, "Auth proxy internal data-plane port (not host-published)")
	proxyRunCmd.Flags().IntVar(&proxyRunAdminPort, "admin-port", defaultAuthAdminPort, "Host-published admin port (bound to 127.0.0.1) for /admin/* endpoints")
	proxyRunCmd.Flags().StringVar(&proxyRunHTTPName, "http-name", defaultHTTPProxyContainerName, "Egress (http) proxy container name")
	proxyRunCmd.Flags().IntVar(&proxyRunHTTPPort, "http-port", defaultHTTPProxyPort, "Egress (http) proxy internal forward port (not host-published)")
	proxyRunCmd.Flags().StringVar(&proxyRunNetwork, "network", network.ProxyNetworkName, "Node egress network to attach the proxy containers to")
	proxyRunCmd.Flags().StringVar(&proxyAdminSecret, "admin-secret", "", "admin secret for /admin/* endpoints")
	proxyRunCmd.Flags().StringVar(&proxyAdminSecretFile, "admin-secret-file", "", "Private host file mounted only into the auth proxy (0600)")
	proxyRunCmd.Flags().StringVar(&proxyCredentialSocket, "credential-store-socket", "", "Private host credential store Unix socket (requires --case-id)")
	proxyRunCmd.Flags().StringVar(&proxyCaseID, "case-id", "", "Credential case for this auth proxy instance")
	bindForwardPolicyFlags(proxyRunCmd)
	bindNetworkResourceFlags(proxyRunCmd)
	proxyRunCmd.Flags().StringVar(&proxyBrokerFile, "credential-routes", "", "Private host JSON file mounted only into the auth proxy")

	// stop flags
	proxyStopCmd.Flags().StringVar(&proxyStopName, "name", defaultProxyContainerName, "Container name")

	// rm flags
	proxyRmCmd.Flags().StringVar(&proxyRmName, "name", defaultProxyContainerName, "Container name")
	proxyRmCmd.Flags().BoolVarP(&proxyRmForce, "force", "f", false, "Force remove a running container")
}

// proxyRunArgs collects the inputs needed to build the "docker run" command for
// one proxy container (auth or egress role).
var proxyBrokerFile string

type proxyRunArgs struct {
	networkResources                                 netquota.Limits
	profileName, profileDirectory, profileConnection string
	profileFingerprint                               string
	profileKey                                       []byte
	brokerFile                                       string
	upstreamProxy, caFile                            string
	name                                             string
	role                                             string // authproxy.RoleAuth / RoleEgress
	networkName                                      string
	image                                            string
	listenAddr                                       string
	adminPort                                        int    // >0: publish to 127.0.0.1 (auth only)
	adminListenAddr                                  string // "" disables the admin listener (egress)
	adminSecret                                      string
	adminSecretFile                                  string
	forwardAllow                                     []string
	forwardPolicy                                    networkpolicy.Rules
	blockPrivate                                     bool
	mountCreds                                       bool // bind host credentials (auth only)

	// OpenAI / Codex credential sources (used when mountCreds).
	apiKeyEnv     string // OPENAI_API_KEY env value
	storedKeyPath string // ~/.config/codex-dock/apikey
	oauthJSONPath string // ~/.codex/auth.json

	// Anthropic / Claude Code credential sources (used when mountCreds).
	anthropicKeyEnv  string // ANTHROPIC_API_KEY env value
	claudeOAuthEnv   string // CLAUDE_CODE_OAUTH_TOKEN from host setup-token
	anthropicKeyPath string // ~/.config/codex-dock/anthropic-apikey
	claudeCredsPath  string // ~/.claude/.credentials.json
	oauthStateDir    string // private writable host directory
	usageStateDir    string // private durable request/usage counters; never shared with workers
	credentialSocket string // private host Unix socket, mounted only into auth proxy
	caseID           string
	allowedModels    []string
}

func caseDigest(id string) string {
	if id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:])
}

// buildProxyRunArgs constructs the "docker run" argument list for one proxy
// container. The auth role publishes only its admin port (host loopback) and
// binds the host's credentials; the egress role publishes nothing and mounts no
// credentials. The data-plane / forward port is never host-published — workers
// reach it over their per-worker Docker network.
func buildProxyRunArgs(a proxyRunArgs) []string {
	args := []string{
		"run", "-d", "--name", a.name,
		"--label", proxyLayoutLabel + "=" + proxyLayoutVersion,
		"--network", a.networkName,
	}
	if id := a.networkResources.ID(); id != "" {
		args = append(args, "--label", netquota.Label+"="+id)
	}
	if a.brokerFile != "" {
		args = append(args, "-v", a.brokerFile+":/run/cxd-broker.json:ro")
	}
	if a.profileName != "" {
		args = append(args, "--label", proxyProfileLabel+"="+profileProxyID(a), "-v", a.profileDirectory+":/run/cxd-profile:rw")
	}
	if a.profileFingerprint != "" {
		args = append(args, "--label", proxyVaultLabel+"="+a.profileFingerprint, "--label", proxyProfileNameLabel+"="+a.profileName, "--label", proxyVaultProtocolLabel+"="+proxyVaultProtocolVersion)
	}
	if a.adminSecretFile != "" {
		args = append(args, "-v", a.adminSecretFile+":/run/cxd-admin-secret:ro")
	}
	if id := proxyTransportID(a); id != "" {
		args = append(args, "--label", proxyTransportLabel+"="+id)
	}
	if a.caFile != "" {
		args = append(args, "-v", a.caFile+":/run/cxd-proxy-ca.pem:ro")
	}
	if a.role == authproxy.RoleEgress {
		args = append(args, "--label", proxyPolicyLabel+"="+forwardPolicyID(a))
	} else if a.caseID != "" {
		args = append(args, "--label", proxyCaseLabel+"="+caseDigest(a.caseID))
	}
	if a.adminPort > 0 {
		args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", a.adminPort, a.adminPort))
	}

	if a.mountCreds {
		if a.apiKeyEnv != "" {
			args = append(args, "-e", "OPENAI_API_KEY="+a.apiKeyEnv)
		}
		if a.anthropicKeyEnv != "" {
			args = append(args, "-e", "ANTHROPIC_API_KEY="+a.anthropicKeyEnv)
		}
		if a.claudeOAuthEnv != "" {
			args = append(args, "-e", "CLAUDE_CODE_OAUTH_TOKEN="+a.claudeOAuthEnv)
		}
		if _, err := os.Stat(a.storedKeyPath); err == nil {
			args = append(args, "-v", a.storedKeyPath+":/root/.config/codex-dock/apikey:ro")
		}
		if _, err := os.Stat(a.anthropicKeyPath); err == nil {
			args = append(args, "-v", a.anthropicKeyPath+":/root/.config/codex-dock/anthropic-apikey:ro")
		}
		if _, err := os.Stat(a.oauthJSONPath); err == nil {
			args = append(args, "-v", a.oauthJSONPath+":/root/.codex/auth.json:ro")
		}
		if _, err := os.Stat(a.claudeCredsPath); err == nil {
			args = append(args, "-v", a.claudeCredsPath+":/root/.claude/.credentials.json:ro")
		}
		if a.oauthStateDir != "" {
			args = append(args, "-v", a.oauthStateDir+":/root/.local/state/codex-dock/oauth:rw")
		}
	}
	if a.credentialSocket != "" {
		args = append(args, "-v", a.credentialSocket+":/run/cxd-credential-store.sock:ro")
	}
	if a.usageStateDir != "" {
		args = append(args, "-v", a.usageStateDir+":/run/cxd-usage:rw")
	}

	args = append(args, a.image, "proxy", "serve", "--listen", a.listenAddr)
	args = append(args, a.networkResources.Args()...)
	if a.profileName != "" {
		args = append(args, "--auth-profile", a.profileName, "--profile-store-dir", "/run/cxd-profile")
	}
	if a.profileFingerprint != "" {
		args = append(args, "--profile-key-stdin")
	}
	for _, model := range a.allowedModels {
		args = append(args, "--allow-model", model)
	}
	if a.brokerFile != "" {
		args = append(args, "--credential-routes", "/run/cxd-broker.json")
	}
	if a.upstreamProxy != "" {
		args = append(args, "--upstream-proxy", a.upstreamProxy)
	}
	if a.caFile != "" {
		args = append(args, "--ca-bundle", "/run/cxd-proxy-ca.pem")
	}
	if a.role != "" {
		args = append(args, "--role", a.role)
	}
	if a.role == authproxy.RoleAuth {
		args = append(args, "--worker-mtls")
	}
	if a.adminListenAddr != "" {
		args = append(args, "--admin-listen", a.adminListenAddr)
	}

	if a.adminSecretFile != "" {
		args = append(args, "--admin-secret-file", "/run/cxd-admin-secret")
	} else if a.adminSecret != "" {
		args = append(args, "--admin-secret", a.adminSecret)
	}
	if a.blockPrivate {
		args = append(args, "--block-private")
	}
	if a.oauthStateDir != "" {
		args = append(args, "--oauth-state-dir", proxyCredentialHome+"/.local/state/codex-dock/oauth")
	}
	if a.usageStateDir != "" {
		args = append(args, "--usage-state-dir", "/run/cxd-usage")
	}
	if a.credentialSocket != "" {
		args = append(args, "--credential-store-socket", "/run/cxd-credential-store.sock", "--case-id", a.caseID)
	}
	rules := effectiveForwardRules(a)
	if rules.DefaultDeny {
		args = append(args, "--forward-default-deny")
	} else {
		args = append(args, "--forward-default-deny=false")
	}
	for _, d := range rules.AllowDomains {
		args = append(args, "--forward-allow-domain", d)
	}
	for _, setting := range []struct {
		flag   string
		values []string
	}{
		{"--forward-deny-domain", rules.DenyDomains},
		{"--forward-allow-cidr", rules.AllowCIDRs},
		{"--forward-deny-cidr", rules.DenyCIDRs},
	} {
		for _, value := range setting.values {
			args = append(args, setting.flag, value)
		}
	}
	for _, setting := range []struct {
		flag   string
		values []int
	}{
		{"--forward-allow-port", rules.AllowPorts},
		{"--forward-deny-port", rules.DenyPorts},
	} {
		for _, value := range setting.values {
			args = append(args, setting.flag, strconv.Itoa(value))
		}
	}

	return args
}

func ensureBridgeNetwork(ctx context.Context, networkName, bridgeName string) error {
	return ensureNodeBridge(ctx, networkName)
}

// resolveProxyDockerfile returns the auth-proxy Dockerfile path and build
// context to use.
// Priority: explicit -f flag > auth-proxy.Dockerfile /
// docker/auth-proxy.Dockerfile in CWD > config-dir default.
// The build context is always "." (CWD) because the proxy image compiles Go
// source from the repo root.
func resolveProxyDockerfile(flagValue string) (string, string, error) {
	if flagValue != "" {
		return flagValue, ".", nil
	}

	// Check well-known locations relative to the current directory.
	// docker/proxy/Dockerfile is the current layout; the older flat names are
	// kept for backward compatibility with existing checkouts.
	for _, p := range []string{"docker/proxy/Dockerfile", "auth-proxy.Dockerfile", "docker/auth-proxy.Dockerfile"} {
		if _, err := os.Stat(p); err == nil {
			return p, ".", nil
		}
	}

	// A development CLI can be launched from a project directory. Its own
	// checkout remains a valid source for a matching proxy build.
	if executable, err := os.Executable(); err == nil {
		dir := filepath.Dir(executable)
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "cmd", "proxy.go")); err == nil {
				if _, err := os.Stat(filepath.Join(dir, "docker", "proxy", "Dockerfile")); err == nil {
					return filepath.Join(dir, "docker", "proxy", "Dockerfile"), dir, nil
				}
			}
		}
	}
	// Fall back to the default Dockerfile written into the config directory.
	// It compiles the current directory's Go source, so fail with a useful
	// instruction before starting a BuildKit build from an unrelated project.
	for _, source := range []string{"go.mod", "cmd/proxy.go", "main.go"} {
		if _, err := os.Stat(source); err != nil {
			return "", "", fmt.Errorf("proxy image build needs the codex-dock source checkout; run init or proxy build from its repository root (%s is missing in the current directory)", source)
		}
	}
	configDir, err := defaultConfigDir()
	if err != nil {
		return "", "", fmt.Errorf("auth-proxy.Dockerfile not found; use -f to specify path")
	}
	if err := ensureProxyDockerfile(configDir); err != nil {
		return "", "", fmt.Errorf("writing proxy Dockerfile to config dir: %w", err)
	}
	return filepath.Join(configDir, "auth-proxy.Dockerfile"), ".", nil
}

// ensureProxyDockerfile writes the embedded auth-proxy.Dockerfile into dir
// if not already present.
func ensureProxyDockerfile(dir string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	dfPath := filepath.Join(dir, "auth-proxy.Dockerfile")
	if _, err := os.Stat(dfPath); os.IsNotExist(err) {
		if err := os.WriteFile(dfPath, dockerdefaults.ProxyDockerfile, 0644); err != nil {
			return err
		}
	}
	return nil
}

// executeProxyBuild runs "docker build" for the auth proxy image.
// --progress=plain forces BuildKit to stream every step's full output line-by-line
// instead of collapsing it into a live-updating TTY view, so an auto-build
// triggered by `run` never looks stalled while it's actually still working.
func executeProxyBuild(ctx context.Context, tag, dockerfile, buildCtx string) error {
	return executeBuild(ctx, tag, dockerfile, buildCtx)
}
