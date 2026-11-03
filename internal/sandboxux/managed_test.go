package sandboxux

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/managedworkspace"
)

type managedFixture struct {
	WorkspaceService
	creates, clones int
	info            managedworkspace.Info
}

func (a *managedFixture) List(context.Context) (managedworkspace.Catalog, error) {
	return managedworkspace.Catalog{CapacityBytes: 16 << 30}, nil
}
func (a *managedFixture) Create(_ context.Context, o managedworkspace.Options) (managedworkspace.Info, error) {
	a.creates++
	a.info = managedworkspace.Info{ID: strings.Repeat("a", 64), Options: o, State: "empty", Kind: "workspace"}
	return a.info, nil
}
func (a *managedFixture) Seed(_ context.Context, _ string, r io.Reader) (managedworkspace.Info, error) {
	_, e := io.Copy(io.Discard, r)
	if e == nil {
		a.info.State = "ready"
	}
	return a.info, e
}
func (a *managedFixture) Inspect(context.Context, string) (managedworkspace.Info, error) {
	return a.info, nil
}
func (a *managedFixture) Clone(_ context.Context, _ string, name string) (managedworkspace.Info, error) {
	a.clones++
	v := a.info
	v.ID = strings.Repeat("b", 64)
	v.Kind = "workspace"
	v.Options.Name = name
	return v, nil
}

func TestManagedLaunchReviewIsReadOnlyAndContentChangesAreRejected(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "dirty.txt"), []byte("base"), 0600)
	api := &managedFixture{}
	l := CLILauncher{Workspaces: api, UserMode: "codex"}
	spec, err := l.Prepare(t.Context(), LaunchSpec{Name: "test", Project: root, Network: "offline", Workspace: "managed", WorkspaceBytes: 64 << 20, WorkspaceInodes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if api.creates != 0 || spec.CopyPlan == nil || len(spec.CopyPlan.Manifest.Files) != 1 || spec.WorkspaceOwner.UID == 0 {
		t.Fatal("review created resources or lost content/owner", spec)
	}
	os.WriteFile(filepath.Join(root, "dirty.txt"), []byte("evil"), 0600)
	info, err := l.materializeWorkspace(t.Context(), spec)
	if err == nil || info.ID == "" || api.creates != 1 || api.info.State != "empty" {
		t.Fatal("stale source accepted or resource lost", info, err)
	}
	if _, err = l.materializeWorkspace(t.Context(), LaunchSpec{Workspace: "managed", Project: root}); err == nil {
		t.Fatal("unreviewed launch accepted")
	}
}
func TestExistingWorkspaceOwnerAndSnapshotRestoration(t *testing.T) {
	api := &managedFixture{info: managedworkspace.Info{ID: strings.Repeat("a", 64), Kind: "workspace", State: "ready", Options: managedworkspace.Options{Name: "workspace", Bytes: 64 << 20, Inodes: 1024, UID: 1234, GID: 1235}}}
	l := CLILauncher{Workspaces: api, UserMode: "current"}
	spec, err := l.Prepare(t.Context(), LaunchSpec{Project: t.TempDir(), Network: "offline", Workspace: "existing", WorkspaceID: api.info.ID})
	if err != nil || spec.WorkspaceOwner.UID != 1234 {
		t.Fatal("existing owner changed", spec, err)
	}
	api.info.Lease = strings.Repeat("c", 64)
	if _, err = l.materializeWorkspace(t.Context(), spec); err == nil {
		t.Fatal("lease race accepted")
	}
	api.info.Lease = ""
	api.info.Kind = "snapshot"
	p := managedworkspace.RecoveryPoint{ID: strings.Repeat("d", 64), State: "ready", Workspace: api.info.ID}
	p.Checkpoint.ID = strings.Repeat("e", 64)
	spec, err = l.Prepare(t.Context(), LaunchSpec{Name: "new", Project: t.TempDir(), Network: "offline", Workspace: "existing", Recovery: &p, Checkpoint: p.Checkpoint.ID})
	if err != nil || api.clones != 0 {
		t.Fatal("review copied snapshot", err)
	}
	info, err := l.materializeWorkspace(t.Context(), spec)
	if err != nil || api.clones != 1 || info.ID == api.info.ID {
		t.Fatal("restore reused saved workspace", info, err)
	}
	env := EnvironmentFrom(spec)
	next := env.Apply(spec)
	if next.WorkspaceID != "" || next.Checkpoint != "" || next.Recovery != nil || next.CopyPlan != nil || next.AI {
		t.Fatal("saved environment retained restoration authority", next)
	}
}
