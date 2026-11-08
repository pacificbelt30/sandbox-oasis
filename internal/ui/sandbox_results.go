package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/rivo/tview"
)

func (d *sandboxDesktop) changes(worker sandbox.Worker) { d.changeSummary(worker, false) }
func (d *sandboxDesktop) changeSummary(worker sandbox.Worker, launch bool) {
	if d.backend.Changes == nil || d.backend.Artifacts == nil {
		d.message("Workspace results are unavailable.")
		return
	}
	view := tview.NewTextView().SetDynamicColors(false).SetScrollable(true).SetText("No workspace changes loaded. Use Refresh to read them.")
	view.SetBorder(true).SetTitle(" Workspace changes · " + tview.Escape(worker.Name) + " ")
	form := tview.NewForm().AddButton("Refresh", func() { d.changeSummary(worker, launch) }).AddButton("Export selected files…", func() { d.exportArtifactPaths(worker, nil, launch) }).AddButton("Back", d.back)

	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(form, 3, 0, false).AddItem(tview.NewTextView().SetText("Tab switches panes · ↑↓ / PgUp / PgDn scroll · Esc back\nShared/Git copy: current HEAD. Managed: initial import hashes; no Git patch. Select files explicitly to export."), 2, 0, false)
	body.SetInputCapture(d.pageFormNavigation(view, form, d.back))
	d.pages.AddAndSwitchToPage("changes", body, true)
	d.app.SetFocus(view)
	d.read("Reading workspace changes", func(ctx context.Context) (func(), error) {
		gitWorkspace := true
		if d.backend.ManagedInfo != nil {
			info, e := d.backend.ManagedInfo(ctx, worker.ID)
			gitWorkspace = e == nil && info == nil
		}
		text, err := d.backend.Changes(ctx, worker.ID)
		if launch {
			var comparison sandboxux.Comparison
			comparison, err = d.backend.Compare(ctx, worker.ID, true)
			text = "Comparison against launch commit " + comparison.Base + "\nIncludes committed and uncommitted changes; untracked paths listed.\n"
			for _, f := range comparison.Files {
				text += f.Status + " " + f.Path + "\n"
			}
		}
		// Export still works for non-Git workspaces only when the artifact backend
		// supports their plan; a failed diff is not presented as a clean tree.
		return func() {
			view.SetText(text)
			if gitWorkspace {
				if d.backend.Compare != nil {
					label := "Since launch"
					if launch {
						label = "Current HEAD"
					}
					form.AddButton(label, func() { d.changeSummary(worker, !launch) }).AddButton("Browse changes…", func() { d.browseChanges(worker, launch) })
				}
			}
		}, err
	})
}

func (d *sandboxDesktop) exportArtifacts(worker sandbox.Worker) {
	d.exportArtifactPaths(worker, nil, false)
}
func (d *sandboxDesktop) exportArtifactPaths(worker sandbox.Worker, initial []string, launch bool) {
	paths, destination := strings.Join(initial, "\n"), ""
	pickerPaths := initial
	pickerText := paths
	back := func() { d.changeSummary(worker, launch) }
	form := tview.NewForm().AddTextArea("Workspace files (one/line)", paths, 56, 5, 24576, func(v string) { paths = v }).AddInputField("New host .tar file", "", 56, nil, func(v string) { destination = v })
	comparisonScope := "current HEAD"
	if launch {
		comparisonScope = "the captured launch commit"
	}
	form.AddTextView("Scope", "Comparison: "+comparisonScope+". Choose 1–256 regular files. The archive includes selected contents, a manifest and, for Git workspaces, their diff against "+comparisonScope+". Managed exports contain an empty changes.patch. Secrets paths and symlinks are rejected. Maximum 32 MiB; host destination must be new. No changes are applied to the original project.", 56, 5, false, false)
	form.AddButton("Review export", func() {
		options := sandbox.ArtifactOptions{SinceLaunch: launch}
		if pickerPaths != nil && paths == pickerText {
			options.Paths = append([]string(nil), pickerPaths...)
		} else {
			for _, p := range strings.Split(paths, "\n") {
				if p = strings.TrimSpace(p); p != "" {
					options.Paths = append(options.Paths, p)
				}
			}
		}
		d.read("Reviewing selected artifacts", func(ctx context.Context) (func(), error) {
			plan, err := d.backend.Artifacts.PlanArtifacts(ctx, worker.ID, options)
			if err != nil {
				return nil, err
			}
			var text strings.Builder
			if plan.Base != "" {
				fmt.Fprintf(&text, "Comparison base: %s\n", plan.Base)
			}
			fmt.Fprintf(&text, "Save to new private file: %s\nWorker: %s\nGuest HEAD / initial import: %s\nSelected file + diff bytes: %d\nPlan: %s\n\n", destination, plan.Worker, plan.Head, plan.TotalBytes, plan.ID)
			for _, f := range plan.Files {
				fmt.Fprintf(&text, "%s · %d bytes · SHA-256 %s\n", f.Path, f.Bytes, f.SHA256)
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
	}).AddButton("Choose files…", func() {
		accept := func(selected []string) {
			pickerPaths = make([]string, len(selected))
			copy(pickerPaths, selected)
			pickerText = strings.Join(selected, "\n")
			if field, ok := form.GetFormItem(0).(*tview.TextArea); ok {
				field.SetText(strings.Join(selected, "\n"), true)
			}
			d.pages.SwitchToPage("export")
			form.SetFocus(1)
			d.app.SetFocus(form)
		}
		back := func() { d.pages.SwitchToPage("export"); d.app.SetFocus(form) }
		if pickerPaths != nil && paths == pickerText {
			d.chooseArtifactPaths(worker, pickerPaths, accept, back)
		} else {
			d.chooseArtifacts(worker, paths, accept, back)
		}
	}).AddButton("Cancel", back)
	form.SetBorder(true).SetTitle(" Export selected workspace files · Esc cancels ")
	d.cancelForm(form, back)
	d.pages.AddAndSwitchToPage("export", form, true)
	d.app.SetFocus(form)
}
