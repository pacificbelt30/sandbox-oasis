//go:build linux

package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/containerd/errdefs"
	"github.com/pacificbelt30/codex-dock/internal/managedworkspace"
	"github.com/pacificbelt30/codex-dock/internal/privatefile"
	"github.com/pacificbelt30/codex-dock/internal/strictjson"
)

func (b *Backend) recoveryPointRecords() string {
	return filepath.Join(b.opts.State, "recovery-points")
}
func (b *Backend) saveRecoveryPoint(p managedworkspace.RecoveryPoint) error {
	return saveWorkspaceJSON(filepath.Join(b.recoveryPointRecords(), p.ID+".json"), p)
}
func (b *Backend) loadRecoveryPoint(id string) (managedworkspace.RecoveryPoint, error) {
	var p managedworkspace.RecoveryPoint
	if !managedworkspace.ID.MatchString(id) {
		return p, fmt.Errorf("invalid recovery point ID")
	}
	data, err := privatefile.Read(filepath.Join(b.recoveryPointRecords(), id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return p, errdefs.ErrNotFound
	}
	if err != nil {
		return p, err
	}
	if err = strictjson.Decode(strings.NewReader(string(data)), privatefile.Limit, &p); err != nil {
		return p, err
	}
	if p.ID != id || !hexID.MatchString(p.Source) || p.Created.IsZero() || len(p.Name) == 0 || len([]rune(p.Name)) > 64 || p.Workspace != "" && !hexID.MatchString(p.Workspace) || p.SourceWorkspace != "" && !hexID.MatchString(p.SourceWorkspace) {
		return p, fmt.Errorf("untrusted recovery point identity")
	}
	switch p.State {
	case "preparing", "ready", "failed", "deleting":
	default:
		return p, fmt.Errorf("untrusted recovery point state")
	}
	if p.Checkpoint.ID != "" {
		if err = p.Checkpoint.Validate(); err != nil {
			return p, err
		}
	}
	return p, nil
}
func (b *Backend) recoveryPoints() ([]managedworkspace.RecoveryPoint, error) {
	points := []managedworkspace.RecoveryPoint{}
	dir, err := os.Open(b.recoveryPointRecords())
	if errors.Is(err, os.ErrNotExist) {
		return points, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	for {
		entries, e := dir.ReadDir(64)
		if e != nil && e != io.EOF {
			return nil, e
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".workspace-") {
				continue
			}
			if !strings.HasSuffix(entry.Name(), ".json") || entry.IsDir() {
				return nil, fmt.Errorf("unexpected recovery point metadata")
			}
			p, err := b.loadRecoveryPoint(strings.TrimSuffix(entry.Name(), ".json"))
			if err != nil {
				return nil, err
			}
			points = append(points, p)
			if len(points) > managedworkspace.MaxItems {
				return nil, fmt.Errorf("recovery point catalog exceeds its bound")
			}
		}
		if e == io.EOF {
			break
		}
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Created.After(points[j].Created) })
	return points, nil
}
func (b *Backend) workspaceReferencedByPointExcept(id, except string) error {
	points, err := b.recoveryPoints()
	if err != nil {
		return err
	}
	for _, p := range points {
		if p.Workspace == id && (p.ID != except || p.State != "deleting") {
			return fmt.Errorf("workspace is retained by recovery point %s: %w", p.ID, errdefs.ErrConflict)
		}
	}
	return nil
}
func (b *Backend) checkpointReferencedByPoint(id string) (bool, error) {
	b.workspaceMu.Lock()
	defer b.workspaceMu.Unlock()
	points, err := b.recoveryPoints()
	if err != nil {
		return false, err
	}
	for _, p := range points {
		if p.Checkpoint.ID == id {
			return true, nil
		}
	}
	return false, nil
}

// A tag alone is not the identity of a saved rootfs. The persisted point binds
// its checkpoint to the exact digest, independently of image-provided labels.
func (b *Backend) verifyPointCheckpointDigest(id, digest string) error {
	b.workspaceMu.Lock()
	defer b.workspaceMu.Unlock()
	points, err := b.recoveryPoints()
	if err != nil {
		return err
	}
	for _, p := range points {
		if p.Checkpoint.ID == id && p.Checkpoint.Digest != digest {
			return fmt.Errorf("recovery point rootfs digest changed; saved reference retained")
		}
	}
	return nil
}
func (b *Backend) beginRecoveryPoint(ctx context.Context, options managedworkspace.SaveOptions) (managedworkspace.RecoveryPoint, error) {
	var p managedworkspace.RecoveryPoint
	if !utf8.ValidString(options.Name) || len(options.Name) == 0 || len([]rune(options.Name)) > 64 || strings.IndexFunc(options.Name, unicode.IsControl) >= 0 {
		return p, fmt.Errorf("recovery point name requires 1–64 characters")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.workspaceMu.Lock()
	defer b.workspaceMu.Unlock()
	points, err := b.recoveryPoints()
	if err != nil {
		return p, err
	}
	if len(points) >= managedworkspace.MaxItems {
		return p, fmt.Errorf("recovery point capacity exhausted")
	}
	r, c, err := b.load(ctx, options.Worker)
	if err != nil {
		return p, err
	}
	if r.Config.Labels["codex-dock.managed"] != "true" || r.Host.Runtime != "io.containerd.kata.v2" || r.Quarantined || r.FailedCreate || r.Config.Labels["codex-dock.authentication-session"] == "true" {
		return p, fmt.Errorf("recovery point requires an ordinary stopped Kata VM")
	}
	if _, err = c.Task(ctx, nil); err == nil {
		return p, fmt.Errorf("stop the VM before saving a recovery point: %w", errdefs.ErrConflict)
	} else if !errdefs.IsNotFound(err) {
		return p, err
	}
	if b.activeExecutions(r.ID) > 0 || b.checkpointing[r.ID] {
		return p, fmt.Errorf("VM has pending operations: %w", errdefs.ErrConflict)
	}
	if err = b.kernelIdle(r); err != nil {
		return p, err
	}
	pointID, err := id()
	if err != nil {
		return p, err
	}
	p = managedworkspace.RecoveryPoint{ID: pointID, Name: options.Name, State: "preparing", Created: time.Now().UTC(), Source: r.ID, Profile: r.Config.Labels["codex-dock.resource-profile"]}
	var workspace *workspaceRecord
	if options.IncludeWorkspace {
		workspace, err = b.loadWorkspace(r.Config.Labels[managedworkspace.Label])
		if err != nil {
			return p, err
		}
		if workspace.Info.Lease != r.ID || workspace.Info.State != "ready" {
			return p, fmt.Errorf("workspace is not exclusively leased to this stopped VM")
		}
		if err = b.validateWorkspaceDisk(workspace); err != nil {
			return p, err
		}
		p.SourceWorkspace = workspace.Info.ID
	}
	if err = b.saveRecoveryPoint(p); err != nil {
		return p, err
	}
	if workspace != nil {
		workspace.Info.State, workspace.Info.Operation = "saving", p.ID
		if err = b.saveWorkspace(workspace); err != nil {
			return p, err
		}
	}
	return p, nil
}
func (b *Backend) finishRecoverySource(p managedworkspace.RecoveryPoint) error {
	if p.SourceWorkspace == "" {
		return nil
	}
	r, err := b.loadWorkspace(p.SourceWorkspace)
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if r.Info.State == "ready" && r.Info.Operation == "" {
		return nil
	}
	if r.Info.State == "saving" && r.Info.Operation != p.ID {
		return nil
	}
	if r.Info.State != "saving" || r.Info.Operation != p.ID || r.Info.Lease != p.Source {
		return fmt.Errorf("recovery source ownership changed")
	}
	r.Info.State, r.Info.Operation = "ready", ""
	return b.saveWorkspace(r)
}
func (b *Backend) createRecoveryPoint(ctx context.Context, options managedworkspace.SaveOptions) (p managedworkspace.RecoveryPoint, resultErr error) {
	p, err := b.beginRecoveryPoint(ctx, options)
	if err != nil {
		return p, err
	}
	defer func() {
		b.workspaceMu.Lock()
		defer b.workspaceMu.Unlock()
		if resultErr != nil {
			p.State = "failed"
			p.Error = "save incomplete; inspect retained components before retrying"
			_ = b.saveRecoveryPoint(p)
		}
		resultErr = errors.Join(resultErr, b.finishRecoverySource(p))
	}()
	if !b.checkpointMu.TryLock() {
		return p, fmt.Errorf("another checkpoint operation is active: %w", errdefs.ErrConflict)
	}
	p.Checkpoint, err = b.createCheckpointLocked(ctx, p.Source, options.Checkpoint)
	if err == nil {
		b.workspaceMu.Lock()
		err = b.saveRecoveryPoint(p)
		b.workspaceMu.Unlock()
	}
	b.checkpointMu.Unlock()
	if err != nil {
		return p, err
	}
	if options.IncludeWorkspace {
		// The persisted saving state prevents the source VM from starting or
		// being removed between rootfs capture and the raw workspace copy.
		b.mu.Lock()
		b.workspaceMu.Lock()
		err = func() error {
			source, e := b.loadWorkspace(p.SourceWorkspace)
			if e != nil {
				return e
			}
			if source.Info.Operation != p.ID || source.Info.State != "saving" || source.Info.Lease != p.Source {
				return fmt.Errorf("recovery workspace lease changed")
			}
			options := source.Info.Options
			options.Name = p.Name
			target, e := b.newWorkspace(ctx, options)
			if e != nil {
				return e
			}
			target.Info.State = "restoring"
			target.Info.Kind = "snapshot"
			if e = b.saveWorkspace(target); e != nil {
				return e
			}
			p.Workspace = target.Info.ID
			if e = b.saveRecoveryPoint(p); e != nil {
				return e
			}
			digest, e := b.copyWorkspaceDisk(ctx, source, target)
			if e != nil {
				return e
			}
			manifest, e := b.workspaceManifest(source)
			if e != nil {
				return e
			}
			if e = saveWorkspaceJSON(filepath.Join(b.workspaceRecords(), target.Info.ID+".seed"), manifest); e != nil {
				return e
			}
			target.Info.Kind, target.Info.State, target.Info.DiskSHA256 = "snapshot", "ready", digest
			target.Info.SeedID, target.Info.SeedFiles, target.Info.SeedBytes = source.Info.SeedID, source.Info.SeedFiles, source.Info.SeedBytes
			return b.saveWorkspace(target)
		}()
		b.workspaceMu.Unlock()
		b.mu.Unlock()
		if err != nil {
			return p, err
		}
	}
	if err = ctx.Err(); err != nil {
		return p, err
	}
	p.State = "ready"
	b.workspaceMu.Lock()
	err = b.saveRecoveryPoint(p)
	b.workspaceMu.Unlock()
	return p, err
}
func (b *Backend) recoverWorkspaceOperations() error {
	b.workspaceMu.Lock()
	defer b.workspaceMu.Unlock()
	points, err := b.recoveryPoints()
	if err != nil {
		return err
	}
	for _, p := range points {
		if p.State == "preparing" {
			p.State = "failed"
			p.Error = "node restarted before recovery point publication"
			if err = b.saveRecoveryPoint(p); err != nil {
				return err
			}
		}
		if p.SourceWorkspace != "" {
			if err = b.finishRecoverySource(p); err != nil {
				return err
			}
		}
	}
	return nil
}
func (b *Backend) removeRecoveryPoint(ctx context.Context, p managedworkspace.RecoveryPoint) error {
	if p.State == "preparing" {
		return fmt.Errorf("recovery point operation is active: %w", errdefs.ErrConflict)
	}
	if p.Checkpoint.ID != "" {
		all, err := b.client.Containers(ctx)
		if err != nil {
			return err
		}
		for _, c := range all {
			info, err := c.Info(ctx)
			if err != nil {
				return err
			}
			image, err := c.Image(ctx)
			if err != nil {
				return err
			}
			if info.Image == p.Checkpoint.Image || image.Target().Digest.String() == p.Checkpoint.Digest {
				return fmt.Errorf("recovery rootfs is used by a retained VM: %w", errdefs.ErrConflict)
			}
		}
	}
	if p.Workspace != "" {
		r, err := b.loadWorkspace(p.Workspace)
		if err != nil && !errdefs.IsNotFound(err) {
			return err
		}
		if err == nil && r.Info.Lease != "" {
			return fmt.Errorf("recovery workspace has a lease")
		}
	}
	p.State = "deleting"
	if err := b.saveRecoveryPoint(p); err != nil {
		return err
	}
	if p.Checkpoint.ID != "" {
		image, err := b.client.ImageService().Get(ctx, p.Checkpoint.Image)
		if err == nil && image.Target.Digest.String() != p.Checkpoint.Digest {
			return fmt.Errorf("recovery checkpoint image identity changed")
		}
		if err != nil && !errdefs.IsNotFound(err) {
			return err
		}
		if err == nil {
			if err = b.client.ImageService().Delete(ctx, p.Checkpoint.Image); err != nil {
				return err
			}
		}
	}
	if p.Workspace != "" {
		r, err := b.loadWorkspace(p.Workspace)
		if err == nil {
			if err = b.removeWorkspaceForPoint(r, p.ID); err != nil {
				return err
			}
		} else if !errdefs.IsNotFound(err) {
			return err
		}
	}
	if err := os.Remove(filepath.Join(b.recoveryPointRecords(), p.ID+".json")); err != nil {
		return err
	}
	return syncStateDirectory(b.recoveryPointRecords())
}
func (b *Backend) recoveryPointsHTTP(w http.ResponseWriter, r *http.Request, ctx context.Context, path string) {
	if path == "/recovery-points" {
		switch r.Method {
		case http.MethodGet:
			b.workspaceMu.Lock()
			out, err := b.recoveryPoints()
			b.workspaceMu.Unlock()
			if err != nil {
				failure(w, err)
			} else {
				reply(w, out)
			}
		case http.MethodPost:
			var options managedworkspace.SaveOptions
			if err := decode(r, &options); err != nil {
				failure(w, err)
				return
			}
			out, err := b.createRecoveryPoint(ctx, options)
			if err != nil {
				if out.ID != "" {
					err = fmt.Errorf("recovery point %s incomplete; inspect catalog: %w", out.ID, err)
				}
				failure(w, err)
			} else {
				reply(w, out)
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}
	if r.Method != http.MethodDelete {
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
	b.workspaceMu.Lock()
	defer b.workspaceMu.Unlock()
	p, err := b.loadRecoveryPoint(strings.TrimPrefix(path, "/recovery-points/"))
	if err == nil {
		err = b.removeRecoveryPoint(ctx, p)
	}
	if err != nil {
		failure(w, err)
	} else {
		reply(w, map[string]bool{"removed": true})
	}
}
