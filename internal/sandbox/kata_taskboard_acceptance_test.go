package sandbox_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/taskboard"
)

// The installed CLI uses real admission, quota, proxy and Kata paths. Shell
// tasks are deterministic fixtures; this does not assert external AI inference.
func TestKataTaskBoard(t *testing.T) {
	if os.Getenv("CODEX_DOCK_KATA_E2E") != "1" {
		t.Skip("requires disposable Linux/KVM setup acceptance")
	}
	binary := os.Getenv("CODEX_DOCK_KATA_TEST_BINARY")
	if binary == "" {
		t.Fatal("matching CLI binary is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	root, e := os.MkdirTemp("/var/tmp", "tb-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	project := filepath.Join(root, "project")
	store := filepath.Join(root, "private")
	if e = os.Mkdir(project, 0700); e != nil {
		t.Fatal(e)
	}
	git := func(args ...string) string {
		t.Helper()
		c := exec.CommandContext(ctx, "git", args...)
		c.Dir = project
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
		b, e := c.CombinedOutput()
		if e != nil {
			t.Fatalf("git: %v %s", e, b)
		}
		return strings.TrimSpace(string(b))
	}
	git("init", "-b", "main")
	if e = os.WriteFile(filepath.Join(project, "base.txt"), []byte("base\n"), 0600); e != nil {
		t.Fatal(e)
	}
	git("add", ".")
	git("commit", "-m", "base")
	node, e := engine.NewClient()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = node.Close() })
	previous, e := node.ContainerList(ctx, container.ListOptions{All: true})
	if e != nil {
		t.Fatal(e)
	}
	ownedBefore := map[string]bool{}
	for _, v := range previous {
		ownedBefore[v.ID] = true
	}
	var taskID string
	cli := func(args ...string) []byte {
		t.Helper()
		c := exec.CommandContext(ctx, binary, append([]string{"task", "--store", store}, args...)...)
		var stderr strings.Builder
		c.Stderr = &stderr
		b, e := c.Output()
		if e != nil {
			t.Fatalf("task %v: %v %s %s", args, e, stderr.String(), b)
		}
		return b
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		records, _ := (taskboard.Store{Root: store}).List()
		for _, v := range records {
			for _, a := range v.Attempts {
				for _, name := range []string{a.Name, a.TestName} {
					if name != "" {
						if _, err := node.ContainerInspect(clean, name); err != nil {
							if !errdefs.IsNotFound(err) {
								t.Error(err)
							}
							continue
						}
						c := exec.CommandContext(clean, binary, "rm", "--force", name)
						if b, e := c.CombinedOutput(); e != nil {
							t.Errorf("worker cleanup: %v %s", e, b)
						}
					}
				}
			}
		}
		current, e := node.ContainerList(clean, container.ListOptions{All: true})
		if e != nil {
			t.Error(e)
			return
		}
		for _, v := range current {
			if ownedBefore[v.ID] {
				continue
			}
			for _, name := range v.Names {
				if name == "/codex-auth-proxy" || name == "/codex-http-proxy" || name == "codex-auth-proxy" || name == "codex-http-proxy" {
					seconds := 10
					if err := node.ContainerStop(clean, v.ID, container.StopOptions{Timeout: &seconds}); err != nil {
						t.Error(err)
					}
					if err := node.ContainerRemove(clean, v.ID, container.RemoveOptions{Force: true}); err != nil {
						t.Error(err)
					}
				}
			}
		}
	})
	prepare := exec.CommandContext(ctx, binary, "init", "--agent", "codex")
	prepare.Dir = project
	if output, err := prepare.CombinedOutput(); err != nil {
		t.Fatalf("prepare task prerequisites: %v %s", err, output)
	}
	var task taskboard.Task
	if e = json.Unmarshal(cli("create", "--title", "Kata product task", "--project", project, "--agent", "shell", "--image", os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE"), "--no-internet", "--prompt", "printf 'reviewed-result\\n' > /workspace/result.txt; sleep 2", "--test-command", "test \"$(cat /workspace/result.txt)\" = reviewed-result"), &task); e != nil {
		t.Fatal(e)
	}
	taskID = task.ID
	cli("run", taskID)
	wait := func(test bool) taskboard.Task {
		t.Helper()
		for {
			var v taskboard.Task
			if e := json.Unmarshal(cli("sync", taskID), &v); e != nil {
				t.Fatal(e)
			}
			a, ok := taskboard.Current(v)
			if ok {
				if !test && v.State == "review" {
					return v
				}
				if test && a.TestState == "passed" {
					return v
				}
				if v.State == "failed" || a.TestState == "failed" {
					t.Fatalf("worker failed: %+v", v)
				}
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	task = wait(false)
	a, _ := taskboard.Current(task)
	b, e := os.ReadFile(filepath.Join(a.Workspace, "result.txt"))
	if e != nil || string(b) != "reviewed-result\n" {
		t.Fatalf("task output: %q %v", b, e)
	}
	cli("test", taskID)
	wait(true)
	var review taskboard.Review
	if e = json.Unmarshal(cli("review", taskID), &review); e != nil {
		t.Fatal(e)
	}
	if len(review.Changes) != 1 {
		t.Fatalf("unexpected review: %+v", review)
	}
	// Review results are adopted without touching dirty owner files.
	if e = os.WriteFile(filepath.Join(project, "base.txt"), []byte("owner dirty\n"), 0600); e != nil {
		t.Fatal(e)
	}
	planPath := filepath.Join(root, "approval", "plan.json")
	cli("plan", taskID, "--review", review.ID, "--target", "main", "--branch", "accepted-result", "--plan-file", planPath)
	cli("accept", taskID, "--plan-file", planPath, "--yes")
	if git("show", "accepted-result:result.txt") != "reviewed-result" {
		t.Fatal("result branch has wrong output")
	}
	b, _ = os.ReadFile(filepath.Join(project, "base.txt"))
	if string(b) != "owner dirty\n" || git("branch", "--show-current") != "main" {
		t.Fatal("owner checkout changed")
	}
	t.Log("verified: CLI task queue → guarded Kata batch task → separate Kata test → frozen review → preview → approved new branch; dirty owner checkout retained")
	// A second task covers stop and retry without replaying the successful result.
	if e = json.Unmarshal(cli("create", "--title", "Stop retry", "--project", project, "--agent", "shell", "--image", os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE"), "--no-internet", "--prompt", "sleep 120"), &task); e != nil {
		t.Fatal(e)
	}
	cli("run", task.ID)
	cli("stop", task.ID)
	if e = json.Unmarshal(cli("retry", task.ID), &task); e != nil {
		t.Fatal(e)
	}
	if len(task.Attempts) != 2 {
		t.Fatal("retry did not preserve attempt history")
	}
	cli("stop", task.ID)
	t.Log("verified: stop/retry preserves first attempt and creates a new Kata worker")
}
