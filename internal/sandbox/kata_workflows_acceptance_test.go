package sandbox_test

import (
	"archive/tar"
	"bytes"
	"context"
	_ "embed"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
)

//go:embed testdata/sandbox-workflows-probe.py
var sandboxWorkflowsProbe string

//go:embed testdata/terminal_screen.py
var terminalScreenProbe string

func TestKataSandboxWorkflows(t *testing.T) {
	if os.Getenv("CODEX_DOCK_KATA_E2E") != "1" {
		t.Skip("requires disposable Linux/KVM setup acceptance")
	}
	binary, image := os.Getenv("CODEX_DOCK_KATA_TEST_BINARY"), os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE")
	if binary == "" || image == "" {
		t.Fatal("matching CLI and worker image are required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("/var/tmp", "workflows-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	project, copyDir, configDir, archive := filepath.Join(root, "project"), filepath.Join(root, "copy"), filepath.Join(root, "config"), filepath.Join(root, "result.tar")
	if err = os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", project}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	git("init", "-q")
	git("config", "user.name", "fixture")
	git("config", "user.email", "fixture@example.invalid")
	if err = os.WriteFile(filepath.Join(project, "tracked.txt"), []byte("committed base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "tracked.txt")
	git("commit", "-qm", "base")
	if err = os.WriteFile(filepath.Join(project, "tracked.txt"), []byte("host uncommitted\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(project, "untracked.txt"), []byte("host only\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store := sandboxux.EnvironmentStore{Path: filepath.Join(configDir, "codex-dock", "sandbox-environments", "config.json")}
	snapshot, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.File.Environments["fixture"] = sandboxux.Environment{Image: image, Profile: "compact", Network: "offline", Workspace: "copy", LifecycleSet: true, Lifecycle: workerpolicy.Lifecycle{MaxLifetime: 2 * time.Hour, Retention: 24 * time.Hour, ProtectMain: true}}
	if err = store.Save(snapshot.Revision, snapshot.File); err != nil {
		t.Fatal(err)
	}
	name := "workflow-" + filepath.Base(root)
	m, err := sandbox.NewManager(sandbox.ManagerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	t.Cleanup(func() {
		clean, done := context.WithTimeout(context.Background(), time.Minute)
		defer done()
		if _, e := m.InspectContext(clean, name); errdefs.IsNotFound(e) {
			return
		} else if e != nil {
			t.Error(e)
			return
		}
		if e := m.RemoveContext(clean, name, true); e != nil {
			t.Error(e)
		}
	})
	command := exec.CommandContext(ctx, "python3", "-c", terminalScreenProbe+"\n"+sandboxWorkflowsProbe, binary, project, name, copyDir, configDir, archive)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("public workflow TUI: %v\n%s", err, output)
	}
	t.Log(strings.TrimSpace(string(output)))
	worker, err := m.InspectContext(ctx, name)
	if err != nil || worker.Status != "running" {
		t.Fatal(worker, err)
	}
	if got, _ := os.ReadFile(filepath.Join(project, "tracked.txt")); string(got) != "host uncommitted\n" {
		t.Fatal("guest changed original project", string(got))
	}
	if _, err = os.Stat(filepath.Join(copyDir, "untracked.txt")); !os.IsNotExist(err) {
		t.Fatal("untracked source file copied", err)
	}
	if got, _ := os.ReadFile(filepath.Join(copyDir, "tracked.txt")); string(got) != "guest change 日本語\n" {
		t.Fatal(string(got))
	}
	f, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	reader := tar.NewReader(f)
	found := map[string]string{}
	for {
		header, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(io.LimitReader(reader, 1<<20))
		if e != nil {
			t.Fatal(e)
		}
		found[header.Name] = string(data)
	}
	if found["files/tracked.txt"] != "guest change 日本語\n" || found["files/new.txt"] != "new guest file\n" || !strings.Contains(found["changes.patch"], "guest change") {
		t.Fatal("artifact export missed reviewed files/diff", found)
	}
	options := sandbox.ArtifactOptions{Paths: []string{"tracked.txt"}}
	plan, err := m.PlanArtifacts(ctx, worker.ID, options)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code, err := m.ExecWithOptions(ctx, worker.ID, sandbox.ExecOptions{Argv: []string{"python3", "-I", "-c", "open('/workspace/tracked.txt','w').write('changed after plan\\n')"}, Stdout: &out, Stderr: &out})
	if err != nil || code != 0 {
		t.Fatal(code, err, out.String())
	}
	stale := filepath.Join(root, "stale.tar")
	if err = sandboxux.ExportReviewedArtifacts(ctx, m, worker.ID, options, plan, stale); err == nil {
		t.Fatal("stale plan exported")
	}
	if _, err = os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("partial stale export published", err)
	}
	life, err := m.InspectLifecycle(ctx, worker.ID)
	if err != nil || life.Policy.MaxLifetime != 2*time.Hour || !life.Policy.ProtectMain {
		t.Fatal(life, err)
	}
	usage, err := m.RootFSUsage(ctx, worker.ID)
	if err != nil || !usage.Available || usage.LimitBytes != 4<<30 {
		t.Fatal(usage, err)
	}
	unapproved := []sandbox.ReapAction{{Worker: worker.ID, Action: "remove", Reason: "retention"}}
	if applied, err := m.ApplyReapActions(ctx, life.CreatedAt.Add(3*time.Hour), unapproved); err == nil || len(applied) != 0 {
		t.Fatal("changed lifecycle action applied", applied, err)
	}
	if current, err := m.InspectContext(ctx, worker.ID); err != nil || current.Status != "running" {
		t.Fatal("unapproved action changed VM", current, err)
	}
	// Artificial time advances only the selected worker's lifecycle decision.
	actions, err := m.ReapWorkers(ctx, life.CreatedAt.Add(3*time.Hour), true, []string{worker.ID})
	if err != nil || len(actions) != 1 || actions[0].Worker != worker.ID || actions[0].Action != "stop" {
		t.Fatal(actions, err)
	}
	if actions, err = m.ApplyReapActions(ctx, life.CreatedAt.Add(3*time.Hour), actions); err != nil || len(actions) != 1 {
		t.Fatal(actions, err)
	}
	if err = m.RemoveContext(ctx, worker.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(copyDir, "tracked.txt")); err != nil {
		t.Fatal("VM removal deleted independent copy", err)
	}
	t.Log("verified: public TUI loads/reviews a private environment; independent committed copy; original/untracked source preserved; scoped supervision toggle; host quota; guest diff and reviewed selected export; stale plan rejection; UI exit retains VM; selected expiry and VM removal retain independent copy")
}
