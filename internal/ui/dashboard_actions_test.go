package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/instanceux"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/rivo/tview"
	"strings"
	"testing"
	"time"
)

func TestActionMenuOffersRelevantActionsAndRejectsChangedSelection(t *testing.T) {
	worker := sandbox.Worker{ID: "first", Name: "sandbox", Status: "running"}
	selected := worker.ID
	var activated *tcell.EventKey
	stale, cancelled := false, false
	menu := dashboardActionMenu(&worker, false, func() string { return selected }, func(e *tcell.EventKey) { activated = e }, func() { stale = true }, func() { cancelled = true })
	menu.SetCurrentItem(1)
	menu.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	if activated == nil || activated.Rune() != 'c' {
		t.Fatal("Connect did not use the existing shortcut")
	}
	activated = nil
	selected = "another"
	menu.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	if !stale || activated != nil {
		t.Fatal("stale menu targeted another sandbox")
	}
	menu.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if !cancelled {
		t.Fatal("Esc did not dismiss actions")
	}
	for _, item := range dashboardActions(nil, false) {
		if item.worker {
			t.Fatal("empty dashboard offers a worker action")
		}
	}
	worker.Status = "exited"
	for _, item := range dashboardActions(&worker, false) {
		if item.key.Rune() == 'c' || item.key.Rune() == 's' || item.key.Rune() == 'f' || item.key.Rune() == 'u' {
			t.Fatal("stopped sandbox offers a running-only action")
		}
	}
}

func screenText(screen tcell.SimulationScreen) string {
	cells, _, _ := screen.GetContents()
	var text strings.Builder
	for _, cell := range cells {
		text.Write(cell.Bytes)
	}
	return text.String()
}
func TestDashboardResizesWithoutHidingStatusOrMainActions(t *testing.T) {
	v := newDashboardViews()
	body := dashboardBody(v)
	model := instanceux.NewModel()
	model.Replace([]sandbox.Worker{{ID: "id", Name: strings.Repeat("sandbox-", 12), Status: "running"}})
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	for _, size := range [][2]int{{160, 40}, {110, 24}, {80, 24}, {60, 22}, {160, 40}} {
		screen.SetSize(size[0], size[1])
		resizeDashboard(v, size[0])
		renderDashboard(v, model, time.Now())
		body.SetRect(0, 0, size[0], size[1])
		screen.Clear()
		body.Draw(screen)
		screen.Show()
		text := screenText(screen)
		for _, expected := range []string{"STATUS", "running", "UPTIME", "Actions F2", "New", "Quit"} {
			if !strings.Contains(text, expected) {
				t.Fatalf("%q hidden at %v: %s", expected, size, text)
			}
		}
		if model.SelectedID != "id" {
			t.Fatal("resize changed selection")
		}
	}
}

func TestPreviewTabCycleAndEscapeNeedNoHiddenShortcut(t *testing.T) {
	d, app, back := testDesktop(t)
	d.preview(sandbox.Worker{ID: "id", Name: "sandbox", Status: "running"})
	_, page := d.pages.GetFrontPage()
	body := page.(*tview.Flex)
	form, list := body.GetItem(0).(*tview.Form), body.GetItem(1).(*tview.List)
	form.SetFocus(form.GetFormItemCount() + form.GetButtonCount() - 1)
	if form.GetInputCapture()(tcell.NewEventKey(tcell.KeyTab, 0, 0)) != nil || app.GetFocus() != list {
		t.Fatal("Tab from last button did not reach previews")
	}
	list.GetInputCapture()(tcell.NewEventKey(tcell.KeyTab, 0, 0))
	// Forms delegate focus to their first field, rather than retaining it.
	if app.GetFocus() != form.GetFormItem(0) {
		t.Fatal("Tab did not return to the first input")
	}
	form.SetFocus(0)
	form.GetInputCapture()(tcell.NewEventKey(tcell.KeyBacktab, 0, 0))
	if app.GetFocus() != list {
		t.Fatal("Shift-Tab from first field did not reach previews")
	}
	list.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if !*back {
		t.Fatal("Esc from URLs did not return to instance list")
	}
}

func TestNarrowDetailViewKeepsFullValuesAndReturnsWithEscape(t *testing.T) {
	worker := sandbox.Worker{ID: "id", Name: strings.Repeat("long-sandbox-name-", 5), Image: "registry.example/team/image:version", Status: "exited"}
	back := false
	page := dashboardDetailPage(worker, func() { back = true })
	view := page.GetItem(0).(*tview.TextView)
	if !strings.Contains(view.GetText(false), worker.Name) || !strings.Contains(view.GetText(false), worker.Image) {
		t.Fatal("details lost the full name or image")
	}
	view.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if !back {
		t.Fatal("Esc did not leave details")
	}
}

func TestPreviewRefreshPreservesSelectedURLWhenOthersExpire(t *testing.T) {
	d, _, _ := testDesktop(t)
	first, second := make(chan struct{}), make(chan struct{})
	d.previews = []desktopPreview{{URL: "first-url", Worker: "id", Port: 3000, Done: first, Close: func() error { return nil }}, {URL: "selected-url", Worker: "id", Port: 4000, Done: second, Close: func() error { return nil }}}
	d.preview(sandbox.Worker{ID: "id", Name: "worker"})
	_, page := d.pages.GetFrontPage()
	list := page.(*tview.Flex).GetItem(1).(*tview.List)
	list.SetCurrentItem(1)
	d.refreshPreviews()
	if list.GetCurrentItem() != 1 {
		t.Fatal("refresh moved the selected preview")
	}
	close(first)
	d.refreshPreviews()
	_, url := list.GetItemText(list.GetCurrentItem())
	if url != "selected-url" || list.GetItemCount() != 1 {
		t.Fatal("expiration selected a different preview", url)
	}
}
