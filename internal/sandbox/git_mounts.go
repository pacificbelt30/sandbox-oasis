package sandbox

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
)

// A linked worktree's .git points outside /workspace. Preserve that pointer
// without adding a nested mount over .git (Kata prepares file mountpoints
// differently from runc). The common metadata keeps its absolute host path.
// This retains normal Git commit/branch behavior; it is not a private snapshot
// of shared repository metadata.
func linkedWorktreeMounts(ctx context.Context, workspace string, readOnly bool, existing []mount.Mount) ([]mount.Mount, []string, error) {
	info, err := os.Lstat(filepath.Join(workspace, ".git"))
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return nil, nil, nil
	}
	if !info.Mode().IsRegular() {
		return nil, nil, fmt.Errorf("workspace .git must be a regular pointer file or directory")
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return nil, nil, err
	}
	cmd := exec.CommandContext(ctx, "git", "-C", workspace, "rev-parse", "--path-format=absolute", "--git-common-dir")
	data, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("resolving linked worktree metadata: %w", err)
	}
	common := strings.TrimSpace(string(data))
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return nil, nil, err
	}
	if !filepath.IsAbs(common) || common == "/" {
		return nil, nil, fmt.Errorf("invalid linked worktree metadata directory")
	}
	gitDirBytes, err := exec.CommandContext(ctx, "git", "-C", workspace, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return nil, nil, fmt.Errorf("resolving worktree Git directory: %w", err)
	}
	gitDir, err := filepath.EvalSymlinks(strings.TrimSpace(string(gitDirBytes)))
	if err != nil {
		return nil, nil, err
	}
	// A workspace pointer is discovery input, never permission to share an
	// arbitrary repository. Only an external reciprocal Git registration can
	// grant this workspace access to common metadata.
	registrationRoot := filepath.Join(common, "worktrees")
	rel, err := filepath.Rel(workspace, common)
	if err != nil || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.Dir(gitDir) != registrationRoot {
		return nil, nil, fmt.Errorf("external Git metadata requires a registered linked worktree; use --no-git-metadata to omit sharing")
	}
	for _, dir := range []string{registrationRoot, gitDir} {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, nil, fmt.Errorf("invalid linked worktree registration")
		}
	}
	backlink, err := worktreeRegistrationPath(filepath.Join(gitDir, "gitdir"))
	if err != nil || backlink != filepath.Join(workspace, ".git") {
		return nil, nil, fmt.Errorf("linked worktree registration does not authorize this workspace")
	}
	registeredCommon, err := worktreeRegistrationPath(filepath.Join(gitDir, "commondir"))
	if err != nil || registeredCommon != common {
		return nil, nil, fmt.Errorf("linked worktree common directory differs from registration")
	}
	for _, m := range existing {
		if m.Target == common {
			return nil, nil, fmt.Errorf("mount target conflicts with linked worktree metadata: %s", common)
		}
	}
	return []mount.Mount{{Type: mount.TypeBind, Source: common, Target: common, ReadOnly: readOnly,
		BindOptions: &mount.BindOptions{Propagation: mount.PropagationRPrivate}}}, []string{"GIT_DIR=" + gitDir, "GIT_WORK_TREE=/workspace"}, nil
}

func worktreeRegistrationPath(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return "", fmt.Errorf("invalid worktree registration file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(data) > 4096 {
		return "", fmt.Errorf("reading worktree registration failed")
	}
	target := strings.TrimSuffix(string(data), "\n")
	if target == "" || strings.ContainsAny(target, "\x00\r\n") {
		return "", fmt.Errorf("invalid worktree registration path")
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return filepath.EvalSymlinks(target)
}

// Remove only worktrees created by the CLI, after the container and network
// have been removed. Never fall back to recursively deleting an unknown path.
func cleanupManagedWorktree(ctx context.Context, v container.InspectResponse) error {
	path := v.Config.Labels[labelPrefix+"worktree"]
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return fmt.Errorf("invalid managed worktree path")
	}
	matched := false
	for _, m := range v.HostConfig.Mounts {
		if m.Target == "/workspace" && m.Source == path {
			matched = true
		}
	}
	if !matched {
		return fmt.Errorf("managed worktree does not match the worker workspace")
	}
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	}
	info, err := os.Lstat(filepath.Join(path, ".git"))
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("managed worktree %s has no regular .git pointer; retained for recovery", path)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", path, "worktree", "remove", "--force", "--", path)
	if data, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("removing managed worktree %s: %w: %s", path, err, strings.TrimSpace(string(data)))
	}
	return nil
}
