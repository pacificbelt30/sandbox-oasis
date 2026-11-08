package sandbox_test

import (
	"archive/tar"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
)

//go:embed testdata/workbench-probe.py
var workbenchProbe string

func TestKataWorkbenchFeatures(t *testing.T) {
	if os.Getenv("CODEX_DOCK_KATA_E2E") != "1" {
		t.Skip("requires disposable Linux/KVM acceptance")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	binary, image := os.Getenv("CODEX_DOCK_KATA_TEST_BINARY"), os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE")
	if binary == "" || image == "" {
		t.Fatal("matching binary and image required")
	}
	root, err := os.MkdirTemp("/var/tmp", "workbench-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	project := filepath.Join(root, "project")
	if err = os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", project}, args...)...)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatal(e, string(out))
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("config", "user.name", "fixture")
	git("config", "user.email", "fixture@example.invalid")
	if err = os.WriteFile(filepath.Join(project, "tracked.txt"), []byte("launch base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "tracked.txt")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	m, err := sandbox.NewManager(sandbox.ManagerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	name := "bench-" + filepath.Base(root)
	id, err := m.RunContext(ctx, sandbox.RunOptions{Name: name, Image: image, ProjectDir: project, ResourceProfile: "compact", ContainerUser: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), NoInternet: true, ExecCmd: "exec sleep infinity", Detach: true, Lifecycle: workerpolicy.Lifecycle{MaxLifetime: time.Hour, ProtectMain: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		clean, stop := context.WithTimeout(context.Background(), time.Minute)
		defer stop()
		if e := m.RemoveContext(clean, id, true); e != nil {
			t.Error(e)
		}
	}()
	captured, err := m.WorkspaceBaseline(ctx, id)
	if err != nil || captured != base {
		t.Fatal("baseline before release", captured, base, err)
	}
	archive := filepath.Join(root, "reviewed.tar")
	targetIndex := 0
	probe := func(phase string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "python3", "-c", terminalScreenProbe+"\n"+workbenchProbe, binary, project, name, archive, phase, fmt.Sprint(targetIndex))
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("workbench TUI %s: %v\n%s", phase, e, out)
		}
		t.Log(strings.TrimSpace(string(out)))
	}
	probe("start")
	jobs, err := m.ListJobs(ctx, id)
	if err != nil || len(jobs) != 1 || !jobs[0].Running {
		t.Fatal("TUI exit did not retain job", jobs, err)
	}
	original := jobs[0].ID
	other, e := m.StartJob(ctx, id, sandbox.ExecOptions{Argv: []string{"python3", "-u", "-c", "import time; print('OTHER_JOB',flush=True); time.sleep(300)"}})
	if e != nil {
		t.Fatal(e)
	}
	if other.ID < original {
		targetIndex = 1
	}
	var output bytes.Buffer
	code, err := m.ExecWithOptions(ctx, id, sandbox.ExecOptions{Argv: []string{"/bin/sh", "-c", "printf 'COMMITTED_AFTER_LAUNCH 日本語\\n' > tracked.txt && git -c commit.gpgsign=false commit -qam guest && printf 'untracked result\\n' > 'new 日本語.txt'"}, Stdout: &output, Stderr: &output})
	if err != nil || code != 0 {
		t.Fatal(code, err, output.String())
	}
	current, err := sandboxux.CompareWorkspace(ctx, m, id, "HEAD")
	if err != nil || len(current.Files) != 1 || current.Files[0].Status != "?" {
		t.Fatal("HEAD compatibility", current, err)
	}
	changed, err := sandboxux.CompareWorkspace(ctx, m, id, base)
	if err != nil || len(changed.Files) != 2 {
		t.Fatal("launch comparison", changed, err)
	}
	probe("finish")
	jobs, err = m.ListJobs(ctx, id)
	if err != nil || len(jobs) != 2 {
		t.Fatal("job list", jobs, err)
	}
	for _, j := range jobs {
		if j.ID == original && j.Running || j.ID == other.ID && !j.Running {
			t.Fatal("selected stop affected wrong job", jobs)
		}
	}
	file, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	reader := tar.NewReader(file)
	found := false
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
		if header.Name == "manifest.json" {
			var plan sandbox.ArtifactPlan
			if e = json.Unmarshal(data, &plan); e != nil || plan.Base != base {
				t.Fatal("artifact comparison base", plan, e)
			}
		}
		if header.Name == "changes.patch" {
			found = bytes.Contains(data, []byte("COMMITTED_AFTER_LAUNCH"))
		}
	}
	if !found {
		t.Fatal("committed launch diff missing from reviewed archive")
	}
	observed, err := m.Diagnostics(ctx, id)
	if err != nil || observed.Resources.CPUUsec == nil || observed.Resources.MemoryBytes == nil {
		t.Fatal("host metrics", observed, err)
	}
	life, err := m.InspectLifecycle(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	actions, err := m.ReapWorkers(ctx, life.CreatedAt.Add(2*time.Hour), true, []string{id})
	if err != nil || len(actions) != 1 || actions[0].Reason != "max_lifetime" {
		t.Fatal(actions, err)
	}
	if _, err = m.ApplyReapActions(ctx, life.CreatedAt.Add(2*time.Hour), actions); err != nil {
		t.Fatal(err)
	}
	observed, err = m.Diagnostics(ctx, id)
	if err != nil || observed.Running || observed.StopReason != "max_lifetime" || observed.Resources.MemoryBytes == nil {
		t.Fatal("retained stop diagnostics", observed, err)
	}
	if err = m.StartContext(ctx, id); err != nil {
		t.Fatal(err)
	}
	captured, err = m.WorkspaceBaseline(ctx, id)
	if err != nil || captured != base {
		t.Fatal("restart replaced baseline", captured, err)
	}
	observed, err = m.Diagnostics(ctx, id)
	if err != nil || !observed.Running || observed.StopReason != "" {
		t.Fatal("restart retained stale reason", observed, err)
	}
	t.Log("verified: pre-release immutable baseline, preserved current HEAD comparison, committed/untracked review and selected artifact export, TUI background start and exit/reopen without stop, selected job termination, live host metrics, retained deadline stop evidence, restart preserves baseline and clears old stop reason")
}
