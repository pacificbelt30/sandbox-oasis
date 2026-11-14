package cmd

import (
	"context"
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/diagnostics"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/kataguard"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func applyRuntimeDefaults(cmd *cobra.Command) {
	for _, setting := range []struct {
		flag, key, env string
		target         *string
		fallback       string
	}{
		{"runtime", "run.runtime", "CODEX_DOCK_RUNTIME", &runOpts.Runtime, sandbox.RuntimeKata},
		{"kata-runtime", "run.kata_runtime", "CODEX_DOCK_KATA_RUNTIME", &runOpts.KataRuntime, sandbox.DefaultKataRuntime},
	} {
		if cmd.Flags().Changed(setting.flag) {
			continue
		}
		*setting.target = setting.fallback
		if viper.IsSet(setting.key) {
			*setting.target = viper.GetString(setting.key)
		}
		if v, ok := os.LookupEnv(setting.env); ok {
			*setting.target = v
		}
	}
}

func init() {
	var asJSON bool
	check := &cobra.Command{
		Use: "runtime-check", Aliases: []string{"doctor"}, Short: "Diagnose the node, KVM, cgroups and builder with recovery steps",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			applyRuntimeDefaults(cmd)
			report := runtimeReport(cmd.Context())
			if err := report.Write(cmd.OutOrStdout(), asJSON); err != nil {
				return err
			}
			return report.Err()
		},
	}
	check.Flags().BoolVar(&asJSON, "json", false, "Print a structured report; failures still return a nonzero exit status")
	check.Flags().StringVar(&runOpts.Runtime, "runtime", sandbox.RuntimeKata, "Worker isolation: kata or runc")
	check.Flags().StringVar(&runOpts.KataRuntime, "kata-runtime", sandbox.DefaultKataRuntime, "Kata v2 runtime registration")
	rootCmd.AddCommand(check)
	var kataPath, daemonPath, containerd, launcher, output, runtime, shim, loggerPath string
	seal := &cobra.Command{Use: "runtime-lock", Short: "Pin already-verified local Kata assets in a new manifest", RunE: func(cmd *cobra.Command, args []string) error {
		if containerd == "" {
			var err error
			containerd, err = exec.LookPath("containerd")
			if err != nil {
				return err
			}
		}
		if loggerPath != "" {
			return kataguard.Seal(kataPath, daemonPath, containerd, launcher, runtime, output, shim, loggerPath)
		}
		return kataguard.Seal(kataPath, daemonPath, containerd, launcher, runtime, output, shim)
	}}
	seal.Flags().StringVar(&kataPath, "kata-config", "/etc/codex-dock/kata.toml", "Installed Kata profile")
	seal.Flags().StringVar(&daemonPath, "daemon-config", "/etc/codex-dock/containerd.toml", "Dedicated containerd config")
	seal.Flags().StringVar(&containerd, "containerd", "", "Trusted containerd executable (default: PATH)")
	seal.Flags().StringVar(&launcher, "launcher", "/opt/codex-dock/worker-guard", "Static trusted worker bootstrap")
	seal.Flags().StringVar(&runtime, "kata-runtime", sandbox.DefaultKataRuntime, "Kata registration")
	seal.Flags().StringVar(&loggerPath, "logger", "", "Pinned binary logger executable (default: PATH)")
	seal.Flags().StringVar(&output, "output", "/etc/codex-dock/kata-assets.json", "New manifest path; never overwritten")
	seal.Flags().StringVar(&shim, "runtime-shim", "/opt/kata/runtime-rs/bin/containerd-shim-kata-v2", "Pinned embedded Dragonball runtime")
	rootCmd.AddCommand(seal)
}

func runtimeReport(ctx context.Context) diagnostics.Report {
	var r diagnostics.Report
	policyErr := configuredPolicy().Validate()
	r.Add("egress-policy", "Host rules are valid; private addresses stay blocked", "Run oaiso policy check and oaiso proxy configure to inspect host rules.", policyErr)
	isolation, registration, resolveErr := sandbox.ResolveRuntime(runOpts.Runtime, runOpts.KataRuntime)
	r.Add("runtime", registration, "Choose --runtime kata or explicit --runtime runc.", resolveErr)
	if isolation == sandbox.RuntimeKata {
		var hostErr error
		if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
			hostErr = fmt.Errorf("this pinned Dragonball profile requires Linux x86_64; found %s/%s", runtime.GOOS, runtime.GOARCH)
		}
		r.Add("host", runtime.GOOS+"/"+runtime.GOARCH, "Use a Linux x86_64 KVM host; automatic setup supports Ubuntu 24.04 and 26.04.", hostErr)
		info, err := os.Stat("/dev/kvm")
		if err == nil && info.Mode()&os.ModeDevice == 0 {
			err = fmt.Errorf("/dev/kvm is not a device")
		}
		r.Add("kvm", "KVM device exists; the root node owns VM access", "Enable CPU virtualization/nested virtualization, then sudo modprobe kvm_intel (Intel) or kvm_amd (AMD).", err)
		data, err := os.ReadFile("/sys/fs/cgroup/cgroup.controllers")
		if err == nil {
			for _, c := range []string{"cpu", "memory", "pids"} {
				if !strings.Contains(" "+strings.TrimSpace(string(data))+" ", " "+c+" ") {
					err = fmt.Errorf("cgroup v2 controller missing: %s", c)
					break
				}
			}
		}
		r.Add("cgroups", "cgroup v2 has cpu, memory and pids", "Use cgroup v2 and check systemctl status codex-dock-node; see doc/setup.md.", err)
	}
	if isolation == sandbox.RuntimeKata && resolveErr == nil {
		probe, cancel := context.WithTimeout(ctx, 5*time.Second)
		storage, err := engine.StorageStatus(probe, engine.Socket())
		cancel()
		r.Add("storage", fmt.Sprintf("XFS project quotas enforced; free %.1f GiB / %.1f GiB; per-worker %d MiB / %d inodes", float64(storage.AvailableBytes)/(1<<30), float64(storage.TotalBytes)/(1<<30), storage.WorkerLimitBytes/(1<<20), storage.WorkerLimitInodes), diagnostics.Hint(err), err)
	}
	if resolveErr == nil {
		m, err := sandbox.NewManager(managementConfig())
		if err == nil {
			defer func() { _ = m.Close() }()
			probe, cancel := context.WithTimeout(ctx, 45*time.Second)
			err = m.CheckRuntime(probe, runOpts)
			cancel()
		}
		r.Add("node", "pinned runtime, assets, resource profile and management socket verified", diagnostics.Hint(err), err)
	}
	addr := os.Getenv("BUILDKIT_HOST")
	if addr == "" {
		addr = "unix:///run/codex-dock-buildkit/buildkitd.sock"
	}
	probe, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Do not emit debug info: report availability without paths to session secrets.
	err := exec.CommandContext(probe, "buildctl", "--addr", addr, "debug", "info").Run()
	r.Add("builder", "BuildKit is reachable", "Run systemctl status codex-dock-buildkit; check BUILDKIT_HOST and membership of the codex-dock group.", err)
	data, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err == nil && strings.TrimSpace(string(data)) != "1" {
		err = fmt.Errorf("IPv4 forwarding is disabled")
	}
	r.Add("forwarding", "IPv4 forwarding enabled", "Run sudo sysctl -w net.ipv4.ip_forward=1; inspect the host firewall forward policy without flushing it.", err)
	return r
}
