package cmd

import (
	"fmt"

	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/spf13/cobra"
)

func init() {
	root := &cobra.Command{Use: "agent-state", Short: "Back up a stopped worker's private Codex database disk"}
	var output, input string
	resolve := func(cmd *cobra.Command, worker string) (string, error) {
		m, err := sandbox.NewManager(managementConfig())
		if err != nil {
			return "", err
		}
		defer func() { _ = m.Close() }()
		w, err := m.InspectContext(cmd.Context(), worker)
		if err != nil {
			return "", err
		}
		return w.ID, nil
	}
	export := &cobra.Command{Use: "export WORKER", Args: cobra.ExactArgs(1), Short: "Write a private offline backup (may contain sensitive session history)", RunE: func(cmd *cobra.Command, args []string) error {
		id, err := resolve(cmd, args[0])
		if err != nil {
			return err
		}
		if err = engine.ExportAgentState(cmd.Context(), engine.Socket(), id, output); err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Agent state backup saved:", output)
		return err
	}}
	export.Flags().StringVar(&output, "output", "", "New backup path; existing files are never overwritten")
	_ = export.MarkFlagRequired("output")
	restore := &cobra.Command{Use: "restore WORKER", Args: cobra.ExactArgs(1), Short: "Restore into a never-started worker with matching UID/GID and state disk size", RunE: func(cmd *cobra.Command, args []string) error {
		id, err := resolve(cmd, args[0])
		if err != nil {
			return err
		}
		if err = engine.RestoreAgentState(cmd.Context(), engine.Socket(), id, input); err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Agent state restored; start the worker explicitly:", id)
		return err
	}}
	restore.Flags().StringVar(&input, "input", "", "Offline backup file")
	_ = restore.MarkFlagRequired("input")
	root.AddCommand(export, restore)
	rootCmd.AddCommand(root)
}
