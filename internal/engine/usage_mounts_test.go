//go:build linux

package engine

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
)

func TestUsageLedgerCannotOverlapWorkersInEitherCreationOrder(t *testing.T) {
	dir := t.TempDir()
	private := filepath.Join(dir, "usage")
	if err := os.Mkdir(private, 0700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(private, "nested")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(private, alias); err != nil {
		t.Fatal(err)
	}
	proxy := &record{Config: container.Config{Labels: map[string]string{"io.codex-dock.proxy-layout": "split-v3-oauth-state"}}, Host: container.HostConfig{Mounts: []mount.Mount{{Type: mount.TypeBind, Source: private, Target: "/run/cxd-usage"}}}}
	for _, source := range []string{dir, private, child, alias} {
		worker := &record{Config: container.Config{Labels: map[string]string{"codex-dock.managed": "true"}}, Host: container.HostConfig{Mounts: []mount.Mount{{Type: mount.TypeBind, Source: source, Target: "/workspace", ReadOnly: true}}}}
		if err := checkUsageShares(worker, []*record{proxy}); err == nil {
			t.Fatal("worker received usage authority")
		}
		if err := checkUsageShares(proxy, []*record{worker}); err == nil {
			t.Fatal("new ledger appeared in an existing worker share")
		}
	}
	worker := &record{Config: container.Config{Labels: map[string]string{"codex-dock.managed": "true"}}, Host: container.HostConfig{Mounts: []mount.Mount{{Type: mount.TypeBind, Source: t.TempDir(), Target: "/workspace"}}}}
	if err := checkUsageShares(worker, []*record{proxy}); err != nil {
		t.Fatal(err)
	}
}
