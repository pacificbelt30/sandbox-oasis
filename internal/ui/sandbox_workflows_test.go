package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/kataguard"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/pacificbelt30/codex-dock/internal/storageinfo"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
	"github.com/rivo/tview"
)

func TestEnvironmentSaveAndLoadRequireSeparateReviews(t *testing.T) {
	d, _, _ := testDesktop(t)
	private := t.TempDir()
	if err := os.Chmod(private, 0700); err != nil {
		t.Fatal(err)
	}
	d.environments = &sandboxux.EnvironmentStore{Path: filepath.Join(private, "settings.json")}
	d.createForm(kataguard.ResourceProfiles(), storageinfo.Status{}, nil)
	_, page := d.pages.GetFrontPage()
	form := page.(*tview.Flex).GetItem(0).(*tview.Form)
	form.GetFormItemByLabel("Command (blank: keep alive)").(*tview.TextArea).SetText("printf '[red]日本語'", true)
	form.GetInputCapture()(tcell.NewEventKey(tcell.KeyCtrlS, 0, 0))
	_, page = d.pages.GetFrontPage()
	save := page.(*tview.Form)
	save.GetFormItem(0).(*tview.InputField).SetText("Python検証")
	save.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	if _, err := os.Stat(d.environments.Path); !os.IsNotExist(err) {
		t.Fatal("saved before review approval", err)
	}
	_, page = d.pages.GetFrontPage()
	review := page.(*tview.Flex)
	if !strings.Contains(review.GetItem(0).(*tview.TextView).GetText(false), "[red]日本語") {
		t.Fatal("command missing from save review")
	}
	review.GetItem(1).(*tview.Form).GetButton(1).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	snapshot, err := d.environments.Load()
	if err != nil || snapshot.File.Environments["Python検証"].Command != "printf '[red]日本語'" {
		t.Fatal(snapshot, err)
	}
	d.loadEnvironment(sandboxux.LaunchSpec{Project: t.TempDir(), AI: true, Mounts: []string{"host:/extra"}}, kataguard.ResourceProfiles(), storageinfo.Status{}, nil, form)
	_, page = d.pages.GetFrontPage()
	list := page.(*tview.List)
	list.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	if name, _ := d.pages.GetFrontPage(); name != "environment-review" {
		t.Fatal("load skipped review", name)
	}
	_, page = d.pages.GetFrontPage()
	review = page.(*tview.Flex)
	review.GetItem(1).(*tview.Form).GetButton(1).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	if name, _ := d.pages.GetFrontPage(); name != "create" {
		t.Fatal("load launched or left creation", name)
	}
	_, page = d.pages.GetFrontPage()
	loaded := page.(*tview.Flex).GetItem(0).(*tview.Form)
	if loaded.GetFormItemByLabel("Enable AI services (optional)").(*tview.Checkbox).IsChecked() {
		t.Fatal("load enabled AI")
	}
	if got := loaded.GetFormItemByLabel("Extra mounts (one/line)").(*tview.TextArea).GetText(); got != "" {
		t.Fatal("load retained mounts", got)
	}
}
func TestScrollableReviewReachesBothButtonsAndCancelsOnSmallScreen(t *testing.T) {
	d, app, _ := testDesktop(t)
	confirmed, cancelled := false, false
	d.reviewPage("review", " Review ", strings.Repeat("setting [red]日本語\n", 100), func() { confirmed = true }, func() { cancelled = true })
	_, p := d.pages.GetFrontPage()
	body := p.(*tview.Flex)
	view := body.GetItem(0).(*tview.TextView)
	buttons := body.GetItem(1).(*tview.Form)
	handler := body.InputHandler()
	focus := func(p tview.Primitive) { app.SetFocus(p) }
	handler(tcell.NewEventKey(tcell.KeyTab, 0, 0), focus)
	if app.GetFocus() != buttons.GetButton(0) {
		t.Fatal("first tab did not reach Cancel")
	}
	handler(tcell.NewEventKey(tcell.KeyTab, 0, 0), focus)
	if app.GetFocus() != buttons.GetButton(1) {
		t.Fatal("second tab did not reach Confirm")
	}
	handler(tcell.NewEventKey(tcell.KeyTab, 0, 0), focus)
	if app.GetFocus() != view {
		t.Fatal("tab did not return to scrollable settings")
	}
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	defer screen.Fini()
	screen.SetSize(60, 22)
	body.SetRect(0, 0, 60, 22)
	body.Draw(screen)
	screen.Show()
	if !strings.Contains(screenText(screen), "Cancel") || !strings.Contains(screenText(screen), "Confirm") {
		t.Fatal("review buttons hidden")
	}
	handler(tcell.NewEventKey(tcell.KeyEscape, 0, 0), focus)
	if confirmed || !cancelled {
		t.Fatal("escape approved or failed to cancel")
	}
}
func TestLifecycleDisplayDistinguishesActiveProtectionAndRetention(t *testing.T) {
	now := time.Now().UTC()
	s := sandbox.LifecycleStatus{Policy: workerpolicy.Lifecycle{MaxLifetime: time.Hour, IdleTimeout: time.Minute, Retention: 24 * time.Hour, ProtectMain: true}, CreatedAt: now.Add(-30 * time.Minute), LastActivity: now.Add(-2 * time.Minute), Running: true, ActiveExecutions: 1}
	text := lifecycleSummary(s, now)
	for _, expected := range []string{"remaining 30m0s", "protected by 1", "starts when stopped"} {
		if !strings.Contains(text, expected) {
			t.Fatal(expected, text)
		}
	}
	s.Running = false
	s.FinishedAt = now.Add(-23 * time.Hour)
	s.ActiveExecutions = 0
	if text = lifecycleSummary(s, now); !strings.Contains(text, "remaining 1h0m0s") {
		t.Fatal(text)
	}
	if text = quotaSummary(storageinfo.Usage{}); !strings.Contains(text, "unavailable") {
		t.Fatal("unknown quota presented as zero usage", text)
	}
}
