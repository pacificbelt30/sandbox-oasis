//go:build linux

package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	cd "github.com/containerd/containerd/v2/client"

	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
)

// A node restart reattaches existing containerd FIFOs and loopback listeners.
// It never restarts or recreates a VM merely because the manager restarted.
func (b *Backend) recover(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	all, err := b.client.Containers(ctx, `labels."`+owned+`"=="true"`)
	if err != nil {
		return err
	}
	if err = b.recoverIntents(ctx); err != nil {
		return err
	}
	if err = b.recoverNetworks(ctx, all); err != nil {
		return err
	}
	for _, c := range all {
		r, _, err := b.load(ctx, c.ID())
		if err != nil {
			return err
		}
		if r.Quarantined {
			if err = b.stop(ctx, r, c, 0); err != nil {
				return fmt.Errorf("complete quarantined worker stop %s: %w", r.ID, err)
			}
			continue
		}
		h, err := newHub()
		if err != nil {
			return err
		}
		task, err := c.Task(ctx, nil)
		if errdefs.IsNotFound(err) {
			_ = h.Close()
			continue
		}
		if err != nil {
			_ = h.Close()
			return err
		}
		h.io = task.IO()
		if r.Host.Runtime != "runc" && r.Config.Tty {
			if _, err := os.Lstat(filepath.Join(b.path(r.ID), "terminal.fifo")); err == nil {
				terminal, err := openTerminalIO(b.path(r.ID), &loggingIO{})
				if err != nil {
					return err
				}
				h.io = terminal
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		b.hubs[r.ID] = h
		if err = b.prepareSupervisor(ctx, r); err != nil {
			_ = h.Close()
			delete(b.hubs, r.ID)
			return fmt.Errorf("prepare recovered worker supervisor %s: %w", r.ID, err)
		}
		wait, err := task.Wait(namespaces.WithNamespace(context.Background(), Namespace))
		if err != nil {
			return err
		}
		state, err := task.Status(ctx)
		if err != nil {
			return err
		}
		if state.Status == cd.Running {
			if err = b.openPorts(ctx, r); err != nil {
				return err
			}
		}
		h.exit = newTaskExit()
		go b.watch(r.ID, h, task, wait)
	}
	return nil
}
func (b *Backend) watch(cid string, h *hub, task cd.Task, wait <-chan cd.ExitStatus) {
	h.exit.capture(wait)
	status := h.exit.status
	code, at, waitErr := status.Result()
	b.mu.Lock()
	if b.closed || b.hubs[cid] != h {
		b.mu.Unlock()
		return
	}
	ctx, cancel := context.WithTimeout(namespaces.WithNamespace(context.Background(), Namespace), 30*time.Second)
	defer cancel()
	r, _, err := b.load(ctx, cid)
	if err == nil && waitErr == nil {
		r.ExitCode = int(code)
		r.FinishedAt = at.Format(time.RFC3339Nano)
		err = b.save(r)
	}
	b.closePorts(cid)
	if h.io != nil {
		done := make(chan struct{})
		go func() { h.io.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			h.io.Cancel()
			<-done
		}
	}
	_ = h.Close()
	delete(b.hubs, cid)
	if err == nil && waitErr == nil {
		err = b.deleteTask(ctx, r, task)
	}
	b.mu.Unlock()
	if err != nil { // No credentials/task data in node logs; VM inventory retains the failure.
		fmt.Printf("containerd task %s cleanup retained for retry: %v\n", cid, err)
	}
}
