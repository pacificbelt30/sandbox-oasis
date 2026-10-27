package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/agentpolicy"
	"github.com/rivo/tview"
)

// RunAgentPolicies is usable without a running node or an unlocked credential store.
func RunAgentPolicies(path, override string) error {
	app := tview.NewApplication()
	page := newAgentPolicyPage(path, override, app.SetFocus, app.Stop)
	return app.SetRoot(page.pages, true).EnableMouse(true).EnablePaste(true).Run()
}

type agentPolicyPage struct {
	pages    *tview.Pages
	list     *tview.List
	fields   *tview.Table
	search   *tview.InputField
	detail   *tview.TextView
	status   *tview.TextView
	store    *agentpolicy.Store
	draft    *agentpolicy.Draft
	selected string
	names    []string
	visible  []agentpolicy.FieldDefinition
	focus    func(tview.Primitive) *tview.Application
	back     func()
	override string
}

func newAgentPolicyPage(path, override string, focus func(tview.Primitive) *tview.Application, back func()) *agentPolicyPage {
	p := &agentPolicyPage{
		pages: tview.NewPages(), list: tview.NewList().ShowSecondaryText(false),
		fields: tview.NewTable().SetSelectable(true, false).SetFixed(1, 0),
		search: tview.NewInputField().SetLabel(" Search settings: "),
		detail: tview.NewTextView().SetWrap(true).SetScrollable(true),
		status: tview.NewTextView().SetWrap(true), focus: focus, back: back, override: override,
	}
	p.list.SetTitle(" Policies ").SetBorder(true)
	p.fields.SetTitle(" Settings: value / source ").SetBorder(true)
	p.detail.SetTitle(" Setting and scope ").SetBorder(true)
	panes := tview.NewFlex().AddItem(p.list, 25, 0, true).AddItem(p.fields, 0, 1, false)
	help := tview.NewTextView().SetText("Tab panes  Enter edit  C clone  D default  R inherit  Ctrl-S review/save  Ctrl-R reload  Esc back")
	header := tview.NewTextView().SetText("Agent policies | Changes are staged until reviewed and saved.")
	body := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(header, 1, 0, false).AddItem(p.search, 1, 0, false).
		AddItem(panes, 0, 1, true).AddItem(p.detail, 6, 0, false).
		AddItem(p.status, 3, 0, false).AddItem(help, 2, 0, false)
	body.SetDrawFunc(func(_ tcell.Screen, _, _, width, _ int) (int, int, int, int) {
		if width < 85 {
			panes.SetDirection(tview.FlexRow).ResizeItem(p.list, 4, 0)
			body.ResizeItem(p.detail, 4, 0)
		} else {
			panes.SetDirection(tview.FlexColumn).ResizeItem(p.list, 25, 0)
			body.ResizeItem(p.detail, 6, 0)
		}
		return body.GetInnerRect()
	})
	p.pages.AddPage("browse", body, true, true)
	p.list.SetChangedFunc(func(index int, _, _ string, _ rune) {
		if index < 0 || index >= len(p.names) {
			return
		}
		p.selected = p.names[index]
		p.renderFields()
	})
	p.list.SetSelectedFunc(func(_ int, _, _ string, _ rune) { p.focus(p.fields) })
	p.fields.SetSelectionChangedFunc(func(row, _ int) { p.renderFieldDetail(row) })
	p.fields.SetSelectedFunc(func(row, _ int) { p.editField(row) })
	p.search.SetChangedFunc(func(_ string) { p.renderFields() })
	p.search.SetDoneFunc(func(_ tcell.Key) { p.focus(p.fields) })
	p.pages.SetInputCapture(p.input)
	var err error
	p.store, err = agentpolicy.NewStore(path)
	if err != nil {
		p.status.SetText(err.Error())
	} else {
		p.load()
	}
	return p
}

func (p *agentPolicyPage) load() {
	if p.store == nil {
		return
	}
	draft, err := agentpolicy.OpenDraft(p.store)
	if err != nil {
		p.status.SetText("Cannot load policies: " + err.Error() + "\nFix the file and use Ctrl-R to reload.")
		return
	}
	p.draft = draft
	if p.selected == "" {
		if current, err := agentpolicy.Resolve(draft.File(), ""); err == nil {
			p.selected = current.Name
		}
	}
	p.render()
}

func (p *agentPolicyPage) render() {
	if p.draft == nil {
		return
	}
	summaries, err := agentpolicy.List(p.draft.File())
	if err != nil {
		p.status.SetText(err.Error())
		return
	}
	selected := p.selected
	p.list.Clear()
	p.names = nil
	for index, summary := range summaries {
		prefix := "  "
		if summary.Default {
			prefix = "* "
		}
		kind := "host"
		if summary.BuiltIn {
			kind = "built-in"
		}
		p.names = append(p.names, summary.Name)
		p.list.AddItem(prefix+summary.Name+" ("+kind+")", "", 0, nil)
		if summary.Name == selected {
			p.list.SetCurrentItem(index)
		}
	}
	if len(summaries) > 0 {
		p.selected = p.names[p.list.GetCurrentItem()]
	}
	p.renderFields()
	state := "Saved"
	if p.draft.Dirty() {
		state = "Unsaved changes"
	}
	text := fmt.Sprintf("%s | * host default | %s\nApplies to new workers; running workers keep their settings.", state, p.store.Path)
	if p.override != "" {
		text += "\nGlobal run.agent_policy=" + p.override + " overrides the host default."
	}
	p.status.SetText(text)
}

func (p *agentPolicyPage) renderFields() {
	if p.draft == nil || p.selected == "" {
		return
	}
	effective, err := agentpolicy.Resolve(p.draft.File(), p.selected)
	if err != nil {
		p.status.SetText(err.Error())
		return
	}
	values := map[string]agentpolicy.Field{}
	for _, field := range effective.Fields() {
		values[field.Path] = field
	}
	p.fields.Clear()
	for col, title := range []string{"SETTING", "VALUE", "SOURCE"} {
		p.fields.SetCell(0, col, tview.NewTableCell(title).SetSelectable(false).SetTextColor(tcell.ColorYellow))
	}
	p.visible = agentpolicy.SearchFields(p.search.GetText())
	for index, definition := range p.visible {
		field := values[definition.Path]
		if definition.Path == "extends" {
			profile := p.draft.File().Profiles[p.selected]
			if builtin, ok := agentpolicy.BuiltIns()[p.selected]; ok {
				profile = builtin
			}
			field.Value, field.Source = profile.Extends, p.selected
		}
		for col, value := range []string{definition.Path, orDash(field.Value), orDash(field.Source)} {
			p.fields.SetCell(index+1, col, tview.NewTableCell(tview.Escape(value)).SetExpansion(1))
		}
	}
	row, _ := p.fields.GetSelection()
	if row < 1 || row > len(p.visible) {
		row = 1
	}
	p.fields.Select(row, 0)
	p.renderFieldDetail(row)
}

func (p *agentPolicyPage) renderFieldDetail(row int) {
	if p.draft == nil || row < 1 || row > len(p.visible) {
		p.detail.SetText("No matching settings. Change the search text.")
		return
	}
	definition := p.visible[row-1]
	scope := "Host profile: Enter edits a staged value. R removes the override and inherits again."
	if _, builtin := agentpolicy.BuiltIns()[p.selected]; builtin {
		scope = "Built-in policy (read-only). C clones it before editing. D stages it as the host default."
	}
	p.detail.SetText(scope + "\n" + agentpolicy.FormatDefinition(definition))
	p.detail.ScrollToBeginning()
}

func (p *agentPolicyPage) input(event *tcell.EventKey) *tcell.EventKey {
	page, _ := p.pages.GetFrontPage()
	if page != "browse" {
		if event.Key() == tcell.KeyEscape {
			p.closeDialog(page)
			return nil
		}
		return event
	}
	if event.Key() == tcell.KeyEscape {
		if p.search.HasFocus() {
			p.focus(p.fields)
			return nil
		}
		p.askDiscard(p.back)
		return nil
	}
	if event.Key() == tcell.KeyCtrlR {
		p.askDiscard(p.load)
		return nil
	}
	if event.Key() == tcell.KeyCtrlS {
		p.review()
		return nil
	}
	if p.search.HasFocus() {
		if event.Key() != tcell.KeyTab && event.Key() != tcell.KeyBacktab {
			return event
		}
	}
	if event.Key() == tcell.KeyTab || event.Key() == tcell.KeyBacktab {
		panes := []tview.Primitive{p.list, p.fields, p.detail, p.search}
		for index, pane := range panes {
			if pane.HasFocus() {
				next := (index + 1) % len(panes)
				if event.Key() == tcell.KeyBacktab {
					next = (index + len(panes) - 1) % len(panes)
				}
				p.focus(panes[next])
				break
			}
		}
		return nil
	}
	if p.draft == nil {
		return event
	}
	switch event.Rune() {
	case '/':
		p.focus(p.search)
	case 'c', 'C':
		p.clone()
	case 'd', 'D':
		if err := p.draft.SetDefault(p.selected); err != nil {
			p.status.SetText(err.Error())
		} else {
			p.render()
		}
	case 'r', 'R':
		row, _ := p.fields.GetSelection()
		if row > 0 && row <= len(p.visible) {
			p.change(p.visible[row-1].Path, "", true)
		}
	default:
		return event
	}
	return nil
}

func (p *agentPolicyPage) closeDialog(name string) {
	p.pages.RemovePage(name)
	p.pages.SwitchToPage("browse")
	p.focus(p.fields)
}

func (p *agentPolicyPage) dialog(name string, primitive tview.Primitive) {
	p.pages.AddAndSwitchToPage(name, primitive, true)
	p.focus(primitive)
}

func (p *agentPolicyPage) askDiscard(action func()) {
	if p.draft == nil || !p.draft.Dirty() {
		action()
		return
	}
	modal := tview.NewModal().SetText("Discard unsaved policy changes?").AddButtons([]string{"Keep editing", "Discard"})
	modal.SetDoneFunc(func(index int, _ string) {
		p.closeDialog("discard")
		if index == 1 {
			action()
		}
	})
	p.dialog("discard", modal)
}

func (p *agentPolicyPage) clone() {
	name := tview.NewInputField().SetLabel("New name: ").SetFieldWidth(30)
	form := tview.NewForm().AddFormItem(name)
	form.SetTitle(" Clone " + p.selected + " ").SetBorder(true)
	form.AddButton("Clone", func() {
		if err := p.draft.Clone(p.selected, name.GetText()); err != nil {
			form.SetTitle(" " + tview.Escape(err.Error()) + " ")
			return
		}
		p.selected = strings.ToLower(strings.TrimSpace(name.GetText()))
		p.closeDialog("clone")
		p.render()
	}).AddButton("Cancel", func() { p.closeDialog("clone") })
	p.dialog("clone", centered(form, 72, 9))
}

func (p *agentPolicyPage) change(path, value string, inherit bool) bool {
	if err := p.draft.Change(p.selected, path, value, inherit); err != nil {
		p.status.SetText(err.Error())
		return false
	}
	p.render()
	return true
}

func (p *agentPolicyPage) editField(row int) {
	if p.draft == nil || row < 1 || row > len(p.visible) {
		return
	}
	if _, builtin := agentpolicy.BuiltIns()[p.selected]; builtin {
		p.status.SetText("Built-in policy is read-only. Press C to clone it, or D to stage it as the host default.")
		return
	}
	definition := p.visible[row-1]
	value := ""
	effective, err := agentpolicy.Resolve(p.draft.File(), p.selected)
	if err != nil {
		p.status.SetText(err.Error())
		return
	}
	for _, field := range effective.Fields() {
		if field.Path == definition.Path {
			value = field.Value
		}
	}
	if definition.Path == "extends" {
		value = p.draft.File().Profiles[p.selected].Extends
	}
	form := tview.NewForm()
	form.SetTitle(" " + definition.Path + " ").SetBorder(true)
	options := definition.Values
	if definition.Type == agentpolicy.ValueBoolean {
		options = []string{"false", "true"}
	}
	if len(options) > 0 {
		selected := 0
		for i, option := range options {
			if option == value {
				selected = i
			}
		}
		value = options[selected]
		form.AddDropDown("Value", options, selected, func(option string, _ int) { value = option })
	} else {
		form.AddInputField("Value", value, 48, nil, func(text string) { value = text })
	}
	apply := func(inherit bool) {
		if p.change(definition.Path, value, inherit) {
			p.closeDialog("edit")
		} else {
			form.SetTitle(" " + tview.Escape(p.status.GetText(false)) + " ")
		}
	}
	form.AddButton("Stage", func() { apply(false) }).AddButton("Inherit", func() { apply(true) }).AddButton("Cancel", func() { p.closeDialog("edit") })
	p.dialog("edit", centered(form, 76, 10))
}

func (p *agentPolicyPage) review() {
	if p.draft == nil || !p.draft.Dirty() {
		p.status.SetText("No policy changes to save.")
		return
	}
	text := tview.NewTextView().SetScrollable(true).SetWrap(true).SetText(p.draft.Review())
	text.SetTitle(" Review staged changes and warnings ").SetBorder(true)
	buttons := tview.NewForm().AddButton("Cancel", func() { p.closeDialog("review") }).AddButton("Save", func() {
		if err := p.draft.Save(); err != nil {
			p.closeDialog("review")
			p.status.SetText("Not saved: " + err.Error() + "\nYour draft is retained. Ctrl-R reloads after discarding it.")
			return
		}
		p.closeDialog("review")
		p.render()
	})
	panel := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(text, 0, 1, true).AddItem(buttons, 3, 0, false)
	text.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab || event.Key() == tcell.KeyBacktab {
			p.focus(buttons)
			return nil
		}
		return event
	})
	buttons.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		item, button := buttons.GetFocusedItemIndex()
		if event.Key() == tcell.KeyBacktab && item < 0 && button == 0 {
			p.focus(text)
			return nil
		}
		return event
	})
	p.dialog("review", panel)
}
