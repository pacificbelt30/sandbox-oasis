package sandbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	dn "github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/pkg/cxd"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

func TestKataDiskCheckpoint(t *testing.T) {
	if os.Getenv("CODEX_DOCK_KATA_E2E") != "1" {
		t.Skip("requires disposable matching Kata node")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	node, err := engine.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close() })

	caseID := fmt.Sprintf("checkpoint-%d", time.Now().UnixNano())
	netw, err := node.NetworkCreate(ctx, caseID, dn.CreateOptions{Driver: "bridge"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := node.NetworkRemove(context.Background(), netw.ID); err != nil {
			t.Error(err)
		}
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	adminSecret := "checkpoint-private-admin-fixture-at-least-32-bytes"
	adminPort := nat.Port("18081/tcp")
	proxy, err := node.ContainerCreate(ctx, &container.Config{Image: os.Getenv("CODEX_DOCK_KATA_PROXY_IMAGE"), User: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), Env: []string{"OPENAI_API_KEY=fixture-checkpoint-host-key-must-not-copy", "HOME=/tmp/checkpoint-proxy-home"}, Cmd: []string{"proxy", "serve", "--role", "auth", "--listen", "0.0.0.0:18080", "--admin-listen", "egress:18081", "--admin-secret", adminSecret}, Labels: map[string]string{"io.codex-dock.proxy-layout": "checkpoint-acceptance"}, ExposedPorts: nat.PortSet{adminPort: {}}}, &container.HostConfig{Runtime: "runc", NetworkMode: container.NetworkMode(netw.ID), CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}, PortBindings: nat.PortMap{adminPort: {{HostIP: "127.0.0.1", HostPort: strconv.Itoa(port)}}}}, nil, nil, caseID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := node.ContainerRemove(context.Background(), proxy.ID, container.RemoveOptions{Force: true}); err != nil {
			t.Error(err)
		}
	})
	if err := node.ContainerStart(ctx, proxy.ID, container.StartOptions{}); err != nil {
		t.Fatal(err)
	}
	adminURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	containerURL := "http://" + caseID + ":18080"
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	client, err := cxd.Open(ctx, cxd.Options{NodeSocket: engine.Socket(), ProxyAdminURL: adminURL, ProxyContainerURL: containerURL, ProxyAdminSecret: adminSecret, StateDir: filepath.Join(dir, "state")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	for attempt := 0; ; attempt++ {
		if _, err := client.ProxyCapabilities(ctx); err == nil {
			break
		}
		if attempt == 100 {
			t.Fatal("proxy did not become ready")
		}
		time.Sleep(50 * time.Millisecond)
	}
	project, shared := t.TempDir(), t.TempDir()
	_ = os.WriteFile(filepath.Join(project, "old-workspace"), []byte("workspace-must-not-copy"), 0600)
	_ = os.WriteFile(filepath.Join(shared, "old-shared"), []byte("shared-must-not-copy"), 0600)
	request := cxd.LaunchRequest{RequestID: caseID + "-source", ProjectDir: project, Image: os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE"), NoInternet: true, Profile: "small", User: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), Filesystem: cxd.FilesystemPolicy{DenyRead: []string{"/workspace/old-workspace"}}, Identity: identity.Identity{CaseID: caseID, RunID: "source", ConnectionVersion: "1", Provider: "openai"}, Mounts: []cxd.Mount{{Source: shared, Target: "/opt/external"}}}
	source, err := client.Launch(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Remove(context.Background(), source.ID, true); err != nil {
			t.Error(err)
		}
	})
	run := func(worker, script string) {
		t.Helper()
		var output bytes.Buffer
		code, err := client.Exec(ctx, worker, cxd.ExecRequest{Argv: []string{"python3", "-c", script}, Stdout: &output, Stderr: &output})
		if err != nil || code != 0 {
			t.Fatalf("guest check: exit=%d err=%v %s", code, err, &output)
		}
	}
	run(source.ID, `import os,pathlib
p=pathlib.Path('/opt/disk-state');(p/'keep').write_text('disk-before-checkpoint');(p/'private').write_text('custom-secret-must-not-copy')
h=pathlib.Path('/home/node/.codex');h.mkdir(parents=True,exist_ok=True);(h/'auth.json').write_text('home-secret-must-not-copy')
os.link(h/'auth.json',p/'secret-alias')
os.symlink('keep',p/'link')
pathlib.Path('/tmp/temporary').write_text('temporary-must-not-copy')`)
	if _, err := client.Checkpoint(ctx, source.ID, cxd.CheckpointOptions{}); err == nil {
		t.Fatal("running source checkpoint accepted")
	}
	if err := client.Stop(ctx, source.ID, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	before, err := client.Checkpoints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Checkpoint(ctx, source.ID, cxd.CheckpointOptions{MaxBytes: 1 << 20}); err == nil {
		t.Fatal("oversized checkpoint accepted")
	}
	after, err := client.Checkpoints(ctx)
	if err != nil || len(after.Items) != len(before.Items) {
		t.Fatal("failed checkpoint published", after, err)
	}
	checkpoint, err := client.Checkpoint(ctx, source.ID, cxd.CheckpointOptions{Exclude: []string{"/opt/disk-state/private", "/opt/disk-state/masked"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.RemoveCheckpoint(context.Background(), checkpoint.ID); err != nil && !strings.Contains(err.Error(), "404") {
			t.Error(err)
		}
	})
	if checkpoint.Bytes <= 1<<20 || checkpoint.Digest == "" {
		t.Fatal("invalid checkpoint", checkpoint)
	}
	// Persisted metadata and OCI content must survive a real node restart.
	restartNodeForCheckpoint(t, ctx)
	inspected, err := client.InspectCheckpoint(ctx, checkpoint.ID)
	if err != nil || inspected.Digest != checkpoint.Digest {
		t.Fatal("checkpoint lost on node restart", err)
	}
	if err := client.Start(ctx, source.ID); err != nil {
		t.Fatal(err)
	}
	run(source.ID, `import pathlib
p=pathlib.Path('/opt/disk-state/keep');assert p.read_text()=='disk-before-checkpoint';p.write_text('source-after-checkpoint')`)
	if err := client.Stop(ctx, source.ID, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	request.RequestID = caseID + "-restored"
	request.ProjectDir = t.TempDir()
	request.Identity.RunID = "restored"
	request.Image = ""
	request.Mounts = nil
	request.Filesystem = cxd.FilesystemPolicy{}
	restored, err := client.RestoreCheckpoint(ctx, checkpoint.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Remove(context.Background(), restored.ID, true) })
	if restored.ID == source.ID {
		t.Fatal("restore reused execution identity")
	}
	run(restored.ID, `import os,pathlib
p=pathlib.Path('/opt/disk-state');assert (p/'keep').read_text()=='disk-before-checkpoint';assert (p/'link').read_text()=='disk-before-checkpoint'
for f in ['/opt/disk-state/private','/opt/disk-state/secret-alias','/opt/disk-state/masked','/opt/disk-state/masked-alias','/tmp/temporary','/workspace/old-workspace','/opt/external/old-shared']:
 assert not os.path.exists(f),f
h=pathlib.Path('/home/node/.codex/auth.json');assert not h.exists() or 'home-secret-must-not-copy' not in h.read_text()
assert os.getuid()!=0
print('DISK_CHECKPOINT_RESTORE_PASS')`)
	native, err := node.ImageInspect(ctx, checkpoint.Image)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(native.Config)
	for _, secret := range []string{"fixture-checkpoint-host-key-must-not-copy", "checkpoint-private-admin-fixture-at-least-32-bytes", "CODEX_TOKEN=", "OPENAI_API_KEY="} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("source auth/config copied into image metadata")
		}
	}
	if err := client.RemoveCheckpoint(ctx, checkpoint.ID); err == nil {
		t.Fatal("checkpoint used by retained worker removed")
	}
	binary := os.Getenv("CODEX_DOCK_KATA_TEST_BINARY")
	output, err := exec.CommandContext(ctx, binary, "checkpoint", "inspect", checkpoint.ID).CombinedOutput()
	if err != nil || !bytes.Contains(output, []byte(checkpoint.ID)) {
		t.Fatalf("CLI inspect: %s %v", output, err)
	}
	if err := client.Remove(ctx, restored.ID, true); err != nil {
		t.Fatal(err)
	}
	// Remove is retry-safe at the worker layer; checkpoint removal is explicit.

	cliName := caseID + "-cli"
	cli := func(arguments ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, binary, arguments...)
		command.Env = append(os.Environ(), "CODEX_DOCK_PROXY_ADMIN_SECRET="+adminSecret)
		return command.CombinedOutput()
	}
	t.Cleanup(func() { _, _ = cli("rm", "--force", cliName) })
	output, err = cli("run", "--checkpoint", checkpoint.ID, "--name", cliName, "--project", t.TempDir(), "--proxy-admin-url", adminURL, "--proxy-container-url", containerURL, "--detach", "--keep", "--no-internet", "--exec", "sleep 300")
	if err != nil {
		t.Fatalf("CLI restore: %s %v", output, err)
	}
	cliWorker, err := client.Get(ctx, cliName)
	if err != nil {
		t.Fatal(err)
	}
	run(cliWorker.ID, `import pathlib;assert pathlib.Path('/opt/disk-state/keep').read_text()=='disk-before-checkpoint';print('CLI_CHECKPOINT_RESTORE_PASS')`)
	output, err = cli("rm", "--force", cliName)
	if err != nil {
		t.Fatalf("CLI remove: %s %v", output, err)
	}
	if err := client.RemoveCheckpoint(ctx, checkpoint.ID); err != nil {
		t.Fatal(err)
	}
	t.Log("PASS: stopped full disk capture, byte-limit atomicity, node restart, unchanged source, fresh verified Kata restore, mount/home/tmp/custom-secret exclusions and in-use deletion refusal")
}
func restartNodeForCheckpoint(t *testing.T, ctx context.Context) {
	t.Helper()
	dir := os.Getenv("CODEX_DOCK_NODE_RESTART_REQUEST_DIR")
	if dir == "" {
		t.Fatal("restart controller required")
	}
	done := filepath.Join(dir, "complete")
	_ = os.Remove(done)
	if err := os.WriteFile(filepath.Join(dir, "request"), []byte("restart checkpoint\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		if _, err := os.Stat(done); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	t.Fatal("node restart did not complete")
}
