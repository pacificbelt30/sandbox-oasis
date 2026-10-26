package instanceux

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

type AccessManager interface {
	InspectContext(context.Context, string) (sandbox.Worker, error)
	StartContext(context.Context, string) error
	ExecWithOptions(context.Context, string, sandbox.ExecOptions) (int64, error)
}

type ConnectOptions struct {
	Shell       string
	WorkingDir  string
	Environment []string
	Start       bool
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
}

func Connect(ctx context.Context, manager AccessManager, reference string, options ConnectOptions) (int64, error) {
	if strings.TrimSpace(reference) == "" {
		return 0, fmt.Errorf("worker name is required")
	}
	worker, err := manager.InspectContext(ctx, reference)
	if err != nil {
		return 0, err
	}
	if worker.Status != "running" {
		if !options.Start {
			return 0, fmt.Errorf("worker %q is %s; pass --start to start it before connecting", worker.Name, worker.Status)
		}
		if err := manager.StartContext(ctx, worker.ID); err != nil {
			return 0, fmt.Errorf("starting worker %q: %w", worker.Name, err)
		}
	}
	shell, err := ShellArgv(options.Shell)
	if err != nil {
		return 0, err
	}
	workingDir := options.WorkingDir
	if workingDir == "" {
		workingDir = "/workspace"
	}
	if err := ValidateEnvironment(options.Environment); err != nil {
		return 0, err
	}
	return manager.ExecWithOptions(ctx, worker.ID, sandbox.ExecOptions{
		Argv: shell, Stdin: options.Stdin, Stdout: options.Stdout, Stderr: options.Stderr,
		WorkingDir: workingDir, Env: append([]string(nil), options.Environment...), TTY: true,
	})
}

func ShellArgv(shell string) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "", "auto":
		return []string{"/bin/sh", "-l"}, nil
	case "bash":
		return []string{"/bin/bash", "-l"}, nil
	case "zsh":
		return []string{"/bin/zsh", "-l"}, nil
	case "sh":
		return []string{"/bin/sh", "-l"}, nil
	default:
		return nil, fmt.Errorf("unsupported connection shell %q; use auto, bash, zsh, or sh", shell)
	}
}

func ValidateEnvironment(environment []string) error {
	for _, entry := range environment {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return fmt.Errorf("invalid guest environment %q; expected KEY=value", entry)
		}
		for index, r := range key {
			valid := r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || index > 0 && r >= '0' && r <= '9'
			if !valid {
				return fmt.Errorf("invalid guest environment key %q", key)
			}
		}
	}
	return nil
}
