package ui

import (
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/rivo/tview"
)

func TestCommandCompletionTabDoesNotExecuteAndEscapeCancels(t *testing.T) {
	executed, cancelled := false, false
	form := commandForm(sandbox.Worker{Name: "sandbox"}, func(string) { executed = true }, func() { cancelled = true })
	setCommandCompletions(form, []string{"fixture-tool"})
	app := tview.NewApplication().SetRoot(form, true)
	app.SetFocus(form)
	input := form.GetFormItem(0).(*tview.InputField)
	input.PasteHandler()("fix", func(p tview.Primitive) { app.SetFocus(p) })
	form.InputHandler()(tcell.NewEventKey(tcell.KeyTab, 0, 0), func(p tview.Primitive) { app.SetFocus(p) })
	if input.GetText() != "fixture-tool " || executed {
		t.Fatal("Tab did not complete without execution", input.GetText())
	}
	input.SetText("fix")
	input.Autocomplete()
	form.InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(p tview.Primitive) { app.SetFocus(p) })
	if !cancelled || executed {
		t.Fatal("Escape with suggestions did not cancel safely")
	}
}
