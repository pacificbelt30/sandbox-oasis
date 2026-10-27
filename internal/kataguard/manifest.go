// Package kataguard owns node-local Kata verification and resource envelopes.
package kataguard

import (
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/pelletier/go-toml/v2"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
)

const Profile = "kata-dragonball-containerd-guard-v2"
const GuestMemory = int64(2048 * 1024 * 1024)
const HostMemory = int64(3072 * 1024 * 1024)
const CPUQuota = "100000 100000"

type Asset struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	Version int              `json:"version"`
	Profile string           `json:"profile"`
	Runtime string           `json:"runtime"`
	Assets  map[string]Asset `json:"assets"`
}
type kataConfig struct {
	Hypervisor map[string]struct {
		Kernel         string   `toml:"kernel"`
		Image          string   `toml:"image"`
		SharedFS       string   `toml:"shared_fs"`
		FSCache        string   `toml:"virtio_fs_cache"`
		FSExtraArgs    []string `toml:"virtio_fs_extra_args"`
		Annotations    []string `toml:"enable_annotations"`
		Hook           string   `toml:"guest_hook_path"`
		Memory         int      `toml:"default_memory"`
		MaxMemory      int      `toml:"default_maxmemory"`
		OverheadMemory int      `toml:"overhead_memory"`
		OverheadCPUs   float64  `toml:"overhead_vcpus"`
	} `toml:"hypervisor"`
	Runtime struct {
		DisableSeccomp bool   `toml:"disable_guest_seccomp"`
		SandboxOnly    bool   `toml:"sandbox_cgroup_only"`
		Static         bool   `toml:"static_sandbox_resource_mgmt"`
		Hypervisor     string `toml:"hypervisor_name"`
	} `toml:"runtime"`
}
type daemonConfig struct {
	Version int    `toml:"version"`
	Root    string `toml:"root"`
	State   string `toml:"state"`
	GRPC    struct {
		Address    string `toml:"address"`
		TCPAddress string `toml:"tcp_address"`
	} `toml:"grpc"`
}

func hash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func cleanPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && path != "/" && !strings.ContainsAny(path, "\r\n\x00")
}

// trustedPath rejects writable assets and replaceable directory ancestors.
func trustedPath(path string, directory bool) error {
	if !cleanPath(path) {
		return fmt.Errorf("expected clean absolute trusted path: %q", path)
	}
	for current := path; current != "/"; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || (current != path || directory) && !info.IsDir() || current == path && !directory && !info.Mode().IsRegular() {
			return fmt.Errorf("untrusted file type: %s", current)
		}
		if !trustedOwner(info) || info.Mode().Perm()&0022 != 0 && (current == path || info.Mode()&os.ModeSticky == 0) {
			return fmt.Errorf("untrusted owner or writable path: %s", current)
		}
	}
	return nil
}

func readConfig(kataPath, daemonPath, runtime string) (kataConfig, daemonConfig, error) {
	var k kataConfig
	var d daemonConfig
	data, err := os.ReadFile(kataPath)
	if err != nil {
		return k, d, err
	}
	if err = toml.Unmarshal(data, &k); err != nil {
		return k, d, err
	}
	var raw map[string]any
	if err = toml.Unmarshal(data, &raw); err != nil {
		return k, d, err
	}
	rt, _ := raw["runtime"].(map[string]any)
	hv, _ := raw["hypervisor"].(map[string]any)
	db, _ := hv["dragonball"].(map[string]any)
	annotations, explicit := db["enable_annotations"].([]any)
	h, ok := k.Hypervisor["dragonball"]
	if !ok || len(k.Hypervisor) != 1 || h.SharedFS != "inline-virtio-fs" || h.Hook != "/usr/share/codex-dock/hooks" || h.Memory != 2560 || h.MaxMemory != 4096 || h.OverheadMemory != 512 || h.OverheadCPUs != 0 || rt["disable_guest_seccomp"] != false || rt["sandbox_cgroup_only"] != true || rt["static_sandbox_resource_mgmt"] != true || rt["hypervisor_name"] != "dragonball" || !explicit || len(annotations) != 0 {
		return k, d, fmt.Errorf("kata config does not match %s (Dragonball, seccomp, static sizing, hooks or cgroups)", Profile)
	}
	if h.FSCache != "none" {
		return k, d, fmt.Errorf("shared workspaces require virtio_fs_cache=\"none\"; regenerate the Kata config and reseal the asset manifest")
	}
	if !slices.Equal(h.FSExtraArgs, []string{"--thread-pool-size=1", "-o", "open,no_writeback_cache"}) {
		return k, d, fmt.Errorf("shared workspaces require virtio_fs_extra_args with open,no_writeback_cache; regenerate the Kata config and reseal the asset manifest")
	}
	if runtime != "io.containerd.kata.v2" {
		return k, d, fmt.Errorf("unsupported Kata registration")
	}
	data, err = os.ReadFile(daemonPath)
	if err != nil {
		return k, d, err
	}
	if err = toml.Unmarshal(data, &d); err != nil {
		return k, d, err
	}
	if d.Version != 3 || !cleanPath(d.Root) || !cleanPath(d.State) || !cleanPath(d.GRPC.Address) || d.GRPC.TCPAddress != "" {
		return k, d, fmt.Errorf("dedicated containerd requires config v3, private root/state and one Unix endpoint")
	}
	return k, d, nil
}

// Seal pins installed, operator-verified files; this checksum record is not a
// signature. Legacy QEMU/Docker profiles must be drained before resealing.
func Seal(kataPath, daemonPath, containerd, launcher, runtime, output string, shimPath ...string) error {
	shim := "/opt/kata/runtime-rs/bin/containerd-shim-kata-v2"
	if len(shimPath) >= 1 {
		shim = shimPath[0]
	}
	if len(shimPath) > 2 {
		return fmt.Errorf("one shim path and optional logger path required")
	}
	binary, err := elf.Open(launcher)
	if err != nil {
		return err
	}
	if binary.Class != elf.ELFCLASS64 || binary.Machine != elf.EM_X86_64 {
		_ = binary.Close()
		return fmt.Errorf("worker guard must be Linux x86_64")
	}
	for _, p := range binary.Progs {
		if p.Type == elf.PT_INTERP {
			_ = binary.Close()
			return fmt.Errorf("worker guard must be statically linked")
		}
	}
	if err = binary.Close(); err != nil {
		return err
	}
	k, _, err := readConfig(kataPath, daemonPath, runtime)
	if err != nil {
		return err
	}
	h := k.Hypervisor["dragonball"]
	paths := map[string]string{"config": kataPath, "daemon": daemonPath, "containerd": containerd, "launcher": launcher, "runtime": shim, "kernel": h.Kernel, "image": h.Image}
	for key, name := range map[string]string{"ip": "ip", "nft": "nft", "sysctl": "sysctl", "runc": "runc", "runc-shim": "containerd-shim-runc-v2", "logger": "codex-dock-io-logger", "mkfs": "mke2fs"} {
		var path string
		var err error
		if key == "logger" && len(shimPath) == 2 && shimPath[1] != "" {
			path = shimPath[1]
		} else {
			path, err = exec.LookPath(name)
		}
		if err != nil {
			return fmt.Errorf("required node tool %s: %w", name, err)
		}
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		paths[key] = path
	}
	m := Manifest{Version: 2, Profile: Profile, Runtime: runtime, Assets: map[string]Asset{}}
	for name, path := range paths {
		if name == "kernel" {
			path, err = filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
		}
		if err = trustedPath(path, false); err != nil {
			return err
		}
		if executableAsset(name) {
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			if info.Mode().Perm()&0111 == 0 {
				return fmt.Errorf("asset is not executable: %s", name)
			}
		}
		checksum, err := hash(path)
		if err != nil {
			return err
		}
		m.Assets[name] = Asset{path, checksum}
	}
	guestPolicy := filepath.Join(filepath.Dir(m.Assets["image"].Path), "assets.lock.json")
	if _, err := os.Lstat(guestPolicy); err == nil {
		supported, err := readGuestPolicy(guestPolicy, m.Assets["image"].SHA256)
		if err != nil {
			return err
		}
		if supported {
			checksum, err := hash(guestPolicy)
			if err != nil {
				return err
			}
			m.Assets["guest-policy"] = Asset{Path: guestPolicy, SHA256: checksum}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err = f.Write(append(data, '\n')); err != nil {
		return err
	}
	return f.Sync()
}
func Load(path string) (Manifest, error) {
	var m Manifest
	if err := trustedPath(path, false); err != nil {
		return m, err
	}
	f, err := os.Open(path)
	if err != nil {
		return m, err
	}
	defer func() { _ = f.Close() }()
	decoder := json.NewDecoder(io.LimitReader(f, 1024*1024))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&m); err != nil {
		return m, err
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return m, fmt.Errorf("trailing manifest content")
	}
	if m.Version != 2 || m.Profile != Profile || m.Runtime != "io.containerd.kata.v2" {
		return m, fmt.Errorf("unsupported Kata manifest; drain the previous profile before migration")
	}
	for _, name := range []string{"config", "daemon", "containerd", "launcher", "runtime", "kernel", "image", "ip", "nft", "sysctl", "runc", "runc-shim", "logger"} {
		if _, ok := m.Assets[name]; !ok {
			return m, fmt.Errorf("missing asset %s", name)
		}
	}
	if err = verifyAssets(m.Assets); err != nil {
		return m, err
	}
	if asset, ok := m.Assets["guest-policy"]; ok {
		supported, err := readGuestPolicy(asset.Path, m.Assets["image"].SHA256)
		if err != nil {
			return m, err
		}
		if !supported {
			return m, fmt.Errorf("guest policy asset must declare dynamic PID support")
		}
	}
	k, _, err := readConfig(m.Assets["config"].Path, m.Assets["daemon"].Path, m.Runtime)
	if err != nil {
		return m, err
	}
	h := k.Hypervisor["dragonball"]
	kernel, err := filepath.EvalSymlinks(h.Kernel)
	if err != nil {
		return m, err
	}
	for name, path := range map[string]string{"kernel": kernel, "image": h.Image} {
		if m.Assets[name].Path != path {
			return m, fmt.Errorf("asset/config binding mismatch: %s", name)
		}
	}
	return m, nil
}

// Each validation still reads and hashes every asset. Bound concurrency to
// avoid multiplying disk readers and buffers with the number of pinned files.
// No checksum is reused based on timestamps or a previous validation.
func verifyAssets(assets map[string]Asset) error {
	limit := min(4, runtime.GOMAXPROCS(0))
	slots := make(chan struct{}, limit)
	var workers sync.WaitGroup
	var failed sync.Once
	var firstErr error
	for name, asset := range assets {
		slots <- struct{}{}
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer func() { <-slots }()
			if err := verifyAsset(name, asset); err != nil {
				failed.Do(func() { firstErr = err })
			}
		}()
	}
	workers.Wait()
	return firstErr
}

func verifyAsset(name string, asset Asset) error {
	if err := trustedPath(asset.Path, false); err != nil {
		return err
	}
	if executableAsset(name) {
		info, err := os.Stat(asset.Path)
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0111 == 0 {
			return fmt.Errorf("asset is not executable: %s", name)
		}
	}
	actual, err := hash(asset.Path)
	if err != nil {
		return err
	}
	if len(asset.SHA256) != 64 || actual != asset.SHA256 {
		return fmt.Errorf("kata asset checksum mismatch: %s", name)
	}
	return nil
}

func executableAsset(name string) bool {
	return name == "launcher" || name == "runtime" || name == "containerd" || name == "ip" || name == "nft" || name == "sysctl" || name == "runc" || name == "runc-shim" || name == "logger" || name == "mkfs"
}
