package cmd

import (
	"encoding/json"
	"github.com/pacificbelt30/codex-dock/internal/egresscontrol"

	"github.com/spf13/cobra"
)

func init() {
	c := &cobra.Command{Use: "policy-control", Hidden: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		apply, err := cmd.Flags().GetBool("apply")
		if err != nil {
			return err
		}
		var update *egresscontrol.Update
		if apply {
			value, err := egresscontrol.ReadUpdate(cmd.InOrStdin())
			if err != nil {
				return err
			}
			update = &value
		}
		state, err := egresscontrol.Call(cmd.Context(), egresscontrol.Directory, update)
		if encodeErr := json.NewEncoder(cmd.OutOrStdout()).Encode(state); encodeErr != nil {
			return encodeErr
		}
		return err
	}}
	c.Flags().Bool("apply", false, "Apply a compare-and-swap policy update from stdin")
	proxyCmd.AddCommand(c)
}
