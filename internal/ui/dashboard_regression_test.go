package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/instanceux"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/rivo/tview"
)

func TestInstanceDetailsKeepLiteralBracketsAndStyles(t *testing.T) {
	worker := sandbox.Worker{ID: "literal", Name: "[red]日本語", Status: "running", Branch: "[::b]branch", Image: "image:[blue]", AgentPolicy: "[green]policy", Task: "[::u]literal [\"region\"] [[] [red[]]"}
	view := tview.NewTextView().SetDynamicColors(true)
	renderDetail(view, worker, time.Now())
	for _, want := range []string{worker.Name, worker.Branch, worker.Image, worker.AgentPolicy, worker.Task} {
		if !strings.Contains(view.GetText(true), want) {
			t.Errorf("literal value %q was changed in details: %s", want, view.GetText(true))
		}
	}
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(100, 30)
	view.SetRect(0, 0, 100, 30)
	view.Draw(screen)
	screen.Show()
	if !strings.Contains(screenText(screen), worker.Name) {
		t.Fatal("literal name missing from rendered screen", screenText(screen))
	}
	_, style, _ := screen.Get(0, 0)
	foreground, _, attributes := style.Decompose()
	if foreground == tcell.ColorRed || attributes&tcell.AttrBold == 0 {
		t.Fatal("literal name replaced the intended heading style", foreground, attributes)
	}
}

func TestDashboardRefreshPreservesDetailScrollAndResetsForNewSelection(t *testing.T) {
	v := newDashboardViews()
	model := instanceux.NewModel()
	model.Replace([]sandbox.Worker{{ID: "first", Name: "first", Task: strings.Repeat("details\n", 50)}, {ID: "second", Name: "second"}})
	renderDashboard(v, model, time.Now())
	v.detail.ScrollTo(12, 0)
	model.Replace(model.All)
	renderDashboard(v, model, time.Now())
	if row, _ := v.detail.GetScrollOffset(); row != 12 {
		t.Fatalf("periodic refresh moved detail scroll to %d", row)
	}
	model.SelectIndex(1)
	renderDashboard(v, model, time.Now())
	if row, _ := v.detail.GetScrollOffset(); row != 0 {
		t.Fatalf("new instance inherited previous scroll offset %d", row)
	}
	if !strings.Contains(v.detail.GetText(true), "second") {
		t.Fatal("new selection did not update details")
	}
}

func TestCommandFormEscapeCancelsFromInputAndButtons(t *testing.T) {
	for _, focus := range []int{0, 1, 2} {
		t.Run(string(rune('0'+focus)), func(t *testing.T) {
			cancelled, executed := 0, 0
			form := commandForm(sandbox.Worker{Name: "sandbox"}, func(string) { executed++ }, func() { cancelled++ })
			form.GetFormItem(0).(*tview.InputField).SetText("echo must-not-run")
			form.SetFocus(focus)
			app := tview.NewApplication().SetRoot(form, true)
			app.SetFocus(form)
			form.InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(p tview.Primitive) { app.SetFocus(p) })
			if cancelled != 1 || executed != 0 {
				t.Fatalf("Esc cancelled %d times and executed %d times", cancelled, executed)
			}
		})
	}
}

func TestCenteredCommandFormFitsAfterResize(t *testing.T) {
	form := commandForm(sandbox.Worker{Name: "[red]literal"}, func(string) {}, func() {})
	page := centered(form, 76, 9)
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	for _, size := range [][2]int{{100, 30}, {60, 22}, {40, 12}, {100, 30}} {
		screen.SetSize(size[0], size[1])
		screen.Clear()
		page.SetRect(0, 0, size[0], size[1])
		page.Draw(screen)
		screen.Show()
		x, y, width, height := form.GetRect()
		if x < 0 || y < 0 || x+width > size[0] || y+height > size[1] {
			t.Errorf("form exceeds %v: (%d,%d,%d,%d)", size, x, y, width, height)
		}
		for _, want := range []string{"Command", "Run", "Cancel", "[red]literal"} {
			if !strings.Contains(screenText(screen), want) {
				t.Errorf("%q missing at %v: %s", want, size, screenText(screen))
			}
		}
	}
}
