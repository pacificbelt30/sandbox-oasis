package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/instanceux"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/rivo/tview"
)

type dashboardViews struct {
	header  *tview.TextView
	table   *tview.Table
	detail  *tview.TextView
	footer  *tview.TextView
	filter  *tview.InputField
	actions *tview.Button
	layout  *dashboardLayout
}

func newDashboardViews() dashboardViews {
	header := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	table := tview.NewTable().SetBorders(false).SetSelectable(true, false).SetFixed(1, 0)
	setupTableHeader(table, false)
	detail := tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWrap(true)
	detail.SetTitle(" Instance ").SetBorder(true)
	footer := tview.NewTextView().SetDynamicColors(true)
	footer.SetText(dashboardHelp(false))
	filter := tview.NewInputField().SetLabel(" Filter: ").SetFieldWidth(0)
	filter.SetPlaceholder(`name, task, or field:value; prefix ! to exclude`)
	return dashboardViews{header: header, table: table, detail: detail, footer: footer, filter: filter, actions: tview.NewButton("Actions F2"), layout: &dashboardLayout{}}
}

func dashboardHelp(_ bool) string {
	return "[yellow]N[-] New  [yellow]F[-] Files  [yellow]U[-] Preview  [yellow]I[-] Info  [yellow]/[-] Filter\n[yellow]Enter[-] Logs  [yellow]C[-] Connect  [yellow]?[-] Help  [yellow]Q[-] Quit"
}

type dashboardLayout struct {
	body     *tview.Flex
	narrow   bool
	width    int
	detailID string
}

// The instance list gets the full width on smaller terminals. Detailed fields
// remain available in the wide layout; actions never depend on visible columns.
func resizeDashboard(v dashboardViews, width int) bool {
	narrow := width < 110
	if narrow == v.layout.narrow && width == v.layout.width {
		return false
	}
	v.layout.narrow = narrow
	v.layout.width = width
	v.layout.body.RemoveItem(v.detail)
	if !narrow {
		v.layout.body.AddItem(v.detail, 38, 0, false)
	}
	return true
}

func dashboardBody(views dashboardViews) *tview.Flex {
	body := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(views.table, 0, 3, true).
		AddItem(views.detail, 38, 1, false)
	views.layout.body = body
	controls := tview.NewFlex().AddItem(views.actions, 14, 0, false).AddItem(views.footer, 0, 1, false)
	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(views.header, 1, 0, false).
		AddItem(views.filter, 1, 0, false).
		AddItem(body, 0, 1, true).
		AddItem(controls, 2, 0, false)
}

func renderDashboard(views dashboardViews, model *instanceux.Model, now time.Time) {
	selected := model.SelectedID
	views.table.Clear()
	setupTableHeader(views.table, views.layout.narrow)
	for index, worker := range model.Visible {
		uptime := "-"
		if worker.StartedAt != nil {
			uptime = formatDuration(now.Sub(*worker.StartedAt))
		}
		values := []string{worker.Name, orDash(worker.Branch), statusIcon(worker.Status) + worker.Status, uptime, orDash(worker.AgentPolicy), truncate(interactiveTask(worker.Task), 34)}
		if views.layout.narrow {
			values = []string{worker.Name, statusIcon(worker.Status) + worker.Status, uptime}
		}
		for col, text := range values {
			cell := tview.NewTableCell(tview.Escape(text)).SetExpansion(1)
			if views.layout.narrow {
				limit := 14
				if col == 0 {
					limit = max(12, views.layout.width/2)
				}
				cell.SetMaxWidth(limit)
			} else {
				cell.SetMaxWidth([]int{24, 12, 14, 10, 18, 34}[col])
			}
			views.table.SetCell(index+1, col, cell)
		}
		if worker.ID == selected {
			views.table.Select(index+1, 0)
		}
	}
	if worker, ok := model.Selected(); ok {
		renderSelectedDetail(views, worker, now)
	} else {
		views.layout.detailID = ""
		views.detail.ScrollToBeginning()
		views.detail.SetText("[gray]No matching instance. Use / to edit the filter or Esc to clear it.[-]")
		if len(model.All) == 0 {
			views.detail.SetText("Create your first sandbox with N or Actions F2.\n\nAfter creation, select it and use C to connect, F for files, or U for a Web preview.")
		}
	}
	running := 0
	for _, worker := range model.All {
		if worker.Status == "running" {
			running++
		}
	}
	filter := ""
	if strings.TrimSpace(model.Query.Raw) != "" {
		filter = fmt.Sprintf("  filter=%q", model.Query.Raw)
	}
	views.header.SetText(fmt.Sprintf("[green]御AgentISOlation (Oaiso)[-]  %d running / %d total  %d shown  sort=%s%s", running, len(model.All), len(model.Visible), model.Sort, tview.Escape(filter)))
}

func renderDetail(view *tview.TextView, worker sandbox.Worker, now time.Time) {
	view.SetText(strings.Join(instanceux.Detail(worker, now), "\n"))
}

func renderSelectedDetail(views dashboardViews, worker sandbox.Worker, now time.Time) {
	if views.layout.detailID != worker.ID {
		views.detail.ScrollToBeginning()
		views.layout.detailID = worker.ID
	}
	renderDetail(views.detail, worker, now)
}

func setupTableHeader(table *tview.Table, compact bool) {
	headers := []string{"NAME", "BRANCH", "STATUS", "UPTIME", "POLICY", "TASK"}
	if compact {
		headers = []string{"NAME", "STATUS", "UPTIME"}
	}
	for index, header := range headers {
		table.SetCell(0, index, tview.NewTableCell("[::b]"+header).
			SetSelectable(false).
			SetTextColor(tcell.ColorYellow))
	}
}

func helpModal(done func()) *tview.Modal {
	text := `Instance dashboard

F2 / Space   choose an action using arrows and Enter; Esc cancels
Tab     focus the Actions button; Tab again returns to the list
Esc     clear the instance filter
N       create a general sandbox (AI routes disabled by default)
M       managed workspaces: reservations, reuse and deletion
Z       recovery points: inspect and restore as a NEW VM
K       save selected stopped VM (filtered rootfs, optional workspace)
F       browse, view and transfer workspace files
W       guest Git changes and selected artifact export
L       lifetime, host quota and selected-VM supervision
J       background jobs: start, logs and stop
U       manage authenticated localhost Web previews
I       inspect full instance details, including on a narrow screen
Enter   view recent logs
C       open a new interactive login shell; exit returns here
X       run a command and show its output
/       filter by text or name:/status:/branch:/task:/image:/policy:
O       cycle name, status, age, branch, and policy sorting
V       show instance status and metadata changes seen while the TUI is open
S       stop the selected running worker
R       start a stopped worker
D       delete after confirmation
E       show recorded egress denials
A       stop every running worker after confirmation
P / B   proxy / broker settings
G       host agent policies: clone, edit, review, and select a default
T       task board: queue, execute, test, review and adopt results
Q       quit; Escape closes a panel

Existing agent processes continue while a connected shell is open.`
	modal := tview.NewModal().SetText(text).AddButtons([]string{"Close"})
	modal.SetTitle(" Help ")
	modal.SetDoneFunc(func(_ int, _ string) { done() })
	return modal
}

func confirmationModal(question string, confirm func(), cancel func()) *tview.Modal {
	modal := tview.NewModal().SetText(question).AddButtons([]string{"Cancel", "Confirm"})
	modal.SetDoneFunc(func(index int, _ string) {
		if index == 1 {
			confirm()
		} else {
			cancel()
		}
	})
	return modal
}

func commandForm(worker sandbox.Worker, run func(string), cancel func()) *tview.Form {
	command := ""
	input := tview.NewInputField().SetLabel("Command").SetFieldWidth(64).SetChangedFunc(func(value string) { command = value })
	form := tview.NewForm().AddFormItem(input)
	form.AddButton("Run", func() {
		if strings.TrimSpace(command) != "" {
			run(command)
		}
	})
	form.AddButton("Cancel", cancel)
	form.SetCancelFunc(cancel)
	form.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			cancel()
			return nil
		}
		if event.Key() == tcell.KeyTab && input.HasFocus() {
			input.Autocomplete()
		}
		return event
	})
	setCommandCompletions(form, instanceux.ShellBuiltins())
	form.SetTitle(" Exec in " + tview.Escape(worker.Name) + " · Esc cancels ").SetBorder(true)
	return form
}

func centered(primitive tview.Primitive, width, height int) tview.Primitive {
	row := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(nil, 0, 1, false).
		AddItem(primitive, width, 0, true).
		AddItem(nil, 0, 1, false)
	column := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(row, height, 0, true).
		AddItem(nil, 0, 1, false)
	column.SetDrawFunc(func(_ tcell.Screen, x, y, availableWidth, availableHeight int) (int, int, int, int) {
		row.ResizeItem(primitive, min(width, availableWidth), 0)
		column.ResizeItem(row, min(height, availableHeight), 0)
		return x, y, availableWidth, availableHeight
	})
	return column
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
func interactiveTask(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(interactive)"
	}
	return value
}
