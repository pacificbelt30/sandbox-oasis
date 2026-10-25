package cmd

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/pacificbelt30/codex-dock/pkg/cxd/localapi"
	"github.com/spf13/cobra"
)

func init() {
	api := &cobra.Command{Use: "api", Short: "Local versioned embedding protocol"}
	api.AddCommand(&cobra.Command{
		Use: "stdio", Short: "Serve local API v1 over private stdin/stdout pipes", Args: cobra.NoArgs,
		// The protocol uses only explicit Open options. Do not read user/project
		// config or print debug configuration into its machine-only stdout.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			if err := localapi.Serve(ctx, os.Stdin, os.Stdout, nil); err != nil && ctx.Err() == nil {
				return err
			}
			return nil
		},
	})
	api.AddCommand(&cobra.Command{Use: "schema", Short: "Print the local API v1 JSON Schema", Args: cobra.NoArgs,
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			data, err := localapi.Schema()
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	})
	rootCmd.AddCommand(api)
}
