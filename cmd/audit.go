package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/auditlog"
	"github.com/spf13/cobra"
)

func init() {
	var directory string
	command := &cobra.Command{Use: "audit", Short: "Verify and export a local audit journal as JSON", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		report, err := (auditlog.Journal{Directory: directory}).Read()
		if err != nil {
			return err
		}
		if err = json.NewEncoder(cmd.OutOrStdout()).Encode(report); err != nil {
			return err
		}
		if len(report.Pending) > 0 {
			return fmt.Errorf("audit contains unfinished operations; reconcile their state")
		}
		return nil
	}}
	command.Flags().StringVar(&directory, "directory", "", "Owner-private audit directory (required)")
	_ = command.MarkFlagRequired("directory")
	rootCmd.AddCommand(command)
}
