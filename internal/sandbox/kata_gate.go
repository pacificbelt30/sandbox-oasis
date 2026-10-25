package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-units"
	"github.com/pacificbelt30/codex-dock/internal/kataguard"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
)

const labelAllocation = labelPrefix + "allocation"
const labelProfile = labelPrefix + "verified-profile"
const guestGuard = "/run/codex-dock-guard"

type kataNode interface {
	Validate(context.Context, string) error
	Reserve(context.Context, string) (string, error)
	Verify(context.Context, string, string) error
	Release(context.Context, string, string) error
	ProtectedPaths() []string
	Launcher() string
	Parent(string) (string, error)
	Stamp() string
}

type kataProfileReserver interface {
	ReserveProfile(context.Context, string, string) (string, error)
}

func (m *Manager) kataNode() (kataNode, error) {
	m.guardMu.Lock()
	defer m.guardMu.Unlock()
	if m.guard != nil {
		return m.guard, nil
	}
	socket := "/run/codex-dock-node.sock"
	if value, ok := os.LookupEnv("CODEX_DOCK_KATA_NODE_SOCKET"); ok {
		socket = value
	}
	if m.kataConfig != nil && m.kataConfig.Socket != "" {
		socket = m.kataConfig.Socket
	}
	guard, err := kataguard.Connect(socket, m.cli.DaemonHost())
	if err != nil {
		return nil, err
	}
	m.guard = guard
	return guard, nil
}

func randomID(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (m *Manager) protectKataMounts(mounts []mount.Mount) error {
	g, err := m.kataNode()
	if err != nil {
		return err
	}
	for _, bind := range mounts {
		source, err := filepath.EvalSymlinks(bind.Source)
		if err != nil {
			return err
		}
		for _, protected := range g.ProtectedPaths() {
			if source == protected || strings.HasPrefix(protected, source+string(filepath.Separator)) || strings.HasPrefix(source, protected+string(filepath.Separator)) {
				return fmt.Errorf("worker mount overlaps protected Kata assets/state: %s", source)
			}
		}
		for _, target := range []string{guestGuard, "/run/codex-dock-control", "/proc", "/sys", "/dev"} {
			destination := filepath.Clean(bind.Target)
			if destination == target || strings.HasPrefix(target, strings.TrimSuffix(destination, "/")+"/") || strings.HasPrefix(destination, target+"/") {
				return fmt.Errorf("mount overlaps trusted Kata bootstrap or kernel interfaces")
			}
		}
	}
	return nil
}

func (m *Manager) prepareKataBootstrap(ctx context.Context, c *container.Config, h *container.HostConfig, allocation string, profile kataguard.ResourceProfile) error {
	g, err := m.kataNode()
	if err != nil {
		return err
	}
	image, err := m.cli.ImageInspect(ctx, c.Image)
	if err != nil {
		return err
	}
	if image.Config == nil {
		return fmt.Errorf("worker image has no configuration")
	}
	var command []string
	if len(c.Entrypoint) > 0 {
		command = append(command, c.Entrypoint...)
	} else {
		command = append(command, image.Config.Entrypoint...)
	}
	command = append(command, c.Cmd...)
	if len(command) == 0 {
		return fmt.Errorf("worker image has no command")
	}
	c.Entrypoint = []string{guestGuard, "wait", allocation}
	c.Cmd = command
	c.Labels[labelAllocation] = allocation
	c.Labels[labelProfile] = g.Stamp()
	h.Mounts = append(h.Mounts, mount.Mount{Type: mount.TypeBind, Source: g.Launcher(), Target: guestGuard, ReadOnly: true})
	if h.Tmpfs == nil {
		h.Tmpfs = make(map[string]string)
	}
	h.Tmpfs["/run/codex-dock-control"] = "rw,noexec,nosuid,nodev,size=1048576,mode=1777"
	h.Memory = profile.GuestMemoryBytes
	h.MemorySwap = profile.GuestMemoryBytes
	h.NanoCPUs = int64(profile.CPUCores) * 1_000_000_000
	pids := profile.PIDLimit
	h.PidsLimit = &pids
	if profile.FileDescriptorLimit != 0 {
		h.Ulimits = []*units.Ulimit{{Name: "nofile", Soft: int64(profile.FileDescriptorLimit), Hard: int64(profile.FileDescriptorLimit)}}
	}
	if profile.Name == "standard" {
		h.CgroupParent, err = g.Reserve(ctx, allocation)
	} else if reserver, ok := g.(kataProfileReserver); ok {
		h.CgroupParent, err = reserver.ReserveProfile(ctx, allocation, profile.Name)
	} else {
		return fmt.Errorf("kata node does not support %s resource profile", profile.Name)
	}
	return err
}

func (m *Manager) execTrusted(ctx context.Context, id string, args []string) ([]byte, error) {
	return m.execTrustedInput(ctx, id, args, nil)
}
func (m *Manager) execTrustedInput(ctx context.Context, id string, args []string, input io.Reader) ([]byte, error) {
	if m.execProbe != nil {
		return m.execProbe(ctx, id, args)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r, err := m.cli.ContainerExecCreate(ctx, id, container.ExecOptions{Cmd: args, AttachStdin: input != nil, AttachStdout: true, AttachStderr: true})
	if err != nil {
		return nil, err
	}
	stream, err := m.cli.ContainerExecAttach(ctx, r.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	stop := context.AfterFunc(ctx, stream.Close)
	defer stop()
	if input != nil {
		if _, err = io.Copy(stream.Conn, input); err != nil {
			return nil, fmt.Errorf("installing worker transport failed")
		}
		if err = stream.CloseWrite(); err != nil {
			return nil, err
		}
	}
	var out, stderr bytes.Buffer
	if _, err = stdcopy.StdCopy(&out, &stderr, io.LimitReader(stream.Reader, 65537)); err != nil {
		return nil, err
	}
	if out.Len()+stderr.Len() > 65536 {
		return nil, fmt.Errorf("trusted guest probe exceeded output limit")
	}
	status, err := m.cli.ContainerExecInspect(ctx, r.ID)
	if err != nil {
		return nil, err
	}
	if status.Running || status.ExitCode != 0 {
		return nil, fmt.Errorf("trusted guest probe failed (exit %d): %s", status.ExitCode, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

func verifyGuest(data []byte, nonce, hostBoot, user string, waiting bool, profile kataguard.ResourceProfile) error {
	var probe map[string]string
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	if probe["boot_id"] == "" || probe["boot_id"] == strings.TrimSpace(hostBoot) {
		return fmt.Errorf("guest does not have a separate kernel boot identity")
	}
	if probe["pids"] != strconv.FormatInt(profile.PIDLimit, 10) || probe["memory"] != strconv.FormatInt(profile.GuestMemoryBytes, 10) || probe["cpu"] != profile.CPUQuota() {
		return fmt.Errorf("guest resource limits do not match the Kata profile")
	}
	if profile.FileDescriptorLimit != 0 {
		expected := fmt.Sprintf("%d %d", profile.FileDescriptorLimit, profile.FileDescriptorLimit)
		if probe["nofile"] != expected || probe["init_nofile"] != expected {
			return fmt.Errorf("guest file descriptor limit mismatch")
		}
	}
	if waiting && !strings.Contains(probe["cmdline"], "\x00wait\x00"+nonce+"\x00") {
		return fmt.Errorf("worker started before trusted bootstrap verification")
	}
	fields := map[string][]string{}
	for _, line := range strings.Split(probe["status"], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			fields[key] = strings.Fields(value)
		}
	}
	for key, want := range map[string]string{"NoNewPrivs": "1", "Seccomp": "2"} {
		if len(fields[key]) != 1 || fields[key][0] != want {
			return fmt.Errorf("guest %s policy mismatch", key)
		}
	}
	for _, key := range []string{"CapInh", "CapPrm", "CapEff", "CapBnd", "CapAmb"} {
		if len(fields[key]) != 1 {
			return fmt.Errorf("guest %s missing", key)
		}
		value, err := strconv.ParseUint(fields[key][0], 16, 64)
		if err != nil || value != 0 {
			return fmt.Errorf("guest capabilities are not fully dropped")
		}
	}
	for _, key := range []string{"Uid", "Gid"} {
		values := fields[key]
		if len(values) != 4 {
			return fmt.Errorf("guest identity is incomplete")
		}
		for _, value := range values {
			if value != values[0] {
				return fmt.Errorf("guest identity is inconsistent")
			}
		}
		if key == "Uid" && values[0] == "0" {
			return fmt.Errorf("kata workload must run as non-root")
		}
	}
	parts := strings.Split(user, ":")
	if expected, err := strconv.ParseUint(parts[0], 10, 32); err == nil && fields["Uid"][0] != strconv.FormatUint(expected, 10) {
		return fmt.Errorf("guest UID differs from configured user")
	}
	if len(parts) == 2 {
		if expected, err := strconv.ParseUint(parts[1], 10, 32); err == nil && fields["Gid"][0] != strconv.FormatUint(expected, 10) {
			return fmt.Errorf("guest GID differs from configured user")
		}
	}
	return nil
}

func (m *Manager) verifyKataGuest(ctx context.Context, id string, waiting bool) error {
	v, err := m.cli.ContainerInspect(ctx, id)
	if err != nil {
		return err
	}
	g, err := m.kataNode()
	if err != nil {
		return err
	}
	if v.Config.Labels[labelProfile] != g.Stamp() {
		return fmt.Errorf("kata assets/profile changed; recreate this worker")
	}
	if err = g.Verify(ctx, v.Config.Labels[labelAllocation], id); err != nil {
		return err
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return err
	}
	storage, err := workerpolicy.ParseTemporaryStorage(v.Config.Labels[workerpolicy.TemporaryStorageLabel])
	if err != nil {
		return err
	}
	args := []string{guestGuard, "probe"}
	for _, s := range storage {
		args = append(args, s.Target)
	}
	data, err := m.execTrusted(ctx, id, args)
	if err != nil {
		return err
	}
	profile, err := m.resourceProfile(ctx, v.Config.Labels[labelPrefix+"resource-profile"])
	if err != nil {
		return err
	}
	if err := verifyGuest(data, v.Config.Labels[labelAllocation], string(boot), v.Config.User, waiting, profile); err != nil {
		return err
	}
	return verifyTemporaryGuest(data, storage, waiting)
}

func (m *Manager) releaseKataAllocation(ctx context.Context, allocation, id string) error {
	g, err := m.kataNode()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return g.Release(ctx, allocation, id)
}
