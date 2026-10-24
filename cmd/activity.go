package cmd

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/spf13/cobra"
)

func init() {
	var ttl time.Duration
	c := &cobra.Command{Use: "activity WORKER", Short: "Renew a bounded idle-protection lease; does not extend maximum lifetime", Args: cobra.ExactArgs(1)}
	c.Flags().DurationVar(&ttl, "ttl", time.Minute, "Lease duration (0s releases it; whole seconds, at most 5m)")
	c.RunE = func(cmd *cobra.Command, args []string) error {
		if ttl < 0 || ttl > 5*time.Minute || ttl%time.Second != 0 {
			return fmt.Errorf("activity TTL must be whole seconds between 0s and 5m")
		}
		m, err := sandbox.NewManager(managementConfig())
		if err != nil {
			return err
		}
		defer func() { _ = m.Close() }()
		until, err := m.Heartbeat(cmd.Context(), args[0], ttl)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"worker": args[0], "active_until": until})
	}
	rootCmd.AddCommand(c)
}
