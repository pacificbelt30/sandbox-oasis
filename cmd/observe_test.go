package cmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/observability"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

type observationRunner struct {
	calls int
	err   error
}

func (r *observationRunner) RunContext(context.Context, sandbox.RunOptions) (string, error) {
	r.calls++
	return "private-worker-id", r.err
}

func TestDiagnosticRunnerPreservesWorkerAndFailureWithUnavailableStorage(t *testing.T) {
	r := &observationRunner{err: errors.New("private-upstream-error")}
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	wrapped := diagnosticRunner{runner: r, directory: dir}
	id, err := wrapped.RunContext(context.Background(), sandbox.RunOptions{})
	if id != "private-worker-id" || err != r.err || r.calls != 1 {
		t.Fatal("diagnostics changed launch result")
	}
	records, err := (observability.Journal{Directory: dir}).Read()
	if err != nil || len(records) != 1 || records[0].Status != "error" || records[0].WorkerHash == id {
		t.Fatal("missing redacted failed-launch record", err)
	}
	file := filepath.Join(dir, "ordinary-file")
	_ = os.WriteFile(file, nil, 0600)
	wrapped.directory = file
	id, err = wrapped.RunContext(context.Background(), sandbox.RunOptions{})
	if id != "private-worker-id" || err != r.err || r.calls != 2 {
		t.Fatal("storage failure changed launch result")
	}
}
