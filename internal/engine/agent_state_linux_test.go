//go:build linux

package engine

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
)

func stateDiskFixture(t *testing.T) (*Backend, *record, *specs.Spec) {
	t.Helper()
	formatter, err := exec.LookPath("mke2fs")
	if err != nil {
		t.Fatal("state disk verification requires e2fsprogs", err)
	}
	b := &Backend{opts: Options{State: t.TempDir(), Tools: map[string]string{"mkfs": formatter}}, volumeRoot: t.TempDir()}
	for _, dir := range []string{b.opts.State, b.volumeRoot} {
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	r := &record{ID: strings.Repeat("a", 64), Config: container.Config{Labels: map[string]string{"codex-dock.managed": "true", workerpolicy.AgentStateLabel: "64MiB"}}, Host: container.HostConfig{Runtime: "io.containerd.kata.v2"}}
	s := &specs.Spec{Process: &specs.Process{User: specs.User{UID: uint32(os.Getuid()), GID: uint32(os.Getgid())}, Env: []string{"CODEX_SQLITE_HOME=/explicit"}}}
	return b, r, s
}

func TestAgentStateProvisionReserveRestartAndDelete(t *testing.T) {
	b, r, s := stateDiskFixture(t)
	if err := b.provisionAgentState(context.Background(), r, s); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(b.agentStateImage(r))
	if err != nil {
		t.Fatal(err)
	}
	if !r.AgentState.Ready || info.Size() != 64<<20 || info.Sys().(*syscall.Stat_t).Blocks*512 < info.Size() {
		t.Fatal("formatting discarded reserved host capacity", info, r.AgentState)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("image is not private")
	}
	before, err := os.ReadFile(b.agentStateImage(r))
	if err != nil {
		t.Fatal(err)
	}
	b.opts.Tools["mkfs"] = "/must/not/run/on/restart"
	if err := b.removeAgentStateMetadata(r); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(b.agentStateMetadata(r), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.agentStateMetadata(r), ".mount-interrupted"), []byte(`{"volume-type":`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := b.ensureAgentState(r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(b.agentStateMetadata(r), ".mount-interrupted")); !os.IsNotExist(err) {
		t.Fatal("interrupted metadata write not reclaimed", err)
	}
	after, err := os.ReadFile(b.agentStateImage(r))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("restart changed existing image", err)
	}
	if !strings.Contains(strings.Join(s.Process.Env, "\n"), "CODEX_SQLITE_HOME=/explicit") {
		t.Fatal("explicit upstream default overwritten")
	}
	if err := b.removeAgentStateMetadata(r); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(b.agentStateMetadata(r)); !os.IsNotExist(err) {
		t.Fatal("runtime metadata retained", err)
	}
	if _, err := os.Stat(b.agentStateImage(r)); err != nil {
		t.Fatal("metadata cleanup deleted durable data", err)
	}
}

func TestAgentStateRejectsReplacementAndMetadataSymlink(t *testing.T) {
	b, r, s := stateDiskFixture(t)
	if err := b.provisionAgentState(context.Background(), r, s); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(b.agentStateMetadata(r), "mountInfo.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(t.TempDir(), "foreign")
	if err := os.WriteFile(foreign, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, path); err != nil {
		t.Fatal(err)
	}
	if err := b.ensureAgentState(r); err == nil {
		t.Fatal("followed metadata symlink")
	}
	if err := b.removeAgentStateMetadata(r); err == nil {
		t.Fatal("cleaned up unowned metadata")
	}
	if got, err := os.ReadFile(foreign); err != nil || !bytes.Equal(got, data) {
		t.Fatal("changed symlink target")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := b.ensureAgentState(r); err != nil {
		t.Fatal(err)
	}
	image := b.agentStateImage(r)
	if err := os.Rename(image, image+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(image, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := b.ensureAgentState(r); err == nil {
		t.Fatal("replaced or reformatted a different image")
	}
	if got, _ := os.ReadFile(image); string(got) != "foreign" {
		t.Fatal("overwrote replacement")
	}
}

func TestAgentStateOptOutConflictAndFailedFormat(t *testing.T) {
	for _, kind := range []string{"off", "readonly", "auth", "runc", "conflict", "failed-format"} {
		t.Run(kind, func(t *testing.T) {
			b, r, s := stateDiskFixture(t)
			switch kind {
			case "off":
				r.Config.Labels[workerpolicy.AgentStateLabel] = "off"
			case "readonly":
				r.Host.ReadonlyRootfs = true
			case "auth":
				r.Config.Labels["codex-dock.authentication-session"] = "true"
			case "runc":
				r.Host.Runtime = "runc"
			case "conflict":
				s.Mounts = []specs.Mount{{Destination: "/var/lib"}}
			case "failed-format":
				b.opts.Tools["mkfs"] = "/bin/false"
			}
			err := b.provisionAgentState(context.Background(), r, s)
			if kind == "conflict" || kind == "failed-format" {
				if err == nil {
					t.Fatal("unsafe/incomplete provisioning succeeded")
				}
				if kind == "failed-format" && b.ensureAgentState(r) == nil {
					t.Fatal("incomplete disk accepted on restart")
				}
			} else if err != nil || r.AgentState != nil {
				t.Fatal("changed an excluded workload", err)
			}
		})
	}
}
