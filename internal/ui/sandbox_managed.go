package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/diskcheckpoint"
	"github.com/pacificbelt30/codex-dock/internal/managedworkspace"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/rivo/tview"
)

func managedSummary(info managedworkspace.Info) string {
	return fmt.Sprintf("%s\nID: %s\n%s / %s | %d MiB fixed / %d inodes\nOwner: %d:%d | VM lease: %s | read-only: %t\nImported %d files / %d bytes\nReservation remains after VM deletion. Physical capacity is enforced by the guest disk size; rootfs quotas are separate.\n", info.Options.Name, info.ID, info.Kind, info.State, info.Options.Bytes>>20, info.Options.Inodes, info.Options.UID, info.Options.GID, info.Lease, info.ReadOnly, info.SeedFiles, info.SeedBytes)
}

func (d *sandboxDesktop) managedWorkspaces() {
	if d.backend.Workspaces == nil {
		d.message("Managed workspace operations are unavailable on this connection.")
		return
	}
	d.read("Listing managed workspaces", func(ctx context.Context) (func(), error) {
		catalog, err := d.backend.Workspaces.List(ctx)
		return func() {
			list := tview.NewList().ShowSecondaryText(true)
			list.SetBorder(true).SetTitle(fmt.Sprintf(" Managed workspaces · %.0f / %.0f MiB reserved · Esc back ", float64(catalog.ReservedBytes)/(1<<20), float64(catalog.CapacityBytes)/(1<<20)))
			for _, info := range catalog.Items {
				list.AddItem(tview.Escape(fmt.Sprintf("%s · %s / %s · %d MiB", info.Options.Name, info.Kind, info.State, info.Options.Bytes>>20)), tview.Escape(info.ID+" · lease "+info.Lease), 0, func() { d.managedWorkspace(info) })
			}
			list.AddItem("Refresh", "Reload node reservations", 'g', d.managedWorkspaces)
			list.AddItem("Back", "Return to instances", 0, d.back)
			list.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
				if e.Key() == tcell.KeyEscape {
					d.back()
					return nil
				}
				return e
			})
			d.pages.AddAndSwitchToPage("workspaces", list, true)
			d.app.SetFocus(list)
		}, err
	})
}
func (d *sandboxDesktop) managedWorkspace(info managedworkspace.Info) {
	view := tview.NewTextView().SetDynamicColors(false).SetScrollable(true).SetText(managedSummary(info))
	view.SetBorder(true).SetTitle(" Managed workspace ")
	form := tview.NewForm()
	if info.Kind == "workspace" && info.State == "ready" && info.Lease == "" {
		form.AddButton("Use in new VM…", func() { d.createFromWorkspace(info) })
	}
	if info.Lease == "" {
		form.AddButton("Delete permanently…", func() {
			d.reviewPage("workspace-delete", " Delete workspace ", managedSummary(info)+"\nPermanently destroy this disk and all its files? Recovery-point references are checked by the node. This cannot be undone.", func() {
				d.work(func(ctx context.Context) (func(), error) {
					err := d.backend.Workspaces.Remove(ctx, info.ID)
					return d.managedWorkspaces, err
				})
			}, func() { d.managedWorkspace(info) })
		})
	}
	form.AddButton("Back", d.managedWorkspaces)
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(form, 3, 0, false)
	body.SetInputCapture(d.pageFormNavigation(view, form, d.managedWorkspaces))
	d.pages.AddAndSwitchToPage("workspace", body, true)
	d.app.SetFocus(view)
}
func (d *sandboxDesktop) createFromWorkspace(info managedworkspace.Info) {
	pwd, _ := os.Getwd()
	d.createPrepared(sandboxux.LaunchSpec{Project: pwd, Network: "offline", Workspace: "existing", WorkspaceID: info.ID})
}
func (d *sandboxDesktop) createPrepared(spec sandboxux.LaunchSpec) {
	if d.backend.Launcher == nil {
		d.message("Sandbox creation is unavailable for this dashboard connection.")
		return
	}
	d.read("Loading profiles and storage", func(ctx context.Context) (func(), error) {
		profiles, err := d.backend.Profiles(ctx)
		if err != nil {
			return nil, err
		}
		storage, storageErr := d.backend.Storage(ctx)
		return func() { d.createFormSpec(profiles, storage, storageErr, spec) }, nil
	})
}
func recoverySummary(p managedworkspace.RecoveryPoint) string {
	return fmt.Sprintf("%s\nID: %s | %s | %s\nSource VM: %s\nRootfs checkpoint: %s\nRootfs digest: %s\nWorkspace snapshot: %s\nExcluded rootfs paths: %s\nHomes, RAM and host bind mounts are not saved. A workspace snapshot may contain secrets written inside the VM.\n%s\n", p.Name, p.ID, p.State, p.Created.Format(time.RFC3339), p.Source, p.Checkpoint.ID, p.Checkpoint.Digest, p.Workspace, strings.Join(p.Checkpoint.Excluded, ", "), p.Error)
}
func (d *sandboxDesktop) recoveryPoints() {
	if d.backend.Workspaces == nil {
		d.message("Recovery-point operations are unavailable.")
		return
	}
	d.read("Listing recovery points", func(ctx context.Context) (func(), error) {
		points, err := d.backend.Workspaces.Points(ctx)
		return func() {
			list := tview.NewList().ShowSecondaryText(true)
			list.SetBorder(true).SetTitle(" Recovery points · Enter inspect / restore · Esc back ")
			for _, p := range points {
				list.AddItem(tview.Escape(p.Name+" · "+p.State), tview.Escape(p.ID+" · "+p.Created.Format(time.RFC3339)), 0, func() { d.recoveryPoint(p) })
			}
			list.AddItem("Refresh", "Reload saved points", 'g', d.recoveryPoints).AddItem("Back", "Return to instances", 0, d.back)
			list.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
				if e.Key() == tcell.KeyEscape {
					d.back()
					return nil
				}
				return e
			})
			d.pages.AddAndSwitchToPage("recovery-points", list, true)
			d.app.SetFocus(list)
		}, err
	})
}
func (d *sandboxDesktop) recoveryPoint(p managedworkspace.RecoveryPoint) {
	view := tview.NewTextView().SetDynamicColors(false).SetScrollable(true).SetText(recoverySummary(p))
	view.SetBorder(true).SetTitle(" Recovery point ")
	form := tview.NewForm()
	if p.State == "ready" {
		form.AddButton("Restore as new VM…", func() {
			pwd, _ := os.Getwd()
			spec := sandboxux.LaunchSpec{Project: pwd, Network: "offline", Checkpoint: p.Checkpoint.ID, Profile: p.Profile}
			if p.Workspace != "" {
				spec.Workspace = "existing"
				spec.Recovery = &p
			}
			d.createPrepared(spec)
		})
	}
	if p.State != "preparing" {
		form.AddButton("Delete point…", func() {
			d.reviewPage("point-delete", " Delete recovery point ", recoverySummary(p)+"\nPermanently delete this point and its saved rootfs/workspace components? Restored workspaces are separate. Retained VM references prevent deletion.", func() {
				d.work(func(ctx context.Context) (func(), error) {
					err := d.backend.Workspaces.RemovePoint(ctx, p.ID)
					return d.recoveryPoints, err
				})
			}, func() { d.recoveryPoint(p) })
		})
	}
	form.AddButton("Back", d.recoveryPoints)
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(form, 3, 0, false)
	body.SetInputCapture(d.pageFormNavigation(view, form, d.recoveryPoints))
	d.pages.AddAndSwitchToPage("recovery-point", body, true)
	d.app.SetFocus(view)
}
func (d *sandboxDesktop) saveRecoveryPoint(worker sandbox.Worker) {
	if d.backend.Workspaces == nil || d.backend.ManagedInfo == nil {
		d.message("Recovery-point operations are unavailable.")
		return
	}
	if worker.Status == "running" {
		d.message("Stop this VM with S before saving a recovery point.")
		return
	}
	d.read("Reading workspace details", func(ctx context.Context) (func(), error) {
		info, err := d.backend.ManagedInfo(ctx, worker.ID)
		return func() { d.saveRecoveryForm(worker, info) }, err
	})
}
func (d *sandboxDesktop) saveRecoveryForm(worker sandbox.Worker, info *managedworkspace.Info) {
	name, exclude := worker.Name+"-save", ""
	if len([]rune(name)) > 64 {
		name = "recovery"
	}
	include := info != nil
	form := tview.NewForm().AddInputField("Recovery-point name", name, 48, nil, func(v string) { name = v })
	if info != nil {
		form.AddCheckbox("Include managed workspace", true, func(v bool) { include = v })
	}
	form.AddTextArea("Extra rootfs exclusions", exclude, 56, 3, 8192, func(v string) { exclude = v })
	form.AddButton("Review", func() {
		opts := managedworkspace.SaveOptions{Worker: worker.ID, Name: name, IncludeWorkspace: include}
		for _, p := range strings.Split(exclude, "\n") {
			if p = strings.TrimSpace(p); p != "" {
				opts.Checkpoint.Exclude = append(opts.Checkpoint.Exclude, p)
			}
		}
		normalized, err := diskcheckpoint.Normalize(opts.Checkpoint, nil)
		if err != nil {
			d.message(err.Error())
			return
		}
		opts.Checkpoint = normalized
		text := fmt.Sprintf("Save stopped VM %s (%s)?\nName: %s\nRootfs exclusions (node also excludes all bind mounts): %s\nNo RAM or homes. Workspace included: %t\n", worker.Name, worker.ID, name, strings.Join(normalized.Exclude, ", "), include)
		if include && info != nil {
			text += "\n" + managedSummary(*info) + "The entire workspace disk is captured, including any secrets added in the VM.\n"
		} else {
			text += "Host-shared files are not saved or rolled back.\n"
		}
		d.reviewPage("point-save-review", " Save recovery point ", text+"\nThis reserves storage and preserves the source. Restoration always creates new resources.", func() {
			d.work(func(ctx context.Context) (func(), error) {
				point, err := d.backend.Workspaces.SavePoint(ctx, opts)
				return func() { d.changed(); d.recoveryPoint(point) }, err
			})
		}, func() { d.pages.SwitchToPage("point-save"); d.app.SetFocus(form) })
	}).AddButton("Cancel", d.back)
	form.SetBorder(true).SetTitle(" Save stopped VM · Esc cancels ")
	d.cancelForm(form, d.back)
	d.pages.AddAndSwitchToPage("point-save", form, true)
	d.app.SetFocus(form)
}
