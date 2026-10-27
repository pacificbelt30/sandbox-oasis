//go:build linux

package kataguard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestManifestBindsDragonballContainerdAssets(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name, contents string) string {
		t.Helper()
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	kernel := write("opt/kata/share/kernel", "kernel fixture")
	image := write("opt/kata/share/guest.image", "guest fixture")
	shim := write("opt/kata/bin/containerd-shim-kata-v2", "shim fixture")
	dockerd := write("bin/dockerd", "daemon fixture")
	logger := write("bin/codex-dock-io-logger", "logger fixture")
	for _, tool := range []string{"ip", "nft", "sysctl", "runc", "containerd-shim-runc-v2", "mke2fs"} {
		path := write("bin/"+tool, "tool fixture")
		if err := os.Chmod(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", filepath.Dir(logger)+string(os.PathListSeparator)+os.Getenv("PATH"))
	launcher := filepath.Join(root, "worker-guard")
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	build := exec.Command(goBinary, "build", "-buildvcs=false", "-o", launcher, "../../cmd/worker-guard")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64")
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build trusted bootstrap: %v %s", err, data)
	}
	for _, path := range []string{shim, dockerd, launcher, logger} {
		if err := os.Chmod(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	kata := write("kata.toml", fmt.Sprintf(`[hypervisor.dragonball]
kernel=%q
image=%q
shared_fs="inline-virtio-fs"
virtio_fs_cache="none"
virtio_fs_extra_args=["--thread-pool-size=1","-o","open,no_writeback_cache"]
enable_annotations=[]
guest_hook_path="/usr/share/codex-dock/hooks"
default_memory=2560
overhead_memory=512
overhead_vcpus=0
default_maxmemory=4096
[runtime]
disable_guest_seccomp=false
sandbox_cgroup_only=true
static_sandbox_resource_mgmt=true
hypervisor_name="dragonball"
`, kernel, image))
	daemon := write("containerd.toml", fmt.Sprintf(`version=3
root=%q
state=%q
[grpc]
address=%q
`, filepath.Join(root, "data"), filepath.Join(root, "run"), filepath.Join(root, "run/containerd.sock")))
	manifest := filepath.Join(root, "assets.json")
	if err := Seal(kata, daemon, dockerd, launcher, "io.containerd.kata.v2", manifest, shim); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(manifest); err != nil {
		t.Fatal("sealed assets rejected:", err)
	}
	if err := Seal(kata, daemon, dockerd, launcher, "io.containerd.kata.v2", manifest, shim); err == nil {
		t.Fatal("manifest was overwritten")
	}
	for _, path := range []string{kernel, image, shim, launcher, kata, daemon, logger} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, append(original, byte('x')), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = Load(manifest); err == nil {
			t.Fatalf("changed asset accepted: %s", path)
		}
		if err = os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.Chmod(path, info.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
	}
	// Every validation must read the bytes, even if the path, inode, size and
	// mtime match a previously successful validation.
	imageInfo, err := os.Stat(image)
	if err != nil {
		t.Fatal(err)
	}
	imageBytes, err := os.ReadFile(image)
	if err != nil {
		t.Fatal(err)
	}
	changed := append([]byte(nil), imageBytes...)
	changed[0] ^= 1
	if err := os.WriteFile(image, changed, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(image, imageInfo.ModTime(), imageInfo.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(manifest); err == nil {
		t.Fatal("same-size image mutation with restored mtime was accepted")
	}
	if err := os.WriteFile(image, imageBytes, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(image, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(manifest); err == nil {
		t.Fatal("writable image was accepted")
	}
	if err := os.Chmod(image, 0600); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(kata)
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range []string{"virtio_fs_cache=\"auto\"", "virtio_fs_cache=\"always\"", ""} {
		changed := strings.Replace(string(original), "virtio_fs_cache=\"none\"", setting, 1)
		if err := os.WriteFile(kata, []byte(changed), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readConfig(kata, daemon, "io.containerd.kata.v2"); err == nil || !strings.Contains(err.Error(), "virtio_fs_cache") {
			t.Fatalf("incoherent shared filesystem cache accepted: %q, %v", setting, err)
		}
	}
	for _, setting := range []string{"writeback_cache,no_open", "open,writeback_cache", "no_open,no_writeback_cache", ""} {
		changed := strings.Replace(string(original), "open,no_writeback_cache", setting, 1)
		if err := os.WriteFile(kata, []byte(changed), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readConfig(kata, daemon, "io.containerd.kata.v2"); err == nil || !strings.Contains(err.Error(), "virtio_fs_extra_args") {
			t.Fatalf("incoherent writeback/open settings accepted: %q, %v", setting, err)
		}
	}
	if err = os.WriteFile(kata, []byte(strings.Replace(string(original), "disable_guest_seccomp=false", "", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = readConfig(kata, daemon, "io.containerd.kata.v2"); err == nil {
		t.Fatal("implicit guest seccomp default was accepted")
	}
}
