package cmd

import (
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/proxysettings"
	"github.com/pacificbelt30/codex-dock/internal/ui"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"path/filepath"
)

var uiCmd = &cobra.Command{
	Use:   "ui",
	Short: "Launch the Oaiso TUI worker manager",
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := proxyConfigPath()
		if err != nil {
			return err
		}
		store, err := agentPolicyStore()
		if err != nil {
			return err
		}
		return ui.RunWithPolicyOptions(managementConfig(), path, store.Path, viper.GetString("run.agent_policy"))
	},
}

func init() {
	rootCmd.AddCommand(uiCmd)
	agentPolicyCmd.AddCommand(&cobra.Command{Use: "configure", Short: "Edit and review host agent policies in the TUI", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if agentPolicyJSON {
			return fmt.Errorf("configure is interactive; use agent-policy show --json for structured output")
		}
		store, err := agentPolicyStore()
		if err != nil {
			return err
		}
		return ui.RunAgentPolicies(store.Path, viper.GetString("run.agent_policy"))
	}})
	proxyCmd.AddCommand(&cobra.Command{Use: "configure", Short: "Edit non-secret proxy policy settings in the TUI", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		path, err := proxyConfigPath()
		if err != nil {
			return err
		}
		return ui.RunProxySettings(path)
	}})
}

func proxyConfigPath() (string, error) {
	path := viper.ConfigFileUsed()
	if cfgFile != "" {
		path = cfgFile
	}
	if path == "" {
		return proxysettings.DefaultPath()
	}
	return filepath.Abs(path)
}
