package sandboxux

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
)

func fixtureRepo(t *testing.T) string {
	t.Helper()
	p := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "fixture"}, {"config", "user.email", "fixture@example.invalid"}} {
		gitFixture(t, p, args...)
	}
	putFile(t, filepath.Join(p, "tracked.txt"), "committed\n")
	gitFixture(t, p, "add", "tracked.txt")
	gitFixture(t, p, "commit", "-qm", "initial")
	return p
}
func privateTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
func gitFixture(t *testing.T, p string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", p}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func TestIndependentLaunchPinsReviewedCommitAndRetainsRecoveryCopy(t *testing.T) {
	source := fixtureRepo(t)
	putFile(t, filepath.Join(source, "untracked.txt"), "not copied")
	destination := filepath.Join(privateTestDir(t), "copy")
	spec, args, err := PrepareLaunch(LaunchSpec{Project: source, Network: "offline", Workspace: "copy", CloneDir: destination, LifecycleSet: true, Lifecycle: workerpolicy.Lifecycle{MaxLifetime: time.Hour}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), "--max-lifetime 1h0m0s --idle-timeout 0s --retention 0s --protect-main=false") {
		t.Fatal(args)
	}
	putFile(t, filepath.Join(source, "tracked.txt"), "moved after review\n")
	gitFixture(t, source, "commit", "-qam", "move")
	launcher := filepath.Join(t.TempDir(), "launcher")
	putFile(t, launcher, "#!/bin/sh\n[ \"$(cat tracked.txt)\" = committed ] || exit 21\n[ ! -e untracked.txt ] || exit 22\ncat >/dev/null\nexit 17\n")
	if err = os.Chmod(launcher, 0700); err != nil {
		t.Fatal(err)
	}
	result, err := (&CLILauncher{Executable: launcher}).Launch(t.Context(), spec)
	if err == nil || !strings.Contains(err.Error(), "exit status 17") || result.Workspace != destination {
		t.Fatal(result, err)
	}
	if head := gitFixture(t, destination, "rev-parse", "HEAD"); head != spec.CloneRevision {
		t.Fatal("copy moved beyond reviewed commit", head, spec.CloneRevision)
	}
	if got, _ := os.ReadFile(filepath.Join(source, "tracked.txt")); string(got) != "moved after review\n" {
		t.Fatal("source was changed")
	}
	if _, _, err = PrepareLaunch(spec); err == nil {
		t.Fatal("existing recovery copy was adopted")
	}
}
func TestCopyRefusesPointersAndUnsafeDestinationsBeforeLaunch(t *testing.T) {
	source := fixtureRepo(t)
	for _, dest := range []string{source, filepath.Join(source, "child"), "relative", filepath.Join(t.TempDir(), "missing", "copy")} {
		if _, _, err := PrepareLaunch(LaunchSpec{Project: source, Network: "offline", Workspace: "copy", CloneDir: dest}); err == nil {
			t.Fatal("unsafe destination", dest)
		}
	}
	pointer := t.TempDir()
	putFile(t, filepath.Join(pointer, ".git"), "gitdir: "+filepath.Join(source, ".git"))
	if _, _, err := PrepareLaunch(LaunchSpec{Project: pointer, Network: "offline", Workspace: "copy", CloneDir: filepath.Join(privateTestDir(t), "copy")}); err == nil {
		t.Fatal("external metadata pointer authorized a copy")
	}
}

func TestReviewedCopyDoesNotRunHostCheckoutFilters(t *testing.T) {
	source := fixtureRepo(t)
	putFile(t, filepath.Join(source, ".gitattributes"), "tracked.txt filter=unexpected\n")
	gitFixture(t, source, "add", ".gitattributes")
	gitFixture(t, source, "commit", "-qm", "attributes")
	root := privateTestDir(t)
	marker := filepath.Join(root, "filter-ran")
	config := filepath.Join(root, "global.gitconfig")
	putFile(t, config, "[filter \"unexpected\"]\n smudge = touch "+marker+"\n required = true\n")
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	destination := filepath.Join(root, "copy")
	spec, _, err := PrepareLaunch(LaunchSpec{Project: source, Network: "offline", Workspace: "copy", CloneDir: destination})
	if err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "launcher")
	putFile(t, launcher, "#!/bin/sh\ncat >/dev/null\nexit 17\n")
	if err = os.Chmod(launcher, 0700); err != nil {
		t.Fatal(err)
	}
	_, err = (&CLILauncher{Executable: launcher}).Launch(t.Context(), spec)
	if err == nil || !strings.Contains(err.Error(), "exit status 17") {
		t.Fatal(err)
	}
	if _, err = os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("host checkout filter executed", err)
	}
}
func TestEnvironmentRoundTripExcludesIdentitiesPathsAndDetectsConcurrentSave(t *testing.T) {
	store := EnvironmentStore{Path: filepath.Join(privateTestDir(t), "settings.json")}
	snapshot, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	spec := LaunchSpec{Name: "worker-secret", Project: "/private/project", Image: "worker:fixed", Command: "printf 日本語", Network: "offline", Workspace: "copy", CloneDir: "/private/copy", AI: true, Mounts: []string{"/private/mount:/data"}, LifecycleSet: true, Lifecycle: workerpolicy.Lifecycle{MaxLifetime: time.Hour}}
	snapshot.File.Environments["python"] = EnvironmentFrom(spec)
	if err = store.Save(snapshot.Revision, snapshot.File); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"worker-secret", "/private/", "Mounts", "AI", "CloneDir"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("unexpected persisted field", secret)
		}
	}
	info, _ := os.Stat(store.Path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	applied := loaded.File.Environments["python"].Apply(spec)
	if applied.AI || len(applied.Mounts) > 0 || applied.CloneDir != "" || applied.Project != spec.Project || applied.Command != spec.Command || !applied.LifecycleSet {
		t.Fatal(applied)
	}
	if err = store.Save(snapshot.Revision, snapshot.File); err == nil {
		t.Fatal("stale save overwrote concurrent state")
	}
	if !reflect.DeepEqual(loaded.Names(), []string{"python"}) {
		t.Fatal(loaded.Names())
	}
}
func TestEnvironmentRejectsAmbiguousUnknownAndPublicFiles(t *testing.T) {
	for _, raw := range []string{`{"version":1,"version":1,"environments":{}}`, `{"version":1,"environments":null}`, `{"version":2,"environments":{}}`, `{"version":1,"environments":{"x":{"Network":"offline","AI":true}}}`, `{"version":1,"environments":{}} {}`} {
		store := EnvironmentStore{Path: filepath.Join(t.TempDir(), "settings.json")}
		putFile(t, store.Path, raw)
		if _, err := store.Load(); err == nil {
			t.Fatal("unsafe file accepted", raw)
		}
	}
	p := filepath.Join(t.TempDir(), "public.json")
	putFile(t, p, `{"version":1,"environments":{}}`)
	if err := os.Chmod(p, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := (&EnvironmentStore{Path: p}).Load(); err == nil {
		t.Fatal("public configuration read")
	}
}

type artifactFixture struct {
	fail, overflow bool
	expected       string
}

func (a *artifactFixture) PlanArtifacts(context.Context, string, sandbox.ArtifactOptions) (sandbox.ArtifactPlan, error) {
	return sandbox.ArtifactPlan{}, nil
}
func (a *artifactFixture) ExportArtifacts(_ context.Context, _ string, o sandbox.ArtifactOptions, w io.Writer) error {
	if o.ExpectedPlan != a.expected {
		return errors.New("wrong plan")
	}
	if a.overflow {
		_, err := io.Copy(w, io.LimitReader(zeroReader{}, 34<<20))
		return err
	}
	_, err := io.WriteString(w, "reviewed-content")
	if err != nil {
		return err
	}
	if a.fail {
		return errors.New("content changed after plan")
	}
	return nil
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func TestReviewedArtifactExportNeverPublishesPartialOrOverwrites(t *testing.T) {
	p := filepath.Join(t.TempDir(), "result.tar")
	plan := sandbox.ArtifactPlan{ID: strings.Repeat("a", 64), Worker: "worker"}
	a := &artifactFixture{expected: plan.ID, fail: true}
	if err := ExportReviewedArtifacts(t.Context(), a, "worker", sandbox.ArtifactOptions{}, plan, p); err == nil {
		t.Fatal("changed plan published")
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("partial export exists", err)
	}
	a.fail = false
	a.overflow = true
	if err := ExportReviewedArtifacts(t.Context(), a, "worker", sandbox.ArtifactOptions{}, plan, p); err == nil {
		t.Fatal("unbounded guest export")
	}
	a.overflow = false
	if err := ExportReviewedArtifacts(t.Context(), a, "worker", sandbox.ArtifactOptions{}, plan, p); err != nil {
		t.Fatal(err)
	}
	if err := ExportReviewedArtifacts(t.Context(), a, "worker", sandbox.ArtifactOptions{}, plan, p); err == nil {
		t.Fatal("overwrote existing export")
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
	if err := ExportReviewedArtifacts(t.Context(), a, "different", sandbox.ArtifactOptions{}, plan, filepath.Join(t.TempDir(), "bad.tar")); err == nil {
		t.Fatal("another worker plan accepted")
	}
}
func TestSupervisorSelectsOnlyExplicitIDsAndStopsOnCancel(t *testing.T) {
	var supervisor Supervisor
	supervisor.Watch("selected", true)
	supervisor.Watch("removed", true)
	supervisor.Watch("removed", false)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		supervisor.Run(ctx, func(_ context.Context, _ time.Time, dry bool, ids []string) ([]sandbox.ReapAction, error) {
			if dry || !reflect.DeepEqual(ids, []string{"selected"}) {
				t.Error("supervision escaped selected IDs", ids)
			}
			cancel()
			return nil, nil
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("supervision did not cancel")
	}
}

type changeFixture struct{ large bool }

func (c changeFixture) ExecWithOptions(_ context.Context, _ string, o sandbox.ExecOptions) (int64, error) {
	if o.WorkingDir != "/workspace" || o.Argv[0] != "git" {
		return 0, errors.New("unsafe execution")
	}
	if c.large {
		_, err := io.Copy(o.Stdout, io.LimitReader(zeroReader{}, 257<<10))
		return 0, err
	}
	_, err := io.WriteString(o.Stdout, "[red]literal\n日本語")
	return 0, err
}
func TestChangesUseBoundedGuestExecution(t *testing.T) {
	text, err := WorkspaceChanges(t.Context(), changeFixture{}, "worker")
	if err != nil || !strings.Contains(text, "[red]literal") {
		t.Fatal(text, err)
	}
	if _, err = WorkspaceChanges(t.Context(), changeFixture{large: true}, "worker"); err == nil {
		t.Fatal("unbounded diff")
	}
}
func TestEnvironmentFileLimit(t *testing.T) {
	f := EnvironmentFile{Version: 1, Environments: map[string]Environment{"x": {Network: "offline", Command: strings.Repeat("x", 32769)}}}
	if err := (&EnvironmentStore{Path: filepath.Join(t.TempDir(), "x.json")}).Save("", f); err == nil {
		t.Fatal("oversized command saved")
	}
}
