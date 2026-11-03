// Package workspace prepares independent local Git copies. Clones are retained
// for recovery and are never deleted by worker removal or lifecycle retention.
package workspace

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func Clone(ctx context.Context, source, destination, branch string, newBranch bool) error {
	return clone(ctx, source, destination, branch, newBranch, "")
}

// CloneAt checks out the reviewed commit, even if the source branch moves.
func CloneAt(ctx context.Context, source, destination, revision string) error {
	if len(revision) != 40 && len(revision) != 64 {
		return fmt.Errorf("a full reviewed Git commit is required")
	}
	if _, err := hex.DecodeString(revision); err != nil {
		return fmt.Errorf("invalid reviewed Git commit")
	}
	if err := validateCopySource(source); err != nil {
		return err
	}
	return clone(ctx, source, destination, "", false, revision)
}

// ReviewCommit accepts a standalone repository, never an external .git pointer
// or shared object store inferred from guest-writable metadata.
func ReviewCommit(ctx context.Context, source string) (string, error) {
	if err := validateCopySource(source); err != nil {
		return "", err
	}
	cmd := copyGit(ctx, "-C", source, "rev-parse", "--verify", "HEAD^{commit}")
	var out limitedGitOutput
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("independent copy requires a Git commit: %w", err)
	}
	revision := strings.TrimSpace(out.String())
	if len(revision) != 40 && len(revision) != 64 {
		return "", fmt.Errorf("invalid Git commit")
	}
	if _, err := hex.DecodeString(revision); err != nil {
		return "", err
	}
	return revision, nil
}
func validateCopySource(source string) error {
	metadata := filepath.Join(source, ".git")
	info, err := os.Lstat(metadata)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("independent TUI copies require a standalone repository root with a real .git directory")
	}
	for _, path := range []string{"commondir", "objects/info/alternates", "objects/info/http-alternates"} {
		if _, err := os.Lstat(filepath.Join(metadata, path)); !os.IsNotExist(err) {
			return fmt.Errorf("independent TUI copy rejects external Git metadata and object stores")
		}
	}
	count := 0
	return inspectMetadata(metadata, 0, &count)
}

// Read small batches instead of WalkDir/ReadDir(-1): a guest may have filled a
// shared repository with arbitrarily many metadata entries before this copy.
func inspectMetadata(path string, depth int, count *int) error {
	if depth > 64 {
		return fmt.Errorf("repository metadata nesting exceeds 64 directories")
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	for {
		entries, err := dir.ReadDir(128)
		if err != nil && err != io.EOF {
			return err
		}
		for _, entry := range entries {
			*count++
			if *count > 100000 || entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("repository metadata must have at most 100000 entries and no symlinks")
			}
			if entry.IsDir() {
				if err := inspectMetadata(filepath.Join(path, entry.Name()), depth+1, count); err != nil {
					return err
				}
			}
		}
		if err == io.EOF {
			return nil
		}
	}
}
func copyGit(ctx context.Context, args ...string) *exec.Cmd {
	options := []string{"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "init.templateDir=", "-c", "uploadpack.packObjectsHook="}
	cmd := exec.CommandContext(ctx, "git", append(options, args...)...)
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GIT_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0")
	return cmd
}

type limitedGitOutput struct{ data []byte }

func (b *limitedGitOutput) Write(p []byte) (int, error) {
	if len(b.data)+len(p) > 64<<10 {
		return 0, fmt.Errorf("repository output exceeds 64 KiB")
	}
	b.data = append(b.data, p...)
	return len(p), nil
}
func (b *limitedGitOutput) String() string { return string(b.data) }

func clone(ctx context.Context, source, destination, branch string, newBranch bool, revision string) error {
	git := func(args ...string) *exec.Cmd { return exec.CommandContext(ctx, "git", args...) }
	if revision != "" {
		git = func(args ...string) *exec.Cmd { return copyGit(ctx, args...) }
	}
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return fmt.Errorf("clone directory must be a clean absolute path")
	}
	if branch != "" {
		cmd := exec.CommandContext(ctx, "git", "check-ref-format", "--branch", branch)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("invalid clone branch")
		}
	}
	// Reserve a new directory atomically; never replace an existing checkout.
	if err := os.Mkdir(destination, 0700); err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(destination)
		}
	}()
	cmd := git("clone", "--no-local", "--no-hardlinks", "--", source, destination)
	out := &limitedGitOutput{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("creating independent clone: %w: %s", err, out.String())
	}
	if branch != "" {
		args := []string{"-C", destination, "checkout"}
		if newBranch {
			args = append(args, "-b")
		}
		args = append(args, branch, "--")
		if out, err := exec.CommandContext(ctx, "git", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("selecting clone branch: %w: %s", err, out)
		}
	}
	if revision != "" {
		cmd := git("-C", destination, "checkout", "--detach", revision, "--")
		out := &limitedGitOutput{}
		cmd.Stdout = out
		cmd.Stderr = out
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("checking out reviewed commit: %w: %s", err, out.String())
		}
	}
	complete = true
	return nil
}
