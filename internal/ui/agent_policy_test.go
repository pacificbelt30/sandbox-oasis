package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func TestPolicyEditorPTYHelper(t *testing.T) {
	path := os.Getenv("OAISO_POLICY_EDITOR_HELPER")
	if path == "" {
		t.Skip("helper for isolated terminal process")
	}
	if err := RunAgentPolicies(path, ""); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyEditorThroughPTY(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("host PTY acceptance requires Linux")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal("PTY acceptance requires Python 3.11+", err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/policy-editor-probe.py", binary, filepath.Join(t.TempDir(), "policies", "config.toml"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("policy editor PTY: %v\n%s", err, output)
	}
	t.Log(string(output))
}

func policyTestPage(t *testing.T) (*agentPolicyPage, *tview.Application, *bool) {
	t.Helper()
	app := tview.NewApplication()
	back := false
	page := newAgentPolicyPage(filepath.Join(t.TempDir(), "policy", "config.toml"), "review", app.SetFocus, func() { back = true })
	app.SetRoot(page.pages, true).SetFocus(page.list)
	return page, app, &back
}

func TestPolicyPageCloneEditReviewSaveAndDiscard(t *testing.T) {
	p, app, back := policyTestPage(t)
	if err := p.draft.Clone("interactive", "my-team"); err != nil {
		t.Fatal(err)
	}
	p.selected = "my-team"
	p.render()
	p.search.SetText("codex.web_search")
	p.editField(1)
	_, modal := p.pages.GetFrontPage()
	// A builtin enum is a constrained dropdown; edits are staged, never saved.
	container := modal.(*tview.Flex).GetItem(1).(*tview.Flex)
	form := container.GetItem(1).(*tview.Form)
	form.GetFormItem(0).(*tview.DropDown).SetCurrentOption(3) // live
	form.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	if _, err := os.Stat(p.store.Path); !os.IsNotExist(err) {
		t.Fatal("edit saved before review")
	}
	p.input(tcell.NewEventKey(tcell.KeyCtrlS, 0, 0))
	if page, _ := p.pages.GetFrontPage(); page != "review" {
		t.Fatal("save skipped review")
	}
	p.input(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if !p.draft.Dirty() {
		t.Fatal("cancelled review lost draft")
	}
	app.SetFocus(p.list)
	p.input(tcell.NewEventKey(tcell.KeyRune, 'D', 0))
	p.review()
	_, review := p.pages.GetFrontPage()
	buttons := review.(*tview.Flex).GetItem(1).(*tview.Form)
	buttons.GetButton(1).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	stored, err := p.store.Load()
	if err != nil || stored.File.Default != "my-team" || p.draft.Dirty() {
		t.Fatal("save failed", stored, err)
	}
	if !strings.Contains(p.status.GetText(false), "run.agent_policy=review overrides") {
		t.Fatal("global override hidden")
	}
	if !p.change("codex.web_search", "cached", false) {
		t.Fatal("could not edit")
	}
	p.input(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if *back {
		t.Fatal("Esc discarded without asking")
	}
	_, discard := p.pages.GetFrontPage()
	discard.(*tview.Modal).InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(tview.Primitive) {})
	if *back {
		t.Fatal("Esc confirmed discard")
	}
}

func TestPolicyPageScreenSizesEscAndMarkup(t *testing.T) {
	p, app, _ := policyTestPage(t)
	if err := p.draft.Clone("interactive", "team"); err != nil {
		t.Fatal(err)
	}
	if err := p.draft.Change("team", "description", "日本語 [red]literal", false); err != nil {
		t.Fatal(err)
	}
	p.selected = "team"
	p.render()
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	for _, size := range [][2]int{{120, 40}, {80, 24}, {60, 22}} {
		screen.SetSize(size[0], size[1])
		p.pages.SetRect(0, 0, size[0], size[1])
		p.pages.Draw(screen)
		screen.Show()
		cells, _, _ := screen.GetContents()
		var visible strings.Builder
		for _, cell := range cells {
			visible.Write(cell.Bytes)
		}
		if !strings.Contains(visible.String(), "Agent policies") || !strings.Contains(visible.String(), "Esc back") {
			t.Fatal("navigation hidden at", size, visible.String())
		}
	}
	p.search.SetText("description")
	p.fields.Select(1, 0)
	app.SetFocus(p.fields)
	p.editField(1)
	_, modal := p.pages.GetFrontPage()
	form := modal.(*tview.Flex).GetItem(1).(*tview.Flex).GetItem(1).(*tview.Form)
	if value := form.GetFormItem(0).(*tview.InputField).GetText(); value != "日本語 [red]literal" {
		t.Fatal("markup escaped into stored value", value)
	}
	p.input(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if !p.fields.HasFocus() {
		t.Fatal("Esc did not restore settings focus")
	}
	app.SetFocus(p.search)
	if p.input(tcell.NewEventKey(tcell.KeyRune, 'D', 0)) == nil {
		t.Fatal("typing D in search changed default")
	}
	p.input(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if page, _ := p.pages.GetFrontPage(); page != "browse" || !p.fields.HasFocus() {
		t.Fatal("Esc in search left the editor rather than the filter")
	}
}

func TestPolicyPageKeyboardReachesScrollableExplanation(t *testing.T) {
	p, _, _ := policyTestPage(t)
	for _, pane := range []tview.Primitive{p.fields, p.detail, p.search, p.list} {
		p.input(tcell.NewEventKey(tcell.KeyTab, 0, 0))
		if !pane.HasFocus() {
			t.Fatal("Tab cannot reach every pane, including the safety explanation")
		}
	}
	p.input(tcell.NewEventKey(tcell.KeyBacktab, 0, 0))
	if !p.search.HasFocus() {
		t.Fatal("Shift-Tab does not move backwards")
	}
}

func TestPolicyPageConflictingSaveAndInvalidFile(t *testing.T) {
	p, _, _ := policyTestPage(t)
	if err := p.draft.SetDefault("private"); err != nil {
		t.Fatal(err)
	}
	if err := p.store.SetDefault("review"); err != nil {
		t.Fatal(err)
	}
	p.review()
	_, review := p.pages.GetFrontPage()
	review.(*tview.Flex).GetItem(1).(*tview.Form).GetButton(1).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	if !p.draft.Dirty() || !strings.Contains(p.status.GetText(false), "Not saved") {
		t.Fatal("conflict lost or hidden")
	}
	path := p.store.Path
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	app := tview.NewApplication()
	broken := newAgentPolicyPage(path, "", app.SetFocus, func() {})
	if broken.draft != nil || !strings.Contains(broken.status.GetText(false), "Cannot load") {
		t.Fatal("unsafe file accepted")
	}
}
