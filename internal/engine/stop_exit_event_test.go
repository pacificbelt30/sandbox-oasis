//go:build linux

package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	eventtypes "github.com/containerd/containerd/api/events"
	cd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/events"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
	"github.com/docker/docker/api/types/container"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func exitEnvelope(t *testing.T, r *record, mutate func(*eventtypes.TaskExit)) *events.Envelope {
	t.Helper()
	exit := &eventtypes.TaskExit{ContainerID: r.ID, ID: r.ID, Pid: 7, ExitStatus: 37, ExitedAt: timestamppb.Now()}
	if mutate != nil {
		mutate(exit)
	}
	value, err := typeurl.MarshalAny(exit)
	if err != nil {
		t.Fatal(err)
	}
	return &events.Envelope{Namespace: Namespace, Topic: "/tasks/exit", Timestamp: time.Now(), Event: value}
}

func TestTaskExitEventRejectsOtherTasksAndEarlierRuns(t *testing.T) {
	r := &record{ID: strings.Repeat("a", 64), StartedAt: time.Now().Add(-time.Minute)}
	for _, tc := range []struct {
		name   string
		mutate func(*eventtypes.TaskExit)
	}{
		{"other-container", func(e *eventtypes.TaskExit) { e.ContainerID = strings.Repeat("b", 64) }},
		{"exec-process", func(e *eventtypes.TaskExit) { e.ID = "exec-id" }},
		{"other-pid", func(e *eventtypes.TaskExit) { e.Pid = 8 }},
		{"previous-run", func(e *eventtypes.TaskExit) { e.ExitedAt = timestamppb.New(r.StartedAt.Add(-time.Second)) }},
		{"missing-time", func(e *eventtypes.TaskExit) { e.ExitedAt = nil }},
		{"future-time", func(e *eventtypes.TaskExit) { e.ExitedAt = timestamppb.New(time.Now().Add(time.Hour)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := stoppedEvent(exitEnvelope(t, r, tc.mutate), r, 7); ok {
				t.Fatal("unrelated exit event accepted")
			}
		})
	}
	for _, mutate := range []func(*events.Envelope){
		func(e *events.Envelope) { e.Namespace = "other" },
		func(e *events.Envelope) { e.Topic = "/tasks/delete" },
		func(e *events.Envelope) { e.Event = nil },
		func(e *events.Envelope) { e.Timestamp = r.StartedAt.Add(-time.Second) },
	} {
		env := exitEnvelope(t, r, nil)
		mutate(env)
		if _, ok := stoppedEvent(env, r, 7); ok {
			t.Fatal("unrelated envelope accepted")
		}
	}
}

type exitEventContainer struct {
	cd.Container
	task cd.Task
	err  error
}

func (c exitEventContainer) Task(context.Context, cio.Attach) (cd.Task, error) { return c.task, c.err }

type exitEventTask struct{ cd.Task }

func (exitEventTask) Status(context.Context) (cd.Status, error) {
	return cd.Status{Status: cd.Running}, nil
}

func TestDisconnectedWaitRequiresExitEventAndIndependentHostProof(t *testing.T) {
	for _, mode := range []string{"confirmed", "missing-event", "live-task", "daemon-unavailable", "occupied-host"} {
		t.Run(mode, func(t *testing.T) {
			r := &record{ID: strings.Repeat("c", 64), StartedAt: time.Now().Add(-time.Minute), Config: container.Config{Labels: map[string]string{"codex-dock.allocation": "owned-allocation"}}}
			eventq := make(chan *events.Envelope, 1)
			if mode != "missing-event" {
				// Kata cleanup can omit its PID. It is still insufficient without
				// the live daemon and independent empty-envelope attestation.
				eventq <- exitEnvelope(t, r, func(e *eventtypes.TaskExit) { e.Pid = 0 })
			}
			c := exitEventContainer{err: errdefs.ErrNotFound}
			if mode == "live-task" {
				c = exitEventContainer{task: exitEventTask{}}
			}
			if mode == "daemon-unavailable" {
				c.err = errors.New("daemon unavailable")
			}
			checks := 0
			b := &Backend{opts: Options{ConfirmStopped: func(_ context.Context, allocation, worker string) error {
				checks++
				if allocation != "owned-allocation" || worker != r.ID {
					t.Fatal("wrong allocation attested")
				}
				if mode == "occupied-host" {
					return errors.New("VM envelope remains occupied")
				}
				return nil
			}}}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()
			status, err := b.confirmStopExit(ctx, r, c, &stopExitObserver{events: eventq, pid: 7})
			if mode == "confirmed" {
				if err != nil || status.Status != cd.Stopped || status.ExitStatus != 37 || checks != 1 {
					t.Fatalf("confirmed exit lost: %+v checks=%d err=%v", status, checks, err)
				}
			} else if err == nil || status.Status == cd.Stopped {
				t.Fatalf("unconfirmed stop accepted: %+v %v", status, err)
			}
			if mode != "confirmed" && mode != "occupied-host" && checks != 0 {
				t.Fatal("host attestation bypassed missing runtime evidence")
			}
		})
	}
}
