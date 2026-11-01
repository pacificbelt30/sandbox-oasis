package taskboard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(gitEnv(nil), "GIT_AUTHOR_NAME=Tester", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Tester", "GIT_COMMITTER_EMAIL=test@example.invalid")
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, b)
	}
	return strings.TrimSpace(string(b))
}
func writeTest(t *testing.T, p, s string) {
	t.Helper()
	if e := os.WriteFile(p, []byte(s), 0600); e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T) (*Service, Task) {
	t.Helper()
	root := t.TempDir()
	p := filepath.Join(root, "project")
	if e := os.Mkdir(p, 0700); e != nil {
		t.Fatal(e)
	}
	gitTest(t, p, "init", "-b", "main")
	var content strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&content, "line %02d\n", i)
	}
	writeTest(t, filepath.Join(p, "code.txt"), content.String())
	gitTest(t, p, "add", ".")
	gitTest(t, p, "commit", "-m", "base")
	runtime := &fakeRuntime{states: map[string]RuntimeState{}}
	s := &Service{Store: Store{filepath.Join(root, "private")}, Runtime: runtime}
	task, e := s.Create(t.Context(), Spec{Title: "Feature", Project: p, Prompt: "Implement", Agent: "shell", TestCommand: "true"})
	if e != nil {
		t.Fatal(e)
	}
	task, e = s.Run(t.Context(), task.ID)
	if e != nil {
		t.Fatal(e)
	}
	return s, task
}

type fakeRuntime struct {
	mu         sync.Mutex
	states     map[string]RuntimeState
	inspectErr error
	launchErr  error
	stopErr    error
}

func (f *fakeRuntime) Launch(_ context.Context, _ Task, a Attempt, test bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	name := a.Name
	if test {
		name = a.TestName
	}
	f.states[name] = RuntimeState{Found: true, Running: true, Workspace: a.Workspace, ID: name}
	return name, f.launchErr
}
func (f *fakeRuntime) Inspect(_ context.Context, name string) (RuntimeState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.states[name], f.inspectErr
}
func (f *fakeRuntime) Stop(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopErr != nil {
		return f.stopErr
	}
	v := f.states[id]
	v.Running = false
	f.states[id] = v
	return nil
}
func finish(t *testing.T, s *Service, task Task) {
	t.Helper()
	a, _ := Current(task)
	if e := s.Runtime.Stop(t.Context(), a.Name); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Sync(t.Context(), task.ID); e != nil {
		t.Fatal(e)
	}
}
func TestSelectedHunkAdoptionPreservesDirtyCheckout(t *testing.T) {
	s, task := fixture(t)
	a, _ := Current(task)
	p := filepath.Join(a.Workspace, "code.txt")
	data, _ := os.ReadFile(p)
	changed := strings.Replace(string(data), "line 02", "line 02\nnew first", 1)
	changed = strings.Replace(changed, "line 25", "changed 25", 1)
	writeTest(t, p, changed)
	// Include committed agent output as well as untracked output.
	gitTest(t, a.Workspace, "add", "code.txt")
	gitTest(t, a.Workspace, "commit", "-m", "agent result")
	writeTest(t, filepath.Join(a.Workspace, "new.txt"), "new file\n")
	writeTest(t, filepath.Join(a.Workspace, ".env"), "FAKE_SECRET=excluded\n")
	finish(t, s, task)
	writeTest(t, filepath.Join(task.Spec.Project, "code.txt"), "staged owner edit\n")
	gitTest(t, task.Spec.Project, "add", "code.txt")
	writeTest(t, filepath.Join(task.Spec.Project, "code.txt"), "unstaged owner edit\n")
	before := gitTest(t, task.Spec.Project, "diff", "--cached")
	head := gitTest(t, task.Spec.Project, "rev-parse", "HEAD")
	review, e := s.Review(t.Context(), task.ID)
	if e != nil {
		t.Fatal(e)
	}
	if len(review.Changes) != 2 || len(review.Excluded) != 1 {
		t.Fatalf("changes/exclusions: %+v", review)
	}
	c := review.Changes[0]
	if len(c.Hunks) != 2 {
		t.Fatalf("expected two hunks: %+v", c)
	}
	selection := Selection{c.Path: []string{c.Hunks[1].ID}, "new.txt": nil}
	plan, e := s.Plan(t.Context(), task.ID, review.ID, "main", "result", selection)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Accept(t.Context(), task.ID, plan); e != nil {
		t.Fatal(e)
	}
	result := gitTest(t, task.Spec.Project, "show", "result:code.txt")
	if strings.Contains(result, "new first") || !strings.Contains(result, "changed 25") {
		t.Fatalf("wrong hunks adopted: %s", result)
	}
	if got := gitTest(t, task.Spec.Project, "show", "result:new.txt"); got != "new file" {
		t.Fatal(got)
	}
	if gitTest(t, task.Spec.Project, "rev-parse", "HEAD") != head || gitTest(t, task.Spec.Project, "diff", "--cached") != before {
		t.Fatal("owner HEAD/index changed")
	}
	b, _ := os.ReadFile(filepath.Join(task.Spec.Project, "code.txt"))
	if string(b) != "unstaged owner edit\n" {
		t.Fatal("owner worktree changed")
	}
	if _, e = s.Accept(t.Context(), task.ID, plan); e != nil {
		t.Fatalf("idempotent apply: %v", e)
	}
}
func TestAdoptionRejectsChangedSourceAndTarget(t *testing.T) {
	for _, kind := range []string{"source", "target"} {
		t.Run(kind, func(t *testing.T) {
			s, task := fixture(t)
			a, _ := Current(task)
			writeTest(t, filepath.Join(a.Workspace, "new.txt"), "one\n")
			finish(t, s, task)
			r, e := s.Review(t.Context(), task.ID)
			if e != nil {
				t.Fatal(e)
			}
			p, e := s.Plan(t.Context(), task.ID, r.ID, "main", "result", Selection{"new.txt": nil})
			if e != nil {
				t.Fatal(e)
			}
			if kind == "source" {
				writeTest(t, filepath.Join(a.Workspace, "new.txt"), "two\n")
			} else {
				gitTest(t, task.Spec.Project, "commit", "--allow-empty", "-m", "moved")
			}
			if _, e = s.Accept(t.Context(), task.ID, p); e == nil {
				t.Fatal("stale plan accepted")
			}
			c := exec.Command("git", "show-ref", "--verify", "refs/heads/result")
			c.Dir = task.Spec.Project
			if e = c.Run(); e == nil {
				t.Fatal("result published on failure")
			}
		})
	}
}
func TestReviewAndAdoptionDoNotExecuteGitPrograms(t *testing.T) {
	s, task := fixture(t)
	a, _ := Current(task)
	marker := filepath.Join(t.TempDir(), "executed")
	command := "touch " + marker
	for _, key := range []string{"filter.evil.clean", "filter.evil.smudge", "filter.evil.process", "core.fsmonitor", "diff.external"} {
		gitTest(t, task.Spec.Project, "config", key, command)
	}
	gitTest(t, task.Spec.Project, "config", "filter.evil.required", "true")
	writeTest(t, filepath.Join(a.Workspace, ".gitattributes"), "*.txt filter=evil diff=evil\n")
	writeTest(t, filepath.Join(a.Workspace, "new.txt"), "160000 is ordinary content\n")
	finish(t, s, task)
	r, e := s.Review(t.Context(), task.ID)
	if e != nil {
		t.Fatal(e)
	}
	p, e := s.Plan(t.Context(), task.ID, r.ID, "main", "result", Selection{"new.txt": nil})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Accept(t.Context(), task.ID, p); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("untrusted Git program ran")
	}
}
func TestUnknownRuntimeNeverBecomesMissingOrDuplicate(t *testing.T) {
	s, task := fixture(t)
	f := s.Runtime.(*fakeRuntime)
	f.inspectErr = fmt.Errorf("node unavailable")
	if _, e := s.Sync(t.Context(), task.ID); e == nil {
		t.Fatal("unavailable node ignored")
	}
	if _, e := s.Stop(t.Context(), task.ID); e == nil {
		t.Fatal("stop pretended success")
	}
	if _, e := s.Run(t.Context(), task.ID); e == nil {
		t.Fatal("duplicate launch")
	}
	if _, e := s.Review(t.Context(), task.ID); e == nil {
		t.Fatal("review allowed against unknown runtime")
	}
	v, _ := s.Store.Get(task.ID)
	if v.State != "running" || len(v.Attempts) != 1 {
		t.Fatalf("corrupted state: %+v", v)
	}
}
func TestStopRetryAndTestSnapshot(t *testing.T) {
	s, task := fixture(t)
	if _, e := s.Stop(t.Context(), task.ID); e != nil {
		t.Fatal(e)
	}
	task, e := s.Run(t.Context(), task.ID)
	if e != nil {
		t.Fatal(e)
	}
	a, _ := Current(task)
	if a.Number != 2 {
		t.Fatal(a)
	}
	finish(t, s, task)
	task, e = s.Test(t.Context(), task.ID)
	if e != nil {
		t.Fatal(e)
	}
	a, _ = Current(task)
	if a.TestTree == "" || a.TestState != "running" {
		t.Fatal(a)
	}
	if _, e = s.Review(t.Context(), task.ID); e == nil {
		t.Fatal("review while tests can write")
	}
	if e = s.Runtime.Stop(t.Context(), a.TestName); e != nil {
		t.Fatal(e)
	}
	task, e = s.Sync(t.Context(), task.ID)
	if e != nil {
		t.Fatal(e)
	}
	a, _ = Current(task)
	if a.TestState != "passed" {
		t.Fatal(a)
	}
}
func TestReviewRejectsSymlinkAndSelectionForgery(t *testing.T) {
	s, task := fixture(t)
	a, _ := Current(task)
	if e := os.Symlink("/etc/passwd", filepath.Join(a.Workspace, "escape")); e != nil {
		t.Fatal(e)
	}
	finish(t, s, task)
	if _, e := s.Review(t.Context(), task.ID); e == nil {
		t.Fatal("symlink captured")
	}
	if _, e := SelectedPatch(Review{}, Selection{"absent": nil}); e == nil {
		t.Fatal("unknown file accepted")
	}
}
func TestPrivateStoreConcurrentUpdates(t *testing.T) {
	s, task := fixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			_, e := s.Store.Update(task.ID, func(v *Task) error { v.Spec.Title += "x"; return nil })
			errs <- e
		})
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	v, _ := s.Store.Get(task.ID)
	if v.Spec.Title != "Featurexxxx" {
		t.Fatal(v.Spec.Title)
	}
	st, e := os.Stat(s.Store.path())
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("store is not private")
	}
}

func TestReviewPrivateIdentityCannotEscapeStore(t *testing.T) {
	s, _ := fixture(t)
	if e := s.Store.SaveReview(Review{ID: strings.Repeat("../", 21) + "x"}); e == nil {
		t.Fatal("unsafe review ID accepted")
	}
	if e := s.Store.SaveReview(Review{ID: strings.Repeat("a", 64)}); e == nil {
		t.Fatal("invalid checksum accepted")
	}
}
func TestTaskOperationHonorsCancellation(t *testing.T) {
	s, task := fixture(t)
	unlock, e := s.Store.operation(t.Context(), task.ID)
	if e != nil {
		t.Fatal(e)
	}
	defer unlock()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, e = s.Stop(ctx, task.ID); e == nil {
		t.Fatal("concurrent lifecycle operation acquired lock")
	}
}
func FuzzReviewSelection(f *testing.F) {
	f.Add("plain text", "a")
	f.Add("@@ -1 +1 @@\n-old\n+new\n", "b")
	f.Fuzz(func(t *testing.T, patch, id string) {
		if len(patch) > 1<<16 || len(id) > 128 {
			return
		}
		c, e := parseChange("file.txt", "M", patch)
		if e != nil {
			return
		}
		_, _ = SelectedPatch(Review{Changes: []Change{c}}, Selection{"file.txt": []string{id}})
	})
}

func TestBatchLaunchPassphraseUsesInheritedDescriptor(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(root, "secret")
	writeTest(t, secret, "fixture-passphrase\n")
	f, e := os.Open(secret)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = f.Close() }()
	script := filepath.Join(root, "launcher")
	writeTest(t, script, "#!/bin/sh\nIFS= read -r value <&3\n[ \"$value\" = fixture-passphrase ] || exit 19\nexit 17\n")
	if e = os.Chmod(script, 0700); e != nil {
		t.Fatal(e)
	}
	runtime := CLIRuntime{Executable: script, PassphraseFD: int(f.Fd()), Store: Store{Root: filepath.Join(root, "private")}}
	_, e = runtime.Launch(t.Context(), Task{Spec: Spec{Agent: "shell", Prompt: "true"}}, Attempt{Name: "task", Workspace: root}, false)
	if e == nil || !strings.Contains(e.Error(), "exit status 17") || strings.Contains(e.Error(), "fixture-passphrase") {
		t.Fatalf("private descriptor not passed safely: %v", e)
	}
}

func TestWorkspaceCheckoutDoesNotExecuteHooksOrSmudgeFilters(t *testing.T) {
	s, task := fixture(t)
	project := task.Spec.Project
	marker := filepath.Join(t.TempDir(), "ran")
	writeTest(t, filepath.Join(project, ".gitattributes"), "*.txt filter=evil\n")
	gitTest(t, project, "add", ".gitattributes")
	gitTest(t, project, "commit", "-m", "attributes")
	hooks := filepath.Join(t.TempDir(), "hooks")
	if e := os.Mkdir(hooks, 0700); e != nil {
		t.Fatal(e)
	}
	hook := filepath.Join(hooks, "post-checkout")
	writeTest(t, hook, "#!/bin/sh\ntouch "+marker+"\n")
	if e := os.Chmod(hook, 0700); e != nil {
		t.Fatal(e)
	}
	gitTest(t, project, "config", "core.hooksPath", hooks)
	gitTest(t, project, "config", "filter.evil.smudge", "touch "+marker+"; cat")
	gitTest(t, project, "config", "filter.evil.required", "true")
	next, e := s.Create(t.Context(), Spec{Title: "Checkout safety", Project: project, Agent: "shell", Prompt: "true"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Run(t.Context(), next.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(marker); !os.IsNotExist(e) {
		t.Fatal("checkout ran an untrusted host program")
	}
}

func TestTemporaryIndexHasPrivateParentBeforeGitCreatesFiles(t *testing.T) {
	p, cleanup, e := temporaryIndex()
	if e != nil {
		t.Fatal(e)
	}
	defer cleanup()
	st, e := os.Stat(filepath.Dir(p))
	if e != nil || st.Mode().Perm() != 0700 {
		t.Fatal("scratch index directory is not private", e)
	}
	if _, e = os.Stat(p); !os.IsNotExist(e) {
		t.Fatal("index should initially be absent")
	}
}

func TestNaturalExitFinalizesReservationBeforeVerificationAndRetry(t *testing.T) {
	s, task := fixture(t)
	a, _ := Current(task)
	f := s.Runtime.(*fakeRuntime)
	f.mu.Lock()
	v := f.states[a.Name]
	v.Running = false
	f.states[a.Name] = v
	f.mu.Unlock()
	task, e := s.Sync(t.Context(), task.ID)
	if e != nil {
		t.Fatal(e)
	}
	a, _ = Current(task)
	if !a.Released || a.ExitCode == nil || *a.ExitCode != 0 || task.State != "review" {
		t.Fatalf("natural completion not finalized: %+v", a)
	}
	task, e = s.Test(t.Context(), task.ID)
	if e != nil {
		t.Fatal(e)
	}
	a, _ = Current(task)
	f.mu.Lock()
	v = f.states[a.TestName]
	v.Running = false
	f.states[a.TestName] = v
	f.mu.Unlock()
	task, e = s.Sync(t.Context(), task.ID)
	if e != nil {
		t.Fatal(e)
	}
	a, _ = Current(task)
	if !a.TestReleased || a.TestState != "passed" {
		t.Fatalf("verification not finalized: %+v", a)
	}
}

func TestFinalizationFailureDoesNotPublishSuccessAndCanRetry(t *testing.T) {
	s, task := fixture(t)
	a, _ := Current(task)
	f := s.Runtime.(*fakeRuntime)
	f.mu.Lock()
	v := f.states[a.Name]
	v.Running = false
	f.states[a.Name] = v
	f.stopErr = fmt.Errorf("release unavailable")
	f.mu.Unlock()
	if _, err := s.Sync(t.Context(), task.ID); err == nil {
		t.Fatal("finalization failure ignored")
	}
	saved, _ := s.Store.Get(task.ID)
	a, _ = Current(saved)
	if saved.State != "running" || a.Released {
		t.Fatal("completion reported before finalization")
	}
	f.stopErr = nil
	if _, err := s.Sync(t.Context(), task.ID); err != nil {
		t.Fatal(err)
	}
	f.stopErr = fmt.Errorf("stop must not be replayed")
	if _, err := s.Sync(t.Context(), task.ID); err != nil {
		t.Fatal("finalization not idempotent", err)
	}
}
