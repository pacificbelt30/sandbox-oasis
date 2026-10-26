package sandbox

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/pacificbelt30/codex-dock/internal/launchtiming"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
)

// Exec runs argv in an existing managed worker. It returns the guest exit code
// separately from transport errors so callers can preserve script semantics.
// Closing ctx closes the stream; the node cancels and kills the exec process.
func (m *Manager) Exec(ctx context.Context, name string, argv []string, stdin io.Reader, stdout, stderr io.Writer) (int64, error) {
	return m.ExecWithOptions(ctx, name, ExecOptions{Argv: argv, Stdin: stdin, Stdout: stdout, Stderr: stderr})
}

type ExecOptions struct {
	OnStart        func(string)
	Argv           []string
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	WorkingDir     string
	Env            []string
	TTY            bool
}

func (m *Manager) ExecWithOptions(ctx context.Context, name string, opts ExecOptions) (int64, error) {
	argv, stdin, stdout, stderr := opts.Argv, opts.Stdin, opts.Stdout, opts.Stderr
	if opts.WorkingDir == "" {
		opts.WorkingDir = "/workspace"
	}
	if !path.IsAbs(opts.WorkingDir) || path.Clean(opts.WorkingDir) != opts.WorkingDir {
		return 0, fmt.Errorf("exec working directory must be a clean absolute guest path")
	}
	if err := validateExecEnv(opts.Env); err != nil {
		return 0, err
	}
	if len(argv) == 0 {
		return 0, fmt.Errorf("exec requires a command")
	}
	launchtiming.Next(ctx, "exec_verification")
	id, err := m.verifyExecWorker(ctx, name)
	if err != nil {
		return 0, err
	}
	launchtiming.Next(ctx, "exec_create")
	created, err := m.cli.ContainerExecCreate(ctx, id, container.ExecOptions{Cmd: argv, AttachStdin: stdin != nil, AttachStdout: true, AttachStderr: true, WorkingDir: opts.WorkingDir, Env: opts.Env, Tty: opts.TTY})
	if err != nil {
		return 0, err
	}
	launchtiming.Next(ctx, "exec_attach")
	stream, err := m.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{Tty: opts.TTY})
	if err != nil {
		return 0, err
	}
	if opts.OnStart != nil {
		opts.OnStart(created.ID)
	}
	defer stream.Close()
	completed := false
	defer func() {
		if !completed {
			m.cancelExecution(created.ID)
		}
	}()
	localTTY, err := openLocalTerminal(stdin, opts.TTY, func(width, height uint) error {
		return m.cli.ContainerExecResize(ctx, created.ID, container.ResizeOptions{Width: width, Height: height})
	})
	if err != nil {
		return 0, err
	}
	var terminalInputDone chan struct{}
	if localTTY != nil {
		terminalInputDone = make(chan struct{})
		defer func() {
			_ = localTTY.Close()
			stream.Close()
			<-terminalInputDone
		}()
	}

	stop := context.AfterFunc(ctx, stream.Close)
	defer stop()
	if stdin != nil {
		go func() {
			if terminalInputDone != nil {
				defer close(terminalInputDone)
				_ = localTTY.copyTo(ctx, stream.Conn)
			} else {
				_, _ = io.Copy(stream.Conn, stdin)
			}
			_ = stream.CloseWrite()
		}()
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	launchtiming.Next(ctx, "exec_io")
	var copyErr error
	if opts.TTY {
		_, copyErr = io.Copy(stdout, stream.Reader)
	} else {
		_, copyErr = stdcopy.StdCopy(stdout, stderr, stream.Reader)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if copyErr != nil {
		return 0, copyErr
	}
	launchtiming.Next(ctx, "exec_result")
	status, err := m.cli.ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return 0, err
	}
	if status.Running {
		return 0, fmt.Errorf("exec stream ended before process exit")
	}
	completed = true
	return int64(status.ExitCode), nil
}

// WaitContext waits for a managed worker to stop and returns its exit code.
func (m *Manager) WaitContext(ctx context.Context, name string) (int64, error) {
	id, err := m.resolveID(name)
	if err != nil {
		return 0, err
	}
	statusCh, errCh := m.cli.ContainerWait(ctx, id, container.WaitConditionNotRunning)
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case err, ok := <-errCh:
			if ok && err != nil {
				return 0, err
			}
			errCh = nil
		case status, ok := <-statusCh:
			if !ok {
				return 0, fmt.Errorf("worker wait stream closed")
			}
			if status.Error != nil {
				return 0, fmt.Errorf("waiting for worker: %s", status.Error.Message)
			}
			v, err := m.cli.ContainerInspect(ctx, id)
			if err != nil {
				return status.StatusCode, err
			}
			return status.StatusCode, m.revokeWorkerTokenChecked(v)
		}
	}
}

func (m *Manager) verifyExecWorker(ctx context.Context, name string) (string, error) {
	launchtiming.Next(ctx, "exec_lookup")
	id, err := m.resolveID(name)
	if err != nil {
		return "", err
	}
	v, err := m.cli.ContainerInspect(ctx, id)
	if err != nil {
		return "", err
	}
	if v.State == nil || !v.State.Running {
		return "", fmt.Errorf("worker %s is not running", name)
	}
	if v.Config == nil || v.Config.Labels[labelManaged] != "true" {
		return "", fmt.Errorf("worker %s is not managed", name)
	}
	if v.Config.Labels[workerpolicy.QuarantinedLabel] == "true" {
		return "", fmt.Errorf("worker %s is quarantined", name)
	}
	if v.Config.Labels[labelIsolation] == RuntimeKata {
		launchtiming.Next(ctx, "exec_runtime")
		_, registration, err := m.checkRuntimeMetadata(ctx, RunOptions{Runtime: RuntimeKata, KataRuntime: v.Config.Labels[labelRuntime], ResourceProfile: v.Config.Labels[labelPrefix+"resource-profile"]})
		if err != nil {
			return "", err
		}
		launchtiming.Next(ctx, "exec_host")
		if err := m.verifyKataGuestWithRuntime(ctx, id, false, registration); err != nil {
			return "", err
		}
	}
	return id, nil
}

// A stdin half-close is valid EOF, so TCP alone cannot reliably communicate
// cancellation. Send a separate bounded kill after closing the data stream.
func (m *Manager) cancelExecution(eid string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		state, err := m.cli.ContainerExecInspect(ctx, eid)
		if err != nil || !state.Running {
			return
		}
		if m.jobRequest(ctx, "POST", "/exec/"+url.PathEscape(eid)+"/kill", nil) == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func validateExecEnv(env []string) error {
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || key == "" || strings.ContainsRune(entry, '\x00') {
			return fmt.Errorf("exec environment entries must be KEY=value")
		}
	}
	return nil
}
