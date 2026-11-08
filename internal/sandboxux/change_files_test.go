package sandboxux

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

type changeGitFixture struct{ dir string }

func (f changeGitFixture) ExecWithOptions(ctx context.Context, _ string, o sandbox.ExecOptions) (int64, error) {
	command := exec.CommandContext(ctx, o.Argv[0], o.Argv[1:]...)
	command.Dir = f.dir
	command.Stdout = o.Stdout
	command.Stderr = o.Stderr
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	err := command.Run()
	if exit, ok := err.(*exec.ExitError); ok {
		return int64(exit.ExitCode()), nil
	}
	return 0, err
}

func TestCompareLaunchIncludesGuestCommitsAndUntrackedFiles(t *testing.T) {
	dir := fixtureRepo(t)
	base := gitFixture(t, dir, "rev-parse", "HEAD")
	putFile(t, filepath.Join(dir, "tracked.txt"), "committed inside guest 日本語\n")
	gitFixture(t, dir, "-c", "commit.gpgsign=false", "commit", "-qam", "guest change")
	putFile(t, filepath.Join(dir, "new 日本語.txt"), "untracked")
	putFile(t, filepath.Join(dir, ".env"), "secret")
	guest := changeGitFixture{dir: dir}
	head, err := CompareWorkspace(t.Context(), guest, "worker", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(head.Files) != 1 || head.Files[0].Path != "new 日本語.txt" {
		t.Fatal("HEAD comparison", head)
	}
	result, err := CompareWorkspace(t.Context(), guest, "worker", base)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 2 {
		t.Fatal(result)
	}
	patch, err := CompareFile(t.Context(), guest, "worker", base, "tracked.txt")
	if err != nil || !strings.Contains(patch, "committed inside guest 日本語") {
		t.Fatal(patch, err)
	}
	for _, name := range []string{".env", "../outside", ":(glob)*"} {
		if name == ":(glob)*" {
			putFile(t, filepath.Join(dir, name), "literal filename")
			if _, err := CompareFile(t.Context(), guest, "worker", base, name); err != nil {
				t.Fatal("literal pathspec failed", err)
			}
		} else if _, err := CompareFile(t.Context(), guest, "worker", base, name); err == nil {
			t.Fatal("unsafe path accepted", name)
		}
	}
	if _, err := CompareWorkspace(t.Context(), guest, "worker", "--output=/tmp/unwanted"); err == nil {
		t.Fatal("option accepted as comparison base")
	}
}
