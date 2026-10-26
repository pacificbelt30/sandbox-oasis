package cmd

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/pacificbelt30/codex-dock/internal/instanceux"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/spf13/cobra"
)

var connectShell string
var connectWorkdir string
var connectEnvironment []string
var connectStart bool

var connectCmd = &cobra.Command{
	Use:     "connect [flags] NAME",
	Aliases: []string{"attach-shell"},
	Short:   "Open an interactive shell in an existing worker",
	Long: `Open a new login shell in an existing managed worker.

This does not replace or attach to the worker's primary agent process. The shell
runs as a separate exec session, so exiting returns to the host (or to the TUI)
while the worker and its agent continue running.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manager, err := sandbox.NewManager(managementConfig())
		if err != nil {
			return err
		}
		defer func() { _ = manager.Close() }()
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		code, err := instanceux.Connect(ctx, manager, args[0], instanceux.ConnectOptions{
			Shell:       connectShell,
			WorkingDir:  connectWorkdir,
			Environment: connectEnvironment,
			Start:       connectStart,
			Stdin:       cmd.InOrStdin(),
			Stdout:      cmd.OutOrStdout(),
			Stderr:      cmd.ErrOrStderr(),
		})
		if errors.Is(err, context.Canceled) {
			return workerExitError{130}
		}
		if err != nil {
			return err
		}
		if code != 0 {
			return workerExitError{int(code)}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(connectCmd)
	f := connectCmd.Flags()
	f.StringVar(&connectShell, "shell", "auto", "Guest login shell: auto, bash, zsh, or sh")
	f.StringVarP(&connectWorkdir, "workdir", "w", "/workspace", "Guest working directory")
	f.StringArrayVarP(&connectEnvironment, "env", "e", nil, "Guest environment KEY=value (repeatable)")
	f.BoolVar(&connectStart, "start", false, "Start a stopped worker before connecting")
	registerCompletion(connectCmd, "shell", cobra.FixedCompletions([]cobra.Completion{"auto", "bash", "zsh", "sh"}, cobra.ShellCompDirectiveNoFileComp))
}
