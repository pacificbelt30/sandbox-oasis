//go:build linux

package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	cd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/errdefs"
)

func executionAllowed(r *record) error {
	if r.Quarantined {
		return fmt.Errorf("worker is quarantined; preserve its workspace and create a new worker: %w", errdefs.ErrConflict)
	}
	if r.PrimaryOutputLimit != primaryOutputLimit {
		return fmt.Errorf("worker output policy requires node recovery before execution: %w", errdefs.ErrConflict)
	}
	return nil
}

// Called with b.mu held, serializing starts and exec creation. Persist denial
// BEFORE stopping, so a lost response or node restart cannot re-enable work.
// There is deliberately no unquarantine operation: rules are immutable and a
// fresh worker must receive the new policy and a fresh credential placeholder.
func (b *Backend) quarantine(parent context.Context, r *record, c cd.Container) error {
	if r.Config.Labels["codex-dock.managed"] != "true" || r.Config.Labels["codex-dock.isolation"] != "kata" && r.Config.Labels["codex-dock.isolation"] != "runc" {
		return fmt.Errorf("quarantine requires a managed worker")
	}
	// Once accepted, a management-client disconnect must not interrupt the
	// stop after denial has been persisted. Keep a bounded node-owned deadline.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 90*time.Second)
	defer cancel()
	r.Quarantined = true
	persistErr := b.save(r)
	// A failed durable write must not prevent an attempt to stop a live task,
	// but cannot be reported as successful durable quarantine.
	stopErr := b.stop(ctx, r, c, 0)
	return errors.Join(persistErr, stopErr)
}
