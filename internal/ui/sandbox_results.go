package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/rivo/tview"
)

func (d *sandboxDesktop) changes(worker sandbox.Worker) {
	if d.backend.Changes == nil || d.backend.Artifacts == nil {
		d.message("Workspace results are unavailable.")
		return
	}
	view := tview.NewTextView().SetDynamicColors(false).SetScrollable(true).SetText("Loading guest Git changes…")
	view.SetBorder(true).SetTitle(" Workspace changes · " + tview.Escape(worker.Name) + " ")
	form := tview.NewForm().AddButton("Refresh", func() { d.changes(worker) }).AddButton("Export selected files…", func() { d.exportArtifacts(worker) }).AddButton("Back", d.back)
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(form, 3, 0, false).AddItem(tview.NewTextView().SetText("Tab switches panes · ↑↓ / PgUp / PgDn scroll · Esc back\nDiff is against current guest HEAD. Untracked contents require explicit export."), 2, 0, false)
	body.SetInputCapture(d.pageFormNavigation(view, form, d.back))
	d.pages.AddAndSwitchToPage("changes", body, true)
	d.app.SetFocus(view)
	d.work(func(ctx context.Context) (func(), error) {
		text, err := d.backend.Changes(ctx, worker.ID)
		// Export still works for non-Git workspaces only when the artifact backend
		// supports their plan; a failed diff is not presented as a clean tree.
		return func() { view.SetText(text) }, err
	})
}

func (d *sandboxDesktop) exportArtifacts(worker sandbox.Worker) {
	paths, destination := "", ""
	back := func() { d.changes(worker) }
	form := tview.NewForm().AddTextArea("Workspace files (one/line)", "", 56, 5, 24576, func(v string) { paths = v }).AddInputField("New host .tar file", "", 56, nil, func(v string) { destination = v })
	form.AddTextView("Scope", "Choose 1–256 regular files. The archive includes selected contents, their Git diff against current HEAD and a manifest. Secrets paths and symlinks are rejected. Maximum 32 MiB; host destination must be new. No changes are applied to the original project.", 56, 5, false, false)
	form.AddButton("Review export", func() {
		options := sandbox.ArtifactOptions{}
		for _, p := range strings.Split(paths, "\n") {
			if p = strings.TrimSpace(p); p != "" {
				options.Paths = append(options.Paths, p)
			}
		}
		d.work(func(ctx context.Context) (func(), error) {
			plan, err := d.backend.Artifacts.PlanArtifacts(ctx, worker.ID, options)
			if err != nil {
				return nil, err
			}
			var text strings.Builder
			fmt.Fprintf(&text, "Save to new private file: %s\nWorker: %s\nGuest HEAD: %s\nSelected file + diff bytes: %d\nPlan: %s\n\n", destination, plan.Worker, plan.Head, plan.TotalBytes, plan.ID)
			for _, f := range plan.Files {
				fmt.Fprintf(&text, "%s · %d bytes\n", f.Path, f.Bytes)
			}
			return func() {
				d.reviewPage("export-review", " Review selected artifacts ", text.String()+"\nA change to selected contents, diff or HEAD invalidates this plan. VM deletion is a separate action.", func() {
					d.work(func(ctx context.Context) (func(), error) {
						err := sandboxux.ExportReviewedArtifacts(ctx, d.backend.Artifacts, worker.ID, options, plan, destination)
						return func() {
							d.pages.SwitchToPage("export")
							d.app.SetFocus(form)
							d.message("Saved reviewed artifacts to " + destination + ". VM and independent copy remain unchanged.")
						}, err
					})
				}, func() { d.pages.SwitchToPage("export"); d.app.SetFocus(form) })
			}, nil
		})
	}).AddButton("Cancel", back)
	form.SetBorder(true).SetTitle(" Export selected workspace files · Esc cancels ")
	d.cancelForm(form, back)
	d.pages.AddAndSwitchToPage("export", form, true)
	d.app.SetFocus(form)
}
