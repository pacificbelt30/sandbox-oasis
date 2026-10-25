package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/authproxy"
	"github.com/pacificbelt30/codex-dock/internal/buildinfo"
	"github.com/pacificbelt30/codex-dock/internal/diagnostics"
	"github.com/pacificbelt30/codex-dock/internal/logincheck"
	"github.com/pacificbelt30/codex-dock/internal/releaseinfo"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/credentialstore"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func init() { rootCmd.AddCommand(newStatusCommand()) }

func newStatusCommand() *cobra.Command {
	var asJSON, checkConnection, checkUpdates bool
	cmd := &cobra.Command{Use: "status", Short: "Show host, profile, proxy and worker readiness without printing credentials", Args: cobra.NoArgs}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print a structured readiness report")
	cmd.Flags().BoolVar(&checkConnection, "check-connection", false, "Explicitly check saved API keys against provider models endpoints (no inference)")
	cmd.Flags().BoolVar(&checkUpdates, "check-updates", false, "Explicitly query GitHub for the latest stable release")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
		defer cancel()
		report := collectStatusReport(ctx, checkConnection, checkUpdates)
		if err := report.Write(cmd.OutOrStdout(), asJSON); err != nil {
			return err
		}
		return report.Err()
	}
	return cmd
}

func collectStatusReport(ctx context.Context, checkConnection, checkUpdates bool) diagnostics.Report {
	report := diagnostics.Report{}
	report.Add("version", buildinfo.Version, "", nil)
	profileStatus(ctx, &report, checkConnection)
	runtime := runtimeReport(ctx)
	report.Checks = append(report.Checks, runtime.Checks...)
	proxyStatus(ctx, &report)
	manager, err := sandbox.NewManager(managementConfig())
	if err == nil {
		defer func() { _ = manager.Close() }()
		workers, listErr := manager.ListContext(ctx, true)
		report.Add("workers", fmt.Sprintf("%d retained workers/sessions", len(workers)), "Run oaiso ps -a and oaiso vm ps -a.", listErr)
	} else {
		report.Add("workers", "", "Check the local node socket.", fmt.Errorf("worker manager is unavailable"))
	}
	if checkUpdates {
		latest, err := releaseinfo.Latest(ctx)
		detail := ""
		if err == nil {
			detail = "latest stable: " + latest.Tag + "; installed: " + buildinfo.Version
		}
		report.Add("updates", detail, "Review the release and installation plan before updating.", err)
	} else {
		report.Checks = append(report.Checks, diagnostics.Check{Name: "updates", Status: "unchecked", Detail: "No external release request was made.", Fix: "Run oaiso status --check-updates."})
	}
	return report
}

func profileStatus(ctx context.Context, report *diagnostics.Report, checkConnection bool) {
	profile := selectedAuthProfile()
	if profile == "" {
		report.Checks = append(report.Checks, diagnostics.Check{Name: "profile", Status: "unchecked", Detail: "No independent profile selected (legacy host integration).", Fix: "Run oaiso login to separate authentication from the host CLI."})
		return
	}
	store, err := openAuthProfile(profile)
	if err != nil {
		report.Add("profile", "", "Run oaiso auth profile list or oaiso login.", fmt.Errorf("selected profile storage is unavailable"))
		return
	}
	storage := "plaintext"
	if encrypted, ok := store.(*credentialstore.EncryptedStore); ok {
		info, err := encrypted.Info(ctx)
		if err != nil {
			report.Add("profile", "", "Check encrypted profile metadata.", fmt.Errorf("encrypted profile metadata is invalid"))
			return
		}
		if info.Format != "age" {
			report.Add("profile", "", "Run oaiso auth profile migrate-age "+profile+" after draining workers.", credentialstore.ErrMigrationRequired)
			return
		}
		storage = "encrypted (" + info.Backend + ", " + info.Format + ")"
		if !checkConnection {
			report.Add("profile", profile+" ("+storage+", 0600); saved, locked for this command", "", nil)
			report.Checks = append(report.Checks, diagnostics.Check{Name: "connection", Status: "unchecked", Detail: "Credentials were not decrypted; connection and inference not tested", Fix: "Start the selected agent, or explicitly use --check-connection for API keys."})
			return
		}
		store, err = unlockedAuthProfile(ctx, profile)
		if err != nil {
			report.Add("profile", "", "Unlock the selected profile.", fmt.Errorf("encrypted profile could not be unlocked"))
			return
		}
	}
	value, err := store.Load(ctx, profile)
	if err != nil {
		report.Add("profile", "", "Run oaiso auth profile list or oaiso login.", fmt.Errorf("selected profile cannot be loaded"))
		return
	}
	report.Add("profile", profile+" ("+storage+", 0600); credentials saved", "", nil)
	for _, provider := range []string{"openai", "anthropic"} {
		key, oauth := value.OpenAIAPIKey, value.OpenAIOAuth != nil
		if provider == "anthropic" {
			key, oauth = value.AnthropicAPIKey, value.AnthropicOAuth != nil
		}
		if key == "" && !oauth {
			continue
		}
		if checkConnection && key != "" {
			err := logincheck.APIKey(ctx, provider, key)
			report.Add(provider+"-connection", "API authentication check passed; inference not tested", "Check the API key and its models-list permission; inference is a separate check.", err)
		} else {
			detail := "API key saved; connection and inference not tested"
			if oauth {
				detail = "OAuth saved; live login/refresh and inference not tested"
			}
			report.Checks = append(report.Checks, diagnostics.Check{Name: provider + "-connection", Status: "unchecked", Detail: detail, Fix: "Start the agent to verify live inference."})
		}
	}
}

func proxyStatus(ctx context.Context, report *diagnostics.Report) {
	secret := viper.GetString("proxy.admin_secret")
	if value, ok := os.LookupEnv("CODEX_DOCK_PROXY_ADMIN_SECRET"); ok {
		secret = value
	}
	secret, err := resolveProxyAdminSecret(secret, viper.GetString("proxy.admin_secret_file"))
	if err != nil {
		report.Add("proxy", "", "Check the configured admin-secret file.", fmt.Errorf("proxy admin authority is unavailable"))
		return
	}
	adminURL, containerURL := viper.GetString("proxy.admin_url"), viper.GetString("proxy.container_url")
	if adminURL == "" {
		adminURL = "http://127.0.0.1:18081"
	}
	if containerURL == "" {
		containerURL = "http://codex-auth-proxy:18080"
	}
	proxy, err := authproxy.NewRemoteProxyContext(ctx, adminURL, containerURL, secret)
	if err != nil {
		report.Add("proxy", "", "Run oaiso init for your selected agent; existing workers must be drained before recreating incompatible proxies.", fmt.Errorf("auth proxy is unavailable or incompatible"))
		return
	}
	if expected := selectedAuthProfile(); expected != "" && proxy.AuthProfile() != expected {
		report.Add("proxy", "", "Drain old workers/proxies, then initialize the selected profile.", fmt.Errorf("running proxy uses a different credential profile"))
		return
	}
	report.Add("proxy", "worker-token enforcement is active; inference not tested", "", nil)
}
