package ui

import (
	"fmt"
	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/rivo/tview"
	"time"
	"unicode"
)

type dashboardAction struct {
	title, hint string
	key         *tcell.EventKey
	worker      bool
}

func dashboardActions(worker *sandbox.Worker, tasks bool) []dashboardAction {
	runeAction := func(title, hint string, r rune, selected bool) dashboardAction {
		return dashboardAction{title, hint, tcell.NewEventKey(tcell.KeyRune, r, 0), selected}
	}
	items := []dashboardAction{runeAction("New sandbox", "N · create an isolated workspace", 'n', false)}
	if worker != nil {
		switch worker.Status {
		case "running":
			items = append(items, runeAction("Connect to shell", "C · exit the shell to return here", 'c', true), runeAction("Browse workspace files", "F · view, upload or download files", 'f', true), runeAction("Workspace changes and export", "W · guest Git diff and selected artifacts", 'w', true), runeAction("Web previews", "U · authenticated localhost URLs", 'u', true), runeAction("Run a command", "X · execute in the selected sandbox", 'x', true))
		case "exited", "stopped", "created":
			items = append(items, runeAction("Start sandbox", "R · start the selected sandbox", 'r', true))
			items = append(items, runeAction("Save recovery point…", "K · filtered rootfs and optional managed workspace", 'k', true))
		}
		items = append(items, runeAction("Instance details", "I · full name, image, policy and storage", 'i', true), dashboardAction{"View logs", "Enter · recent output", tcell.NewEventKey(tcell.KeyEnter, 0, 0), true})
		items = append(items, runeAction("Background jobs", "J · start, inspect output or stop a job", 'J', true))
		items = append(items, runeAction("Lifetime and storage", "L · deadlines, host quota, scoped supervision", 'l', true))
		if worker.Status == "running" {
			items = append(items, runeAction("Stop sandbox", "S · preserve it for a later restart", 's', true))
		}
		items = append(items, runeAction("Delete sandbox…", "D · asks for confirmation", 'd', true))
	}
	items = append(items, runeAction("Managed workspaces", "M · reservations, reuse and deletion", 'm', false), runeAction("Recovery points", "Z · inspect and restore as new VM", 'z', false))
	items = append(items, runeAction("Filter instances", "/ · name, status, branch or other fields", '/', false), runeAction("Change sort order", "O · cycle ordering", 'o', false), runeAction("Recent activity", "V · lifecycle changes", 'v', false))
	if tasks {
		items = append(items, runeAction("Tasks and result review", "T · open the task board", 't', false))
	}
	return append(items, runeAction("Agent policies", "G · host-managed defaults", 'g', false), runeAction("Proxy settings", "P · general egress policy", 'p', false), runeAction("Credential broker settings", "B · configured credential routes", 'b', false), runeAction("Help", "? · all keyboard shortcuts", '?', false), runeAction("Quit TUI", "Q · sandboxes remain; local previews close", 'q', false))
}

func dashboardActionMenu(worker *sandbox.Worker, tasks bool, currentID func() string, activate func(*tcell.EventKey), stale, cancel func()) *tview.List {
	list := tview.NewList().ShowSecondaryText(true)
	title := " Actions · ↑↓ choose · Enter select · Esc back "
	id := ""
	if worker != nil {
		id = worker.ID
		title = fmt.Sprintf(" Actions · %s · Esc back ", tview.Escape(worker.Name))
	}
	list.SetBorder(true).SetTitle(title)
	for _, item := range dashboardActions(worker, tasks) {
		list.AddItem(item.title, item.hint, item.key.Rune(), func() {
			if item.worker && currentID() != id {
				stale()
				return
			}
			activate(item.key)
		})
	}
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			cancel()
			return nil
		}
		if event.Key() == tcell.KeyRune && unicode.IsUpper(event.Rune()) && event.Rune() != 'J' {
			return tcell.NewEventKey(tcell.KeyRune, unicode.ToLower(event.Rune()), event.Modifiers())
		}
		return event
	})
	return list
}

func dashboardMenuPage(menu *tview.List) *tview.Flex {
	return tview.NewFlex().SetDirection(tview.FlexRow).AddItem(menu, 0, 1, true).AddItem(tview.NewTextView().SetText("↑↓ choose · Enter or a shortcut selects · Esc returns\nTarget shown above. Delete asks for confirmation."), 2, 0, false)
}

func dashboardDetailPage(worker sandbox.Worker, back func()) *tview.Flex {
	view := tview.NewTextView().SetDynamicColors(true).SetScrollable(true)
	view.SetBorder(true).SetTitle(" Instance details · Esc back ")
	renderDetail(view, worker, time.Now())
	view.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape || event.Rune() == 'q' || event.Rune() == 'Q' {
			back()
			return nil
		}
		return event
	})
	return tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(tview.NewTextView().SetText("↑↓ / PgUp / PgDn scroll · Esc back · Values captured when opened"), 2, 0, false)
}
