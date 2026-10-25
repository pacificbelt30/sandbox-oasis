//go:build linux

package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	eventtypes "github.com/containerd/containerd/api/events"
	cd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/events"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/errdefs"
	"github.com/containerd/typeurl/v2"
)

type stopExitObserver struct {
	events <-chan *events.Envelope
	errors <-chan error
	cancel context.CancelFunc
	pid    uint32
}

func (b *Backend) observeStopExit(ctx context.Context, r *record, task cd.Task) *stopExitObserver {
	if b.client == nil || b.opts.ConfirmStopped == nil || r.Host.Runtime == "runc" || r.StartedAt.IsZero() {
		return nil
	}
	ctx, cancel := context.WithCancel(namespaces.WithNamespace(ctx, Namespace))
	events, errs := b.client.Subscribe(ctx, `topic=="/tasks/exit"`)
	return &stopExitObserver{events: events, errors: errs, cancel: cancel, pid: task.Pid()}
}

func stoppedEvent(env *events.Envelope, r *record, pid uint32) (cd.Status, bool) {
	if env == nil || env.Event == nil || env.Namespace != Namespace || env.Topic != "/tasks/exit" || env.Timestamp.Before(r.StartedAt) {
		return cd.Status{}, false
	}
	decoded, err := typeurl.UnmarshalAny(env.Event)
	if err != nil {
		return cd.Status{}, false
	}
	exit, ok := decoded.(*eventtypes.TaskExit)
	if !ok || exit.ContainerID != r.ID || exit.ID != r.ID || exit.ExitedAt == nil || exit.ExitedAt.CheckValid() != nil || exit.Pid != 0 && exit.Pid != pid {
		return cd.Status{}, false
	}
	at := exit.ExitedAt.AsTime()
	if at.Before(r.StartedAt) || at.After(time.Now().Add(time.Second)) {
		return cd.Status{}, false
	}
	// Kata's shim cleanup can omit the PID. Identity/freshness alone never
	// proves a stop: confirmStopExit also requires task and host attestations.
	return cd.Status{Status: cd.Stopped, ExitStatus: exit.ExitStatus, ExitTime: at}, true
}

func (b *Backend) confirmStopExit(parent context.Context, r *record, c cd.Container, observer *stopExitObserver) (cd.Status, error) {
	if observer == nil || b.opts.ConfirmStopped == nil {
		return cd.Status{}, errors.New("no independent exit attestation available")
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	var status cd.Status
	for status.Status != cd.Stopped {
		select {
		case env, ok := <-observer.events:
			if !ok {
				return cd.Status{}, errors.New("exit event subscription closed")
			}
			if candidate, ok := stoppedEvent(env, r, observer.pid); ok {
				status = candidate
			}
		case err, ok := <-observer.errors:
			if !ok || err == nil {
				err = errors.New("exit event subscription unavailable")
			}
			return cd.Status{}, err
		case <-ctx.Done():
			return cd.Status{}, fmt.Errorf("exit event not confirmed: %w", ctx.Err())
		}
	}
	var lastErr error
	for {
		lastErr = b.confirmStoppedTask(ctx, r, c)
		if lastErr == nil {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return cd.Status{}, fmt.Errorf("host stop not confirmed: %w", errors.Join(ctx.Err(), lastErr))
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (b *Backend) confirmStoppedTask(ctx context.Context, r *record, c cd.Container) error {
	task, err := c.Task(ctx, nil)
	if err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	if err == nil {
		status, err := task.Status(ctx)
		if err != nil && !errdefs.IsNotFound(err) {
			return err
		}
		if err == nil && status.Status != cd.Stopped {
			return errors.New("runtime task remains active")
		}
	}
	return b.opts.ConfirmStopped(ctx, r.Config.Labels["codex-dock.allocation"], r.ID)
}
