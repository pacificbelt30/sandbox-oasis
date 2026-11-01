package taskboard

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const MaxPatch = 32 << 20

type Repository struct{ Root, GitDir, Common, Head, Branch string }
type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, fmt.Errorf("git output exceeds size limit")
	}
	return b.Buffer.Write(p)
}
func gitEnv(extra []string) []string {
	var env []string
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GIT_") {
			env = append(env, v)
		}
	}
	return append(append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"), extra...)
}
func gitCommand(ctx context.Context, dir, gitdir string, extra []string, input io.Reader, args ...string) ([]byte, error) {
	prefix := []string{"--no-pager", "--literal-pathspecs", "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false", "-c", "core.autocrlf=false", "-c", "core.attributesFile=/dev/null", "-c", "core.quotePath=true"}
	if gitdir != "" {
		prefix = append(prefix, "--git-dir="+gitdir, "--work-tree="+dir)
	}
	c := exec.CommandContext(ctx, "git", append(prefix, args...)...)
	c.Dir = dir
	c.Env = gitEnv(extra)
	c.Stdin = input
	out := &limitedBuffer{max: MaxPatch}
	stderr := &limitedBuffer{max: 64 << 10}
	c.Stdout = out
	c.Stderr = stderr
	if e := c.Run(); e != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], e, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}
func InspectRepository(ctx context.Context, dir string) (Repository, error) {
	var r Repository
	dir, e := filepath.Abs(dir)
	if e != nil {
		return r, e
	}
	dir, e = filepath.EvalSymlinks(dir)
	if e != nil {
		return r, e
	}
	out, e := gitCommand(ctx, dir, "", nil, nil, "rev-parse", "--show-toplevel")
	if e != nil {
		return r, e
	}
	r.Root = string(bytes.TrimSpace(out))
	if r.Root != dir {
		return r, fmt.Errorf("select the Git repository root")
	}
	out, e = gitCommand(ctx, dir, "", nil, nil, "rev-parse", "--absolute-git-dir")
	if e != nil {
		return r, e
	}
	r.GitDir = string(bytes.TrimSpace(out))
	out, e = gitCommand(ctx, dir, r.GitDir, nil, nil, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if e != nil {
		return r, e
	}
	r.Common = string(bytes.TrimSpace(out))
	out, e = gitCommand(ctx, dir, r.GitDir, nil, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if e != nil {
		return r, e
	}
	r.Head = string(bytes.TrimSpace(out))
	if !oidPattern.MatchString(r.Head) {
		return r, fmt.Errorf("invalid repository HEAD")
	}
	out, e = gitCommand(ctx, dir, r.GitDir, nil, nil, "symbolic-ref", "--quiet", "--short", "HEAD")
	if e == nil {
		r.Branch = string(bytes.TrimSpace(out))
	}
	return r, nil
}

// Local config may be writable by a guest sharing Git metadata. Disable every
// configured content filter in addition to hooks/fsmonitor; worktree checkout
// and diff must never execute programs selected by repository attributes.
func filterOptions(ctx context.Context, root, gitdir, common string) ([]string, error) {
	out, e := gitCommand(ctx, root, gitdir, []string{"GIT_COMMON_DIR=" + common}, nil, "config", "--null", "--name-only", "--get-regexp", `^filter\..*\.(clean|smudge|process|required)$`)
	if e != nil {
		// git config exits 1 for an empty match, with no output.
		if strings.Contains(e.Error(), "exit status 1:") {
			return nil, nil
		}
		return nil, e
	}
	valid := regexp.MustCompile(`^filter\.[a-zA-Z0-9_.-]+\.(clean|smudge|process|required)$`)
	var args []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name == "" {
			continue
		}
		if !valid.MatchString(name) {
			return nil, fmt.Errorf("unsupported Git filter name")
		}
		value := ""
		if strings.HasSuffix(name, ".required") {
			value = "false"
		}
		args = append(args, "-c", name+"="+value)
	}
	return args, nil
}
func (r Repository) run(ctx context.Context, env []string, in io.Reader, args ...string) ([]byte, error) {
	filters, e := filterOptions(ctx, r.Root, r.GitDir, r.Common)
	if e != nil {
		return nil, e
	}
	return gitCommand(ctx, r.Root, r.GitDir, append(env, "GIT_COMMON_DIR="+r.Common), in, append(filters, args...)...)
}
func PrepareWorkspace(ctx context.Context, t Task, a Attempt) (string, error) {
	r := Repository{Root: t.Spec.Project, GitDir: t.GitDir, Common: t.Common}
	if _, e := os.Lstat(a.Workspace); e == nil {
		return "", fmt.Errorf("task workspace already exists; reconcile before retrying")
	} else if !os.IsNotExist(e) {
		return "", e
	}
	if e := os.MkdirAll(filepath.Dir(a.Workspace), 0700); e != nil {
		return "", e
	}
	_, e := r.run(ctx, nil, nil, "worktree", "add", "--detach", a.Workspace, t.Base)
	if e != nil {
		return "", e
	}
	out, e := gitCommand(ctx, a.Workspace, "", nil, nil, "rev-parse", "--absolute-git-dir")
	if e != nil {
		return "", e
	}
	return string(bytes.TrimSpace(out)), nil
}
func validBranch(name string) bool {
	if len(name) == 0 || len(name) > 180 || strings.HasPrefix(name, "-") {
		return false
	}
	for _, r := range name {
		allowed := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("/_-.", r)
		if !allowed {
			return false
		}
	}
	return !strings.Contains(name, "..") && !strings.HasPrefix(name, "/") && !strings.HasSuffix(name, "/") && !strings.HasSuffix(name, ".lock")
}
func (r Repository) branch(ctx context.Context, name string) (string, error) {
	if !validBranch(name) {
		return "", fmt.Errorf("invalid branch name")
	}
	if _, e := r.run(ctx, nil, nil, "check-ref-format", "refs/heads/"+name); e != nil {
		return "", e
	}
	out, e := r.run(ctx, nil, nil, "rev-parse", "--verify", "refs/heads/"+name+"^{commit}")
	if e != nil {
		return "", e
	}
	id := strings.TrimSpace(string(out))
	if !oidPattern.MatchString(id) {
		return "", fmt.Errorf("invalid branch commit")
	}
	return id, nil
}

// A private directory also protects the initially absent index and its lock.
// Git creates both itself, so an unlinked temporary file is insufficient.
func temporaryIndex() (string, func(), error) {
	dir, err := os.MkdirTemp("", "oaiso-git-index-")
	if err != nil {
		return "", nil, err
	}
	return filepath.Join(dir, "index"), func() { _ = os.RemoveAll(dir) }, nil
}
