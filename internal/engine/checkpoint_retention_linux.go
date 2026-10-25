//go:build linux

package engine

import (
	"context"
	"fmt"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/errdefs"
	"github.com/pacificbelt30/codex-dock/internal/diskcheckpoint"
	"net/http"
	"time"
)

func (b *Backend) checkpointMaintenanceHTTP(w http.ResponseWriter, r *http.Request, ctx context.Context, path string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !b.checkpointMu.TryLock() {
		failure(w, fmt.Errorf("checkpoint operation active: %w", errdefs.ErrConflict))
		return
	}
	defer b.checkpointMu.Unlock()
	b.mu.Lock()
	defer b.mu.Unlock()
	if path == "/checkpoints/pin" {
		var p diskcheckpoint.PinRequest
		if err := decode(r, &p); err != nil {
			failure(w, err)
			return
		}
		info, err := b.checkpointInfo(ctx, p.ID)
		if err != nil {
			failure(w, err)
			return
		}
		value := "false"
		if p.Pinned {
			value = "true"
		}
		_, err = b.client.ImageService().Update(ctx, images.Image{Name: info.Image, Labels: map[string]string{diskcheckpoint.PinLabel: value}}, "labels."+diskcheckpoint.PinLabel)
		if err != nil {
			failure(w, err)
			return
		}
		reply(w, map[string]bool{"pinned": p.Pinned})
		return
	}
	var o diskcheckpoint.RetentionOptions
	if err := decode(r, &o); err != nil {
		failure(w, err)
		return
	}
	catalog, err := b.checkpointCatalog(ctx)
	if err != nil {
		failure(w, err)
		return
	}
	all, err := b.client.Containers(ctx)
	if err != nil {
		failure(w, err)
		return
	}
	used := map[string]bool{}
	for _, c := range all {
		img, err := c.Image(ctx)
		if err != nil {
			failure(w, err)
			return
		}
		for _, v := range catalog.Items {
			if img.Target().Digest.String() == v.Digest {
				used[v.ID] = true
			}
		}
	}
	plan, err := diskcheckpoint.PlanRetention(catalog, o, used, time.Now())
	if err != nil {
		failure(w, fmt.Errorf("%w: %v", errdefs.ErrInvalidArgument, err))
		return
	}
	if o.Apply {
		if o.ExpectedPlan == "" || o.ExpectedPlan != plan.ID {
			failure(w, fmt.Errorf("retention plan changed; review again: %w", errdefs.ErrConflict))
			return
		}
		for _, id := range plan.Delete {
			if err := b.client.ImageService().Delete(ctx, checkpointRef(id)); err != nil {
				failure(w, fmt.Errorf("retention partially applied; inspect catalog before retrying: %w", err))
				return
			}
			plan.Removed = append(plan.Removed, id)
		}
	}
	reply(w, plan)
}
