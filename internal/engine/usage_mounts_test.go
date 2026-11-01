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

func TestNodeAuthorityRootIsProtectedBeforeFilesExist(t *testing.T) {
	b := &Backend{opts: Options{State: t.TempDir()}}
	if err := b.ensureAuthorityRoot(); err != nil {
		t.Fatal(err)
	}
	private := b.privateAuthorityRecord()
	for _, source := range []string{b.opts.State, b.authorityRoot()} {
		worker := &record{Config: container.Config{Labels: map[string]string{"codex-dock.managed": "true"}}, Host: container.HostConfig{Mounts: []mount.Mount{{Type: mount.TypeBind, Source: source, Target: "/workspace"}}}}
		if checkUsageShares(worker, []*record{private}) == nil {
			t.Fatal("worker can share node authority before publication")
		}
		if checkUsageShares(private, []*record{worker}) == nil {
			t.Fatal("authority can be published into existing share")
		}
	}
	file := b.caseAuthorityPath("proxy", "case")
	secret := "fixture-private-secret-at-least-32-bytes"
	if err := writeCaseAuthority(file, secret, os.Geteuid(), os.Getegid()); err != nil {
		t.Fatal(err)
	}
	if err := writeCaseAuthority(file, secret, os.Geteuid(), os.Getegid()); err != nil {
		t.Fatal("restart lost authority:", err)
	}
	if writeCaseAuthority(file, secret+"changed", os.Geteuid(), os.Getegid()) == nil {
		t.Fatal("existing authority overwritten")
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != secret {
		t.Fatal("failed replacement changed authority")
	}
}
