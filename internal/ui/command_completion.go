package ui

import (
	"github.com/pacificbelt30/codex-dock/internal/instanceux"
	"github.com/rivo/tview"
)

func setCommandCompletions(form *tview.Form, names []string) {
	input, ok := form.GetFormItem(0).(*tview.InputField)
	if !ok {
		return
	}
	input.SetAutocompleteUseTags(false).SetAutocompleteFunc(func(prefix string) []string {
		return instanceux.CommandSuggestions(prefix, names)
	}).SetAutocompletedFunc(func(text string, _ int, source int) bool {
		if source == tview.AutocompletedNavigate {
			return false
		}
		input.SetText(text + " ")
		return true
	})
}
