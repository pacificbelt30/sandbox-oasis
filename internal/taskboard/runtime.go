package taskboard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/containerd/errdefs"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

// CLIRuntime routes launches through the normal CLI so image/project defaults,
// host agent policies, credential setup and Kata admission remain identical.
type CLIRuntime struct {
	PassphraseFD int
	Executable   string
	GlobalArgs   []string
	Store        Store
	Config       sandbox.ManagerConfig
}

func (r *CLIRuntime) Launch(ctx context.Context, t Task, a Attempt, test bool) (string, error) {
	prompt := t.Spec.Prompt
	agent := t.Spec.Agent
	name := a.Name
	if test {
		prompt = t.Spec.TestCommand
		agent = "shell"
		name = a.TestName
	}
	dir := filepath.Join(r.Store.Root, "prompts")
	if e := privateDirectory(dir); e != nil {
		return "", e
	}
	f, e := os.CreateTemp(dir, ".launch-")
	if e != nil {
		return "", e
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, e = f.WriteString(prompt); e != nil {
		return "", e
	}
	if e = f.Close(); e != nil {
		return "", e
	}
	args := append([]string{}, r.GlobalArgs...)
	var passphrase *os.File
	if r.PassphraseFD >= 3 {
		fd, err := syscall.Dup(r.PassphraseFD)
		if err != nil {
			return "", fmt.Errorf("duplicate passphrase descriptor: %w", err)
		}
		passphrase = os.NewFile(uintptr(fd), "task-passphrase")
		defer func() { _ = passphrase.Close() }()
		args = append(args, "--passphrase-fd", "3")
	}
	args = append(args, "run", "--name", name, "--project", a.Workspace, "--agent", agent, "--task-file", f.Name(), "--headless", "--detach", "--keep")
	if t.Spec.Image != "" {
		args = append(args, "--image", t.Spec.Image)
	}
	if t.Spec.Policy != "" {
		args = append(args, "--agent-policy", t.Spec.Policy)
	}
	if t.Spec.NoInternet {
		args = append(args, "--no-internet")
	}
	c := exec.CommandContext(ctx, r.Executable, args...)
	c.Dir = a.Workspace
	if passphrase != nil {
		c.ExtraFiles = []*os.File{passphrase}
	}
	output := &limitedBuffer{max: 1 << 20}
	c.Stdout = output
	c.Stderr = output
	if e = c.Run(); e != nil {
		return "", fmt.Errorf("task launch: %w: %s", e, strings.TrimSpace(output.String()))
	}
	state, e := r.Inspect(ctx, name)
	if e != nil {
		return "", e
	}
	if !state.Found || state.Workspace != a.Workspace {
		return "", fmt.Errorf("launch response has no matching worker; reconcile task before retrying")
	}
	return state.ID, nil
}
func (r *CLIRuntime) Inspect(ctx context.Context, name string) (RuntimeState, error) {
	socket := r.Config.Socket
	if socket == "" {
		socket = engine.Socket()
	}
	c, e := engine.NewClientAt(socket)
	if e != nil {
		return RuntimeState{}, e
	}
	defer func() { _ = c.Close() }()
	v, e := c.ContainerInspect(ctx, name)
	if errdefs.IsNotFound(e) {
		return RuntimeState{}, nil
	}
	if e != nil {
		return RuntimeState{}, e
	}
	if v.Config == nil || v.HostConfig == nil || v.State == nil || v.Config.Labels["codex-dock.managed"] != "true" {
		return RuntimeState{}, fmt.Errorf("task requires a managed worker")
	}
	st := RuntimeState{Found: true, Running: v.State.Running, ID: v.ID, ExitCode: v.State.ExitCode, Image: v.Config.Image}
	for _, m := range v.HostConfig.Mounts {
		if m.Target == "/workspace" {
			st.Workspace = m.Source
		}
	}
	return st, nil
}
func (r *CLIRuntime) Stop(ctx context.Context, id string) error {
	m, e := sandbox.NewManager(r.Config)
	if e != nil {
		return e
	}
	defer func() { _ = m.Close() }()
	return m.StopContext(ctx, id, 10)
}
