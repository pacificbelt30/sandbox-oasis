package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/proxyimage"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKataSecurityBoundaries(t *testing.T) {
	if os.Getenv("CODEX_DOCK_KATA_E2E") != "1" {
		t.Skip("requires isolated real Kata node")
	}
	requestDir := os.Getenv("CODEX_DOCK_NODE_RESTART_REQUEST_DIR")
	if requestDir == "" {
		t.Fatal("requires disposable node restart controller")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	m, err := NewManager(ManagerConfig{StateDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	for _, tag := range []string{"docker.io/library/codex-dock-proxy:latest", os.Getenv("CODEX_DOCK_KATA_PROXY_IMAGE")} {
		if tag == "" {
			continue
		}
		if err := engine.Build(ctx, tag, "/nonexistent-project-recipe", "/nonexistent-project", io.Discard); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("project can build a protected proxy reference %s: %v", tag, err)
		}
	}
	proxyImage := os.Getenv("CODEX_DOCK_KATA_PROXY_IMAGE")
	// Exercise the legitimate explicit build, immutable promotion and node
	// authorization path, then reject the same reference as a project target.
	trustedTag := "security-proxy-" + randomSuffix() + ":verification"
	buildDir := t.TempDir()
	recipe := filepath.Join(buildDir, "Dockerfile")
	if err := os.WriteFile(recipe, []byte("FROM "+proxyImage+"\nLABEL oaiso.trusted-control=true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := engine.BuildProxyAt(ctx, engine.Socket(), trustedTag, recipe, buildDir, io.Discard); err != nil {
		t.Fatal("explicit trusted proxy build:", err)
	}
	defer func() {
		if _, err := m.cli.ImageRemove(context.Background(), trustedTag, image.RemoveOptions{}); err != nil {
			t.Error(err)
		}
	}()
	built, err := m.cli.ImageInspect(ctx, trustedTag)
	if err != nil {
		t.Fatal(err)
	}
	if err := proxyimage.Verify(ctx, engine.Socket(), trustedTag, built.ID); err != nil {
		t.Fatal("built proxy authority:", err)
	}
	if err := engine.Build(ctx, trustedTag, "/nonexistent-project-recipe", "/nonexistent-project", io.Discard); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("project replaced explicitly built proxy: %v", err)
	}
	created, digestErr := m.cli.ContainerCreate(ctx, &container.Config{Image: proxyImage, Labels: map[string]string{"codex-dock.image-id": "sha256:" + strings.Repeat("0", 64)}}, &container.HostConfig{Runtime: "runc"}, nil, nil, "security-digest-"+randomSuffix())
	if created.ID != "" {
		_ = m.cli.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})
	}
	if digestErr == nil || !strings.Contains(digestErr.Error(), "digest changed") {
		t.Fatalf("changed proxy digest reached creation: %v", digestErr)
	}
	repo, wt := gitFixture(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".git"), []byte("gitdir: "+filepath.Join(repo, ".git")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	opts := RunOptions{Name: "security-" + randomSuffix(), Image: os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE"), ProjectDir: project, ContainerUser: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), NoInternet: true, Detach: true, ExecCmd: "exec sleep 600"}
	if id, err := m.RunContext(ctx, opts); err == nil {
		_ = m.Remove(id, true)
		t.Fatal("unrelated host repository pointer accepted")
	} else if !strings.Contains(err.Error(), "registered linked worktree") {
		t.Fatal(err)
	}
	opts.ProjectDir = repo
	opts.WorktreePath = wt
	opts.UseWorktree = true
	id, err := m.RunContext(ctx, opts)
	if err != nil {
		t.Fatal("registered worktree launch:", err)
	}
	defer func() {
		if err := m.Remove(id, true); err != nil {
			t.Error(err)
		}
	}()
	// The guest must retain ordinary Git functionality on the selected repo.
	kataExec(t, ctx, m.cli, id, false, []string{"git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "--allow-empty", "-m", "guest-control"})
	cmd := exec.CommandContext(ctx, "git", "-C", repo, "log", "--all", "-1", "--format=%s")
	if out, err := cmd.Output(); err != nil || strings.TrimSpace(string(out)) != "guest-control" {
		t.Fatalf("host Git control: %s %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(wt, "json.py"), []byte("raise RuntimeError('workspace import executed')"), 0600); err != nil {
		t.Fatal(err)
	}
	var listing bytes.Buffer
	if err := m.ListFiles(ctx, id, ".", &listing); err != nil || !strings.Contains(listing.String(), "json.py") {
		t.Fatalf("isolated file helper: %s %v", listing.String(), err)
	}
	probe := `import os,errno,fcntl
assert os.stat("/tmp").st_dev == os.stat("/").st_dev, "quota probe must use the writable rootfs, not tmpfs"
open('/tmp/oaiso-rootfs-quota-marker','w').write('retained')
f=open('/tmp/oaiso-rootfs-quota-probe','wb')
# Attempt to clear the host project identity through the guest filesystem.
# Regardless of ioctl support, allocation must remain bounded afterwards.
try:
 fcntl.ioctl(f.fileno(),0x401c5820,bytes(28))
 print('PROJECT_RESET_IOCTL_ACCEPTED')
except OSError as e:
 print('PROJECT_RESET_IOCTL_DENIED',e.errno)
try:
 try:
  os.posix_fallocate(f.fileno(),0,5<<30)
 except OSError as e:
  assert e.errno in (errno.ENOSPC,errno.EDQUOT),e
 else:raise AssertionError('rootfs exceeded 4 GiB ceiling')
finally:
 f.close();os.unlink('/tmp/oaiso-rootfs-quota-probe')
print('ROOTFS_QUOTA_ENFORCED')`
	if out := kataExec(t, ctx, m.cli, id, false, []string{"python3", "-I", "-c", probe}); !strings.Contains(out, "ROOTFS_QUOTA_ENFORCED") {
		t.Fatal(out)
	}
	if err := m.Stop(id, 0); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(requestDir, "complete"))
	if err := os.WriteFile(filepath.Join(requestDir, "request"), []byte("restart "+id+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(requestDir, "complete")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if err := m.StartContext(ctx, id); err != nil {
		t.Fatal("quota restart/recovery:", err)
	}
	kataExec(t, ctx, m.cli, id, false, []string{"sh", "-c", "test $(cat /tmp/oaiso-rootfs-quota-marker) = retained"})
	if out := kataExec(t, ctx, m.cli, id, false, []string{"python3", "-I", "-c", probe}); !strings.Contains(out, "ROOTFS_QUOTA_ENFORCED") {
		t.Fatal(out)
	}
}
