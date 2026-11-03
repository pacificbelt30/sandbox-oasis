//go:build linux

package engine

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/pacificbelt30/codex-dock/internal/diskcheckpoint"
	"github.com/pacificbelt30/codex-dock/internal/managedworkspace"
	"github.com/pacificbelt30/codex-dock/internal/workspacecopy"
)

func workspaceFixture(t *testing.T) (*Backend, *workspaceRecord, *record, *specs.Spec) {
	t.Helper()
	b, worker, spec := stateDiskFixture(t)
	if spec.Process.User.UID == 0 {
		spec.Process.User.UID = 1001
		spec.Process.User.GID = 1001
	}
	b.opts.StorageRoot = t.TempDir()
	b.workspaceCapacityCheck = func(context.Context, int64) error { return nil }
	options := managedworkspace.Options{Name: "日本語", Bytes: 64 << 20, Inodes: 1024, UID: spec.Process.User.UID, GID: spec.Process.User.GID}
	r, err := b.newWorkspace(t.Context(), options)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "uncommitted"), []byte("current contents"), 0600)
	plan, err := workspacecopy.Prepare(t.Context(), root, options.SeedLimit())
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err = plan.Write(t.Context(), &archive); err != nil {
		t.Fatal(err)
	}
	if err = b.seedWorkspace(t.Context(), r, &archive); err != nil {
		t.Fatal(err)
	}
	worker.Config.Labels[managedworkspace.Label] = r.Info.ID
	return b, r, worker, spec
}
func TestManagedWorkspaceReserveAttachLeaseAndRetain(t *testing.T) {
	b, r, worker, spec := workspaceFixture(t)
	st, err := os.Stat(b.workspaceImage(r.Info.ID))
	if err != nil {
		t.Fatal(err)
	}
	if st.Size() != 64<<20 || st.Sys().(*syscall.Stat_t).Blocks*512 < st.Size() {
		t.Fatal("reservation missing")
	}
	f, err := os.Open(b.workspaceImage(r.Info.ID))
	if err != nil {
		t.Fatal(err)
	}
	var super [1024]byte
	_, err = f.ReadAt(super[:], 1024)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint32(super[:4]) > 1024 {
		t.Fatal("inode ceiling missing")
	}
	if err = b.attachManagedWorkspace(worker, spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Mounts) != 1 || spec.Mounts[0].Destination != "/workspace" {
		t.Fatal(spec.Mounts)
	}
	r, err = b.loadWorkspace(r.Info.ID)
	if err != nil || r.Info.Lease != worker.ID {
		t.Fatal(r, err)
	}
	other := *worker
	other.ID = strings.Repeat("b", 64)
	second := &specs.Spec{Process: spec.Process}
	if err = b.attachManagedWorkspace(&other, second); err == nil {
		t.Fatal("second lease acquired")
	}
	if err = b.releaseManagedWorkspace(&other); err != nil {
		t.Fatal("rejected create blocks cleanup", err)
	}
	if err = b.removeWorkspace(r); err == nil {
		t.Fatal("leased workspace deleted")
	}
	b.opts.Tools["mkfs"] = "/must/not/parse/guest/filesystem"
	if err = b.ensureManagedWorkspace(worker); err != nil {
		t.Fatal("restart reparsed or changed data", err)
	}
	if err = b.seedWorkspace(t.Context(), r, strings.NewReader("")); err == nil {
		t.Fatal("used disk reseeded")
	}
	if err = b.clearWorkspaceMetadata(r); err != nil {
		t.Fatal(err)
	}
	r.Info.Lease = ""
	r.Info.ReadOnly = false
	if err = b.saveWorkspace(r); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(b.workspaceImage(r.Info.ID)); err != nil {
		t.Fatal("metadata cleanup removed data", err)
	}
	if err = b.removeWorkspace(r); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(b.workspaceImage(r.Info.ID)); !os.IsNotExist(err) {
		t.Fatal("disk retained after explicit deletion", err)
	}
}
func TestManagedWorkspaceIdentityAndCloneBoundaries(t *testing.T) {
	b, r, _, _ := workspaceFixture(t)
	p := b.workspaceImage(r.Info.ID)
	if err := os.Rename(p, p+".foreign"); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(r.Info.Options.Bytes)
	f.Close()
	if err = b.validateWorkspaceDisk(r); err == nil {
		t.Fatal("foreign disk accepted")
	}
	if err = b.removeWorkspace(r); err == nil {
		t.Fatal("foreign disk deleted")
	}
	if _, err = os.Stat(p); err != nil {
		t.Fatal("foreign disk not preserved")
	}
	os.Remove(p)
	os.Rename(p+".foreign", p)
	r.Info.Kind = "snapshot"
	r.Info.DiskSHA256 = strings.Repeat("a", 64)
	b.saveWorkspace(r)
	clone, err := b.cloneWorkspace(t.Context(), r, "restore")
	if err == nil || clone == nil || clone.Info.State != "restoring" {
		t.Fatal("bad snapshot hash accepted", clone, err)
	}
	if err = b.seedWorkspace(t.Context(), clone, strings.NewReader("")); err == nil {
		t.Fatal("guest-written disk eligible for host formatting")
	}
	if err = b.removeWorkspace(clone); err != nil {
		t.Fatal(err)
	}
}
func TestWorkspaceRecoveryReferencesAndInterruptedOperations(t *testing.T) {
	b, r, worker, _ := workspaceFixture(t)
	old := managedworkspace.RecoveryPoint{ID: strings.Repeat("b", 64), Name: "older", Source: worker.ID, SourceWorkspace: r.Info.ID, State: "ready", Created: time.Now()}
	current := old
	current.ID = strings.Repeat("c", 64)
	current.State = "preparing"
	current.Workspace = r.Info.ID
	b.saveRecoveryPoint(old)
	b.saveRecoveryPoint(current)
	r.Info.State = "saving"
	r.Info.Lease = worker.ID
	r.Info.Operation = current.ID
	b.saveWorkspace(r)
	if err := b.recoverWorkspaceOperations(); err != nil {
		t.Fatal("older point blocked current recovery", err)
	}
	r, err := b.loadWorkspace(r.Info.ID)
	if err != nil || r.Info.State != "ready" || r.Info.Operation != "" {
		t.Fatal(r, err)
	}
	p, err := b.loadRecoveryPoint(current.ID)
	if err != nil || p.State != "failed" {
		t.Fatal(p, err)
	}
	r.Info.Lease = ""
	b.saveWorkspace(r)
	if err = b.removeWorkspace(r); err == nil {
		t.Fatal("point snapshot removed independently")
	}
	p.State = "deleting"
	b.saveRecoveryPoint(p)
	if err = b.removeWorkspaceForPoint(r, p.ID); err != nil {
		t.Fatal(err)
	}
}

func TestFailedWorkspaceAllocationRemainsExplicitlyRemovable(t *testing.T) {
	b, r, _, _ := workspaceFixture(t)
	r.Info.State = "empty"
	if err := os.Truncate(b.workspaceImage(r.Info.ID), 4096); err != nil {
		t.Fatal(err)
	}
	if err := b.saveWorkspace(r); err != nil {
		t.Fatal(err)
	}
	if err := b.removeWorkspace(r); err != nil {
		t.Fatal("partial allocation cannot be reclaimed", err)
	}
	// A deletion retry must also finish after the raw file was already removed.
	b, r, _, _ = workspaceFixture(t)
	r.Info.State = "deleting"
	b.saveWorkspace(r)
	os.Remove(b.workspaceImage(r.Info.ID))
	if err := b.removeWorkspace(r); err != nil {
		t.Fatal("deletion retry lost ownership intent", err)
	}
}

func TestRecoveryPointRootfsDigestIsIndependentOfImageLabels(t *testing.T) {
	b := &Backend{opts: Options{State: t.TempDir()}}
	id := strings.Repeat("a", 64)
	info := diskcheckpoint.Info{Format: diskcheckpoint.Format, ID: id, Source: id, Image: checkpointRef(id), Created: time.Now(), Bytes: 4096, Entries: 1, EntrypointSHA256: entrypointDigest(), Digest: "sha256:" + strings.Repeat("b", 64)}
	p := managedworkspace.RecoveryPoint{ID: strings.Repeat("c", 64), Name: "point", Source: id, State: "ready", Created: time.Now(), Checkpoint: info}
	if err := b.saveRecoveryPoint(p); err != nil {
		t.Fatal(err)
	}
	if err := b.verifyPointCheckpointDigest(id, info.Digest); err != nil {
		t.Fatal(err)
	}
	if err := b.verifyPointCheckpointDigest(id, "sha256:"+strings.Repeat("d", 64)); err == nil {
		t.Fatal("changed image digest accepted for saved point")
	}
}

func TestManagedWorkspaceRejectsUnverifiablePathMasksBeforeLeasing(t *testing.T) {
	b, r, worker, spec := workspaceFixture(t)
	spec.Linux = &specs.Linux{MaskedPaths: []string{"/workspace/secret"}}
	if err := b.attachManagedWorkspace(worker, spec); err == nil {
		t.Fatal("host-side mask check applied to unrelated source")
	}
	r, err := b.loadWorkspace(r.Info.ID)
	if err != nil || r.Info.Lease != "" {
		t.Fatal("rejected spec acquired lease", r, err)
	}
	spec.Linux.MaskedPaths = []string{"/proc/kcore"}
	worker.Config.Labels[managedworkspace.ReadOnlyLabel] = "true"
	if err = b.attachManagedWorkspace(worker, spec); err != nil {
		t.Fatal("whole disk read-only rejected", err)
	}
	r, err = b.loadWorkspace(r.Info.ID)
	if err != nil || !r.Info.ReadOnly || b.workspaceMountInfo(r).Options[0] != "ro" {
		t.Fatal("read-only mode not enforced", r, err)
	}
}
