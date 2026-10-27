//go:build linux

package engine

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
	"golang.org/x/sys/unix"
)

const agentStateTarget = workerpolicy.AgentStateTarget
const directVolumeRoot = "/run/kata-containers/shared/direct-volumes"

type agentStateDisk struct {
	Version       int
	Bytes         int64
	UID, GID      uint32
	Device, Inode uint64
	Ready         bool
}

type directVolumeInfo struct {
	Type    string   `json:"volume-type"`
	Device  string   `json:"device"`
	FSType  string   `json:"fstype"`
	Options []string `json:"options"`
}

func (b *Backend) agentStateSource(r *record) string {
	return filepath.Join(b.path(r.ID), "agent-state")
}
func (b *Backend) agentStateImage(r *record) string {
	return filepath.Join(b.path(r.ID), "agent-state.ext4")
}
func (b *Backend) agentStateMetadata(r *record) string {
	return filepath.Join(b.stateVolumeRoot(), base64.URLEncoding.EncodeToString([]byte(b.agentStateSource(r))))
}

func (b *Backend) stateVolumeRoot() string {
	if b.volumeRoot != "" {
		return b.volumeRoot
	}
	return directVolumeRoot
}
func (b *Backend) stateMountInfo(r *record) directVolumeInfo {
	return directVolumeInfo{Type: "directvol", Device: b.agentStateImage(r), FSType: "ext4", Options: []string{"rw", "nosuid", "nodev", "noexec", "nodiscard", "errors=remount-ro"}}
}

func stateMetadataFile(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 || info.Mode().Perm() != 0600 || info.Size() > 4096 {
		return nil, fmt.Errorf("untrusted agent state metadata")
	}
	return io.ReadAll(io.LimitReader(f, 4097))
}

func syncStateDirectory(path string) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func stateDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || !ok || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("untrusted direct-volume metadata directory: %s", path)
	}
	return nil
}

func (b *Backend) cleanupAgentStateTemps(r *record) error {
	entries, err := os.ReadDir(b.agentStateMetadata(r))
	if err != nil {
		return err
	}
	expected, _ := json.Marshal(b.stateMountInfo(r))
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), ".mount-") {
			continue
		}
		path := filepath.Join(b.agentStateMetadata(r), entry.Name())
		data, err := stateMetadataFile(path)
		if err != nil {
			return err
		}
		if !bytes.HasPrefix(expected, data) {
			return fmt.Errorf("unrecognized temporary agent state metadata; cleanup retained")
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func (b *Backend) provisionAgentState(ctx context.Context, r *record, spec *specs.Spec) error {
	size, err := workerpolicy.AgentStateBytes(r.Config.Labels[workerpolicy.AgentStateLabel])
	if err != nil {
		return err
	}
	if size == 0 {
		return nil
	}
	if b.opts.Tools["mkfs"] == "" && r.Config.Labels[workerpolicy.AgentStateLabel] != "" {
		return fmt.Errorf("agent state disk requires an updated, resealed node with mke2fs")
	}
	if r.Config.Labels["codex-dock.managed"] != "true" || r.Host.Runtime != "io.containerd.kata.v2" || r.Host.ReadonlyRootfs || r.Config.Labels["codex-dock.authentication-session"] == "true" || b.opts.Tools["mkfs"] == "" {
		return nil
	}
	for _, m := range spec.Mounts {
		if workerpolicy.PathsOverlap(m.Destination, agentStateTarget) {
			return fmt.Errorf("mount %q conflicts with private agent state", m.Destination)
		}
	}
	r.AgentState = &agentStateDisk{Version: 1, Bytes: size, UID: spec.Process.User.UID, GID: spec.Process.User.GID}
	if err := b.save(r); err != nil {
		return err
	}
	image := b.agentStateImage(r)
	file, err := os.OpenFile(image, os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return fmt.Errorf("creating agent state image: %w", err)
	}
	defer func() { _ = file.Close() }()
	// Reserve host blocks before formatting; avoid sparse-image oversubscription.
	if err := unix.Fallocate(int(file.Fd()), 0, 0, r.AgentState.Bytes); err != nil {
		return fmt.Errorf("reserving agent state disk (%d bytes): %w", r.AgentState.Bytes, err)
	}
	command := exec.CommandContext(ctx, b.opts.Tools["mkfs"], "-q", "-t", "ext4", "-F", "-m", "0", "-E", fmt.Sprintf("nodiscard,root_owner=%d:%d,lazy_itable_init=0,lazy_journal_init=0", r.AgentState.UID, r.AgentState.GID), image)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("formatting new agent state disk: %w: %s", err, output)
	}
	// Even with nodiscard, formatter zeroing can punch holes in regular files.
	// Re-reserve after formatting without changing any filesystem contents.
	if err := unix.Fallocate(int(file.Fd()), 0, 0, r.AgentState.Bytes); err != nil {
		return fmt.Errorf("reserving formatted agent state disk: %w", err)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("agent state disk identity unavailable")
	}
	r.AgentState.Device, r.AgentState.Inode, r.AgentState.Ready = uint64(stat.Dev), stat.Ino, true
	if err := os.Mkdir(b.agentStateSource(r), 0700); err != nil {
		return err
	}
	spec.Mounts = append(spec.Mounts, specs.Mount{Source: b.agentStateSource(r), Destination: agentStateTarget, Type: "bind", Options: []string{"rbind", "rprivate", "rw"}})
	if err := b.ensureAgentState(r); err != nil {
		return err
	}
	if !slices.ContainsFunc(spec.Process.Env, func(v string) bool { return strings.HasPrefix(v, "CODEX_SQLITE_HOME=") }) {
		value := "CODEX_SQLITE_HOME=" + agentStateTarget + "/codex"
		spec.Process.Env = append(spec.Process.Env, value)
		r.Config.Env = append(r.Config.Env, value)
	}
	spec.Process.Env = slices.DeleteFunc(spec.Process.Env, func(v string) bool { return strings.HasPrefix(v, "OAISO_AGENT_STATE_DISK=") })
	r.Config.Env = slices.DeleteFunc(r.Config.Env, func(v string) bool { return strings.HasPrefix(v, "OAISO_AGENT_STATE_DISK=") })
	spec.Process.Env = append(spec.Process.Env, "OAISO_AGENT_STATE_DISK=1")
	r.Config.Env = append(r.Config.Env, "OAISO_AGENT_STATE_DISK=1")
	return nil
}

// Restart only validates the existing disk and regenerates runtime metadata.
// It never reformats or parses a guest-controlled filesystem on the host.
func (b *Backend) ensureAgentState(r *record) error {
	if r.AgentState == nil {
		return nil
	}
	s := r.AgentState
	info, err := os.Lstat(b.agentStateImage(r))
	if err != nil {
		return fmt.Errorf("agent state image unavailable (not reformatted): %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if s.Version != 1 || !s.Ready || !info.Mode().IsRegular() || info.Size() != s.Bytes || !ok || uint64(stat.Dev) != s.Device || stat.Ino != s.Inode || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) || info.Mode().Perm() != 0600 {
		return fmt.Errorf("agent state image identity, readiness or permissions changed; refusing to reformat")
	}
	for _, path := range []string{b.agentStateSource(r), b.stateVolumeRoot(), b.agentStateMetadata(r)} {
		if err := stateDirectory(path); err != nil {
			return err
		}
	}
	data, _ := json.Marshal(b.stateMountInfo(r))
	path := filepath.Join(b.agentStateMetadata(r), "mountInfo.json")
	if existing, err := stateMetadataFile(path); err == nil {
		if !slices.Equal(existing, data) {
			return fmt.Errorf("agent state runtime metadata changed")
		}
		return b.cleanupAgentStateTemps(r)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := b.cleanupAgentStateTemps(r); err != nil {
		return err
	}
	f, err := os.CreateTemp(b.agentStateMetadata(r), ".mount-")
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer func() { _ = os.Remove(temporary) }()
	_, writeErr := f.Write(data)
	if err := errors.Join(writeErr, f.Sync(), f.Close()); err != nil {
		return err
	}
	if err := unix.Renameat2(unix.AT_FDCWD, temporary, unix.AT_FDCWD, path, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	return errors.Join(syncStateDirectory(b.agentStateMetadata(r)), syncStateDirectory(b.stateVolumeRoot()))
}

func (b *Backend) removeAgentStateMetadata(r *record) error {
	if r.AgentState == nil {
		return nil
	}
	path := b.agentStateMetadata(r)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := stateDirectory(path); err != nil {
		return err
	}
	data, err := stateMetadataFile(filepath.Join(path, "mountInfo.json"))
	if errors.Is(err, os.ErrNotExist) {
		if err := b.cleanupAgentStateTemps(r); err != nil {
			return err
		}
		return os.Remove(path)
	}
	if err != nil {
		return err
	}
	expected, _ := json.Marshal(b.stateMountInfo(r))
	if !slices.Equal(data, expected) {
		return fmt.Errorf("agent state metadata ownership mismatch; cleanup retained")
	}
	if err := b.cleanupAgentStateTemps(r); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(path, "mountInfo.json")); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return syncStateDirectory(b.stateVolumeRoot())
}
