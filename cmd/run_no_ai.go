package cmd

import (
	"fmt"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/spf13/cobra"
)

func noAIEnabled(cmd *cobra.Command) bool {
	value, _ := cmd.Flags().GetBool("no-ai")
	return value
}

func applyNoAIRun(cmd *cobra.Command) error {
	if !noAIEnabled(cmd) {
		return nil
	}
	if runOpts.Agent != sandbox.AgentNone || runOpts.DirectAgent || runOpts.Task != "" {
		return fmt.Errorf("--no-ai requires a shell or command; choose --agent shell and omit agent task options")
	}
	for _, name := range []string{"max-requests", "max-concurrent", "max-transfer-bytes", "max-observed-tokens"} {
		if cmd.Flags().Changed(name) {
			return fmt.Errorf("--%s requires AI proxy access and cannot combine with --no-ai", name)
		}
	}
	// AI request budgets have no recipient without an auth proxy. Host/guest
	// runtime, filesystem and general egress policy still apply normally.
	runOpts.TokenUsagePolicy = nil
	runOpts.TokenModelPolicy = nil
	if !cmd.Flags().Changed("agent-state-size") {
		runOpts.AgentStateSize = "off"
	}
	return nil
}
