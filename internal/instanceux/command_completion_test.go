package instanceux

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

type discoveryExecutor struct{ t *testing.T }

func (e discoveryExecutor) ExecWithOptions(ctx context.Context, _ string, options sandbox.ExecOptions) (int64, error) {
	e.t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		return 0, err
	}
	command := exec.CommandContext(ctx, python, options.Argv[1:]...)
	command.Stdout = options.Stdout
	command.Stderr = io.Discard
	return 0, command.Run()
}

func TestCommandDiscoveryDoesNotExecuteCandidatesOrProjectModules(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	marker := filepath.Join(root, "executed")
	program := filepath.Join(root, "fixture-tool")
	if err := os.WriteFile(program, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "json.py"), []byte("raise RuntimeError('project import executed')"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("PYTHONPATH", root)
	if err := os.Symlink(python, filepath.Join(root, "python3")); err != nil {
		t.Fatal(err)
	}
	names, err := CommandNames(t.Context(), discoveryExecutor{t}, "worker")
	if err != nil || !slices.Contains(names, "fixture-tool") || !slices.Contains(names, "printf") {
		t.Fatal(names, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("completion executed a candidate")
	}
	filtered, err := CommandNamesMatching(t.Context(), discoveryExecutor{t}, "worker", "fixture-")
	if err != nil || !slices.Equal(filtered, []string{"fixture-tool"}) {
		t.Fatal("prefix discovery", filtered, err)
	}
	if _, err := CommandNamesMatching(t.Context(), discoveryExecutor{t}, "worker", "$(touch)"); err == nil {
		t.Fatal("unsafe prefix accepted")
	}
}

func TestCommandSuggestionsOnlyCompleteOneSafeCommandWord(t *testing.T) {
	names := []string{"printf", "printenv", "printf", "private;touch", "[red]command"}
	if got := CommandSuggestions("pri", names); !slices.Equal(got, []string{"printenv", "printf"}) {
		t.Fatal(got)
	}
	for _, prefix := range []string{"", "printf", "echo ", "echo;pri", "$(pri", "../pri", "pri\n"} {
		if got := CommandSuggestions(prefix, names); len(got) != 0 {
			t.Fatalf("unsafe or completed input %q was replaced: %v", prefix, got)
		}
	}
}
