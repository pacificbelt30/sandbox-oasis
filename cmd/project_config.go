package cmd

import (
	"context"
	"fmt"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/projectconfig"
	"github.com/pacificbelt30/codex-dock/internal/template"
	"github.com/spf13/cobra"
)

const projectConfigName = projectconfig.Filename

type ProjectConfig = projectconfig.Config

func loadProjectConfig(dir string) (*ProjectConfig, error) { return projectconfig.Load(dir) }

func applyProjectRunConfig(cmd *cobra.Command, cfg *ProjectConfig) {
	if cfg == nil {
		return
	}
	flags := cmd.Flags()
	if !flags.Changed("policy-preset") && cfg.Run.PolicyPreset != "" {
		runPolicyPreset = cfg.Run.PolicyPreset
	}
	f, l, e, _ := cfg.Policies()
	if limits, err := cfg.UsagePolicy(); err == nil && limits != nil {
		if runOpts.TokenUsagePolicy == nil {
			runOpts.TokenUsagePolicy = &identity.UsagePolicy{}
		}
		for _, setting := range []struct {
			flag   string
			value  int64
			target *int64
		}{
			{"max-requests", limits.MaxRequests, &runOpts.TokenUsagePolicy.MaxRequests},
			{"max-concurrent", limits.MaxConcurrent, &runOpts.TokenUsagePolicy.MaxConcurrent},
			{"max-transfer-bytes", limits.MaxBytes, &runOpts.TokenUsagePolicy.MaxBytes},
			{"max-observed-tokens", limits.MaxTokens, &runOpts.TokenUsagePolicy.MaxTokens},
		} {
			if !flags.Changed(setting.flag) && setting.value > 0 && (*setting.target == 0 || setting.value < *setting.target) {
				*setting.target = setting.value
			}
		}
		if *runOpts.TokenUsagePolicy == (identity.UsagePolicy{}) {
			runOpts.TokenUsagePolicy = nil
		}
	}
	if !flags.Changed("protect-main") && l.ProtectMain {
		runOpts.Lifecycle.ProtectMain = true
	}
	runOpts.Filesystem.DenyRead = append(runOpts.Filesystem.DenyRead, f.DenyRead...)
	runOpts.Filesystem.DenyWrite = append(runOpts.Filesystem.DenyWrite, f.DenyWrite...)
	if !flags.Changed("max-lifetime") && l.MaxLifetime > 0 && (runOpts.Lifecycle.MaxLifetime == 0 || l.MaxLifetime < runOpts.Lifecycle.MaxLifetime) {
		runOpts.Lifecycle.MaxLifetime = l.MaxLifetime
	}
	if !flags.Changed("idle-timeout") && l.IdleTimeout > 0 && (runOpts.Lifecycle.IdleTimeout == 0 || l.IdleTimeout < runOpts.Lifecycle.IdleTimeout) {
		runOpts.Lifecycle.IdleTimeout = l.IdleTimeout
	}
	if !flags.Changed("retention") && l.Retention > 0 {
		runOpts.Lifecycle.Retention = l.Retention
	}
	if !flags.Changed("start-hook") && len(e.StartHooks) > 0 {
		runOpts.Environment.StartHooks = e.StartHooks
	}
	if !flags.Changed("readiness") && len(e.Readiness) > 0 {
		runOpts.Environment.Readiness = e.Readiness
	}
	if !flags.Changed("readiness-timeout") && e.ReadinessTimeout > 0 {
		runOpts.Environment.ReadinessTimeout = e.ReadinessTimeout
	}

	if !flags.Changed("image") && !flags.Changed("template") {
		if cfg.Run.Image != "" {
			runOpts.Image = cfg.Run.Image
		} else if cfg.Run.Template != "" {
			tmpl, _ := template.Get(cfg.Run.Template) // validated at load
			runOpts.Image = tmpl.Tag("")
		}
	}
	if !flags.Changed("model") && cfg.Run.Model != "" {
		runOpts.Model = cfg.Run.Model
	}
	if !flags.Changed("profile-size") && cfg.Run.ProfileSize != "" {
		runOpts.ResourceProfile = cfg.Run.ProfileSize
	}
	if !runOpts.DirectAgent && !flags.Changed("agent") && cfg.Run.Agent != "" {
		agentFlag = cfg.Run.Agent
	}
	if !flags.Changed("approval-mode") && cfg.Run.ApprovalMode != "" {
		approvalModeFlag = cfg.Run.ApprovalMode
	}
	if !flags.Changed("watch-polling") && cfg.Run.WatchPolling != nil {
		runOpts.WatchPolling = *cfg.Run.WatchPolling
	}
	if !flags.Changed("read-only") && cfg.Run.ReadOnly != nil && *cfg.Run.ReadOnly {
		runOpts.ReadOnly = true
	}
	if !flags.Changed("read-only-rootfs") && cfg.Run.ReadOnlyRootFS != nil && *cfg.Run.ReadOnlyRootFS {
		runOpts.ReadOnlyRootFS = true
	}
	if !flags.Changed("no-git-metadata") && cfg.Run.NoGitMetadata != nil && *cfg.Run.NoGitMetadata {
		runOpts.NoGitMetadata = true
	}
	if !flags.Changed("no-internet") && cfg.Run.NoInternet != nil && *cfg.Run.NoInternet {
		runOpts.NoInternet = true
	}
	if !flags.Changed("egress-allow-domain") && len(cfg.Run.EgressAllowDomains) > 0 {
		runEgressAllow = cfg.Run.EgressAllowDomains
	}
}

func applyProjectBuildConfig(cmd *cobra.Command, cfg *ProjectConfig) {
	if cfg == nil {
		return
	}
	flags := cmd.Flags()
	if !flags.Changed("template") && !flags.Changed("dockerfile") {
		if cfg.Build.Template != "" {
			buildTemplate = cfg.Build.Template
			buildDockerfile = ""
		} else if cfg.Build.Dockerfile != "" {
			buildDockerfile = filepath.Join(cfg.Root, cfg.Build.Dockerfile)
			buildTemplate = ""
		}
	}
	if !flags.Changed("tag") && cfg.Build.Tag != "" {
		buildTag = cfg.Build.Tag
	}
}

func projectDockerfile(cfg *ProjectConfig) (string, string, bool) {
	if cfg == nil || cfg.Build.Dockerfile == "" {
		return "", "", false
	}
	return filepath.Join(cfg.Root, cfg.Build.Dockerfile), cfg.Root, true
}

func buildProjectImage(ctx context.Context, cfg *ProjectConfig, tag string) error {
	dockerfile, buildCtx, ok := projectDockerfile(cfg)
	if !ok {
		return fmt.Errorf("no project Dockerfile configured")
	}
	data, err := os.ReadFile(dockerfile)
	if err != nil {
		return err
	}
	// The usual project Dockerfile extends the bundled worker image. Build that
	// base on a fresh clone before asking BuildKit to resolve it locally.
	if projectDockerfileUsesBase(data, "codex-dock:latest") {
		if err := ensureBaseImage(ctx, "codex-dock:latest"); err != nil {
			return err
		}
	}
	return executeBuild(ctx, tag, dockerfile, buildCtx)
}

func projectDockerfileUsesBase(data []byte, image string) bool {
	return projectconfig.DockerfileUsesBase(data, image)
}

func projectSettingsSummary(cfg *ProjectConfig) string {
	if cfg == nil {
		return "No " + projectConfigName + " in this directory."
	}
	rows := []string{"Project config: " + filepath.Join(cfg.Root, projectConfigName)}
	if cfg.Run.Image != "" {
		rows = append(rows, "run.image = "+cfg.Run.Image)
	}
	if cfg.Run.Template != "" {
		rows = append(rows, "run.template = "+cfg.Run.Template)
	}
	if cfg.Run.Agent != "" {
		rows = append(rows, "run.agent = "+cfg.Run.Agent)
	}
	if cfg.Build.Dockerfile != "" {
		rows = append(rows, "build.dockerfile = "+cfg.Build.Dockerfile)
	}
	if cfg.Build.Template != "" {
		rows = append(rows, "build.template = "+cfg.Build.Template)
	}
	if cfg.Build.Tag != "" {
		rows = append(rows, "build.tag = "+cfg.Build.Tag)
	}
	sort.Strings(rows[1:])
	return strings.Join(rows, "\n")
}
