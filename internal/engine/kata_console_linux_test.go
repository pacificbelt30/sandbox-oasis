//go:build linux

package engine

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKataConsoleCleanupRequiresDeadOwnedSocket(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "kc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	id := strings.Repeat("a", 64)
	dir := filepath.Join(root, id)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "console.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	defer l.Close()
	if err := removeStaleKataSockets(root, id); err == nil {
		t.Fatal("live listener removed")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := removeStaleKataSockets(root, id); err != nil {
		t.Fatal("dead socket retained:", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket retained: %v", err)
	}
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeStaleKataSockets(root, id); err == nil {
		t.Fatal("regular file removed")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "other"), path); err != nil {
		t.Fatal(err)
	}
	if err := removeStaleKataSockets(root, id); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Chmod(dir, 0777); err != nil {
		t.Fatal(err)
	}
	if err := removeStaleKataSockets(root, id); err == nil {
		t.Fatal("untrusted directory accepted")
	}
}

func TestKataSocketCleanupRetainsMetadata(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "ks-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	id := strings.Repeat("b", 64)
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(filepath.Join(dir, "root"), 0700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state.json")
	if err := os.WriteFile(state, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"console.sock", "shim-monitor.sock", "root/kata.hvsock"} {
		l, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, name), Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		l.SetUnlinkOnClose(false)
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeStaleKataSockets(root, id); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"console.sock", "shim-monitor.sock", "root/kata.hvsock"} {
		if _, err := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("stale socket retained: %v", err)
		}
	}
	if data, err := os.ReadFile(state); err != nil || string(data) != "retained" {
		t.Fatalf("runtime metadata removed: %s %v", data, err)
	}
}
