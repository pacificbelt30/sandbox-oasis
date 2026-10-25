package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var runUsage identity.UsagePolicy

func bindUsageFlags(command *cobra.Command) {
	f := command.Flags()
	f.Int64Var(&runUsage.MaxRequests, "max-requests", 0, "Maximum AI/broker requests accepted for this execution (0 unlimited)")
	f.Int64Var(&runUsage.MaxConcurrent, "max-concurrent", 0, "Maximum concurrent AI/broker requests (0 unlimited)")
	f.Int64Var(&runUsage.MaxBytes, "max-transfer-bytes", 0, "Maximum combined request/response body bytes at the auth proxy (0 unlimited)")
	f.Int64Var(&runUsage.MaxTokens, "max-observed-tokens", 0, "Stop new AI requests after observed token usage reaches this value; not a prepaid billing cap")
}

func applyUsageRunDefaults(command *cobra.Command) error {
	for _, setting := range []struct {
		flag, key string
		target    *int64
	}{
		{"max-requests", "run.max_requests", &runUsage.MaxRequests},
		{"max-concurrent", "run.max_concurrent", &runUsage.MaxConcurrent},
		{"max-transfer-bytes", "run.max_transfer_bytes", &runUsage.MaxBytes},
		{"max-observed-tokens", "run.max_observed_tokens", &runUsage.MaxTokens},
	} {
		if !command.Flags().Changed(setting.flag) {
			*setting.target = viper.GetInt64(setting.key)
		}
	}
	runOpts.TokenUsagePolicy = nil
	if runUsage != (identity.UsagePolicy{}) {
		runOpts.TokenUsagePolicy = runUsage.Clone()
	}
	return runOpts.TokenUsagePolicy.Validate()
}

func init() {
	var asJSON bool
	c := &cobra.Command{Use: "usage WORKER", Short: "Show execution usage counters without credentials", Args: cobra.ExactArgs(1)}
	c.Flags().BoolVar(&asJSON, "json", false, "Print structured usage counters")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		m, err := sandbox.NewManager(managementConfig())
		if err != nil {
			return err
		}
		defer func() { _ = m.Close() }()
		s, err := m.Usage(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(s)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Requests: %d\nActive: %d\nBody bytes: %d\nObserved tokens: %d\nRequests without complete token usage: %d\nUsage indeterminate: %t\nLimits: requests=%d concurrent=%d bytes=%d observed-tokens=%d\n", s.Requests, s.Active, s.Bytes, s.Tokens, s.UnmeteredRequests, s.Indeterminate, s.Policy.MaxRequests, s.Policy.MaxConcurrent, s.Policy.MaxBytes, s.Policy.MaxTokens)
		return err
	}
	rootCmd.AddCommand(c)
}
