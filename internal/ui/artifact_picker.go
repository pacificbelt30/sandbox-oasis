package ui

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/rivo/tview"
)

func (d *sandboxDesktop) chooseArtifacts(worker sandbox.Worker, current string, accept func([]string), back func()) {
	var initial []string
	for _, name := range strings.Split(current, "\n") {
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		initial = append(initial, name)
	}
	d.chooseArtifactPaths(worker, initial, accept, back)
}

func (d *sandboxDesktop) chooseArtifactPaths(worker sandbox.Worker, initial []string, accept func([]string), back func()) {
	selected := map[string]bool{}
	for _, name := range initial {
		if err := sandboxux.ArtifactPath(name); err != nil {
			d.message(fmt.Sprintf("%s: %v", name, err))
			return
		}
		selected[name] = true
	}
	if len(selected) > 256 {
		d.message("Choose at most 256 files.")
		return
	}
	dir := "."
	var entries []sandboxux.FileEntry
	table := tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	table.SetBorder(true).SetTitle(" Choose artifact files · " + tview.Escape(worker.Name) + " ")
	status := tview.NewTextView().SetDynamicColors(false)
	render := func() {
		table.Clear()
		for c, text := range []string{"Select", "Name", "Type", "Bytes"} {
			table.SetCell(0, c, tview.NewTableCell(text).SetSelectable(false))
		}
		for i, entry := range entries {
			mark := "[ ]"
			if entry.Kind == "directory" {
				mark = "→"
			} else if selected[path.Join(dir, entry.Name)] {
				mark = "[x]"
			}
			for c, text := range []string{mark, entry.Name, entry.Kind, fmt.Sprint(entry.Size)} {
				table.SetCell(i+1, c, tview.NewTableCell(tview.Escape(text)))
			}
		}
		status.SetText(fmt.Sprintf("/workspace/%s · %d / 256 selected\nOnly regular files. Links and credential paths are excluded. Review verifies sizes and hashes.", dir, len(selected)))
	}
	load := func(nextDir, preferred string) {
		d.read("Listing artifact files", func(ctx context.Context) (func(), error) {
			listed, err := d.backend.Files.List(ctx, worker.ID, nextDir)
			if err != nil {
				return nil, err
			}
			return func() {
				dir = nextDir
				entries = nil
				for _, entry := range listed {
					if (entry.Kind == "file" || entry.Kind == "directory") && sandboxux.ArtifactPath(path.Join(dir, entry.Name)) == nil {
						entries = append(entries, entry)
					}
				}
				render()
				if len(entries) > 0 {
					row := 1
					for i, entry := range entries {
						if entry.Name == preferred {
							row = i + 1
						}
					}
					table.Select(row, 0)
				}
			}, nil
		})
	}
	buttons := tview.NewForm().AddButton("Use selected files", func() {
		paths := make([]string, 0, len(selected))
		for name := range selected {
			paths = append(paths, name)
		}
		slices.Sort(paths)
		accept(paths)
	}).AddButton("Cancel", back)
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(table, 0, 1, true).AddItem(status, 2, 0, false).AddItem(buttons, 3, 0, false).AddItem(tview.NewTextView().SetText("Enter opens folders / toggles files · Space selects · Backspace parent\nG refresh · Tab switches to buttons · Esc discards selection changes"), 2, 0, false)
	body.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			back()
			return nil
		}
		if event.Key() == tcell.KeyTab || event.Key() == tcell.KeyBacktab {
			if table.HasFocus() {
				buttons.SetFocus(0)
				d.app.SetFocus(buttons)
				return nil
			}
			_, button := buttons.GetFocusedItemIndex()
			if (event.Key() == tcell.KeyTab && button == 1) || (event.Key() == tcell.KeyBacktab && button == 0) {
				d.app.SetFocus(table)
				return nil
			}
		}
		return event
	})
	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		row, _ := table.GetSelection()
		if event.Key() == tcell.KeyBackspace || event.Key() == tcell.KeyBackspace2 {
			if dir != "." {
				load(path.Dir(dir), path.Base(dir))
			}
			return nil
		}
		preferred := ""
		if row > 0 && row <= len(entries) {
			preferred = entries[row-1].Name
		}
		if event.Rune() == 'g' || event.Rune() == 'G' {
			load(dir, preferred)
			return nil
		}
		if preferred == "" || (event.Key() != tcell.KeyEnter && event.Rune() != ' ') {
			return event
		}
		entry := entries[row-1]
		name := path.Join(dir, entry.Name)
		if entry.Kind == "directory" {
			if event.Key() == tcell.KeyEnter {
				load(name, "")
			}
		} else {
			if selected[name] {
				delete(selected, name)
			} else if len(selected) < 256 {
				selected[name] = true
			} else {
				d.message("Choose at most 256 files.")
			}
			render()
		}
		return nil
	})
	d.pages.AddAndSwitchToPage("artifact-picker", body, true)
	d.app.SetFocus(table)
	load(dir, "")
}
