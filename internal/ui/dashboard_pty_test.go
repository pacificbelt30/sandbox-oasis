package ui

import (
	"context"
	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/instanceux"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/rivo/tview"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestDashboardActionsPTYHelper(t *testing.T) {
	root := os.Getenv("OAISO_ACTIONS_PTY_HELPER")
	if root == "" {
		t.Skip("isolated PTY helper")
	}
	app := tview.NewApplication()
	views := newDashboardViews()
	model := instanceux.NewModel()
	model.Replace([]sandbox.Worker{{ID: "id", Name: "sandbox-fixture", Status: "running"}})
	pages := tview.NewPages().AddPage("main", dashboardBody(views), true, true)
	showMain := func() { pages.SwitchToPage("main"); app.SetFocus(views.table) }
	open := func() {
		w, _ := model.Selected()
		closeMenu := func() { pages.RemovePage("actions"); showMain() }
		menu := dashboardActionMenu(&w, false, func() string { return model.SelectedID }, func(key *tcell.EventKey) {
			if key.Rune() == 'x' {
				closeMenu()
				form := commandForm(w, func(command string) {
					if err := os.WriteFile(filepath.Join(root, "executed"), []byte(command), 0600); err != nil {
						t.Error(err)
					}
					pages.RemovePage("command")
					showMain()
				}, func() { pages.RemovePage("command"); showMain() })
				setCommandCompletions(form, append(instanceux.ShellBuiltins(), "fixture-completion"))
				pages.AddAndSwitchToPage("command", centered(form, 76, 9), true)
				app.SetFocus(form)
				return
			}
			if err := os.WriteFile(filepath.Join(root, "selected"), []byte(string(key.Rune())), 0600); err != nil {
				t.Error(err)
			}
			closeMenu()
		}, closeMenu, closeMenu)
		pages.AddAndSwitchToPage("actions", dashboardMenuPage(menu), true)
		app.SetFocus(menu)
	}
	views.actions.SetSelectedFunc(open)
	views.table.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyF2 || e.Rune() == ' ' {
			open()
			return nil
		}
		if e.Key() == tcell.KeyTab {
			app.SetFocus(views.actions)
			return nil
		}
		if e.Rune() == 'q' {
			app.Stop()
			return nil
		}
		return e
	})
	app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		width, _ := screen.Size()
		resizeDashboard(views, width)
		renderDashboard(views, model, time.Now())
		return false
	})
	app.SetRoot(pages, true)
	app.SetFocus(views.table)
	if err := runTerminal(app); err != nil {
		t.Fatal(err)
	}
}
func TestDashboardActionsThroughPTY(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux PTY")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "python3", "testdata/dashboard-actions-probe.py", binary, t.TempDir())
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("actions PTY: %v\n%s", err, b)
	}
	t.Log(string(b))
}
