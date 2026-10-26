package instanceux

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

func testWorkers(now time.Time) []sandbox.Worker {
	old, recent := now.Add(-2*time.Hour), now.Add(-time.Minute)
	return []sandbox.Worker{
		{ID: "bbb", Name: "review-api", Status: "exited", Image: "image:b", Branch: "review", Task: "audit API", AgentPolicy: "review", StartedAt: &old},
		{ID: "aaa", Name: "feature-ui", Status: "running", Image: "image:a", Branch: "feature", Task: "improve terminal", AgentPolicy: "approve-for-me", StartedAt: &recent},
	}
}

func TestQueryFilterSortAndStableSelection(t *testing.T) {
	now := time.Now()
	workers := testWorkers(now)
	query, err := ParseQuery(`status:running !task:"api migration" policy:approve`)
	if err != nil {
		t.Fatal(err)
	}
	filtered := Filter(workers, query)
	if len(filtered) != 1 || filtered[0].Name != "feature-ui" {
		t.Fatalf("filtered = %#v", filtered)
	}
	if _, err := ParseQuery("unknown:value"); err == nil {
		t.Fatal("unknown field accepted")
	}
	if got := Sort(workers, SortNewest)[0].Name; got != "feature-ui" {
		t.Fatalf("newest = %s", got)
	}
	model := NewModel()
	model.Replace(workers)
	model.SelectIndex(1)
	selected, _ := model.Selected()
	model.SetSort(SortStatus)
	after, _ := model.Selected()
	if selected.ID != after.ID {
		t.Fatalf("selection changed from %s to %s", selected.ID, after.ID)
	}
}

func TestSnapshotRenderSaveLoadAndCompare(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	workers := testWorkers(now)
	before := NewSnapshot(workers, now)
	var table bytes.Buffer
	if err := WriteTable(&table, before, RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(table.String(), "POLICY") || !strings.Contains(table.String(), "approve-for-me") {
		t.Fatalf("table = %s", table.String())
	}
	path := filepath.Join(t.TempDir(), "instances.json")
	if err := SaveSnapshot(path, before); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Workers) != 2 {
		t.Fatalf("loaded = %#v", loaded)
	}
	workers[0].Status = "running"
	workers = append(workers, sandbox.Worker{ID: "ccc", Name: "new", Status: "running", Image: "image:c"})
	after := NewSnapshot(workers, now.Add(time.Minute))
	diff := CompareSnapshots(loaded, after)
	if diff.Added != 1 || diff.Started != 1 {
		t.Fatalf("diff = %#v", diff)
	}
	var output bytes.Buffer
	if err := WriteDiff(&output, diff, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "ADDED") || !strings.Contains(output.String(), "STARTED") {
		t.Fatalf("diff output = %s", output.String())
	}
}

func TestTrackerRecordsTransitions(t *testing.T) {
	now := time.Now()
	workers := testWorkers(now)
	tracker := NewTracker(2)
	if events := tracker.Observe(workers, now); len(events) != 0 {
		t.Fatalf("initial events = %#v", events)
	}
	workers[1].Status = "exited"
	events := tracker.Observe(workers, now.Add(time.Second))
	if len(events) != 1 || events[0].Kind != EventStatus {
		t.Fatalf("events = %#v", events)
	}
	tracker.Observe(workers[:1], now.Add(2*time.Second))
	tracker.Observe(nil, now.Add(3*time.Second))
	if len(tracker.Events()) != 2 {
		t.Fatalf("retained = %#v", tracker.Events())
	}
	tracker.Clear()
	if len(tracker.Events()) != 0 {
		t.Fatal("clear failed")
	}
}

type fakeAccessManager struct {
	worker  sandbox.Worker
	started bool
	exec    sandbox.ExecOptions
}

func (f *fakeAccessManager) InspectContext(context.Context, string) (sandbox.Worker, error) {
	return f.worker, nil
}
func (f *fakeAccessManager) StartContext(context.Context, string) error {
	f.started = true
	f.worker.Status = "running"
	return nil
}
func (f *fakeAccessManager) ExecWithOptions(_ context.Context, _ string, options sandbox.ExecOptions) (int64, error) {
	f.exec = options
	return 0, nil
}

func TestConnectStartsAndUsesSeparateTTY(t *testing.T) {
	manager := &fakeAccessManager{worker: sandbox.Worker{ID: "id", Name: "worker", Status: "exited"}}
	if _, err := Connect(context.Background(), manager, "worker", ConnectOptions{}); err == nil {
		t.Fatal("stopped worker connected without --start")
	}
	input := strings.NewReader("")
	if _, err := Connect(context.Background(), manager, "worker", ConnectOptions{Start: true, Shell: "bash", Environment: []string{"TERM=xterm"}, Stdin: input, Stdout: io.Discard, Stderr: io.Discard}); err != nil {
		t.Fatal(err)
	}
	if !manager.started || !manager.exec.TTY || strings.Join(manager.exec.Argv, " ") != "/bin/bash -l" {
		t.Fatalf("manager = %#v", manager)
	}
	if err := ValidateEnvironment([]string{"BAD-NAME=x"}); err == nil {
		t.Fatal("invalid environment accepted")
	}
}
