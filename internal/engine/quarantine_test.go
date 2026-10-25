//go:build linux

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
)

type quarantineContainer struct {
	cd.Container
	beforeTask func()
	taskErr    error
}

func (c quarantineContainer) Task(context.Context, cio.Attach) (cd.Task, error) {
	c.beforeTask()
	return nil, c.taskErr
}
func TestQuarantinePersistsBeforeStopAndBlocksStartAfterReload(t *testing.T) {
	for _, stopFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "stop-failure"}[stopFails], func(t *testing.T) {
			b := &Backend{opts: Options{State: t.TempDir()}}
			r := &record{ID: strings.Repeat("a", 64), Config: container.Config{Labels: map[string]string{"codex-dock.managed": "true", "codex-dock.isolation": "kata"}}}
			var taskErr error = errdefs.ErrNotFound
			if stopFails {
				taskErr = errors.New("runtime unavailable")
			}
			calls := 0
			c := quarantineContainer{taskErr: taskErr, beforeTask: func() {
				calls++
				data, err := os.ReadFile(filepath.Join(b.path(r.ID), "record.json"))
				if err != nil {
					t.Fatal(err)
				}
				var restored record
				if err = json.Unmarshal(data, &restored); err != nil || !restored.Quarantined {
					t.Fatalf("stop preceded durable denial: %s %v", data, err)
				}
				// A restarted node enforces the record without the original Manager or
				// its private cleanup directory. No containerd Task call is needed.
				restarted := &Backend{}
				if err = restarted.start(context.Background(), &restored, nil); !errdefs.IsConflict(err) {
					t.Fatalf("quarantined worker restarted: %v", err)
				}
			}}
			err := b.quarantine(context.Background(), r, c)
			if calls != 1 || stopFails && !errors.Is(err, taskErr) || !stopFails && err != nil {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}
func TestQuarantineRefusesProxyAndReportsPersistenceFailure(t *testing.T) {
	for _, managed := range []bool{false, true} {
		b := &Backend{opts: Options{State: t.TempDir()}}
		r := &record{ID: strings.Repeat("b", 64), Config: container.Config{Labels: map[string]string{}}}
		if managed {
			r.Config.Labels = map[string]string{"codex-dock.managed": "true", "codex-dock.isolation": "runc"}
		}
		// Prevent state writes while keeping the test meaningful when run as root.
		if err := os.WriteFile(filepath.Join(b.opts.State, "tasks"), []byte("occupied"), 0600); err != nil {
			t.Fatal(err)
		}
		calls := 0
		c := quarantineContainer{taskErr: errdefs.ErrNotFound, beforeTask: func() { calls++ }}
		if err := b.quarantine(context.Background(), r, c); err == nil {
			t.Fatal("unsafe/undurable quarantine acknowledged")
		}
		if managed && calls != 1 || !managed && calls != 0 {
			t.Fatalf("unexpected stop attempts: %d", calls)
		}
	}
}
func TestOldRecordRequiresOutputPolicyMigration(t *testing.T) {
	var r record
	if err := json.Unmarshal([]byte(`{"ID":"legacy"}`), &r); err != nil {
		t.Fatal(err)
	}
	if err := executionAllowed(&r); !errdefs.IsConflict(err) {
		t.Fatalf("legacy output policy allowed execution: %v", err)
	}
	r.PrimaryOutputLimit = primaryOutputLimit
	if err := executionAllowed(&r); err != nil {
		t.Fatalf("current output policy rejected: %v", err)
	}
}

func TestAcceptedQuarantineContinuesAfterClientCancellation(t *testing.T) {
	b := &Backend{opts: Options{State: t.TempDir()}}
	r := &record{ID: strings.Repeat("e", 64), Config: container.Config{Labels: map[string]string{"codex-dock.managed": "true", "codex-dock.isolation": "runc"}}}
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	c := quarantineDeadlineContainer{check: func(ctx context.Context) {
		called = true
		if ctx.Err() != nil {
			t.Fatal("client cancellation interrupted accepted stop")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 90*time.Second || time.Until(deadline) <= 0 {
			t.Fatal("stop deadline is not bounded")
		}
	}}
	if err := b.quarantine(parent, r, c); err != nil || !called {
		t.Fatalf("accepted stop incomplete: %v", err)
	}
}

type quarantineDeadlineContainer struct {
	cd.Container
	check func(context.Context)
}

func (c quarantineDeadlineContainer) Task(ctx context.Context, _ cio.Attach) (cd.Task, error) {
	c.check(ctx)
	return nil, errdefs.ErrNotFound
}
