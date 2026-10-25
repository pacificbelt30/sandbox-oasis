package cmd

import (
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/spf13/cobra"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func init() {
	var lifetime time.Duration
	c := &cobra.Command{Use: "preview WORKER PORT", Short: "Open an authenticated localhost preview of a guest HTTP/WebSocket app", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		port, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid guest port")
		}
		m, err := sandbox.NewManager(managementConfig())
		if err != nil {
			return err
		}
		defer func() { _ = m.Close() }()
		p, err := m.Preview(ctx, args[0], port, lifetime)
		if err != nil {
			return err
		}
		defer func() { _ = p.Close() }()
		if _, err = fmt.Fprintln(cmd.OutOrStdout(), p.URL); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
		case <-p.Done():
		}
		return nil
	}}
	c.Flags().DurationVar(&lifetime, "lifetime", 30*time.Minute, "Preview lifetime, at most 1h; Ctrl-C closes all connections")
	rootCmd.AddCommand(c)
}
