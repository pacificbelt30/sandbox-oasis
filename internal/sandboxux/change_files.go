package sandboxux

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

type FileChange struct{ Path, Status string }
type Comparison struct {
	Base  string
	Files []FileChange
}

func validChangeBase(base string) bool {
	if base == "HEAD" {
		return true
	}
	if len(base) != 40 && len(base) != 64 {
		return false
	}
	for _, r := range base {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func runChangeGit(ctx context.Context, guest ChangeExecutor, id string, args ...string) (string, error) {
	var out, stderr bytes.Buffer
	argv := append([]string{"git", "--literal-pathspecs", "--no-pager", "-c", "core.fsmonitor=false", "-c", "color.ui=false"}, args...)
	code, err := guest.ExecWithOptions(ctx, id, sandbox.ExecOptions{Argv: argv, WorkingDir: "/workspace", Stdout: &LimitedWriter{Writer: &out, Remaining: 256 << 10}, Stderr: &LimitedWriter{Writer: &stderr, Remaining: 64 << 10}})
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("guest Git comparison failed (exit %d); the reference may be unavailable", code)
	}
	return out.String(), nil
}

func CompareWorkspace(ctx context.Context, guest ChangeExecutor, id, base string) (Comparison, error) {
	result := Comparison{Base: base}
	if !validChangeBase(base) {
		return result, fmt.Errorf("invalid comparison reference")
	}
	raw, err := runChangeGit(ctx, guest, id, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--name-status", "-z", base, "--")
	if err != nil {
		return result, err
	}
	parts := strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00")
	if raw != "" {
		if len(parts)%2 != 0 {
			return result, fmt.Errorf("invalid guest change list")
		}
		for i := 0; i < len(parts); i += 2 {
			if len(result.Files) >= 4096 {
				return result, fmt.Errorf("change list exceeds 4096 files")
			}
			if ArtifactPath(parts[i+1]) != nil {
				continue
			}
			if parts[i] != "M" && parts[i] != "A" && parts[i] != "D" && parts[i] != "T" {
				return result, fmt.Errorf("invalid guest change status")
			}
			result.Files = append(result.Files, FileChange{Path: parts[i+1], Status: parts[i]})
		}
	}
	raw, err = runChangeGit(ctx, guest, id, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return result, err
	}
	if raw != "" {
		for _, name := range strings.Split(strings.TrimSuffix(raw, "\x00"), "\x00") {
			if ArtifactPath(name) != nil {
				continue
			}
			if len(result.Files) >= 4096 {
				return result, fmt.Errorf("change list exceeds 4096 files")
			}
			result.Files = append(result.Files, FileChange{Path: name, Status: "?"})
		}
	}
	return result, nil
}

func CompareFile(ctx context.Context, guest ChangeExecutor, id, base, path string) (string, error) {
	if !validChangeBase(base) {
		return "", fmt.Errorf("invalid comparison reference")
	}
	if err := ArtifactPath(path); err != nil {
		return "", err
	}
	return runChangeGit(ctx, guest, id, "diff", "--no-ext-diff", "--no-textconv", base, "--", path)
}
