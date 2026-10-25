//go:build linux

package engine

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/docker/docker/api/types/mount"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
)

// Called before snapshots/tasks are allocated, under the node lifecycle lock.
// The check is symmetric: a new proxy cannot put its ledger inside an existing
// worker share either. These paths are authority, not guest workspace data.
func (b *Backend) protectUsageMounts(ctx context.Context, candidate *record) error {
	if candidate.Config.Labels["codex-dock.managed"] != "true" && len(usageMountPaths(candidate)) == 0 {
		return nil
	}
	containers, err := b.client.Containers(ctx, `labels."`+owned+`"=="true"`)
	if err != nil {
		return err
	}
	var records []*record
	for _, c := range containers {
		r, _, err := b.loadOwned(ctx, c)
		if err != nil {
			return err
		}
		records = append(records, r)
	}
	return checkUsageShares(candidate, records)
}

func usageMountPaths(r *record) []string {
	if r.Config.Labels["io.codex-dock.proxy-layout"] == "" {
		return nil
	}
	var paths []string
	for _, m := range r.Host.Mounts {
		if m.Type == mount.TypeBind && m.Target == "/run/cxd-usage" {
			paths = append(paths, m.Source)
		}
	}
	return paths
}

func checkUsageShares(candidate *record, records []*record) error {
	check := func(worker *record, private []string) error {
		for _, path := range private {
			secret, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("usage state path cannot be verified")
			}
			for _, m := range worker.Host.Mounts {
				if m.Type != mount.TypeBind {
					continue
				}
				source, err := filepath.EvalSymlinks(m.Source)
				if err != nil {
					return fmt.Errorf("worker share cannot be verified")
				}
				if workerpolicy.PathsOverlap(source, secret) {
					return fmt.Errorf("worker share overlaps protected execution usage state")
				}
			}
		}
		return nil
	}
	for _, existing := range records {
		if candidate.Config.Labels["codex-dock.managed"] == "true" {
			if err := check(candidate, usageMountPaths(existing)); err != nil {
				return err
			}
		}
		if existing.Config.Labels["codex-dock.managed"] == "true" {
			if err := check(existing, usageMountPaths(candidate)); err != nil {
				return err
			}
		}
	}
	return nil
}
