//go:build linux

package kataguard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Config struct {
	Manifest, CgroupRoot, StateDir, Endpoint, Socket string
	ResourceProfiles                                 string
	MaxWorkers                                       int
	DNS                                              []string
}
type Guard struct {
	config           Config
	manifest         Manifest
	profiles         []ResourceProfile
	temporaryStorage bool
	agentState       bool
}

func trustedOwner(info os.FileInfo) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && (s.Uid == 0 || s.Uid == uint32(os.Geteuid()))
}
func rootOwner(info os.FileInfo) bool { s, ok := info.Sys().(*syscall.Stat_t); return ok && s.Uid == 0 }

func New(c Config) (*Guard, error) {
	if c.MaxWorkers == 0 {
		c.MaxWorkers = 2
	}
	if c.MaxWorkers < 1 || c.MaxWorkers > 64 {
		return nil, fmt.Errorf("kata max workers must be 1..64")
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || u.Scheme != "unix" || u.Host != "" || !cleanPath(u.Path) || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("kata verification requires a local unix node endpoint")
	}
	if err = trustedPath(c.CgroupRoot, true); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(c.CgroupRoot, "/sys/fs/cgroup/") {
		return nil, fmt.Errorf("kata requires a dedicated cgroup v2 subtree")
	}
	var stat syscall.Statfs_t
	if err = syscall.Statfs(c.CgroupRoot, &stat); err != nil || stat.Type != 0x63677270 {
		return nil, fmt.Errorf("kata requires a cgroup v2 kernel filesystem")
	}
	if err = os.MkdirAll(c.StateDir, 0700); err != nil {
		return nil, err
	}
	if err = trustedPath(c.StateDir, true); err != nil {
		return nil, err
	}
	m, err := Load(c.Manifest)
	if err != nil {
		return nil, err
	}
	profiles, err := loadResourceProfiles(c.ResourceProfiles)
	if err != nil {
		return nil, fmt.Errorf("loading node resource profiles: %w", err)
	}
	g := &Guard{config: c, manifest: m, profiles: profiles, temporaryStorage: launcherTemporaryStorage(m.Assets["launcher"].Path), agentState: launcherFeature(m.Assets["launcher"].Path, "agent-state-probe-v1")}
	for _, p := range profiles {
		if p.PIDLimit != 512 && !g.supportsPIDProfiles() {
			return nil, fmt.Errorf("non-512 PID profiles require prepared guest policy assets; update and reseal the guest image before enabling them")
		}
	}
	return g, nil
}

func (g *Guard) Validate(ctx context.Context, runtime string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m, err := Load(g.config.Manifest)
	if err != nil {
		return err
	}
	profiles, err := loadResourceProfiles(g.config.ResourceProfiles)
	if err != nil {
		return err
	}
	if !slices.EqualFunc(profiles, g.profiles, ResourceProfile.Equal) {
		return fmt.Errorf("node resource profiles changed; restart the node after draining changed definitions")
	}
	if m.Runtime != runtime {
		return fmt.Errorf("kata registration differs from pinned manifest")
	}
	// The manifest used for process checks must not change during this manager.
	old, _ := json.Marshal(g.manifest)
	fresh, _ := json.Marshal(m)
	if string(old) != string(fresh) {
		return fmt.Errorf("kata manifest changed; restart the manager")
	}
	_, d, err := readConfig(m.Assets["config"].Path, m.Assets["daemon"].Path, runtime)
	if err != nil {
		return err
	}
	// Attest the dedicated containerd process itself. A pinned config's Unix
	// address is separate from the operator-facing node socket.
	want, err := os.Stat(m.Assets["containerd"].Path)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return err
	}
	matches := 0
	for _, entry := range entries {
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		base := "/proc/" + entry.Name()
		exe, err := os.Stat(base + "/exe")
		if err != nil || !os.SameFile(exe, want) {
			continue
		}
		info, err := os.Stat(base)
		if err != nil || !rootOwner(info) {
			continue
		}
		data, err := os.ReadFile(base + "/cmdline")
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
		valid := false
		overrides := false
		configs := 0
		for i, arg := range args {
			if arg == "--config" || arg == "-c" {
				configs++
				if i+1 < len(args) && args[i+1] == m.Assets["daemon"].Path {
					valid = true
				}
			}
			if strings.HasPrefix(arg, "--config=") {
				configs++
				valid = strings.TrimPrefix(arg, "--config=") == m.Assets["daemon"].Path
			}
			for _, flag := range []string{"--address", "-a", "--root", "--state"} {
				if arg == flag || strings.HasPrefix(arg, flag+"=") {
					overrides = true
				}
			}
		}
		if valid && configs == 1 && !overrides {
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("dedicated containerd process/config was not uniquely attested")
	}
	for _, path := range []string{d.Root, d.State} {
		if err = trustedPath(path, true); err != nil {
			return err
		}
	}
	if err = trustedPath(filepath.Dir(d.GRPC.Address), true); err != nil {
		return err
	}
	info, err := os.Stat(d.GRPC.Address)
	if err != nil || info.Mode()&os.ModeSocket == 0 || !rootOwner(info) {
		return fmt.Errorf("untrusted containerd socket")
	}
	for _, name := range []string{"cgroup.controllers", "cgroup.subtree_control"} {
		data, err := os.ReadFile(filepath.Join(g.config.CgroupRoot, name))
		if err != nil {
			return err
		}
		for _, controller := range []string{"cpu", "memory", "pids"} {
			if !slices.Contains(strings.Fields(string(data)), controller) {
				return fmt.Errorf("kata %s must enable %s", name, controller)
			}
		}
	}
	return nil
}

var groupID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var containerID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (g *Guard) group(id string) (string, error) {
	if !groupID.MatchString(id) {
		return "", fmt.Errorf("invalid Kata allocation id")
	}
	return filepath.Join(g.config.CgroupRoot, "cdx-"+id), nil
}
func groupLimitsFor(profile ResourceProfile) map[string]string {
	return map[string]string{"cpu.max": profile.CPUQuota(), "memory.max": strconv.FormatInt(profile.HostMemoryBytes, 10), "memory.swap.max": "0", "pids.max": strconv.FormatInt(profile.PIDLimit, 10), "memory.oom.group": "1"}
}
func (g *Guard) checkLimits(path string) error {
	id := strings.TrimPrefix(filepath.Base(path), "cdx-")
	record, err := g.readRecord(id)
	if err != nil {
		return err
	}
	profile, _ := g.resourceProfile(record.Profile)
	for name, want := range groupLimitsFor(profile) {
		b, err := os.ReadFile(filepath.Join(path, name))
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(b)) != want {
			return fmt.Errorf("kata host envelope mismatch: %s", name)
		}
	}
	if len(profile.BlockIO) > 0 {
		data, err := os.ReadFile(filepath.Join(path, "io.max"))
		if err != nil {
			return err
		}
		if err := VerifyIOLimits(string(data), profile.BlockIO); err != nil {
			return err
		}
	}
	return nil
}

func (g *Guard) Reserve(ctx context.Context, id string) (string, error) {
	return g.ReserveProfile(ctx, id, "standard")
}

func (g *Guard) ReserveProfile(ctx context.Context, id, profileName string) (string, error) {
	profile, ok := g.resourceProfile(profileName)
	if !ok {
		return "", fmt.Errorf("unknown Kata resource profile %q", profileName)
	}
	if len(profile.BlockIO) > 0 {
		enabled, err := os.ReadFile(filepath.Join(g.config.CgroupRoot, "cgroup.subtree_control"))
		if err != nil || !slices.Contains(strings.Fields(string(enabled)), "io") {
			return "", fmt.Errorf("requested block I/O limits require io controller delegation in the node subtree")
		}
		for _, limit := range profile.BlockIO {
			if _, err := os.Stat(filepath.Join("/sys/dev/block", limit.Device())); err != nil {
				return "", fmt.Errorf("requested I/O device %s is unavailable: %w", limit.Device(), err)
			}
		}
	}
	path, err := g.group(id)
	if err != nil {
		return "", err
	}
	unlock, err := g.admissionLock(ctx)
	if err != nil {
		return "", err
	}
	defer unlock()
	if _, err := os.Lstat(path); err == nil {
		if err := g.checkLimits(path); err != nil {
			return "", err
		}
		data, err := os.ReadFile(filepath.Join(path, "cgroup.events"))
		if err != nil {
			return "", err
		}
		if strings.Contains(string(data), "populated 1") {
			return "", fmt.Errorf("kata allocation is already occupied")
		}
		if err := g.trackAllocation(ctx, id, "", profile.Name); err != nil {
			return "", err
		}
		return strings.TrimPrefix(path, "/sys/fs/cgroup"), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	entries, err := os.ReadDir(g.config.CgroupRoot)
	if err != nil {
		return "", err
	}
	count := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "cdx-") {
			count++
		}
	}
	if count >= g.config.MaxWorkers {
		return "", fmt.Errorf("%w (%d workers); stop/remove a worker or change the node limit", ErrCapacity, g.config.MaxWorkers)
	}
	// Persist the reservation before its cgroup exists, so interrupted creation
	// remains discoverable and does not silently lose capacity.
	if err = g.trackAllocation(ctx, id, "", profile.Name); err != nil {
		return "", err
	}
	if err = os.Mkdir(path, 0755); err != nil {
		return "", err
	}
	for name, value := range groupLimitsFor(profile) {
		if err = os.WriteFile(filepath.Join(path, name), []byte(value), 0600); err != nil {
			_ = os.Remove(path)
			return "", err
		}
	}
	for _, limit := range profile.BlockIO {
		if err = os.WriteFile(filepath.Join(path, "io.max"), []byte(limit.Line()), 0600); err != nil {
			_ = os.Remove(path)
			return "", err
		}
	}
	if err = g.checkLimits(path); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err = os.WriteFile(filepath.Join(path, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0600); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return strings.TrimPrefix(path, "/sys/fs/cgroup"), nil
}

func (g *Guard) walkGroups(id, worker string, visit func(string) error) error {
	path, err := g.group(id)
	if err != nil {
		return err
	}
	if err = g.checkLimits(path); err != nil {
		return err
	}
	return filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		if current != path && entry.Name() != worker && entry.Name() != "kata_"+worker && entry.Name() != "supervisor" {
			return fmt.Errorf("unknown cgroup below Kata envelope")
		}
		return visit(current)
	})
}

func (g *Guard) Verify(ctx context.Context, id, worker string) error {
	return g.VerifyRuntime(ctx, g.manifest.Runtime, id, worker)
}

// VerifyRuntime combines runtime attestation and host verification without
// retaining validation results across operations.
func (g *Guard) VerifyRuntime(ctx context.Context, runtime, id, worker string) error {
	unlock, err := g.admissionLock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	// Validate after waiting for admission, before any process/ledger mutation.
	// The same operation may then retire the verified supervisor without another
	// asset read; ownership, process and limit checks still run in full.
	if err := g.Validate(ctx, runtime); err != nil {
		return err
	}
	if _, err = g.scanProcesses(ctx, id, worker, true); err != nil {
		return err
	}
	if err = g.trackAllocation(ctx, id, worker); err != nil {
		return err
	}
	// Kata may clean up its sandbox while serving Wait, before the engine sees
	// an exit result. Keep its supervisor out of that self-deleted leaf before
	// the bootstrap gate releases user code, including naturally exiting tasks.
	return g.retireVerified(ctx, id, worker)
}

func (g *Guard) scanProcesses(ctx context.Context, id, worker string, complete bool) ([]VMProcess, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !containerID.MatchString(worker) {
		return nil, fmt.Errorf("invalid worker id")
	}
	seen := map[string]bool{}
	var processes []VMProcess
	err := g.walkGroups(id, worker, func(path string) error {
		b, err := os.ReadFile(filepath.Join(path, "cgroup.procs"))
		if err != nil {
			return err
		}
		for _, pid := range strings.Fields(string(b)) {
			if _, err := strconv.ParseUint(pid, 10, 32); err != nil {
				return err
			}
			proc := "/proc/" + pid
			before, err := os.ReadFile(proc + "/stat")
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			exe, err := os.Stat(proc + "/exe")
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			matched := ""
			for _, name := range []string{"runtime", "logger"} {
				want, err := os.Stat(g.manifest.Assets[name].Path)
				if err != nil {
					return err
				}
				if os.SameFile(exe, want) {
					matched = name
					break
				}
			}
			if matched == "" {
				return fmt.Errorf("unexpected process in Kata envelope")
			}
			args, err := os.ReadFile(proc + "/cmdline")
			if err != nil {
				return err
			}
			if !strings.Contains(string(args), worker) {
				return fmt.Errorf("kata process has another worker identity")
			}
			cg, err := os.ReadFile(proc + "/cgroup")
			if err != nil {
				return err
			}
			relative := strings.TrimPrefix(path, "/sys/fs/cgroup")
			if strings.TrimSpace(string(cg)) != "0::"+relative {
				return fmt.Errorf("kata process moved out of its envelope")
			}
			after, err := os.ReadFile(proc + "/stat")
			if err != nil {
				return err
			}
			if procStart(string(before)) == "" || procStart(string(before)) != procStart(string(after)) {
				return fmt.Errorf("kata process identity changed")
			}
			seen[matched] = true
			numeric, _ := strconv.Atoi(pid)
			processes = append(processes, VMProcess{PID: numeric, Kind: matched})
		}
		return nil
	})
	if err != nil {
		return processes, err
	}
	if !complete {
		return processes, nil
	}
	for _, name := range []string{"runtime"} {
		if !seen[name] {
			return processes, fmt.Errorf("kata %s not found inside resource envelope", name)
		}
	}
	return processes, nil
}
func procStart(stat string) string {
	i := strings.LastIndex(stat, ")")
	if i < 0 {
		return ""
	}
	fields := strings.Fields(stat[i+1:])
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}

// Release never kills or moves a process. Retain occupied groups for an
// ownership-verified retry rather than expanding a cleanup's blast radius.
func (g *Guard) Release(ctx context.Context, id, worker string) error {
	unlock, err := g.admissionLock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	return g.releaseUnlocked(ctx, id, worker)
}

func (g *Guard) releaseUnlocked(ctx context.Context, id, worker string) error {
	if record, err := g.readRecord(id); err == nil {
		if record.Worker != "" && worker != "" && record.Worker != worker {
			return fmt.Errorf("allocation belongs to another worker")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	path, err := g.group(id)
	if err != nil {
		return err
	}
	if _, err = os.Lstat(path); os.IsNotExist(err) {
		return g.removeRecord(id)
	}
	for {
		var groups []string
		occupied := false
		err = g.walkGroups(id, worker, func(current string) error {
			b, err := os.ReadFile(filepath.Join(current, "cgroup.procs"))
			if err != nil {
				return err
			}
			if strings.TrimSpace(string(b)) != "" {
				occupied = true
			}
			groups = append(groups, current)
			return nil
		})
		if err != nil {
			return err
		}
		if !occupied {
			for i := len(groups) - 1; i >= 0; i-- {
				if err = os.Remove(groups[i]); err != nil {
					return err
				}
			}
			return g.removeRecord(id)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("kata envelope remains occupied: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (g *Guard) ProtectedPaths() []string {
	// Direct-volume metadata identifies private block images and is never a
	// user-provided workspace or additional mount source.
	storage := "/run/kata-containers/shared/direct-volumes"
	paths := []string{g.config.Manifest, g.config.StateDir, g.config.CgroupRoot, "/proc", "/sys/fs/cgroup", "/dev/kvm", "/run/netns", "/run/codex-dock-buildkit", "/var/lib/codex-dock-buildkit", "/var/lib/codex-dock-setup", "/var/cache/codex-dock-setup", "/opt/codex-dock/releases"}
	paths = append(paths, storage)
	if g.config.ResourceProfiles != "" {
		paths = append(paths, g.config.ResourceProfiles)
	}
	if _, d, err := readConfig(g.manifest.Assets["config"].Path, g.manifest.Assets["daemon"].Path, g.manifest.Runtime); err == nil {
		paths = append(paths, d.Root, d.State, d.GRPC.Address)
	}
	if endpoint, err := url.Parse(g.config.Endpoint); err == nil {
		paths = append(paths, endpoint.Path)
		if canonical, err := filepath.EvalSymlinks(endpoint.Path); err == nil {
			paths = append(paths, canonical)
		}
	}
	for _, a := range g.manifest.Assets {
		paths = append(paths, a.Path)
	}
	return paths
}

func (g *Guard) Launcher() string { return g.manifest.Assets["launcher"].Path }
func (g *Guard) Parent(id string) (string, error) {
	path, err := g.group(id)
	return strings.TrimPrefix(path, "/sys/fs/cgroup"), err
}
func (g *Guard) Stamp() string {
	b, _ := json.Marshal(g.manifest)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
