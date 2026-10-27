//go:build linux

package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	cd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/errdefs"
)

// Never discard the private OCI record if a timed-out Create may have committed.
// A subsequent service start can reconcile its exact ID instead of losing the
// only ownership record for a snapshot, namespace or endpoint.
func (b *Backend) cancelCreation(ctx context.Context, r *record) error {
	c, err := b.client.LoadContainer(ctx, r.ID)
	if err == nil {
		if _, _, err = b.load(ctx, r.ID); err != nil {
			return err
		}
		if _, err = c.Task(ctx, nil); err == nil {
			return fmt.Errorf("creation has an active task; intent retained")
		} else if !errdefs.IsNotFound(err) {
			return err
		}
		if err = c.Delete(ctx, cd.WithSnapshotCleanup); err != nil {
			return err
		}
	} else if !errdefs.IsNotFound(err) {
		return err
	}
	return b.clearIntent(ctx, r)
}
func (b *Backend) clearIntent(ctx context.Context, r *record) error {
	if err := b.kernelIdle(r); err != nil {
		return err
	}
	if err := b.removeNamespace(ctx, r); err != nil {
		return err
	}
	if err := b.client.SnapshotService(b.opts.Snapshotter).Remove(ctx, r.ID); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	for eid, e := range b.execs {
		if e.Container == r.ID {
			delete(b.execs, eid)
		}
	}
	if err := b.removeAgentStateMetadata(r); err != nil {
		return err
	}
	return os.RemoveAll(b.path(r.ID))
}
func (b *Backend) recoverIntents(ctx context.Context) error {
	entries, err := os.ReadDir(filepath.Join(b.opts.State, "tasks"))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || !hexID.MatchString(e.Name()) {
			return fmt.Errorf("unexpected private task entry")
		}
		if _, err = b.client.LoadContainer(ctx, e.Name()); err == nil {
			continue
		} else if !errdefs.IsNotFound(err) {
			return err
		}
		data, err := os.ReadFile(filepath.Join(b.path(e.Name()), "record.json"))
		if err != nil {
			return err
		}
		var r record
		if err = json.Unmarshal(data, &r); err != nil {
			return err
		}
		if r.ID != e.Name() {
			return fmt.Errorf("creation intent identity mismatch")
		}
		if err = b.clearIntent(ctx, &r); err != nil {
			return fmt.Errorf("creation intent %s retained: %w", r.ID, err)
		}
	}
	return nil
}
