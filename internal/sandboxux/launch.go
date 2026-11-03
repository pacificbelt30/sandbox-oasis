// Package sandboxux supplies host-side actions for the sandbox dashboard.
package sandboxux

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
	"github.com/pacificbelt30/codex-dock/internal/workspace"
)

type LaunchSpec struct {
	Name, Project, Image, Command, Profile string
	AI                                     bool
	Network                                string // offline, host, dependencies, or explicit allowlist
	Domains                                []string
	ReadOnly, ReadOnlyRoot                 *bool // nil preserves configured defaults
	Mounts                                 []string
	Workspace, CloneDir, CloneRevision     string
	Lifecycle                              workerpolicy.Lifecycle
	LifecycleSet                           bool
}
type LaunchResult struct{ ID, Name, Workspace string }
type Launcher interface {
	Launch(context.Context, LaunchSpec) (LaunchResult, error)
}
type CLILauncher struct {
	Executable   string
	GlobalArgs   []string
	PassphraseFD int
	Config       sandbox.ManagerConfig
}

var workerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,99}$`)

func PrepareLaunch(spec LaunchSpec) (LaunchSpec, []string, error) {
	if spec.Name == "" {
		var id [8]byte
		if _, err := rand.Read(id[:]); err != nil {
			return spec, nil, err
		}
		spec.Name = "oaiso-ui-" + hex.EncodeToString(id[:])
	}
	if !workerName.MatchString(spec.Name) || len(spec.Command) > 32768 || strings.ContainsRune(spec.Command, 0) || len(spec.Image) > 512 || len(spec.Profile) > 128 || len(spec.Mounts) > 8 {
		return spec, nil, fmt.Errorf("invalid sandbox launch settings")
	}
	p, err := filepath.Abs(spec.Project)
	if err != nil {
		return spec, nil, err
	}
	p, err = filepath.EvalSymlinks(p)
	if err != nil {
		return spec, nil, err
	}
	st, err := os.Stat(p)
	if err != nil || !st.IsDir() {
		return spec, nil, fmt.Errorf("project must be an existing host directory")
	}
	spec.Project = p
	if err := spec.Lifecycle.Validate(); err != nil {
		return spec, nil, err
	}
	if spec.Workspace != "" && spec.Workspace != "shared" && spec.Workspace != "copy" {
		return spec, nil, fmt.Errorf("choose shared workspace or independent copy")
	}
	if spec.Workspace == "copy" {
		if err := validateCopyDestination(p, spec.CloneDir); err != nil {
			return spec, nil, err
		}
		if spec.CloneRevision == "" {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			spec.CloneRevision, err = workspace.ReviewCommit(ctx, p)
			if err != nil {
				return spec, nil, err
			}
		}
	} else if spec.CloneDir != "" || spec.CloneRevision != "" {
		return spec, nil, fmt.Errorf("clone destination and revision require independent copy")
	}
	args := []string{"run", "--name", spec.Name, "--project", p, "--runtime", "kata", "--agent", "shell", "--detach", "--keep"}
	for _, v := range []struct {
		name  string
		value time.Duration
	}{{"--max-lifetime", spec.Lifecycle.MaxLifetime}, {"--idle-timeout", spec.Lifecycle.IdleTimeout}, {"--retention", spec.Lifecycle.Retention}} {
		// Explicit zero also prevents a saved environment inheriting a shorter host setting.
		if spec.LifecycleSet || spec.Lifecycle != (workerpolicy.Lifecycle{}) {
			args = append(args, v.name, v.value.String())
		}
	}
	if spec.LifecycleSet || spec.Lifecycle != (workerpolicy.Lifecycle{}) {
		args = append(args, fmt.Sprintf("--protect-main=%t", spec.Lifecycle.ProtectMain))
	}
	if !spec.AI {
		args = append(args, "--no-ai")
	}
	if spec.Image != "" {
		args = append(args, "--image", spec.Image)
	}
	if spec.Profile != "" {
		args = append(args, "--profile-size", spec.Profile)
	}
	for _, v := range []struct {
		name  string
		value *bool
	}{{"--read-only", spec.ReadOnly}, {"--read-only-rootfs", spec.ReadOnlyRoot}} {
		if v.value != nil {
			args = append(args, fmt.Sprintf("%s=%t", v.name, *v.value))
		}
	}
	for _, v := range spec.Mounts {
		if strings.TrimSpace(v) == "" || strings.ContainsAny(v, "\x00\r\n") {
			return spec, nil, fmt.Errorf("invalid additional mount")
		}
		args = append(args, "--mount", v)
	}
	switch spec.Network {
	case "offline":
		args = append(args, "--no-internet")
	case "host":
	case "dependencies":
		args = append(args, "--policy-preset", "dependencies")
	case "allowlist":
		if len(spec.Domains) == 0 {
			return spec, nil, fmt.Errorf("enter at least one allowed domain")
		}
		for _, d := range spec.Domains {
			if strings.TrimSpace(d) != d || d == "" || strings.ContainsAny(d, "\x00\r\n") {
				return spec, nil, fmt.Errorf("invalid allowed domain")
			}
			args = append(args, "--egress-allow-domain", d)
		}
	default:
		return spec, nil, fmt.Errorf("choose an egress mode")
	}
	args = append(args, "--headless", "--task-file", "-")
	return spec, args, nil
}

type boundedOutput struct{ bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 1<<20 {
		return 0, fmt.Errorf("launch output exceeds 1 MiB")
	}
	return b.Buffer.Write(p)
}
func (l *CLILauncher) Launch(ctx context.Context, spec LaunchSpec) (LaunchResult, error) {
	spec, args, err := PrepareLaunch(spec)
	result := LaunchResult{Name: spec.Name}
	if err != nil {
		return result, err
	}
	if spec.Workspace == "copy" {
		if err := workspace.CloneAt(ctx, spec.Project, spec.CloneDir, spec.CloneRevision); err != nil {
			return result, err
		}
		result.Workspace = spec.CloneDir
		// The ordinary launch command validates the copied project and mounts.
		for i := range args {
			if args[i] == "--project" {
				args[i+1] = spec.CloneDir
				break
			}
		}
	}
	globals := append([]string{}, l.GlobalArgs...)
	var passphrase *os.File
	if spec.AI && l.PassphraseFD >= 3 {
		fd, e := syscall.Dup(l.PassphraseFD)
		if e != nil {
			return result, e
		}
		passphrase = os.NewFile(uintptr(fd), "ui-passphrase")
		defer func() { _ = passphrase.Close() }()
		globals = append(globals, "--passphrase-fd", "3")
	}
	c := exec.CommandContext(ctx, l.Executable, append(globals, args...)...)
	c.Dir = spec.Project
	if result.Workspace != "" {
		c.Dir = result.Workspace
	}
	command := spec.Command
	if command == "" {
		// A detached interactive shell receives EOF. Keep the sandbox alive;
		// the dashboard opens a separate verified exec shell when connecting.
		command = "exec sleep infinity"
	}
	c.Stdin = strings.NewReader(command)
	if passphrase != nil {
		c.ExtraFiles = []*os.File{passphrase}
	}
	out := &boundedOutput{}
	c.Stdout = out
	c.Stderr = out
	if err = c.Run(); err != nil {
		return result, fmt.Errorf("launch %s: %w: %s; inspect this name before retrying; retained copy: %s", spec.Name, err, strings.TrimSpace(out.String()), result.Workspace)
	}
	socket := l.Config.Socket
	if socket == "" {
		socket = engine.Socket()
	}
	client, err := engine.NewClientAt(socket)
	if err != nil {
		return result, err
	}
	defer func() { _ = client.Close() }()
	v, err := client.ContainerInspect(ctx, spec.Name)
	if err != nil {
		return result, err
	}
	if v.Config == nil || v.Config.Labels["codex-dock.managed"] != "true" {
		return result, fmt.Errorf("launch response did not identify a managed sandbox")
	}
	result.ID = v.ID
	return result, nil
}

// LimitedWriter bounds data received from a guest before it reaches the UI.
type LimitedWriter struct {
	Writer    io.Writer
	Remaining int64
	Exceeded  bool
}

func (w *LimitedWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.Remaining {
		w.Exceeded = true
		n, err := w.Writer.Write(p[:int(w.Remaining)])
		w.Remaining -= int64(n)
		if err != nil {
			return n, err
		}
		return n, fmt.Errorf("guest output exceeds display limit")
	}
	n, e := w.Writer.Write(p)
	w.Remaining -= int64(n)
	return n, e
}
