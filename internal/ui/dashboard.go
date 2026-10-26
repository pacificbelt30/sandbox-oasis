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
	header *tview.TextView
	table  *tview.Table
	detail *tview.TextView
	footer *tview.TextView
	filter *tview.InputField
}

func newDashboardViews() dashboardViews {
	header := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	table := tview.NewTable().SetBorders(false).SetSelectable(true, false).SetFixed(1, 0)
	setupTableHeader(table)
	detail := tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWrap(true)
	detail.SetTitle(" Instance ").SetBorder(true)
	footer := tview.NewTextView().SetDynamicColors(true)
	footer.SetText(dashboardHelp(false))
	filter := tview.NewInputField().SetLabel(" Filter: ").SetFieldWidth(0)
	filter.SetPlaceholder(`name, task, or field:value; prefix ! to exclude`)
	return dashboardViews{header: header, table: table, detail: detail, footer: footer, filter: filter}
}

func dashboardHelp(compact bool) string {
	if compact {
		return "[yellow]Enter[-] logs  [yellow]C[-] connect  [yellow]/[-] filter  [yellow]?[-] help  [yellow]Q[-] quit"
	}
	return "[yellow][↑↓][-] Select  [yellow][Enter][-] Logs  [yellow][C][-] Connect  [yellow][X][-] Exec  [yellow][/][-] Filter  [yellow][O][-] Sort  [yellow][V][-] Activity  [yellow][S/R/D][-] Manage  [yellow][?][-] Help  [yellow][Q][-] Quit"
}

func dashboardBody(views dashboardViews) *tview.Flex {
	body := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(views.table, 0, 3, true).
		AddItem(views.detail, 38, 1, false)
	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(views.header, 1, 0, false).
		AddItem(views.filter, 1, 0, false).
		AddItem(body, 0, 1, true).
		AddItem(views.footer, 1, 0, false)
}

func renderDashboard(views dashboardViews, model *instanceux.Model, now time.Time) {
	selected := model.SelectedID
	for row := views.table.GetRowCount() - 1; row > 0; row-- {
		views.table.RemoveRow(row)
	}
	for index, worker := range model.Visible {
		uptime := "-"
		if worker.StartedAt != nil {
			uptime = formatDuration(now.Sub(*worker.StartedAt))
		}
		views.table.SetCell(index+1, 0, tview.NewTableCell(worker.Name).SetExpansion(1))
		views.table.SetCell(index+1, 1, tview.NewTableCell(orDash(worker.Branch)).SetExpansion(1))
		views.table.SetCell(index+1, 2, tview.NewTableCell(statusIcon(worker.Status)+worker.Status).SetExpansion(1))
		views.table.SetCell(index+1, 3, tview.NewTableCell(uptime).SetExpansion(0))
		views.table.SetCell(index+1, 4, tview.NewTableCell(orDash(worker.AgentPolicy)).SetExpansion(1))
		views.table.SetCell(index+1, 5, tview.NewTableCell(truncate(interactiveTask(worker.Task), 34)).SetExpansion(2))
		if worker.ID == selected {
			views.table.Select(index+1, 0)
		}
	}
	if worker, ok := model.Selected(); ok {
		renderDetail(views.detail, worker, now)
	} else {
		views.detail.SetText("[gray]No matching instance[-]")
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
	view.ScrollToBeginning()
}

func setupTableHeader(table *tview.Table) {
	headers := []string{"NAME", "BRANCH", "STATUS", "UPTIME", "POLICY", "TASK"}
	for index, header := range headers {
		table.SetCell(0, index, tview.NewTableCell("[::b]"+header).
			SetSelectable(false).
			SetTextColor(tcell.ColorYellow))
	}
}

func helpModal(done func()) *tview.Modal {
	text := `Instance dashboard

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
	form := tview.NewForm().AddInputField("Command", "", 64, nil, func(value string) { command = value })
	form.AddButton("Run", func() {
		if strings.TrimSpace(command) != "" {
			run(command)
		}
	})
	form.AddButton("Cancel", cancel)
	form.SetTitle(" Exec in " + worker.Name + " ").SetBorder(true)
	return form
}

func centered(primitive tview.Primitive, width, height int) tview.Primitive {
	return tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexColumn).
			AddItem(nil, 0, 1, false).
			AddItem(primitive, width, 0, true).
			AddItem(nil, 0, 1, false), height, 0, true).
		AddItem(nil, 0, 1, false)
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
