package ui

import (
	"context"
	"fmt"
	"sort"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/rivo/tview"
)

func (d *sandboxDesktop) browseChanges(worker sandbox.Worker, launch bool) {
	d.read("Listing changed files", func(ctx context.Context) (func(), error) {
		comparison, err := d.backend.Compare(ctx, worker.ID, launch)
		return func() { d.changeFiles(worker, comparison, launch) }, err
	})
}

func (d *sandboxDesktop) changeFiles(worker sandbox.Worker, comparison sandboxux.Comparison, launch bool) {
	selected := map[string]bool{}
	list := tview.NewList().ShowSecondaryText(true)
	list.SetBorder(true).SetTitle(" Changed files · " + tview.Escape(comparison.Base) + " ")
	render := func() {
		current := list.GetCurrentItem()
		list.Clear()
		for _, f := range comparison.Files {
			mark := "[ ] "
			if selected[f.Path] {
				mark = "[x] "
			}
			list.AddItem(tview.Escape(mark+f.Status+" "+f.Path), "Enter: review this file · Space: select for export", 0, func() {
				d.read("Reviewing "+f.Path, func(ctx context.Context) (func(), error) {
					var text string
					var err error
					if f.Status == "?" {
						var file sandboxux.Text
						file, err = d.backend.Files.Text(ctx, worker.ID, f.Path)
						if file.Binary {
							text = "Binary file; use F to download or export after review."
						} else {
							text = file.Content
							if file.Truncated {
								text += "\n[Display truncated]"
							}
						}
					} else {
						text, err = d.backend.FileDiff(ctx, worker.ID, comparison.Base, f.Path)
					}
					return func() {
						view := tview.NewTextView().SetDynamicColors(false).SetScrollable(true).SetText(text)
						view.SetBorder(true).SetTitle(" File change · " + tview.Escape(f.Path) + " ")
						back := func() { d.pages.RemovePage("file-diff"); d.pages.SwitchToPage("change-files"); d.app.SetFocus(list) }
						form := tview.NewForm().AddButton("Back", back)
						body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(form, 3, 0, false)
						body.SetInputCapture(d.pageFormNavigation(view, form, back))
						d.pages.AddAndSwitchToPage("file-diff", body, true)
						d.app.SetFocus(view)
					}, err
				})
			})
		}
		if len(comparison.Files) == 0 {
			list.AddItem("No comparable files changed", "Credential paths and links cannot be exported.", 0, nil)
		} else {
			list.SetCurrentItem(min(current, len(comparison.Files)-1))
		}
	}
	list.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Rune() == ' ' && len(comparison.Files) > 0 {
			f := comparison.Files[list.GetCurrentItem()]
			if f.Status == "D" || f.Status == "T" {
				d.message("Deleted or type-changed files can be reviewed; export requires current regular files.")
				return nil
			}
			if selected[f.Path] {
				delete(selected, f.Path)
			} else if len(selected) < 256 {
				selected[f.Path] = true
			} else {
				d.message("Choose at most 256 files.")
			}
			render()
			return nil
		}
		return e
	})
	form := tview.NewForm().AddButton("Export selected…", func() {
		if len(selected) == 0 {
			d.message("Select files with Space before exporting.")
			return
		}
		var names []string
		for n := range selected {
			names = append(names, n)
		}
		sort.Strings(names)
		d.exportArtifactPaths(worker, names, launch)
	}).AddButton("Refresh", func() {
		if len(selected) > 0 {
			d.confirmation("Refresh and discard this draft file selection?", func() { d.browseChanges(worker, launch) }, func() { d.pages.SwitchToPage("change-files"); d.app.SetFocus(list) })
		} else {
			d.browseChanges(worker, launch)
		}
	}).AddButton("Back", func() { d.changeSummary(worker, launch) })
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(list, 0, 1, true).AddItem(tview.NewTextView().SetText(fmt.Sprintf("%d changed paths · comparison base %s", len(comparison.Files), comparison.Base)), 1, 0, false).AddItem(form, 3, 0, false).AddItem(tview.NewTextView().SetText("Space selects regular files · Enter reviews · Tab switches panes · Esc back"), 1, 0, false)
	body.SetInputCapture(d.pageFormNavigation(list, form, func() { d.changeSummary(worker, launch) }))
	render()
	d.pages.AddAndSwitchToPage("change-files", body, true)
	d.app.SetFocus(list)
}
