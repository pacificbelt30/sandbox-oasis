package sandboxux

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

type ChangeExecutor interface {
	ExecWithOptions(context.Context, string, sandbox.ExecOptions) (int64, error)
}

// Git runs only inside the VM. Never resolve guest-controlled .git pointers or
// execute its Git configuration using host Git during result inspection.
func WorkspaceChanges(ctx context.Context, guest ChangeExecutor, id string) (string, error) {
	var out, stderr bytes.Buffer
	w := &LimitedWriter{Writer: &out, Remaining: 256 << 10}
	e := &LimitedWriter{Writer: &stderr, Remaining: 64 << 10}
	for _, args := range [][]string{{"status", "--short", "--untracked-files=normal"}, {"diff", "--no-ext-diff", "--no-textconv", "HEAD", "--"}} {
		_, _ = io.WriteString(w, "\n--- "+args[0]+" ---\n")
		argv := append([]string{"git", "--no-pager", "-c", "core.fsmonitor=false", "-c", "color.ui=false"}, args...)
		code, err := guest.ExecWithOptions(ctx, id, sandbox.ExecOptions{Argv: argv, WorkingDir: "/workspace", Stdout: w, Stderr: e})
		if err != nil {
			return "", err
		}
		if code != 0 {
			return "", fmt.Errorf("guest Git %s failed (exit %d); workspace needs a Git commit", args[0], code)
		}
	}
	return out.String(), nil
}
