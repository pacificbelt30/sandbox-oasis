package cmd

import (
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"time"
)

func applyOptionalRunDefaults(cmd *cobra.Command) error {
	if _, err := workerpolicy.AgentStateBytes(runOpts.AgentStateSize); err != nil {
		return err
	}
	flags := cmd.Flags()
	if !flags.Changed("protect-main") {
		runOpts.Lifecycle.ProtectMain = viper.GetBool("run.protect_main")
	}
	for _, setting := range []struct {
		flag, key string
		target    *time.Duration
	}{{"max-lifetime", "run.max_lifetime", &runOpts.Lifecycle.MaxLifetime}, {"idle-timeout", "run.idle_timeout", &runOpts.Lifecycle.IdleTimeout}, {"retention", "run.retention", &runOpts.Lifecycle.Retention}, {"readiness-timeout", "run.readiness_timeout", &runOpts.Environment.ReadinessTimeout}} {
		if !flags.Changed(setting.flag) && viper.IsSet(setting.key) {
			d, err := time.ParseDuration(viper.GetString(setting.key))
			if err != nil || d < 0 {
				return fmt.Errorf("invalid %s duration", setting.key)
			}
			*setting.target = d
		}
	}
	if !flags.Changed("deny-read") {
		runOpts.Filesystem.DenyRead = viper.GetStringSlice("run.deny_read")
	}
	if !flags.Changed("deny-write") {
		runOpts.Filesystem.DenyWrite = viper.GetStringSlice("run.deny_write")
	}
	if !flags.Changed("ssh-proxy") {
		runOpts.Compatibility.SSHProxy = viper.GetBool("run.ssh_proxy")
	}
	if !flags.Changed("jvm-proxy") {
		runOpts.Compatibility.JVMProxy = viper.GetBool("run.jvm_proxy")
	}
	if !flags.Changed("ca-bundle") {
		runOpts.Compatibility.CABundle = viper.GetString("run.ca_bundle")
	}
	if !flags.Changed("environment-lock") {
		runEnvironmentLock = viper.GetString("run.environment_lock")
	}
	if !flags.Changed("image-id") {
		runOpts.Environment.ImageID = viper.GetString("run.image_id")
	}
	if !flags.Changed("start-hook") {
		runStartHooks = viper.GetStringSlice("run.start_hooks")
	}
	if !flags.Changed("readiness") {
		runReadiness = viper.GetString("run.readiness")
	}
	runOpts.Environment.StartHooks = nil
	for _, hook := range runStartHooks {
		runOpts.Environment.StartHooks = append(runOpts.Environment.StartHooks, []string{"sh", "-c", hook})
	}
	runOpts.Environment.Readiness = nil
	if runReadiness != "" {
		runOpts.Environment.Readiness = []string{"sh", "-c", runReadiness}
	}
	return applyUsageRunDefaults(cmd)
}
