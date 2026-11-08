package taskboard

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Discovery through a writable .git pointer is not authority to use another
// repository. Retained tasks also recheck the originally selected paths before
// invoking host Git, rather than following replacement metadata implicitly.
func (r Repository) authorize() error {
	for _, path := range []string{r.Root, r.GitDir, r.Common} {
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil || !filepath.IsAbs(path) || canonical != path || path == "/" {
			return fmt.Errorf("task repository metadata path changed or is unavailable")
		}
		st, err := os.Lstat(path)
		if err != nil || !st.IsDir() {
			return fmt.Errorf("task repository metadata must be a directory")
		}
	}
	if r.GitDir == filepath.Join(r.Root, ".git") && r.Common == r.GitDir {
		return nil
	}
	rel, err := filepath.Rel(r.Root, r.Common)
	if err != nil || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.Dir(r.GitDir) != filepath.Join(r.Common, "worktrees") {
		return fmt.Errorf("external task Git metadata requires a registered linked worktree")
	}
	backlink, err := taskRegistrationPath(filepath.Join(r.GitDir, "gitdir"))
	if err != nil || backlink != filepath.Join(r.Root, ".git") {
		return fmt.Errorf("worktree registration does not authorize the task workspace")
	}
	common, err := taskRegistrationPath(filepath.Join(r.GitDir, "commondir"))
	if err != nil || common != r.Common {
		return fmt.Errorf("task worktree common directory differs from registration")
	}
	return nil
}

func taskRegistrationPath(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 4096 {
		return "", fmt.Errorf("invalid task worktree registration file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(data) > 4096 {
		return "", fmt.Errorf("task worktree registration exceeds limit")
	}
	target := strings.TrimSuffix(string(data), "\n")
	if target == "" || strings.ContainsAny(target, "\x00\r\n") {
		return "", fmt.Errorf("invalid task worktree registration path")
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return filepath.EvalSymlinks(target)
}
