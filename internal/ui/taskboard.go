package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/instanceux"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/taskboard"
	"github.com/rivo/tview"
)

type taskBoardPage struct {
	app            *tview.Application
	service        *taskboard.Service
	pages          *tview.Pages
	table          *tview.Table
	detail, footer *tview.TextView
	tasks          []taskboard.Task
	busy           bool
	back           func()
}

func RunTaskBoard(service *taskboard.Service) error {
	app := tview.NewApplication()
	board := newTaskBoardPage(app, service, app.Stop)
	done := make(chan struct{})
	defer close(done)
	board.start(done)
	app.SetRoot(board.pages, true)
	return runTerminal(app)
}
func newTaskBoardPage(app *tview.Application, service *taskboard.Service, back func()) *taskBoardPage {
	b := &taskBoardPage{app: app, service: service, pages: tview.NewPages(), table: tview.NewTable().SetSelectable(true, false).SetFixed(1, 0), detail: tview.NewTextView().SetDynamicColors(false).SetScrollable(true), footer: tview.NewTextView().SetDynamicColors(true), back: back}
	b.table.SetBorder(true).SetTitle(" Tasks ")
	b.detail.SetBorder(true).SetTitle(" Task / attempts ")
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(b.table, 0, 2, true).AddItem(b.detail, 0, 1, false).AddItem(b.footer, 3, 0, false)
	b.pages.AddPage("board", body, true, true)
	b.table.SetSelectionChangedFunc(func(row, _ int) {
		if row > 0 && row <= len(b.tasks) {
			b.detail.SetText(taskDetail(b.tasks[row-1]))
		}
	})
	b.table.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			if !b.busy {
				b.back()
			}
			return nil
		}
		if e.Key() == tcell.KeyTab {
			app.SetFocus(b.detail)
			return nil
		}
		row, _ := b.table.GetSelection()
		var t taskboard.Task
		if row > 0 && row <= len(b.tasks) {
			t = b.tasks[row-1]
		}
		switch e.Rune() {
		case 'q', 'Q':
			if !b.busy {
				b.back()
			}
			return nil
		case 'n', 'N':
			if !b.busy {
				b.create()
			}
			return nil
		case 'g', 'G':
			b.work(func(ctx context.Context) error {
				list, err := service.Store.List()
				if err != nil {
					return err
				}
				for _, v := range list {
					if len(v.Attempts) > 0 {
						if _, err = service.Sync(ctx, v.ID); err != nil {
							return err
						}
					}
				}
				return nil
			})
			return nil
		}
		if t.ID == "" || b.busy {
			return e
		}
		switch e.Rune() {
		case 'r', 'R':
			b.work(func(ctx context.Context) error { _, err := service.Run(ctx, t.ID); return err })
			return nil
		case 's', 'S':
			b.confirm("Stop task and verification workers?", func() { b.work(func(ctx context.Context) error { _, err := service.Stop(ctx, t.ID); return err }) })
			return nil
		case 't', 'T':
			b.work(func(ctx context.Context) error { _, err := service.Test(ctx, t.ID); return err })
			return nil
		case 'v', 'V':
			b.workResult(func(ctx context.Context) (func(), error) {
				r, err := service.Review(ctx, t.ID)
				if err != nil {
					return nil, err
				}
				fresh, err := service.Store.Get(t.ID)
				return func() { b.review(fresh, r) }, err
			})
			return nil
		case 'c', 'C':
			b.connect(t)
			return nil
		}
		return e
	})
	b.detail.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyTab || e.Key() == tcell.KeyEscape {
			app.SetFocus(b.table)
			return nil
		}
		return e
	})
	b.load()
	return b
}
func taskDetail(t taskboard.Task) string {
	var s strings.Builder
	fmt.Fprintf(&s, "%s\n%s | %s | base %s\nProject: %s\nAgent: %s | Policy: %s\n\n%s\n", t.Spec.Title, t.ID, t.State, t.Base, t.Spec.Project, t.Spec.Agent, t.Spec.Policy, t.Spec.Prompt)
	for _, a := range t.Attempts {
		fmt.Fprintf(&s, "\nAttempt %d: %s | %s\nWorkspace: %s\nAgent exit: %v | test: %s", a.Number, a.State, a.Name, a.Workspace, exitText(a.ExitCode), a.TestState)
		if a.TestExit != nil {
			fmt.Fprintf(&s, " (exit %d)", *a.TestExit)
		}
		if a.TestTree != "" {
			fmt.Fprintf(&s, "\nTested snapshot: %s", a.TestTree)
		}
		if a.Error != "" {
			fmt.Fprintf(&s, "\n%s", a.Error)
		}
	}
	if t.AcceptedBranch != "" {
		fmt.Fprintf(&s, "\nAdopted: %s %s", t.AcceptedBranch, t.AcceptedCommit)
	}
	return s.String()
}
func exitText(n *int) string {
	if n == nil {
		return "pending"
	}
	return fmt.Sprint(*n)
}
func (b *taskBoardPage) load() {
	list, err := b.service.Store.List()
	if err != nil {
		b.footer.SetText(tview.Escape(err.Error()))
		return
	}
	selected := ""
	row, _ := b.table.GetSelection()
	if row > 0 && row <= len(b.tasks) {
		selected = b.tasks[row-1].ID
	}
	b.tasks = list
	b.table.Clear()
	for i, h := range []string{"State", "Task", "Agent", "Attempt", "Test"} {
		b.table.SetCell(0, i, tview.NewTableCell(h).SetSelectable(false).SetTextColor(tcell.ColorYellow))
	}
	index := 1
	for i, t := range list {
		attempt, test := 0, "—"
		if a, ok := taskboard.Current(t); ok {
			attempt = a.Number
			test = a.TestState
		}
		for col, text := range []string{t.State, t.Spec.Title, t.Spec.Agent, fmt.Sprint(attempt), test} {
			b.table.SetCell(i+1, col, tview.NewTableCell(tview.Escape(text)))
		}
		if t.ID == selected {
			index = i + 1
		}
	}
	if len(list) > 0 {
		b.table.Select(index, 0)
		b.detail.SetText(taskDetail(list[index-1]))
	} else {
		b.detail.SetText("No tasks. Press N to create one. New attempts start from committed HEAD; uncommitted project changes are preserved.")
	}
	b.footer.SetText("[yellow]N[-] new  [yellow]R[-] run/retry  [yellow]S[-] stop  [yellow]C[-] connect  [yellow]T[-] test  [yellow]V[-] review  [yellow]G[-] sync  [yellow]Tab[-] details  [yellow]Q/Esc[-] back")
}
func (b *taskBoardPage) home() { b.pages.SwitchToPage("board"); b.app.SetFocus(b.table); b.load() }
func (b *taskBoardPage) work(fn func(context.Context) error) {
	b.workResult(func(ctx context.Context) (func(), error) { return nil, fn(ctx) })
}
func (b *taskBoardPage) workResult(fn func(context.Context) (func(), error)) {
	if b.busy {
		return
	}
	b.busy = true
	b.footer.SetText("Working…")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		next, err := fn(ctx)
		b.app.QueueUpdateDraw(func() {
			b.busy = false
			b.load()
			if err != nil {
				b.footer.SetText("[red]" + tview.Escape(err.Error()) + "[-]")
				previous, primitive := b.pages.GetFrontPage()
				modal := tview.NewModal().SetText(tview.Escape(err.Error())).AddButtons([]string{"Close"}).SetDoneFunc(func(_ int, _ string) {
					b.pages.RemovePage("error")
					b.pages.SwitchToPage(previous)
					b.app.SetFocus(primitive)
				})
				b.pages.AddAndSwitchToPage("error", modal, true)
				b.app.SetFocus(modal)
			} else if next != nil {
				next()
			}
		})
	}()
}
func (b *taskBoardPage) confirm(text string, yes func()) {
	modal := confirmationModal(text, func() { b.pages.RemovePage("confirm"); yes() }, func() { b.pages.RemovePage("confirm"); b.app.SetFocus(b.table) })
	b.pages.AddAndSwitchToPage("confirm", modal, true)
	b.app.SetFocus(modal)
}
func (b *taskBoardPage) create() {
	form := tview.NewForm()
	project, _ := os.Getwd()
	title, prompt, agent, image, policy, test := "", "", "codex", "", "", ""
	offline := false
	form.AddInputField("Title", title, 48, nil, func(v string) { title = v }).AddInputField("Project root", project, 48, nil, func(v string) { project = v }).AddDropDown("Agent", []string{"codex", "claude", "shell"}, 0, func(v string, _ int) { agent = v }).AddTextArea("Instructions", prompt, 50, 5, 32768, func(v string) { prompt = v }).AddInputField("Image (optional)", image, 48, nil, func(v string) { image = v }).AddInputField("Host policy (optional)", policy, 32, nil, func(v string) { policy = v }).AddInputField("Test command", test, 48, nil, func(v string) { test = v }).AddCheckbox("No general internet", false, func(v bool) { offline = v })
	form.AddButton("Queue", func() {
		spec := taskboard.Spec{Title: title, Project: project, Prompt: prompt, Agent: agent, Image: image, Policy: policy, NoInternet: offline, TestCommand: test}
		b.workResult(func(ctx context.Context) (func(), error) { _, err := b.service.Create(ctx, spec); return b.home, err })
	}).AddButton("Cancel", b.home)
	form.SetBorder(true).SetTitle(" New task · committed HEAD · Esc cancels ")
	form.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			b.home()
			return nil
		}
		return e
	})
	b.pages.AddAndSwitchToPage("create", form, true)
	b.app.SetFocus(form)
}
func (b *taskBoardPage) connect(t taskboard.Task) {
	a, ok := taskboard.Current(t)
	if !ok {
		b.footer.SetText("Run the task first.")
		return
	}
	runtime, ok := b.service.Runtime.(*taskboard.CLIRuntime)
	if !ok {
		b.footer.SetText("Connect is unavailable for this runtime.")
		return
	}
	var err error
	b.app.Suspend(func() {
		mgr, e := sandbox.NewManager(runtime.Config)
		if e != nil {
			err = e
			return
		}
		defer func() { _ = mgr.Close() }()
		_, err = instanceux.Connect(context.Background(), mgr, a.Worker, instanceux.ConnectOptions{Shell: "auto", WorkingDir: "/workspace", Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr})
	})
	if err != nil {
		b.footer.SetText(tview.Escape(err.Error()))
	}
}

type reviewRow struct{ path, hunk string }

func toggleReviewSelection(selection taskboard.Selection, row reviewRow, hunks []string) {
	ids, ok := selection[row.path]
	if row.hunk == "" {
		if ok {
			delete(selection, row.path)
		} else {
			selection[row.path] = nil
		}
		return
	}
	if ok && len(ids) == 0 {
		var remaining []string
		for _, id := range hunks {
			if id != row.hunk {
				remaining = append(remaining, id)
			}
		}
		if len(remaining) == 0 {
			delete(selection, row.path)
		} else {
			selection[row.path] = remaining
		}
		return
	}
	for i, id := range ids {
		if id == row.hunk {
			ids = append(ids[:i], ids[i+1:]...)
			if len(ids) == 0 {
				delete(selection, row.path)
			} else {
				selection[row.path] = ids
			}
			return
		}
	}
	selection[row.path] = append(ids, row.hunk)
}
func (b *taskBoardPage) review(t taskboard.Task, r taskboard.Review) {
	selection := taskboard.Selection{}
	var rows []reviewRow
	list := tview.NewList().ShowSecondaryText(false)
	diff := tview.NewTextView().SetDynamicColors(false).SetWrap(false).SetScrollable(true)
	list.SetBorder(true).SetTitle(" Changes · Space selects ")
	diff.SetBorder(true).SetTitle(" Frozen diff ")
	help := tview.NewTextView().SetDynamicColors(true)
	testStatus := "not verified"
	if a, ok := taskboard.Current(t); ok && a.TestTree != "" {
		testStatus = a.TestState + " (full task snapshot)"
		if a.TestTree != r.ID {
			testStatus = "stale: workspace differs from tested snapshot"
		}
	}
	help.SetText(tview.Escape("Test: "+testStatus+fmt.Sprintf(" | excluded credential paths: %d", len(r.Excluded))) + "\n[yellow]Space[-] toggle  [yellow]A[-] all  [yellow]P[-] preview adoption  [yellow]Tab[-] diff  [yellow]Esc[-] back")
	render := func() {
		index := list.GetCurrentItem()
		list.Clear()
		rows = nil
		for _, c := range r.Changes {
			_, ok := selection[c.Path]
			mark := "[ ] "
			if ok {
				mark = "[x] "
			}
			list.AddItem(tview.Escape(mark+c.Status+" "+c.Path), "", 0, nil)
			rows = append(rows, reviewRow{path: c.Path})
			if !c.WholeOnly {
				for _, h := range c.Hunks {
					chosen := ok && len(selection[c.Path]) == 0
					for _, id := range selection[c.Path] {
						chosen = chosen || id == h.ID
					}
					mark = "  [ ] "
					if chosen {
						mark = "  [x] "
					}
					list.AddItem(tview.Escape(mark+h.Header), "", 0, nil)
					rows = append(rows, reviewRow{path: c.Path, hunk: h.ID})
				}
			}
		}
		if len(rows) > 0 {
			if index >= len(rows) {
				index = len(rows) - 1
			}
			list.SetCurrentItem(index)
		}
	}
	list.SetChangedFunc(func(i int, _ string, _ string, _ rune) {
		if i >= 0 && i < len(rows) {
			for _, c := range r.Changes {
				if c.Path == rows[i].path {
					diff.SetText(c.Patch)
					diff.ScrollToBeginning()
					break
				}
			}
		}
	})
	list.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		switch {
		case e.Key() == tcell.KeyEscape:
			b.home()
			return nil
		case e.Key() == tcell.KeyTab:
			b.app.SetFocus(diff)
			return nil
		case e.Rune() == ' ':
			i := list.GetCurrentItem()
			if i >= 0 && i < len(rows) {
				var hunks []string
				for _, c := range r.Changes {
					if c.Path == rows[i].path {
						for _, h := range c.Hunks {
							hunks = append(hunks, h.ID)
						}
					}
				}
				toggleReviewSelection(selection, rows[i], hunks)
				render()
			}
			return nil
		case e.Rune() == 'a' || e.Rune() == 'A':
			for _, c := range r.Changes {
				selection[c.Path] = nil
			}
			render()
			return nil
		case e.Rune() == 'p' || e.Rune() == 'P':
			b.plan(t, r, selection)
			return nil
		}
		return e
	})
	diff.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape || e.Key() == tcell.KeyTab {
			b.app.SetFocus(list)
			return nil
		}
		return e
	})
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(tview.NewFlex().AddItem(list, 0, 2, true).AddItem(diff, 0, 3, false), 0, 1, true).AddItem(help, 3, 0, false)
	render()
	if len(r.Changes) > 0 {
		diff.SetText(r.Changes[0].Patch)
	}
	b.pages.AddAndSwitchToPage("review", body, true)
	b.app.SetFocus(list)
}
func (b *taskBoardPage) plan(t taskboard.Task, r taskboard.Review, selection taskboard.Selection) {
	patch, err := taskboard.SelectedPatch(r, selection)
	if err != nil {
		b.footer.SetText(tview.Escape(err.Error()))
		return
	}
	// Copy selection so preview cannot be changed by later key presses.
	frozen := taskboard.Selection{}
	for p, ids := range selection {
		frozen[p] = append([]string(nil), ids...)
	}
	target := t.Branch
	if target == "" {
		target = "main"
	}
	result := "oaiso/result-" + t.ID[:12]
	form := tview.NewForm().AddInputField("Base branch", target, 45, nil, func(v string) { target = v }).AddInputField("New result branch", result, 45, nil, func(v string) { result = v })
	back := func() { b.pages.SwitchToPage("review") }
	form.AddButton("Preview", func() {
		b.workResult(func(ctx context.Context) (func(), error) {
			a, e := b.service.Plan(ctx, t.ID, r.ID, target, result, frozen)
			return func() {
				preview := fmt.Sprintf("Create branch %s from %s (%s)?\nCommit: %s\nFiles: %d | patch: %d bytes\nCurrent checkout and index are preserved.\nNo push is performed.", a.ResultBranch, a.TargetBranch, a.Target, a.Commit, len(frozen), len(patch))
				b.confirm(preview, func() {
					b.home()
					b.work(func(ctx context.Context) error { _, e := b.service.Accept(ctx, t.ID, a); return e })
				})
			}, e
		})
	}).AddButton("Cancel", back)
	form.SetBorder(true).SetTitle(" Adopt selected changes · Esc cancels ")
	form.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			back()
			return nil
		}
		return e
	})
	b.pages.AddAndSwitchToPage("plan", form, true)
	b.app.SetFocus(form)
}

// Poll only unfinished work. Lifecycle operations remain serialized by Service.
func (b *taskBoardPage) start(done <-chan struct{}) {
	closed := make(chan struct{})
	previousBack := b.back
	b.back = func() {
		select {
		case <-closed:
		default:
			close(closed)
		}
		previousBack()
	}
	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-closed:
				return
			case <-ticker.C:
				b.app.QueueUpdateDraw(func() {
					page, _ := b.pages.GetFrontPage()
					if b.busy || page != "board" {
						return
					}
					var ids []string
					for _, t := range b.tasks {
						a, ok := taskboard.Current(t)
						if ok && (t.State == "running" || t.State == "launching" || t.State == "unknown" || a.TestState == "running" || a.TestState == "launching" || a.TestState == "unknown") {
							ids = append(ids, t.ID)
						}
					}
					if len(ids) == 0 {
						return
					}
					b.work(func(ctx context.Context) error {
						for _, id := range ids {
							if _, err := b.service.Sync(ctx, id); err != nil {
								return err
							}
						}
						return nil
					})
				})
			}
		}
	}()
}
