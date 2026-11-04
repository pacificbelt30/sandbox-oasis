package sandbox_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/instanceux"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

func TestKataQualityLifecycle(t *testing.T) {
	if os.Getenv("CODEX_DOCK_KATA_E2E") != "1" {
		t.Skip("requires disposable matching Kata node")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	m, err := sandbox.NewManager(sandbox.ManagerConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	started := time.Now()
	id, err := m.RunContext(ctx, sandbox.RunOptions{Name: fmt.Sprintf("quality-lifecycle-%d", time.Now().UnixNano()), Image: os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE"), ProjectDir: t.TempDir(), ContainerUser: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), ResourceProfile: "compact", NoInternet: true, NoGitMetadata: true, Detach: true, ExecCmd: "exec sleep 300", AgentStateSize: "off"})
	if id != "" {
		defer func() {
			if e := m.RemoveContext(context.Background(), id, true); e != nil {
				t.Error(e)
			}
		}()
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("quality_metric no_ai_launch_ms=%.3f", float64(time.Since(started))/float64(time.Millisecond))
	listed := func() sandbox.Worker {
		t.Helper()
		workers, e := m.ListContext(ctx, true)
		if e != nil {
			t.Fatal(e)
		}
		for _, worker := range workers {
			if worker.ID == id {
				inspect, e := m.InspectContext(ctx, id)
				if e != nil || worker.StartedAt == nil || inspect.StartedAt == nil || !worker.StartedAt.Equal(*inspect.StartedAt) {
					t.Fatal("list/inspect task timestamps disagree", worker, inspect, e)
				}
				return worker
			}
		}
		t.Fatal("worker missing from list")
		return sandbox.Worker{}
	}
	before := listed()
	tracker := instanceux.NewTracker(10)
	tracker.Observe([]sandbox.Worker{before}, time.Now())
	if err = m.StopContext(ctx, id, 5); err != nil {
		t.Fatal(err)
	}
	stopped := listed()
	if stopped.FinishedAt == nil {
		t.Fatal("stop time missing from list")
	}
	if err = m.StartContext(ctx, id); err != nil {
		t.Fatal(err)
	}
	after := listed()
	if events := tracker.Observe([]sandbox.Worker{after}, time.Now()); len(events) != 1 || events[0].Kind != instanceux.EventRestarted {
		t.Fatal("unobserved stop/start was not recognized as a restart", events)
	}
	t.Log("verified: list uses actual task start/finish times; restart detection without extra per-worker list inspections")
}
