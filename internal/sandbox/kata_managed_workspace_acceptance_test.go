package sandbox_test

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/managedworkspace"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
)

//go:embed testdata/sandbox-managed-probe.py
var sandboxManagedProbe string

func TestKataManagedWorkspace(t *testing.T) {
	if os.Getenv("CODEX_DOCK_KATA_E2E") != "1" {
		t.Skip("requires disposable Linux/KVM setup acceptance")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Minute)
	defer cancel()
	binary, image := os.Getenv("CODEX_DOCK_KATA_TEST_BINARY"), os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE")
	if binary == "" || image == "" {
		t.Fatal("matching CLI and image required")
	}
	root, err := os.MkdirTemp("/var/tmp", "managed-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	project := filepath.Join(root, "project")
	os.Mkdir(project, 0700)
	for p, data := range map[string]string{"tracked.txt": "uncommitted 日本語\n", "untracked.txt": "not in git\n", ".env": "fixture-secret-must-not-copy"} {
		os.WriteFile(filepath.Join(project, p), []byte(data), 0600)
	}
	os.Symlink("/etc/passwd", filepath.Join(project, "escape"))
	// A real dirty Git source proves this mode copies current content rather
	// than committed HEAD. The copied workspace deliberately has no .git.
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", project}, args...)...)
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("git %v %s", e, out)
		}
	}
	git("init", "-q")
	git("config", "user.name", "fixture")
	git("config", "user.email", "fixture@example.invalid")
	git("add", "tracked.txt")
	git("commit", "-qm", "fixture")
	os.WriteFile(filepath.Join(project, "tracked.txt"), []byte("dirty current 日本語\n"), 0600)
	config := filepath.Join(root, "config")
	store := sandboxux.EnvironmentStore{Path: filepath.Join(config, "codex-dock", "sandbox-environments", "config.json")}
	saved, e := store.Load()
	if e != nil {
		t.Fatal(e)
	}
	saved.File.Environments["fixture"] = sandboxux.Environment{Image: image, Profile: "compact", Network: "offline", Workspace: "managed", WorkspaceBytes: 64 << 20, WorkspaceInodes: 1024}
	if e = store.Save(saved.Revision, saved.File); e != nil {
		t.Fatal(e)
	}
	m, e := sandbox.NewManager(sandbox.ManagerConfig{})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.Close() })
	api := engine.WorkspaceClient{}
	var names, workspaceIDs, pointIDs []string
	t.Cleanup(func() {
		clean, done := context.WithTimeout(context.Background(), 2*time.Minute)
		defer done()
		for _, name := range names {
			_ = m.RemoveContext(clean, name, true)
		}
		for _, id := range pointIDs {
			if e := api.RemovePoint(clean, id); e != nil {
				t.Error(e)
			}
		}
		for _, id := range workspaceIDs {
			if e := api.Remove(clean, id); e != nil && !strings.Contains(e.Error(), "404") {
				t.Error(e)
			}
		}
	})
	name := "mw-" + filepath.Base(root)
	names = append(names, name)
	probe := func(mode, name string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "python3", "-c", terminalScreenProbe+"\n"+sandboxManagedProbe, binary, project, name, config, mode)
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("managed TUI %s: %v\n%s", mode, e, out)
		} else {
			t.Log(strings.TrimSpace(string(out)))
		}
	}
	probe("create", name)
	source, e := m.InspectContext(ctx, name)
	if e != nil {
		t.Fatal(e)
	}
	info, e := m.ManagedWorkspaceInfo(ctx, source.ID)
	if e != nil || info == nil {
		t.Fatal(info, e)
	}
	workspaceIDs = append(workspaceIDs, info.ID)
	run := func(id, program string) {
		t.Helper()
		var output bytes.Buffer
		code, e := m.ExecWithOptions(ctx, id, sandbox.ExecOptions{Argv: []string{"python3", "-I", "-c", program}, Stdout: &output, Stderr: &output})
		if e != nil || code != 0 {
			t.Fatalf("guest %d %v %s", code, e, &output)
		}
	}
	run(source.ID, `import os,pathlib,errno
p=pathlib.Path('/workspace');assert (p/'tracked.txt').read_text()=='dirty current 日本語\n';assert (p/'untracked.txt').read_text()=='not in git\n'
for n in ['.env','.git','escape']:assert not (p/n).exists(),n
assert os.getuid()!=0
s=os.statvfs('/workspace');assert s.f_blocks*s.f_frsize<=67108864 and s.f_files<=1024
f=p/'fill'
try:
 with f.open('wb') as out:
  for i in range(100):out.write(b'x'*1048576)
 raise AssertionError('capacity was not enforced')
except OSError as e:assert e.errno==errno.ENOSPC
finally:f.unlink(missing_ok=True)
made=[]
try:
 for i in range(2000):
  f=p/('inode-'+str(i));f.touch();made.append(f)
 raise AssertionError('inode ceiling was not enforced')
except OSError as e:assert e.errno==errno.ENOSPC
finally:
 for f in made:f.unlink()
(p/'tracked.txt').write_text('saved guest 日本語\n');(p/'new.txt').write_text('guest addition\n')
assert pathlib.Path('/entrypoint.sh').is_file();assert pathlib.Path('/etc/os-release').is_file()
print('MANAGED_CAPACITY_INODES_PASS')`)
	if _, e = api.SavePoint(ctx, managedworkspace.SaveOptions{Worker: source.ID, Name: "running", IncludeWorkspace: true}); e == nil {
		t.Fatal("running point saved")
	}
	if e = api.Remove(ctx, info.ID); e == nil {
		t.Fatal("leased workspace removed")
	}

	changes, e := m.ManagedWorkspaceChanges(ctx, source.ID)
	if e != nil || !strings.Contains(changes, "M tracked.txt") || !strings.Contains(changes, "A new.txt") {
		t.Fatal("managed changes", changes, e)
	}
	selected := sandbox.ArtifactOptions{Paths: []string{"tracked.txt", "new.txt"}}
	artifact, e := m.PlanArtifacts(ctx, source.ID, selected)
	if e != nil || !strings.HasPrefix(artifact.Head, "workspace:") || artifact.PatchBytes != 0 {
		t.Fatal("non-Git artifact plan", artifact, e)
	}
	if e = sandboxux.ExportReviewedArtifacts(ctx, m, source.ID, selected, artifact, filepath.Join(root, "managed-results.tar")); e != nil {
		t.Fatal("non-Git export", e)
	}
	if e = m.StopContext(ctx, source.ID, 5); e != nil {
		t.Fatal(e)
	}
	probe("save", name)
	savedPoints, e := api.Points(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var point managedworkspace.RecoveryPoint
	for _, p := range savedPoints {
		if p.Name == "managed-recovery" && p.Source == source.ID {
			point = p
			pointIDs = append(pointIDs, p.ID)
		}
	}
	if point.State != "ready" || point.Workspace == "" || point.Checkpoint.ID == "" {
		t.Fatal(point)
	}
	if e = api.Remove(ctx, point.Workspace); e == nil {
		t.Fatal("point snapshot deleted independently")
	}
	restartNodeForCheckpoint(t, ctx)
	points, e := api.Points(ctx)
	if e != nil || len(points) == 0 {
		t.Fatal(points, e)
	}
	restoredName := name + "-new"
	names = append(names, restoredName)
	probe("restore", restoredName)
	restored, e := m.InspectContext(ctx, restoredName)
	if e != nil || restored.ID == source.ID {
		t.Fatal(restored, e)
	}
	newInfo, e := m.ManagedWorkspaceInfo(ctx, restored.ID)
	if e != nil || newInfo == nil || newInfo.ID == info.ID || newInfo.ID == point.Workspace {
		t.Fatal(newInfo, e)
	}
	workspaceIDs = append(workspaceIDs, newInfo.ID)
	run(restored.ID, `import pathlib,os;p=pathlib.Path('/workspace');assert (p/'tracked.txt').read_text()=='saved guest 日本語\n';assert (p/'new.txt').read_text()=='guest addition\n';assert pathlib.Path('/entrypoint.sh').is_file();assert pathlib.Path('/etc/os-release').is_file();(p/'tracked.txt').write_text('new VM change');assert os.getuid()!=0;print('MANAGED_RESTORE_PASS')`)
	if e = api.RemovePoint(ctx, point.ID); e == nil {
		t.Fatal("point rootfs used by retained VM deleted")
	}
	if e = m.RemoveContext(ctx, restored.ID, true); e != nil {
		t.Fatal(e)
	}
	if e = m.StartContext(ctx, source.ID); e != nil {
		t.Fatal(e)
	}
	run(source.ID, `assert open('/workspace/tracked.txt').read()=='saved guest 日本語\n';print('SOURCE_PRESERVED_PASS')`)
	if e = m.RemoveContext(ctx, source.ID, true); e != nil {
		t.Fatal(e)
	}
	retained, e := api.Inspect(ctx, info.ID)
	if e != nil || retained.Lease != "" || retained.State != "ready" {
		t.Fatal(retained, e)
	}
	if got, _ := os.ReadFile(filepath.Join(project, "tracked.txt")); string(got) != "dirty current 日本語\n" {
		t.Fatal("host source changed", string(got))
	}
	if out, e := exec.CommandContext(ctx, binary, "workspace", "list").CombinedOutput(); e != nil || !json.Valid(out) {
		t.Fatal("CLI catalog", e, string(out))
	}
	t.Logf("verified: TUI reviewed current-file import/cancel, dirty/untracked source, exclusions, %d-byte/%d-inode kernel ceilings, retained lease, paired rootfs/workspace save, real node restart, TUI new-VM restore, separate data, original host/VM preservation and explicit cleanup", info.Options.Bytes, info.Options.Inodes)
}
