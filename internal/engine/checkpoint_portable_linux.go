//go:build linux

package engine

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/archive"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/errdefs"
	"github.com/pacificbelt30/codex-dock/internal/diskcheckpoint"
)

func (b *Backend) portableCheckpointHTTP(w http.ResponseWriter, r *http.Request, ctx context.Context, id string) {
	if !b.checkpointMu.TryLock() {
		failure(w, fmt.Errorf("checkpoint operation active: %w", errdefs.ErrConflict))
		return
	}
	defer b.checkpointMu.Unlock()
	if id != "" {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		info, err := b.checkpointInfo(ctx, id)
		if err != nil {
			failure(w, err)
			return
		}
		if info.EntrypointSHA256 != entrypointDigest() {
			failure(w, fmt.Errorf("checkpoint entrypoint is incompatible"))
			return
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.layout.v1+tar")
		if err := b.client.Export(ctx, w, archive.WithImage(b.client.ImageService(), info.Image), archive.WithSkipDockerManifest()); err != nil {
			// Headers may already have been sent. The client validates the completed
			// archive before publication, so a truncated stream is never a backup.
			return
		}
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	f, err := os.CreateTemp(b.opts.State, ".checkpoint-import-")
	if err != nil {
		failure(w, err)
		return
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	n, err := io.Copy(f, http.MaxBytesReader(w, r.Body, diskcheckpoint.MaxArchiveBytes))
	if err != nil || n == 0 {
		failure(w, fmt.Errorf("reading bounded checkpoint archive: %w", err))
		return
	}
	verified, err := diskcheckpoint.ValidatePortable(ctx, f, entrypointDigest())
	if err != nil {
		failure(w, err)
		return
	}
	catalog, err := b.checkpointCatalog(ctx)
	if err != nil {
		failure(w, err)
		return
	}
	for _, other := range catalog.Items {
		if other.ID == verified.Info.ID {
			if other.Digest != verified.Info.Digest {
				failure(w, fmt.Errorf("checkpoint ID already has different content: %w", errdefs.ErrConflict))
				return
			}
			reply(w, other)
			return
		}
	}
	if catalog.UsedBytes+verified.Info.Bytes > diskcheckpoint.CapacityBytes || len(catalog.Items) >= 1024 {
		failure(w, fmt.Errorf("checkpoint catalog capacity exhausted"))
		return
	}
	if _, err = b.client.ImageService().Get(ctx, verified.Info.Image); !errdefs.IsNotFound(err) {
		failure(w, fmt.Errorf("checkpoint image name already exists or cannot be checked: %w", errdefs.ErrConflict))
		return
	}
	ctx, release, err := b.client.WithLease(ctx, leases.WithRandomID(), leases.WithExpiration(30*time.Minute))
	if err != nil {
		failure(w, err)
		return
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		_ = release(cleanup)
	}()
	if _, err = f.Seek(0, io.SeekStart); err == nil {
		_, err = archive.ImportIndex(ctx, b.client.ContentStore(), f)
	}
	if err != nil {
		failure(w, err)
		return
	}
	// ImportIndex only ingests content. Establish native GC edges before the
	// lease is released; otherwise a valid manifest could lose its layer.
	store := b.client.ContentStore()
	if err := images.Walk(ctx, images.SetChildrenLabels(store, images.ChildrenHandler(store)), verified.Target); err != nil {
		failure(w, err)
		return
	}
	// Never publish archive-supplied tags.
	_, err = b.client.ImageService().Create(ctx, images.Image{Name: verified.Info.Image, Target: verified.Target, Labels: map[string]string{diskcheckpoint.OwnerLabel: verified.Info.ID}})
	if err != nil {
		failure(w, err)
		return
	}
	reply(w, verified.Info)
}
