package sandboxux

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/managedworkspace"
	"github.com/pacificbelt30/codex-dock/internal/workspacecopy"
)

type WorkspaceService interface {
	List(context.Context) (managedworkspace.Catalog, error)
	Inspect(context.Context, string) (managedworkspace.Info, error)
	Create(context.Context, managedworkspace.Options) (managedworkspace.Info, error)
	Seed(context.Context, string, io.Reader) (managedworkspace.Info, error)
	Remove(context.Context, string) error
	Clone(context.Context, string, string) (managedworkspace.Info, error)
	Points(context.Context) ([]managedworkspace.RecoveryPoint, error)
	SavePoint(context.Context, managedworkspace.SaveOptions) (managedworkspace.RecoveryPoint, error)
	RemovePoint(context.Context, string) error
}

func (l *CLILauncher) WorkspaceService() WorkspaceService {
	if l.Workspaces != nil {
		return l.Workspaces
	}
	return engine.WorkspaceClient{Socket: l.Config.Socket}
}

// Prepare performs only reads. The returned plan is the exact content contract
// presented for review; Launch must never silently replace it with a new scan.
func (l *CLILauncher) Prepare(ctx context.Context, spec LaunchSpec) (LaunchSpec, error) {
	spec, _, err := PrepareLaunch(spec)
	if err != nil {
		return spec, err
	}
	if spec.Workspace != "managed" && spec.Workspace != "existing" {
		return spec, nil
	}
	api := l.WorkspaceService()
	if spec.Workspace == "existing" {
		id := spec.WorkspaceID
		if spec.Recovery != nil {
			if spec.Recovery.State != "ready" || spec.Checkpoint != spec.Recovery.Checkpoint.ID || spec.Recovery.Workspace == "" {
				return spec, fmt.Errorf("recovery point is incomplete")
			}
			id = spec.Recovery.Workspace
		}
		info, err := api.Inspect(ctx, id)
		if err != nil {
			return spec, err
		}
		kind := "workspace"
		if spec.Recovery != nil {
			kind = "snapshot"
		}
		if info.State != "ready" || info.Lease != "" || info.Kind != kind {
			return spec, fmt.Errorf("workspace is unavailable or still leased to a VM")
		}
		spec.WorkspaceOwner = info.Options
		spec.WorkspaceBytes, spec.WorkspaceInodes = info.Options.Bytes, info.Options.Inodes
		return spec, nil
	}
	if spec.WorkspaceBytes == 0 {
		spec.WorkspaceBytes = managedworkspace.DefaultBytes
	}
	if spec.WorkspaceInodes == 0 {
		spec.WorkspaceInodes = 16384
	}
	owner, err := l.workspaceOwner(spec)
	if err != nil {
		return spec, err
	}
	if err = owner.Validate(); err != nil {
		return spec, err
	}
	catalog, err := api.List(ctx)
	if err != nil {
		return spec, err
	}
	if catalog.ReservedBytes+owner.Bytes > catalog.CapacityBytes {
		return spec, fmt.Errorf("managed workspace catalog has insufficient capacity")
	}
	plan, err := workspacecopy.Prepare(ctx, spec.Project, owner.SeedLimit())
	if err != nil {
		return spec, err
	}
	spec.WorkspaceOwner, spec.CopyPlan = owner, &plan
	return spec, nil
}

func (l *CLILauncher) workspaceOwner(spec LaunchSpec) (managedworkspace.Options, error) {
	o := managedworkspace.Options{Name: spec.Name, Bytes: spec.WorkspaceBytes, Inodes: spec.WorkspaceInodes}
	mode := l.UserMode
	if mode == "" || mode == "current" {
		o.UID, o.GID = uint32(os.Getuid()), uint32(os.Getgid())
		return o, nil
	}
	if mode == "codex" {
		o.UID, o.GID = 1001, 1001
		return o, nil
	}
	if mode == "dir" {
		st, err := os.Stat(spec.Project)
		if err != nil {
			return o, err
		}
		s, ok := st.Sys().(*syscall.Stat_t)
		if !ok {
			return o, fmt.Errorf("cannot resolve workspace owner")
		}
		o.UID, o.GID = s.Uid, s.Gid
		return o, nil
	}
	parts := strings.Split(mode, ":")
	if len(parts) != 2 {
		return o, fmt.Errorf("managed workspace requires --user current, codex, dir or numeric UID:GID")
	}
	uid, e1 := strconv.ParseUint(parts[0], 10, 32)
	gid, e2 := strconv.ParseUint(parts[1], 10, 32)
	if e1 != nil || e2 != nil {
		return o, fmt.Errorf("managed workspace requires a numeric UID:GID")
	}
	o.UID, o.GID = uint32(uid), uint32(gid)
	return o, nil
}

func (l *CLILauncher) materializeWorkspace(ctx context.Context, spec LaunchSpec) (managedworkspace.Info, error) {
	api := l.WorkspaceService()
	if spec.Workspace == "existing" {
		if spec.Recovery != nil {
			return api.Clone(ctx, spec.Recovery.Workspace, spec.Name)
		}
		info, err := api.Inspect(ctx, spec.WorkspaceID)
		if err == nil && (info.Kind != "workspace" || info.State != "ready" || info.Lease != "" || info.Options != spec.WorkspaceOwner) {
			err = fmt.Errorf("workspace changed after review")
		}
		return info, err
	}
	if spec.CopyPlan == nil || spec.CopyPlan.Source != spec.Project || spec.WorkspaceOwner.Name != spec.Name {
		return managedworkspace.Info{}, fmt.Errorf("review the current-files copy before launching")
	}
	if err := spec.CopyPlan.Manifest.Validate(spec.WorkspaceOwner.SeedLimit()); err != nil {
		return managedworkspace.Info{}, err
	}
	return ImportWorkspace(ctx, api, spec.WorkspaceOwner, *spec.CopyPlan)
}

func ImportWorkspace(ctx context.Context, api WorkspaceService, options managedworkspace.Options, plan workspacecopy.Plan) (managedworkspace.Info, error) {
	if err := options.Validate(); err != nil {
		return managedworkspace.Info{}, err
	}
	if err := plan.Manifest.Validate(options.SeedLimit()); err != nil {
		return managedworkspace.Info{}, err
	}
	info, err := api.Create(ctx, options)
	if err != nil {
		return info, err
	}
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := plan.Write(ctx, writer)
		_ = writer.CloseWithError(err)
		done <- err
	}()
	seeded, seedErr := api.Seed(ctx, info.ID, reader)
	_ = reader.CloseWithError(seedErr)
	writeErr := <-done
	if seedErr != nil || writeErr != nil {
		return info, errors.Join(seedErr, writeErr)
	}
	return seeded, nil
}
