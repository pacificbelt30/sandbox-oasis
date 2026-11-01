package sandbox

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func gitFixture(t *testing.T) (string, string) {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repo")
	wt := filepath.Join(t.TempDir(), "linked")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git fixture: %v: %s", err, out)
		}
	}
	git("init", repo)
	git("-C", repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "fixture")
	git("-C", repo, "worktree", "add", "-b", "fixture-worker", wt)
	return repo, wt
}

func TestExternalGitPointerRequiresReciprocalRegistration(t *testing.T) {
	repo, wt := gitFixture(t)
	attacker := t.TempDir()
	writePointer := func(target string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(attacker, ".git"), []byte("gitdir: "+target+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// A normal external repository and another worktree are both valid Git
	// pointers, but neither registers the attacker workspace with its owner.
	for _, target := range []string{filepath.Join(repo, ".git"), strings.TrimSpace(strings.TrimPrefix(string(mustReadGitFile(t, filepath.Join(wt, ".git"))), "gitdir: "))} {
		writePointer(target)
		if mounts, _, err := linkedWorktreeMounts(context.Background(), attacker, false, nil); err == nil || len(mounts) != 0 {
			t.Fatalf("external pointer authorized: %s %v", target, err)
		}
	}
	// A genuine registration remains usable through a host path alias.
	alias := filepath.Join(t.TempDir(), "worktree-alias")
	if err := os.Symlink(wt, alias); err != nil {
		t.Fatal(err)
	}
	if mounts, _, err := linkedWorktreeMounts(context.Background(), alias, false, nil); err != nil || len(mounts) != 1 {
		t.Fatalf("registered worktree rejected: %v", err)
	}
	gitdir := strings.TrimSpace(strings.TrimPrefix(string(mustReadGitFile(t, filepath.Join(wt, ".git"))), "gitdir: "))
	backlink := filepath.Join(gitdir, "gitdir")
	data := mustReadGitFile(t, backlink)
	if err := os.Remove(backlink); err != nil {
		t.Fatal(err)
	}
	copy := filepath.Join(t.TempDir(), "backlink")
	if err := os.WriteFile(copy, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(copy, backlink); err != nil {
		t.Fatal(err)
	}
	if _, _, err := linkedWorktreeMounts(context.Background(), wt, false, nil); err == nil {
		t.Fatal("symlink registration accepted")
	}
}

func mustReadGitFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestKataLinkedWorktreePreservesPointerAndLifetime(t *testing.T) {
	repo, wt := gitFixture(t)
	pointer := filepath.Join(wt, ".git")
	before, err := os.ReadFile(pointer)
	if err != nil {
		t.Fatal(err)
	}
	f, m := newEngineFixture(t)
	id, err := m.Run(RunOptions{Name: "worker", ProjectDir: repo, WorktreePath: wt, UseWorktree: true, Detach: true})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(pointer)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(before, after) {
		t.Fatal("host .git pointer changed during preparation")
	}
	common := filepath.Join(repo, ".git")
	found := false
	for _, mount := range f.worker.HostConfig.Mounts {
		if mount.Target == "/workspace/.git" {
			t.Fatal("nested .git file mount can corrupt a host pointer")
		}
		if mount.Source == common && mount.Target == common {
			found = true
		}
	}
	if !found {
		t.Fatal("linked worktree metadata not available in guest")
	}
	if value, _ := envValue(f.worker.Config.Env, "GIT_DIR"); value == "" {
		t.Fatal("canonical Git directory not configured")
	}
	if err := m.Remove(id, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("removed worker worktree still exists: %v", err)
	}
	if _, err := os.Stat(common); err != nil {
		t.Fatal("main repository was removed")
	}
}

func TestLinkedWorktreeReadOnlyMetadata(t *testing.T) {
	_, wt := gitFixture(t)
	mounts, _, err := linkedWorktreeMounts(context.Background(), wt, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 1 || !mounts[0].ReadOnly {
		t.Fatal("review workspace received writable Git metadata")
	}
}

func TestKataOptInPrivateGitAndReadOnlyRootFS(t *testing.T) {
	repo, wt := gitFixture(t)
	f, m := newEngineFixture(t)
	_, err := m.Run(RunOptions{Name: "isolated", ProjectDir: wt, NoGitMetadata: true, ReadOnlyRootFS: true, Detach: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, mount := range f.worker.HostConfig.Mounts {
		if mount.Source == filepath.Join(repo, ".git") {
			t.Fatal("common Git metadata was shared")
		}
	}
	if _, ok := envValue(f.worker.Config.Env, "GIT_DIR"); ok {
		t.Fatal("shared Git directory was configured")
	}
	host := f.worker.HostConfig
	if !host.ReadonlyRootfs || host.Tmpfs["/tmp"] == "" || host.Tmpfs["/var/tmp"] == "" || host.Tmpfs["/run/codex-dock-control"] == "" {
		t.Fatalf("read-only rootfs missing writable temporary areas: %+v", host)
	}
	if value, _ := envValue(f.worker.Config.Env, "HOME"); value != "/var/tmp/codex-home" {
		t.Fatalf("HOME = %q", value)
	}
}
