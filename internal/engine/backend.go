//go:build linux

package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/containerd/containerd/api/types/runc/options"
	runtimeoptions "github.com/containerd/containerd/api/types/runtimeoptions/v1"
	cd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/contrib/seccomp"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/errdefs"
	"github.com/distribution/reference"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	dn "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/system"
	dockerimage "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/sys/signal"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
)

const owned = "codex-dock.engine"

var hexID = regexp.MustCompile(`^[a-f0-9]{64}$`)
var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type Options struct {
	Address, State, Runtime, KataConfig, Snapshotter string
	Logger                                           string
	DNS                                              []string
	TemporaryStorage                                 bool
	// Retire keeps an attested Dragonball supervisor in a sibling leaf inside
	// the same bounded envelope before its self-owned cgroup is deleted.
	Retire      func(context.Context, string, string) error
	AbortCreate func(context.Context, string, string) error
	// ConfirmStopped independently attests an empty, owned Kata envelope when
	// a runtime exit event arrives after its Wait RPC disconnects.
	ConfirmStopped func(context.Context, string, string) error
	Tools          map[string]string
}
type Backend struct {
	client        *cd.Client
	opts          Options
	checkpointMu  sync.Mutex
	checkpointing map[string]bool
	mu            sync.Mutex
	hubs          map[string]*hub
	execs         map[string]*execution
	ports         map[string][]net.Listener
	closed        bool
}
type record struct {
	ID, Name                string
	Created                 time.Time
	Config                  container.Config
	Host                    container.HostConfig
	Spec                    specs.Spec
	Networks                map[string]*dn.EndpointSettings
	ExitCode                int
	FinishedAt              string
	StartedAt               time.Time
	LastActivity            time.Time
	ActivityUntil           time.Time
	NetNSDevice, NetNSInode uint64
	FailedCreate            bool
	Quarantined             bool `json:",omitempty"`
	PrimaryOutputLimit      int64
}
type execution struct {
	cancel     context.CancelFunc `json:"-"`
	done       chan struct{}      `json:"-"`
	ID         string
	Detached   bool
	FinishedAt time.Time
	Container  string
	Config     container.ExecOptions
	Running    bool
	Started    bool
	ExitCode   int
}

func Open(o Options) (*Backend, error) {
	if os.Geteuid() != 0 {
		return nil, fmt.Errorf("containerd node backend requires root")
	}
	if o.Address == "" || !filepath.IsAbs(o.Address) || o.State == "" || !filepath.IsAbs(o.State) {
		return nil, fmt.Errorf("absolute containerd socket and state paths required")
	}
	for _, name := range []string{"ip", "nft", "sysctl", "runc", "runc-shim"} {
		if !filepath.IsAbs(o.Tools[name]) {
			return nil, fmt.Errorf("missing pinned node tool: %s", name)
		}
	}
	if o.Snapshotter == "" {
		o.Snapshotter = "overlayfs"
	}
	if err := os.MkdirAll(filepath.Join(o.State, "tasks"), 0700); err != nil {
		return nil, err
	}
	c, err := cd.New(o.Address)
	if err != nil {
		return nil, err
	}
	b := &Backend{client: c, opts: o, hubs: map[string]*hub{}, execs: map[string]*execution{}, ports: map[string][]net.Listener{}}
	ctx, cancel := context.WithTimeout(namespaces.WithNamespace(context.Background(), Namespace), 30*time.Second)
	defer cancel()
	if _, err = c.Version(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	if err = b.recover(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	if err = b.recoverJobs(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return b, nil
}
func (b *Backend) Close() error {
	b.mu.Lock()
	b.closed = true
	var pending []<-chan struct{}
	for _, e := range b.execs {
		if !e.Detached && e.cancel != nil {
			e.cancel()
			if e.done != nil {
				pending = append(pending, e.done)
			}
		}
	}
	b.mu.Unlock()
	// Streaming executions belong to the connection; background jobs and main
	// workers belong to containerd and deliberately survive a node restart.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	waitExpired := false
	for _, done := range pending {
		select {
		case <-done:
		case <-deadline.C:
			waitExpired = true
		}
		if waitExpired {
			break
		}
	}
	b.mu.Lock()
	for cid, h := range b.hubs {
		b.closePorts(cid)
		if h.io != nil {
			h.io.Cancel()
		}
		_ = h.Close()
	}
	b.mu.Unlock()
	return b.client.Close()
}

func id() (string, error) {
	var data [32]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}
func (b *Backend) path(cid string) string { return filepath.Join(b.opts.State, "tasks", cid) }
func (b *Backend) save(r *record) error {
	dir := b.path(r.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".record-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err = os.Rename(name, filepath.Join(dir, "record.json")); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}
func (b *Backend) load(ctx context.Context, value string) (*record, cd.Container, error) {
	all, err := b.client.Containers(ctx, `labels."`+owned+`"=="true"`)
	if err != nil {
		return nil, nil, err
	}
	var found cd.Container
	for _, c := range all {
		l, e := c.Labels(ctx)
		if e != nil {
			return nil, nil, e
		}
		if l["codex-dock.name"] == value || len(value) >= 12 && strings.HasPrefix(c.ID(), value) {
			if found != nil {
				return nil, nil, fmt.Errorf("ambiguous container identity")
			}
			found = c
		}
	}
	if found == nil && hexID.MatchString(value) {
		if _, err := b.client.LoadContainer(ctx, value); err == nil {
			return nil, nil, fmt.Errorf("container exists with changed ownership: %w", errdefs.ErrFailedPrecondition)
		} else if !errdefs.IsNotFound(err) {
			return nil, nil, err
		}
	}
	if found == nil {
		return nil, nil, fmt.Errorf("container %s: %w", value, errdefs.ErrNotFound)
	}
	return b.loadOwned(ctx, found)
}

// loadOwned avoids repeating name/ID discovery when an owned container was
// already selected from containerd's namespace-filtered inventory.
func (b *Backend) loadOwned(ctx context.Context, found cd.Container) (*record, cd.Container, error) {
	data, err := os.ReadFile(filepath.Join(b.path(found.ID()), "record.json"))
	if err != nil {
		return nil, nil, err
	}
	var r record
	if err = json.Unmarshal(data, &r); err != nil {
		return nil, nil, err
	}
	if r.ID != found.ID() || !hexID.MatchString(r.ID) {
		return nil, nil, fmt.Errorf("invalid engine record")
	}
	info, err := found.Info(ctx)
	if err != nil {
		return nil, nil, err
	}
	for key, value := range r.Config.Labels {
		if len(value) <= 4096 && info.Labels[key] != value {
			return nil, nil, fmt.Errorf("native container labels changed")
		}
	}
	expectedRuntime := b.opts.Runtime
	if r.Host.Runtime == "runc" {
		expectedRuntime = b.opts.Tools["runc-shim"]
	}
	if info.Labels["codex-dock.name"] != r.Name || info.Runtime.Name != expectedRuntime || info.SnapshotKey != r.ID || info.Snapshotter != b.opts.Snapshotter {
		return nil, nil, fmt.Errorf("native container identity/runtime/snapshot drift")
	}
	actual, err := found.Spec(ctx)
	if err != nil {
		return nil, nil, err
	}
	want, _ := json.Marshal(&r.Spec)
	got, _ := json.Marshal(actual)
	if string(want) != string(got) {
		return nil, nil, fmt.Errorf("container OCI specification drift")
	}
	return &r, found, nil
}
func imageName(name string) (string, error) {
	ref, err := reference.ParseNormalizedNamed(name)
	if err != nil {
		return "", err
	}
	return reference.TagNameOnly(ref).String(), nil
}
func (b *Backend) image(ctx context.Context, name string) (cd.Image, v1.Image, error) {
	var config v1.Image
	n, err := imageName(name)
	if err != nil {
		return nil, config, err
	}
	img, err := b.client.GetImage(ctx, n)
	if err != nil {
		return nil, config, err
	}
	desc, err := img.Config(ctx)
	if err != nil {
		return nil, config, err
	}
	data, err := content.ReadBlob(ctx, b.client.ContentStore(), desc)
	if err != nil {
		return nil, config, err
	}
	err = json.Unmarshal(data, &config)
	return img, config, err
}

func (b *Backend) create(ctx context.Context, c container.Config, h container.HostConfig, name string) (result string, resultErr error) {
	if !validName.MatchString(name) {
		return "", fmt.Errorf("invalid container name")
	}
	if _, _, err := b.load(ctx, name); err == nil {
		return "", fmt.Errorf("container name already exists: %w", errdefs.ErrAlreadyExists)
	} else if !errdefs.IsNotFound(err) {
		return "", err
	}
	if h.Privileged || len(h.CapAdd) > 0 || h.PidMode != "" || h.IpcMode != "" || h.NetworkMode == "host" || h.NetworkMode == "none" || h.PublishAllPorts || len(h.Binds) > 0 {
		return "", fmt.Errorf("unsupported or unsafe node container configuration")
	}
	if h.Runtime != "" && h.Runtime != "runc" && h.Runtime != "io.containerd.kata.v2" {
		return "", fmt.Errorf("unknown runtime; no fallback")
	}
	if err := b.protectUsageMounts(ctx, &record{Config: c, Host: h}); err != nil {
		return "", err
	}
	img, ic, err := b.image(ctx, c.Image)
	if err != nil {
		return "", err
	}
	if err := validateCheckpointImage(ic, h.Runtime); err != nil {
		return "", err
	}
	if expected := c.Labels["codex-dock.image-id"]; expected != "" && img.Target().Digest.String() != expected {
		return "", fmt.Errorf("image digest changed since lock validation")
	}
	if err = img.Unpack(ctx, b.opts.Snapshotter); err != nil {
		return "", err
	}
	cid, err := id()
	if err != nil {
		return "", err
	}
	if c.StopSignal == "" {
		c.StopSignal = ic.Config.StopSignal
	}
	r := &record{ID: cid, Name: name, Created: time.Now().UTC(), Config: c, Host: h, Networks: map[string]*dn.EndpointSettings{}, PrimaryOutputLimit: primaryOutputLimit}
	if r.Host.Runtime == "" {
		r.Host.Runtime = "runc"
	}
	if err = b.save(r); err != nil {
		return "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			if err := b.cancelCreation(cleanupCtx, r); err != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf("creation intent retained for retry: %w", err))
			}
		}
	}()
	if err = b.newNamespace(ctx, r); err != nil {
		return "", err
	}
	if err = b.connect(ctx, string(h.NetworkMode), r); err != nil {
		return "", err
	}
	if err = b.resolverFiles(r); err != nil {
		return "", err
	}
	args := append([]string{}, c.Entrypoint...)
	if len(args) == 0 {
		args = append(args, ic.Config.Entrypoint...)
	}
	if len(c.Cmd) > 0 {
		args = append(args, c.Cmd...)
	} else if len(c.Entrypoint) == 0 {
		args = append(args, ic.Config.Cmd...)
	}
	user := c.User
	if user == "" {
		user = ic.Config.User
	}
	if user == "" {
		user = "0:0"
	}
	cwd := c.WorkingDir
	if cwd == "" {
		cwd = ic.Config.WorkingDir
	}
	if cwd == "" {
		cwd = "/"
	}
	labels := map[string]string{owned: "true", "codex-dock.name": name}
	for k, v := range c.Labels {
		if len(v) <= 4096 {
			labels[k] = v
		}
	}
	runtime := b.opts.Runtime
	var runtimeOptions any = &runtimeoptions.Options{ConfigPath: b.opts.KataConfig}
	if r.Host.Runtime == "runc" {
		runtime = b.opts.Tools["runc-shim"]
		runtimeOptions = &options.Options{BinaryName: b.opts.Tools["runc"]}
	}
	opts := []oci.SpecOpts{oci.WithImageConfig(img), oci.WithProcessArgs(args...), oci.WithEnv(c.Env), oci.WithProcessCwd(cwd), oci.WithUser(user), oci.WithHostname(name), oci.WithLinuxNamespace(specs.LinuxNamespace{Type: specs.NetworkNamespace, Path: b.nsPath(cid)}), oci.WithLinuxNamespace(specs.LinuxNamespace{Type: specs.CgroupNamespace}), oci.WithNoNewPrivileges, seccomp.WithDefaultProfile()}
	if h.ReadonlyRootfs {
		opts = append(opts, oci.WithRootFSReadonly())
	}
	if c.Tty {
		opts = append(opts, withWorkerTTY)
	}
	if len(h.CapDrop) > 0 {
		if len(h.CapDrop) != 1 || h.CapDrop[0] != "ALL" {
			return "", fmt.Errorf("only dropping ALL capabilities is supported")
		}
		opts = append(opts, oci.WithCapabilities([]string{}))
	}
	if h.Memory > 0 {
		opts = append(opts, oci.WithMemoryLimit(uint64(h.Memory)))
	}
	if h.PidsLimit != nil {
		opts = append(opts, oci.WithPidsLimit(*h.PidsLimit))
	}
	if len(h.Ulimits) > 0 {
		if len(h.Ulimits) != 1 || h.Ulimits[0] == nil || h.Ulimits[0].Name != "nofile" || h.Ulimits[0].Soft != h.Ulimits[0].Hard || h.Ulimits[0].Soft < 128 || h.Ulimits[0].Hard > 1<<20 {
			return "", fmt.Errorf("unsupported process file descriptor limit")
		}
		limit := uint64(h.Ulimits[0].Soft)
		opts = append(opts, func(_ context.Context, _ oci.Client, _ *containers.Container, s *oci.Spec) error {
			// Replace the runtime default so conflicting duplicate limits cannot win.
			var keep []specs.POSIXRlimit
			for _, r := range s.Process.Rlimits {
				if r.Type != "RLIMIT_NOFILE" {
					keep = append(keep, r)
				}
			}
			s.Process.Rlimits = append(keep, specs.POSIXRlimit{Type: "RLIMIT_NOFILE", Soft: limit, Hard: limit})
			return nil
		})
	}
	storage, err := workerpolicy.ParseTemporaryStorage(c.Labels[workerpolicy.TemporaryStorageLabel])
	if err != nil {
		return "", err
	}
	if len(storage) > 0 && !b.opts.TemporaryStorage {
		return "", fmt.Errorf("node bootstrap does not support temporary storage")
	}
	temporary := make(map[string]bool)
	for _, v := range storage {
		if h.Tmpfs[v.Target] != v.MountOptions() {
			return "", fmt.Errorf("temporary storage declaration does not match tmpfs")
		}
		temporary[v.Target] = true
	}
	opts = append(opts, func(_ context.Context, _ oci.Client, _ *containers.Container, s *oci.Spec) error {
		s.Linux.MaskedPaths = append(s.Linux.MaskedPaths, h.MaskedPaths...)
		s.Linux.ReadonlyPaths = append(s.Linux.ReadonlyPaths, h.ReadonlyPaths...)
		s.Mounts = append(s.Mounts, specs.Mount{Destination: "/sys/fs/cgroup", Type: "cgroup", Source: "cgroup", Options: []string{"nosuid", "noexec", "nodev", "relatime", "ro"}})
		if h.PidsLimit != nil && h.Runtime != "runc" {
			if s.Annotations == nil {
				s.Annotations = make(map[string]string)
			}
			s.Annotations["io.codex-dock.pids-limit"] = strconv.FormatInt(*h.PidsLimit, 10)
		}
		s.Linux.CgroupsPath = filepath.Join(h.CgroupParent, cid)
		if h.CgroupParent == "" {
			s.Linux.CgroupsPath = "/codex-dock-services/" + cid
		}
		if h.NanoCPUs > 0 {
			period := uint64(100000)
			quota := h.NanoCPUs / 10000
			s.Linux.Resources.CPU = &specs.LinuxCPU{Period: &period, Quota: &quota}
		}
		if h.MemorySwap > 0 {
			swap := h.MemorySwap
			s.Linux.Resources.Memory.Swap = &swap
		}
		for _, m := range h.Mounts {
			if m.Type != mount.TypeBind {
				return fmt.Errorf("only bind mounts are supported")
			}
			options := []string{"rbind", "rprivate"}
			if m.ReadOnly {
				options = append(options, "ro")
			}
			s.Mounts = append(s.Mounts, specs.Mount{Type: "bind", Source: m.Source, Destination: m.Target, Options: options})
		}
		targets := make([]string, 0, len(h.Tmpfs))
		for target := range h.Tmpfs {
			targets = append(targets, target)
		}
		sort.Strings(targets)
		for _, target := range targets {
			options := strings.Split(h.Tmpfs[target], ",")
			if temporary[target] {
				options = append(options, "uid="+strconv.FormatUint(uint64(s.Process.User.UID), 10), "gid="+strconv.FormatUint(uint64(s.Process.User.GID), 10))
			}
			s.Mounts = append(s.Mounts, specs.Mount{Type: "tmpfs", Source: "tmpfs", Destination: target, Options: options})
		}
		for _, file := range []string{"hosts", "resolv.conf", "hostname"} {
			s.Mounts = append(s.Mounts, specs.Mount{Type: "bind", Source: filepath.Join(b.path(cid), file), Destination: "/etc/" + file, Options: []string{"bind", "ro"}})
		}
		r.Spec = *s
		return b.save(r)
	})
	native, err := b.client.NewContainer(ctx, cid, cd.WithImage(img), cd.WithSnapshotter(b.opts.Snapshotter), cd.WithNewSnapshot(cid, img), cd.WithNewSpec(opts...), cd.WithRuntime(runtime, runtimeOptions), cd.WithContainerLabels(labels))
	if err != nil {
		return "", err
	}
	s, err := native.Spec(ctx)
	if err != nil {
		return "", err
	}
	r.Spec = *s
	if err = b.save(r); err != nil {
		_ = native.Delete(ctx, cd.WithSnapshotCleanup)
		return "", err
	}
	cleanup = false
	return cid, nil
}

func (b *Backend) state(ctx context.Context, c cd.Container, r *record) (*container.State, error) {
	s := &container.State{StartedAt: r.StartedAt.Format(time.RFC3339Nano), Status: "created", ExitCode: r.ExitCode, FinishedAt: r.FinishedAt}
	t, err := c.Task(ctx, nil)
	if errdefs.IsNotFound(err) {
		if r.FinishedAt != "" {
			s.Status = "exited"
		}
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	status, err := t.Status(ctx)
	if err != nil {
		return nil, err
	}
	s.Status = string(status.Status)
	if status.Status == cd.Stopped {
		s.Status = "exited"
	}
	s.Running = status.Status == cd.Running
	s.Paused = status.Status == cd.Paused
	s.Pid = int(t.Pid())
	s.ExitCode = int(status.ExitStatus)
	return s, nil
}
func (b *Backend) inspect(ctx context.Context, value string) (container.InspectResponse, error) {
	r, c, err := b.load(ctx, value)
	if err != nil {
		return container.InspectResponse{}, err
	}
	state, err := b.state(ctx, c, r)
	if err != nil {
		return container.InspectResponse{}, err
	}
	if r.Config.Labels == nil {
		r.Config.Labels = map[string]string{}
	}
	if !r.LastActivity.IsZero() {
		r.Config.Labels["codex-dock.last-activity"] = r.LastActivity.Format(time.RFC3339Nano)
	}
	active, err := b.activeWork(r, state.Running || state.Paused, time.Now())
	if err != nil {
		return container.InspectResponse{}, err
	}
	r.Config.Labels["codex-dock.active-executions"] = strconv.Itoa(active)
	r.Config.Labels["codex-dock.activity-until"] = r.ActivityUntil.Format(time.RFC3339Nano)
	// This status belongs to the node's durable record, not launch labels.
	r.Config.Labels[workerpolicy.QuarantinedLabel] = strconv.FormatBool(r.Quarantined)
	// Docker's current API still requires NetworkSettingsBase for published ports.
	return container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: r.ID, Name: "/" + r.Name, Created: r.Created.Format(time.RFC3339Nano), State: state, HostConfig: &r.Host}, Config: &r.Config, NetworkSettings: &container.NetworkSettings{NetworkSettingsBase: container.NetworkSettingsBase{Ports: r.Host.PortBindings}, Networks: r.Networks}}, nil //nolint:staticcheck
}

// FindRequest checks a deterministic worker name against the durable engine
// record. The node uses this only to reconcile a lost launch response.
func (b *Backend) FindRequest(ctx context.Context, name string) (id, key, fingerprint string, found bool, err error) {
	ctx = namespaces.WithNamespace(ctx, Namespace)
	b.mu.Lock()
	defer b.mu.Unlock()
	r, _, err := b.load(ctx, name)
	if errdefs.IsNotFound(err) {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	if r.Name != name {
		return "", "", "", false, fmt.Errorf("worker name resolves to another engine record")
	}
	return r.ID, r.Config.Labels["codex-dock.request-key"], r.Config.Labels["codex-dock.request-fingerprint"], true, nil
}

func (b *Backend) start(ctx context.Context, r *record, c cd.Container) error {
	if err := executionAllowed(r); err != nil {
		return err
	}
	if t, err := c.Task(ctx, nil); err == nil {
		status, e := t.Status(ctx)
		if e != nil {
			return e
		}
		if status.Status == cd.Running {
			return nil
		}
		if err = b.deleteTask(ctx, r, t); err != nil {
			return err
		}
	} else if !errdefs.IsNotFound(err) {
		return err
	}
	if r.Host.Runtime != "runc" {
		if err := b.refreshWorkerNamespace(ctx, r); err != nil {
			return err
		}
	}
	if old := b.hubs[r.ID]; old != nil {
		if old.io != nil {
			old.io.Cancel()
		}
		_ = old.Close()
		delete(b.hubs, r.ID)
	}
	h, err := newHub()
	if err != nil {
		return err
	}
	creator, err := b.logIO(r)
	if err != nil {
		_ = h.Close()
		return err
	}
	t, err := c.NewTask(ctx, creator)
	if err != nil {
		_ = h.Close()
		return b.failedStart(ctx, r, c, err)
	}
	wait, err := t.Wait(namespaces.WithNamespace(context.Background(), Namespace))
	if err != nil {
		_ = h.Close()
		return b.failedStart(ctx, r, c, err)
	}
	b.hubs[r.ID] = h
	h.io = t.IO()
	if err = b.openPorts(ctx, r); err != nil {
		delete(b.hubs, r.ID)
		_ = h.Close()
		return b.failedStart(ctx, r, c, err)
	}
	if err = t.Start(ctx); err != nil {
		b.closePorts(r.ID)
		delete(b.hubs, r.ID)
		_ = h.Close()
		return b.failedStart(ctx, r, c, err)
	}
	if !r.Config.OpenStdin && !r.Config.Tty {
		if closer, ok := h.io.(interface{ CloseInput() error }); ok {
			if err = closer.CloseInput(); err != nil {
				return b.failedStart(ctx, r, c, err)
			}
		}
		if err = t.CloseIO(ctx, cd.WithStdinCloser); err != nil && !errdefs.IsNotFound(err) {
			return b.failedStart(ctx, r, c, err)
		}
	}
	r.FailedCreate = false
	r.StartedAt = time.Now().UTC()
	r.LastActivity = r.StartedAt
	r.ActivityUntil = time.Time{}
	r.FinishedAt = ""
	r.ExitCode = 0
	if err = b.save(r); err != nil {
		return b.failedStart(ctx, r, c, err)
	}

	h.exit = newTaskExit()
	go b.watch(r.ID, h, t, wait)
	return nil
}
func (b *Backend) deleteTask(ctx context.Context, r *record, t cd.Task) error {
	s, err := t.Status(ctx)
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if s.Status != cd.Stopped {
		return fmt.Errorf("task must exit before deletion")
	}
	if err = b.prepareSupervisor(ctx, r); err != nil {
		return err
	}
	_, err = t.Delete(ctx)
	if errdefs.IsNotFound(err) {
		return nil
	}
	return err
}

func (b *Backend) prepareSupervisor(ctx context.Context, r *record) error {
	if r.Host.Runtime == "runc" {
		return nil
	}
	if b.opts.Retire == nil {
		return fmt.Errorf("dragonball retirement verifier missing")
	}
	return b.opts.Retire(ctx, r.Config.Labels["codex-dock.allocation"], r.ID)
}

func (b *Backend) stop(ctx context.Context, r *record, c cd.Container, timeout int) error {
	if r.FailedCreate {
		return b.abortTask(ctx, r, c)
	}
	t, err := c.Task(ctx, nil)
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("loading task to stop: %w", err)
	}
	observer := b.observeStopExit(ctx, r, t)
	if observer != nil {
		defer observer.cancel()
	}
	// Wait can trigger Kata's cgroup cleanup itself. Preparing only after exit
	// is too late, especially after reconnecting to a live VM.
	if err = b.prepareSupervisor(ctx, r); err != nil {
		return fmt.Errorf("preparing Dragonball supervisor for stop: %w", err)
	}
	var result *taskExit
	if h := b.hubs[r.ID]; h != nil {
		result = h.exit
	}
	if result == nil {
		// Recovered quarantine and tasks without an attached hub have no
		// existing watcher. Normal and reattached tasks share their first Wait.
		wait, err := t.Wait(ctx)
		if err != nil {
			return fmt.Errorf("subscribing to task exit for stop: %w", err)
		}
		result = newTaskExit()
		go result.capture(wait)
	}
	status, err := t.Status(ctx)
	if err != nil {
		return fmt.Errorf("reading task status before stop: %w", err)
	}
	if status.Status != cd.Stopped {
		stopSignal := syscall.SIGTERM
		var killOpts []cd.KillOpts
		if timeout == 0 {
			// Immediate stop has no graceful phase. Sending TERM followed by
			// KILL can race Kata tearing down its shim while serving Wait.
			stopSignal = syscall.SIGKILL
			killOpts = []cd.KillOpts{cd.WithKillAll}
		} else if r.Config.StopSignal != "" {
			parsed, e := signal.ParseSignal(r.Config.StopSignal)
			if e != nil {
				return e
			}
			stopSignal = parsed
		}
		if err = t.Kill(ctx, stopSignal, killOpts...); err != nil && !errdefs.IsNotFound(err) {
			return err
		}
		timer := time.NewTimer(time.Duration(max(timeout, 0)) * time.Second)
		defer timer.Stop()
		deadline := timer.C
		if timeout <= 0 {
			timer.Stop()
			deadline = nil
		}
		var exit cd.ExitStatus
		var received bool
		select {
		case <-result.ready:
			exit, received = result.status, result.received
		case <-deadline:
			if err = t.Kill(ctx, syscall.SIGKILL, cd.WithKillAll); err != nil && !errdefs.IsNotFound(err) {
				return err
			}
			select {
			case <-result.ready:
				exit, received = result.status, result.received
			case <-ctx.Done():
				return ctx.Err()
			}
		case <-ctx.Done():
			return ctx.Err()
		}
		if !received {
			return fmt.Errorf("task exit channel closed without a result")
		}
		code, at, err := exit.Result()
		if err != nil {
			confirmed, confirmErr := b.confirmStopExit(ctx, r, c, observer)
			if confirmErr != nil {
				return fmt.Errorf("waiting for task exit during stop: %w", errors.Join(err, confirmErr))
			}
			code, at = confirmed.ExitStatus, confirmed.ExitTime
		}
		// Wait already supplied authoritative exit evidence. A second Status
		// RPC can race the runtime forgetting the exited task and lose both the
		// successful stop and its actual exit code. deleteTask still checks that
		// the task is stopped/absent before retirement and deletion.
		status = cd.Status{Status: cd.Stopped, ExitStatus: code, ExitTime: at}
	}
	r.ExitCode = int(status.ExitStatus)
	r.FinishedAt = status.ExitTime.Format(time.RFC3339Nano)
	if err = b.save(r); err != nil {
		return err
	}
	b.closePorts(r.ID)
	if h := b.hubs[r.ID]; h != nil {
		if h.io != nil {
			h.io.Cancel()
		}
		_ = h.Close()
		delete(b.hubs, r.ID)
	}
	if err := b.deleteTask(ctx, r, t); err != nil {
		return fmt.Errorf("deleting stopped task: %w", err)
	}
	return nil
}

func reply(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
func failure(w http.ResponseWriter, err error) {
	code := 500
	if errdefs.IsNotFound(err) {
		code = 404
	}
	if errdefs.IsAlreadyExists(err) || errdefs.IsConflict(err) {
		code = 409
	}
	w.WriteHeader(code)
	reply(w, map[string]string{"message": err.Error()})
}
func decode(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20)).Decode(v)
}

// ServeHTTP deliberately implements only the operations used by this product.
// All objects are confined to one containerd namespace and an ownership label.
func (b *Backend) serveHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := namespaces.WithNamespace(r.Context(), Namespace)
	path := r.URL.Path
	if strings.HasPrefix(path, "/v1.") {
		i := strings.Index(path[1:], "/")
		if i >= 0 {
			path = path[i+1:]
		}
	}
	if path == "/_ping" {
		w.Header().Set("API-Version", "1.51")
		_, _ = io.WriteString(w, "OK")
		return
	}
	if path == "/version" {
		reply(w, map[string]string{"ApiVersion": "1.51", "Version": "containerd"})
		return
	}
	if path == "/info" {
		if _, err := b.client.Version(ctx); err != nil {
			failure(w, err)
			return
		}
		features := [][2]string{{"cxd.disk-checkpoint", "1"}, {"cxd.checkpoint-retention", "1"}, {"cxd.worker-policy", "1"}, {"cxd.idle-safe", "1"}, {"cxd.main-activity", "1"}, {"cxd.exec-options", "1"}, {"cxd.jobs", "1"}, {"cxd.quarantine", "1"}}
		if b.opts.TemporaryStorage {
			features = append(features, [2]string{"cxd.temporary-storage", "1"})
		}
		reply(w, system.Info{DriverStatus: features, OSType: "linux", CgroupDriver: "cgroupfs", CgroupVersion: "2", Runtimes: map[string]system.RuntimeWithStatus{"io.containerd.kata.v2": {Runtime: system.Runtime{Type: b.opts.Runtime}}, "runc": {Runtime: system.Runtime{Type: "io.containerd.runc.v2"}}}})
		return
	}
	if path == "/checkpoints" || strings.HasPrefix(path, "/checkpoints/") {
		b.checkpointHTTP(w, r, ctx, path)
		return
	}
	if path == "/images/check" && r.Method == "GET" {
		probe, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		report, err := b.checkImage(probe, r.URL.Query().Get("name"), r.URL.Query().Get("agent"))
		if err != nil {
			failure(w, err)
			return
		}
		reply(w, report)
		return
	}
	if path == "/images/load" && r.Method == "POST" {
		b.importImages(w, r, ctx)
		return
	}
	if strings.HasPrefix(path, "/images/") && r.Method == http.MethodDelete {
		b.mu.Lock()
		defer b.mu.Unlock()
		name, err := imageName(strings.TrimPrefix(path, "/images/"))
		if err != nil {
			failure(w, err)
			return
		}
		// Remove one reference only, never force deletion or prune shared
		// content/snapshots. Retained containers must remain restartable.
		all, err := b.client.Containers(ctx)
		if err != nil {
			failure(w, err)
			return
		}
		for _, c := range all {
			info, err := c.Info(ctx)
			if err != nil {
				failure(w, err)
				return
			}
			if info.Image == name {
				failure(w, fmt.Errorf("%w: image is referenced by a retained container", errdefs.ErrConflict))
				return
			}
		}
		if err := b.client.ImageService().Delete(ctx, name); err != nil {
			failure(w, err)
			return
		}
		reply(w, []image.DeleteResponse{{Untagged: name}})
		return
	}
	if strings.HasPrefix(path, "/images/") && strings.HasSuffix(path, "/json") {
		name := strings.TrimSuffix(strings.TrimPrefix(path, "/images/"), "/json")
		img, ic, err := b.image(ctx, name)
		if err != nil {
			failure(w, err)
			return
		}
		created := ""
		if ic.Created != nil {
			created = ic.Created.Format(time.RFC3339Nano)
		}
		reply(w, image.InspectResponse{ID: img.Target().Digest.String(), Created: created, Config: &dockerimage.DockerOCIImageConfig{ImageConfig: ic.Config}})
		return
	}
	if strings.HasPrefix(path, "/networks") {
		b.networkHTTP(w, r, ctx, path)
		return
	}
	if strings.HasPrefix(path, "/exec/") {
		b.execHTTP(w, r, ctx, path)
		return
	}
	if path == "/containers/create" && r.Method == "POST" {
		var req struct {
			container.Config
			HostConfig container.HostConfig
		}
		if err := decode(r, &req); err != nil {
			failure(w, err)
			return
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		cid, err := b.create(ctx, req.Config, req.HostConfig, r.URL.Query().Get("name"))
		if err != nil {
			failure(w, err)
			return
		}
		w.WriteHeader(201)
		reply(w, container.CreateResponse{ID: cid})
		return
	}
	if path == "/containers/json" {
		b.mu.Lock()
		defer b.mu.Unlock()
		all, err := b.client.Containers(ctx, `labels."`+owned+`"=="true"`)
		if err != nil {
			failure(w, err)
			return
		}
		rows := []container.Summary{}
		f, err := filters.FromJSON(r.URL.Query().Get("filters"))
		if err != nil {
			failure(w, err)
			return
		}
		for _, c := range all {
			v, err := b.inspect(ctx, c.ID())
			if err != nil {
				failure(w, err)
				return
			}
			if r.URL.Query().Get("all") != "1" && !v.State.Running {
				continue
			}
			matched := true
			for _, label := range f.Get("label") {
				k, val, has := strings.Cut(label, "=")
				if got, ok := v.Config.Labels[k]; !ok || has && got != val {
					matched = false
				}
			}
			if matched {
				t, _ := time.Parse(time.RFC3339Nano, v.Created)
				rows = append(rows, container.Summary{ID: v.ID, Names: []string{v.Name}, Image: v.Config.Image, Labels: v.Config.Labels, State: v.State.Status, Status: v.State.Status, Created: t.Unix()})
			}
		}
		reply(w, rows)
		return
	}
	if !strings.HasPrefix(path, "/containers/") {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/containers/"), "/")
	if len(parts) == 0 || parts[0] == path {
		http.NotFound(w, r)
		return
	}
	value := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	if action == "attach" || action == "logs" || action == "wait" || action == "exec" || action == "jobs" {
		b.streamHTTP(w, r, ctx, value, action)
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	rec, c, err := b.load(ctx, value)
	if err != nil {
		failure(w, err)
		return
	}
	if b.checkpointing[rec.ID] && action != "json" {
		failure(w, fmt.Errorf("worker checkpoint is active: %w", errdefs.ErrConflict))
		return
	}
	switch action {
	case "json":
		var v container.InspectResponse
		v, err = b.inspect(ctx, rec.ID)
		if err == nil {
			reply(w, v)
			return
		}
	case "start":
		err = b.start(ctx, rec, c)
	case "quarantine":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if value != rec.ID {
			failure(w, fmt.Errorf("quarantine requires the full worker ID"))
			return
		}
		err = b.quarantine(ctx, rec, c)
	case "idle-stop":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		before, parseErr := time.Parse(time.RFC3339Nano, r.URL.Query().Get("before"))
		if parseErr != nil || before.After(time.Now()) {
			failure(w, fmt.Errorf("invalid idle cutoff"))
			return
		}
		// Serialize exec creation/start with the final idle decision and stop.
		state, stateErr := b.state(ctx, c, rec)
		if stateErr != nil {
			failure(w, stateErr)
			return
		}
		active, activeErr := b.activeWork(rec, state.Running || state.Paused, time.Now())
		if activeErr != nil {
			failure(w, activeErr)
			return
		}
		if !idleEligible(rec.LastActivity, before, active) {
			reply(w, map[string]bool{"stopped": false})
			return
		}
		err = b.stop(ctx, rec, c, 10)
		if err == nil {
			reply(w, map[string]bool{"stopped": true})
			return
		}
	case "activity":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		ttl, parseErr := strconv.Atoi(r.URL.Query().Get("ttl"))
		if parseErr != nil || ttl < 0 || ttl > 300 || value != rec.ID || rec.Config.Labels["codex-dock.managed"] != "true" {
			failure(w, fmt.Errorf("activity requires a managed worker ID and ttl between 0 and 300 seconds: %w", errdefs.ErrInvalidArgument))
			return
		}
		if err = executionAllowed(rec); err == nil {
			var state *container.State
			state, err = b.state(ctx, c, rec)
			if err == nil && !state.Running {
				err = fmt.Errorf("activity requires a running worker: %w", errdefs.ErrConflict)
			}
		}
		if err == nil {
			rec.LastActivity = time.Now().UTC()
			rec.ActivityUntil = rec.LastActivity.Add(time.Duration(ttl) * time.Second)
			err = b.save(rec)
		}
		if err == nil {
			reply(w, map[string]any{"worker": rec.ID, "active_until": rec.ActivityUntil})
			return
		}
	case "stop":
		seconds := 10
		if v := r.URL.Query().Get("t"); v != "" {
			seconds, _ = strconv.Atoi(v)
		}

		err = b.stop(ctx, rec, c, seconds)
	case "resize":
		var task cd.Task
		task, err = c.Task(ctx, nil)
		if err == nil {
			width, _ := strconv.Atoi(r.URL.Query().Get("w"))
			height, _ := strconv.Atoi(r.URL.Query().Get("h"))
			err = task.Resize(ctx, uint32(width), uint32(height))
		}
	case "":
		if r.Method != "DELETE" {
			http.NotFound(w, r)
			return
		}
		s, e := b.state(ctx, c, rec)
		err = e
		if err == nil && s.Running && r.URL.Query().Get("force") != "1" {
			err = fmt.Errorf("container is running")
		}
		if err == nil {
			err = b.stop(ctx, rec, c, 0)
		}
		if err == nil {
			err = b.removeNamespace(ctx, rec)
		}
		if err == nil {
			err = c.Delete(ctx, cd.WithSnapshotCleanup)
		}
		if err == nil {
			for eid, e := range b.execs {
				if e.Container == rec.ID {
					delete(b.execs, eid)
				}
			}
			err = os.RemoveAll(b.path(rec.ID))
		}
	default:
		http.NotFound(w, r)
		return
	}
	if err != nil {
		failure(w, err)
		return
	}
	w.WriteHeader(204)
}

func (b *Backend) importImages(w http.ResponseWriter, r *http.Request, ctx context.Context) {
	// Import is streamed, never buffered or logged: archives may contain custom
	// source code. containerd owns the resulting content and snapshot store.
	imgs, err := b.client.Import(ctx, r.Body)
	if err != nil {
		failure(w, err)
		return
	}
	for _, img := range imgs {
		im := cd.NewImage(b.client, img)
		if err = im.Unpack(ctx, b.opts.Snapshotter); err != nil {
			// Import registers the tag before Unpack. Keep a failed unpack from
			// looking like a usable image to the next build or container create.
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			err = errors.Join(err, b.client.ImageService().Delete(cleanupCtx, img.Name))
			cancel()
			failure(w, err)
			return
		}
	}
	reply(w, map[string]string{"stream": "Imported into containerd namespace " + Namespace + "\n"})
}

// These DTO dependencies are wire compatibility only. Execution uses the
// containerd SDK and OCI specifications exclusively.
var _ types.HijackedResponse
