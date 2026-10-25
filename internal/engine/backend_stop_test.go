//go:build linux

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	cd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/pkg/cio"
	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
)

type stopContainer struct {
	cd.Container
	task cd.Task
}

func (c stopContainer) Task(context.Context, cio.Attach) (cd.Task, error) { return c.task, nil }

type stopExitTask struct {
	cd.Task
	wait       chan cd.ExitStatus
	exit       *cd.ExitStatus
	terminated bool
	deleted    bool
	beforeKill func() error
	beforeWait func() error
	signals    []syscall.Signal
	killAll    bool
}

func (t *stopExitTask) Wait(context.Context) (<-chan cd.ExitStatus, error) {
	if t.beforeWait != nil {
		if err := t.beforeWait(); err != nil {
			return nil, err
		}
	}
	return t.wait, nil
}
func (t *stopExitTask) Status(context.Context) (cd.Status, error) {
	if t.terminated {
		return cd.Status{}, errdefs.ErrNotFound
	}
	return cd.Status{Status: cd.Running}, nil
}
func (t *stopExitTask) Kill(ctx context.Context, sig syscall.Signal, opts ...cd.KillOpts) error {
	t.signals = append(t.signals, sig)
	info := &cd.KillInfo{}
	for _, opt := range opts {
		if err := opt(ctx, info); err != nil {
			return err
		}
	}
	t.killAll = info.All
	if t.terminated {
		return errdefs.ErrNotFound
	}
	if t.beforeKill != nil {
		if err := t.beforeKill(); err != nil {
			return err
		}
	}
	t.terminated = true
	if t.exit != nil {
		t.wait <- *t.exit
	}
	close(t.wait)
	return nil
}

func TestForceStopSendsOnlyKillAll(t *testing.T) {
	task := &stopExitTask{wait: make(chan cd.ExitStatus, 1), exit: cd.NewExitStatus(137, time.Now().UTC(), nil)}
	b := &Backend{opts: Options{State: t.TempDir()}}
	r := &record{ID: strings.Repeat("e", 64), Config: container.Config{StopSignal: "SIGUSR1"}, Host: container.HostConfig{Runtime: "runc"}}
	if err := b.stop(context.Background(), r, stopContainer{task: task}, 0); err != nil {
		t.Fatal(err)
	}
	if len(task.signals) != 1 || task.signals[0] != syscall.SIGKILL || !task.killAll || r.ExitCode != 137 {
		t.Fatalf("force stop must send one kill-all and retain exit evidence: signals=%v all=%t exit=%d", task.signals, task.killAll, r.ExitCode)
	}
}

func TestStopReusesWatchedExitWithoutAnotherRuntimeWait(t *testing.T) {
	for _, mode := range []string{"exit-result", "wait-failure", "closed-channel"} {
		t.Run(mode, func(t *testing.T) {
			cause := errors.New("runtime wait failed")
			at := time.Now().UTC()
			var exitErr error
			if mode == "wait-failure" {
				exitErr = cause
			}
			task := &stopExitTask{wait: make(chan cd.ExitStatus, 1), exit: cd.NewExitStatus(37, at, exitErr)}
			if mode == "closed-channel" {
				task.exit = nil
			}
			calls := 0
			task.beforeWait = func() error {
				calls++
				if calls > 1 {
					return errors.New("duplicate Wait races Kata teardown")
				}
				return nil
			}
			wait, err := task.Wait(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			h := &hub{exit: newTaskExit()}
			go h.exit.capture(wait)
			r := &record{ID: strings.Repeat("f", 64), Host: container.HostConfig{Runtime: "runc"}}
			b := &Backend{opts: Options{State: t.TempDir()}, hubs: map[string]*hub{r.ID: h}}
			// Capture must publish without this lock: normal lifecycle calls hold
			// it while waiting, and the background cleanup takes it afterward.
			b.mu.Lock()
			err = b.stop(t.Context(), r, stopContainer{task: task}, 1)
			b.mu.Unlock()
			if mode != "exit-result" {
				if err == nil || mode == "wait-failure" && !errors.Is(err, cause) || r.FinishedAt != "" || task.deleted {
					t.Fatalf("unconfirmed shared result accepted: %v %+v", err, r)
				}
			} else if err != nil || r.ExitCode != 37 || r.FinishedAt != at.Format(time.RFC3339Nano) {
				t.Fatalf("authoritative shared exit lost: %v %+v", err, r)
			}
			if calls != 1 {
				t.Fatalf("runtime Wait called %d times", calls)
			}
		})
	}
}

func TestKataStopPreparesSupervisorBeforeSignalingGuest(t *testing.T) {
	at := time.Now().UTC()
	task := &stopExitTask{wait: make(chan cd.ExitStatus, 1), exit: cd.NewExitStatus(0, at, nil)}
	prepared := false
	task.beforeKill = func() error {
		if !prepared {
			return errors.New("Kata Wait would destroy an occupied sandbox cgroup")
		}
		return nil
	}
	task.beforeWait = task.beforeKill
	b := &Backend{opts: Options{State: t.TempDir(), Retire: func(_ context.Context, allocation, worker string) error {
		if allocation != "reservation" || worker != strings.Repeat("c", 64) {
			t.Fatal("retirement did not use the worker's exact identity")
		}
		prepared = true
		return nil
	}}}
	r := &record{ID: strings.Repeat("c", 64), Config: container.Config{Labels: map[string]string{"codex-dock.allocation": "reservation"}}, Host: container.HostConfig{Runtime: "io.containerd.kata.v2"}}
	if err := b.stop(context.Background(), r, stopContainer{task: task}, 1); err != nil {
		t.Fatal(err)
	}
	if !prepared || !task.terminated || r.FinishedAt == "" {
		t.Fatal("stop did not prepare the supervisor and retain exit evidence")
	}
}

func TestKataStopRejectsUnverifiedSupervisorWithoutSignaling(t *testing.T) {
	cause := errors.New("allocation ownership mismatch")
	task := &stopExitTask{wait: make(chan cd.ExitStatus, 1)}
	b := &Backend{opts: Options{State: t.TempDir(), Retire: func(context.Context, string, string) error { return cause }}}
	r := &record{ID: strings.Repeat("d", 64), Host: container.HostConfig{Runtime: "io.containerd.kata.v2"}}
	if err := b.stop(context.Background(), r, stopContainer{task: task}, 1); !errors.Is(err, cause) {
		t.Fatalf("retirement error was lost: %v", err)
	}
	if task.terminated || task.deleted || r.FinishedAt != "" {
		t.Fatal("unverified worker was signaled or marked stopped")
	}
}
func (t *stopExitTask) Delete(context.Context, ...cd.ProcessDeleteOpts) (*cd.ExitStatus, error) {
	t.deleted = true
	return t.exit, nil
}

func TestStopPreservesWaitResultWhenRuntimeForgetsExitedTask(t *testing.T) {
	at := time.Date(2026, 10, 21, 0, 0, 0, 123, time.UTC)
	task := &stopExitTask{wait: make(chan cd.ExitStatus, 1), exit: cd.NewExitStatus(37, at, nil)}
	b := &Backend{opts: Options{State: t.TempDir()}}
	r := &record{ID: strings.Repeat("a", 64), Host: container.HostConfig{Runtime: "runc"}}
	if err := b.stop(context.Background(), r, stopContainer{task: task}, 1); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(b.path(r.ID), "record.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved record
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.ExitCode != 37 || saved.FinishedAt != at.Format(time.RFC3339Nano) {
		t.Fatalf("exit evidence was lost: code=%d time=%s", saved.ExitCode, saved.FinishedAt)
	}
	if task.deleted {
		t.Fatal("deleted a task whose runtime reported it already absent")
	}
}

func TestStopDoesNotClaimSuccessWithoutAnExitResult(t *testing.T) {
	for _, closed := range []bool{false, true} {
		t.Run(map[bool]string{false: "wait-error", true: "closed-channel"}[closed], func(t *testing.T) {
			cause := errors.New("runtime wait failed; process may still be running")
			task := &stopExitTask{wait: make(chan cd.ExitStatus, 1)}
			if !closed {
				task.exit = cd.NewExitStatus(255, time.Time{}, cause)
			}
			b := &Backend{opts: Options{State: t.TempDir()}}
			r := &record{ID: strings.Repeat("b", 64), Host: container.HostConfig{Runtime: "runc"}}
			err := b.stop(context.Background(), r, stopContainer{task: task}, 1)
			if err == nil || !closed && !errors.Is(err, cause) {
				t.Fatalf("missing or failed exit evidence accepted: %v", err)
			}
			if r.FinishedAt != "" || task.deleted {
				t.Fatal("wait failure completed task cleanup")
			}
		})
	}
}
