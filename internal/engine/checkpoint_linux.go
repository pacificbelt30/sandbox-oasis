//go:build linux

package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	dockerdefaults "github.com/pacificbelt30/codex-dock/docker"
	"github.com/pacificbelt30/codex-dock/internal/buildinfo"
	"github.com/pacificbelt30/codex-dock/internal/diskcheckpoint"
	"golang.org/x/sys/unix"
)

func checkpointRef(id string) string { return "docker.io/library/oaiso-checkpoint:" + id }
func entrypointDigest() string {
	s := sha256.Sum256(dockerdefaults.Entrypoint)
	return hex.EncodeToString(s[:])
}

// A private OS-thread mount namespace dies with the thread, including on a
// node crash. Mount propagation cannot expose snapshot mounts to the host.
func isolatedCheckpointMount(ctx context.Context, mounts []mount.Mount, fn func(string) error) error {
	done := make(chan error, 1)
	go func() {
		runtime.LockOSThread() // Intentionally not unlocked: exit destroys this thread.
		if err := unix.Unshare(unix.CLONE_NEWNS | unix.CLONE_FS); err != nil {
			done <- err
			return
		}
		if err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, ""); err != nil {
			done <- err
			return
		}
		done <- mount.WithReadonlyTempMount(ctx, mounts, fn)
	}()
	return <-done
}

func (b *Backend) checkpointInfo(ctx context.Context, id string) (diskcheckpoint.Info, error) {
	var info diskcheckpoint.Info
	if !diskcheckpoint.IDPattern.MatchString(id) {
		return info, fmt.Errorf("invalid checkpoint ID: %w", errdefs.ErrInvalidArgument)
	}
	img, ic, err := b.image(ctx, checkpointRef(id))
	if err != nil {
		return info, err
	}
	metadata, err := b.client.ImageService().Get(ctx, checkpointRef(id))
	if err != nil {
		return info, err
	}
	if metadata.Labels[diskcheckpoint.OwnerLabel] != id {
		return info, fmt.Errorf("image is not an owned checkpoint")
	}
	raw := ic.Config.Labels[diskcheckpoint.MetadataLabel]
	if len(raw) > 1<<16 || json.Unmarshal([]byte(raw), &info) != nil || info.Validate() != nil || info.ID != id || ic.OS != "linux" || ic.Architecture != "amd64" || len(ic.RootFS.DiffIDs) != 1 {
		return diskcheckpoint.Info{}, fmt.Errorf("checkpoint metadata is incompatible or corrupt")
	}
	info.Digest = img.Target().Digest.String()
	return info, nil
}
func (b *Backend) checkpointCatalog(ctx context.Context) (diskcheckpoint.Catalog, error) {
	catalog := diskcheckpoint.Catalog{CapacityBytes: diskcheckpoint.CapacityBytes, Items: []diskcheckpoint.Info{}}
	all, err := b.client.ImageService().List(ctx, `labels."`+diskcheckpoint.OwnerLabel+`"`)
	if err != nil {
		return catalog, err
	}
	if len(all) > 1024 {
		return catalog, fmt.Errorf("checkpoint catalog exceeds 1024 items")
	}
	for _, img := range all {
		info, err := b.checkpointInfo(ctx, img.Labels[diskcheckpoint.OwnerLabel])
		if err != nil {
			return catalog, err
		}
		catalog.UsedBytes += info.Bytes
		catalog.Items = append(catalog.Items, info)
	}
	slices.SortFunc(catalog.Items, func(a, b diskcheckpoint.Info) int { return strings.Compare(a.ID, b.ID) })
	return catalog, nil
}

func (b *Backend) createCheckpoint(ctx context.Context, worker string, options diskcheckpoint.Options) (result diskcheckpoint.Info, resultErr error) {
	if !b.checkpointMu.TryLock() {
		return result, fmt.Errorf("another checkpoint operation is active: %w", errdefs.ErrConflict)
	}
	defer b.checkpointMu.Unlock()
	b.mu.Lock()
	rec, c, err := b.load(ctx, worker)
	if err != nil {
		b.mu.Unlock()
		return result, err
	}
	if rec.Config.Labels["codex-dock.managed"] != "true" || rec.Host.Runtime != "io.containerd.kata.v2" || rec.Config.Labels["codex-dock.authentication-session"] == "true" || rec.Quarantined || rec.Config.Labels["codex-dock.quarantined"] == "true" {
		b.mu.Unlock()
		return result, fmt.Errorf("checkpoint requires a regular managed Kata worker")
	}
	if _, err = c.Task(ctx, nil); err == nil {
		b.mu.Unlock()
		return result, fmt.Errorf("stop the worker before checkpointing: %w", errdefs.ErrConflict)
	} else if !errdefs.IsNotFound(err) {
		b.mu.Unlock()
		return result, err
	}
	if b.activeExecutions(rec.ID) > 0 {
		b.mu.Unlock()
		return result, fmt.Errorf("worker has active or pending executions: %w", errdefs.ErrConflict)
	}
	if err = b.kernelIdle(rec); err != nil {
		b.mu.Unlock()
		return result, err
	}
	targets := checkpointExcludedMounts(rec)
	options, err = diskcheckpoint.Normalize(options, targets)
	if err != nil {
		b.mu.Unlock()
		return result, err
	}
	mounts, err := b.client.SnapshotService(b.opts.Snapshotter).Mounts(ctx, rec.ID)
	if err != nil {
		b.mu.Unlock()
		return result, err
	}
	if b.checkpointing == nil {
		b.checkpointing = map[string]bool{}
	}
	b.checkpointing[rec.ID] = true
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.checkpointing, rec.ID); b.mu.Unlock() }()
	catalog, err := b.checkpointCatalog(ctx)
	if err != nil {
		return result, err
	}
	remaining := diskcheckpoint.CapacityBytes - catalog.UsedBytes
	if remaining < 1<<20 || len(catalog.Items) >= 1024 {
		return result, fmt.Errorf("checkpoint capacity exhausted")
	}
	if options.MaxBytes > remaining {
		options.MaxBytes = remaining
	}
	// Leases protect all staged content from GC, but publish only a fully written
	// sanitized image after the read-only mount has successfully been removed.
	ctx, release, err := b.client.WithLease(ctx, leases.WithRandomID(), leases.WithExpiration(30*time.Minute))
	if err != nil {
		return result, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		defer cancel()
		resultErr = errors.Join(resultErr, release(cleanup))
	}()
	checkpointID, err := id()
	if err != nil {
		return result, err
	}
	result = diskcheckpoint.Info{Format: diskcheckpoint.Format, ID: checkpointID, Image: checkpointRef(checkpointID), Created: time.Now().UTC(), Source: rec.ID, BuildVersion: buildinfo.Version, EntrypointSHA256: entrypointDigest(), Excluded: options.Exclude}
	store := b.client.ContentStore()
	ref := "oaiso-checkpoint-" + checkpointID
	writer, err := store.Writer(ctx, content.WithRef(ref))
	if err != nil {
		return result, err
	}
	defer func() {
		_ = writer.Close()
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = store.Abort(cleanup, ref)
	}()
	err = isolatedCheckpointMount(ctx, mounts, func(root string) error {
		// Custom entrypoints may require excluded state or obsolete auth protocol.
		// Require the canonical script; restoration always performs a fresh boot.
		fs, err := os.OpenRoot(root)
		if err != nil {
			return err
		}
		defer func() { _ = fs.Close() }()
		f, err := fs.OpenFile("entrypoint.sh", os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
		if err != nil {
			return err
		}
		stat, statErr := f.Stat()
		if statErr != nil || !stat.Mode().IsRegular() || stat.Size() > 1<<20 {
			_ = f.Close()
			return fmt.Errorf("checkpoint entrypoint must be a small regular file")
		}
		data, readErr := io.ReadAll(io.LimitReader(f, 1<<20))
		_ = f.Close()
		if readErr != nil {
			return readErr
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != entrypointDigest() {
			return fmt.Errorf("checkpoint requires the current canonical entrypoint")
		}
		result.Bytes, result.Entries, err = diskcheckpoint.Archive(ctx, root, writer, options)
		return err
	})
	if err != nil {
		return diskcheckpoint.Info{}, err
	}
	layer := v1.Descriptor{MediaType: v1.MediaTypeImageLayer, Size: result.Bytes, Digest: writer.Digest()}
	if err = writer.Commit(ctx, result.Bytes, layer.Digest); err != nil && !errdefs.IsAlreadyExists(err) {
		return diskcheckpoint.Info{}, err
	}
	metadata, err := encodeCheckpointMetadata(result)
	if err != nil {
		return diskcheckpoint.Info{}, err
	}
	config := v1.Image{Platform: v1.Platform{OS: "linux", Architecture: "amd64"}, Created: &result.Created, RootFS: v1.RootFS{Type: "layers", DiffIDs: []digest.Digest{layer.Digest}}, Config: v1.ImageConfig{Entrypoint: []string{"/entrypoint.sh"}, WorkingDir: "/workspace", Env: []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}, Labels: map[string]string{"io.codex-dock.start-hooks": "1", diskcheckpoint.MetadataLabel: string(metadata)}}}
	writeJSON := func(value any, media string, labels map[string]string) (v1.Descriptor, error) {
		data, err := json.Marshal(value)
		if err != nil {
			return v1.Descriptor{}, err
		}
		desc := v1.Descriptor{MediaType: media, Size: int64(len(data)), Digest: digest.FromBytes(data)}
		err = content.WriteBlob(ctx, store, ref+"-"+desc.Digest.Encoded(), bytes.NewReader(data), desc, content.WithLabels(labels))
		return desc, err
	}
	configDesc, err := writeJSON(config, v1.MediaTypeImageConfig, nil)
	if err != nil {
		return diskcheckpoint.Info{}, err
	}
	manifest := v1.Manifest{Versioned: imagespec.Versioned{SchemaVersion: 2}, MediaType: v1.MediaTypeImageManifest, Config: configDesc, Layers: []v1.Descriptor{layer}}
	target, err := writeJSON(manifest, v1.MediaTypeImageManifest, map[string]string{"containerd.io/gc.ref.content.config": configDesc.Digest.String(), "containerd.io/gc.ref.content.l.0": layer.Digest.String()})
	if err != nil {
		return diskcheckpoint.Info{}, err
	}
	if err = ctx.Err(); err != nil {
		return diskcheckpoint.Info{}, err
	}
	_, err = b.client.ImageService().Create(ctx, images.Image{Name: result.Image, Target: target, Labels: map[string]string{diskcheckpoint.OwnerLabel: checkpointID}})
	if err != nil {
		return diskcheckpoint.Info{}, err
	}
	result.Digest = target.Digest.String()
	return result, nil
}

func (b *Backend) checkpointHTTP(w http.ResponseWriter, r *http.Request, ctx context.Context, path string) {
	if path == "/checkpoints" {
		if r.Method == http.MethodGet {
			catalog, err := b.checkpointCatalog(ctx)
			if err != nil {
				failure(w, err)
				return
			}
			reply(w, catalog)
			return
		}
		if r.Method == http.MethodPost {
			var request struct {
				Worker  string                 `json:"worker"`
				Options diskcheckpoint.Options `json:"options"`
			}
			if err := decode(r, &request); err != nil {
				failure(w, err)
				return
			}
			info, err := b.createCheckpoint(ctx, request.Worker, request.Options)
			if err != nil {
				failure(w, err)
				return
			}
			reply(w, info)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(path, "/checkpoints/")
	info, err := b.checkpointInfo(ctx, id)
	if err != nil {
		failure(w, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		if info.EntrypointSHA256 != entrypointDigest() {
			failure(w, fmt.Errorf("checkpoint entrypoint is incompatible with this node"))
			return
		}
		reply(w, info)
	case http.MethodDelete:
		if !b.checkpointMu.TryLock() {
			failure(w, fmt.Errorf("checkpoint operation active: %w", errdefs.ErrConflict))
			return
		}
		defer b.checkpointMu.Unlock()
		b.mu.Lock()
		defer b.mu.Unlock()
		all, err := b.client.Containers(ctx)
		if err != nil {
			failure(w, err)
			return
		}
		for _, c := range all {
			record, err := c.Info(ctx)
			if err != nil {
				failure(w, err)
				return
			}
			if record.Image == info.Image {
				failure(w, fmt.Errorf("checkpoint is used by a retained worker: %w", errdefs.ErrConflict))
				return
			}
		}
		if err := b.client.ImageService().Delete(ctx, info.Image); err != nil {
			failure(w, err)
			return
		}
		reply(w, map[string]bool{"removed": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// Applies even when a caller selects the checkpoint image through --image.
func validateCheckpointImage(ic v1.Image, runtime string) error {
	raw := ic.Config.Labels[diskcheckpoint.MetadataLabel]
	if raw == "" {
		return nil
	}
	var info diskcheckpoint.Info
	if runtime != "io.containerd.kata.v2" || json.Unmarshal([]byte(raw), &info) != nil || info.Validate() != nil || info.EntrypointSHA256 != entrypointDigest() {
		return fmt.Errorf("checkpoint image requires compatible disk format, canonical entrypoint and Kata runtime")
	}
	return nil
}

func checkpointExcludedMounts(rec *record) []string {
	var targets []string
	for _, m := range rec.Spec.Mounts {
		targets = append(targets, m.Destination)
	}
	targets = append(targets, rec.Host.MaskedPaths...)
	if rec.Spec.Linux != nil {
		targets = append(targets, rec.Spec.Linux.MaskedPaths...)
	}
	return targets
}

func encodeCheckpointMetadata(info diskcheckpoint.Info) ([]byte, error) {
	if err := info.Validate(); err != nil {
		return nil, err
	}
	data, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<16 {
		return nil, fmt.Errorf("checkpoint metadata exceeds 64 KiB; reduce exclusions")
	}
	return data, nil
}
