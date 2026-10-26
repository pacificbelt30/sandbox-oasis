package instanceux

import (
	"fmt"
	"strings"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

type Model struct {
	All        []sandbox.Worker
	Visible    []sandbox.Worker
	Query      Query
	Sort       SortKey
	SelectedID string
	Generation uint64
}

func NewModel() *Model { return &Model{Sort: SortName} }

func (m *Model) Replace(workers []sandbox.Worker) {
	m.All = append(m.All[:0], workers...)
	m.Generation++
	m.rebuild()
}

func (m *Model) SetQuery(raw string) error {
	query, err := ParseQuery(raw)
	if err != nil {
		return err
	}
	m.Query = query
	m.rebuild()
	return nil
}

func (m *Model) SetSort(key SortKey) error {
	if _, err := ParseSortKey(string(key)); err != nil {
		return err
	}
	m.Sort = key
	m.rebuild()
	return nil
}

func (m *Model) CycleSort() SortKey {
	keys := SortKeys()
	for index, key := range keys {
		if m.Sort == key {
			m.Sort = keys[(index+1)%len(keys)]
			m.rebuild()
			return m.Sort
		}
	}
	m.Sort = keys[0]
	m.rebuild()
	return m.Sort
}

func (m *Model) SelectIndex(index int) {
	if index < 0 || index >= len(m.Visible) {
		return
	}
	m.SelectedID = m.Visible[index].ID
}

func (m *Model) Selected() (sandbox.Worker, bool) {
	for _, worker := range m.Visible {
		if worker.ID == m.SelectedID {
			return worker, true
		}
	}
	return sandbox.Worker{}, false
}

func (m *Model) Index() int {
	for index, worker := range m.Visible {
		if worker.ID == m.SelectedID {
			return index
		}
	}
	return -1
}

func (m *Model) rebuild() {
	previous := m.SelectedID
	m.Visible = Sort(Filter(m.All, m.Query), m.Sort)
	for _, worker := range m.Visible {
		if worker.ID == previous {
			return
		}
	}
	if len(m.Visible) == 0 {
		m.SelectedID = ""
	} else {
		m.SelectedID = m.Visible[0].ID
	}
}

type Action string

const (
	ActionConnect Action = "connect"
	ActionExec    Action = "exec"
	ActionLogs    Action = "logs"
	ActionDenials Action = "denials"
	ActionStart   Action = "start"
	ActionStop    Action = "stop"
	ActionRestart Action = "restart"
	ActionDelete  Action = "delete"
)

type ActionState struct {
	Action       Action
	Enabled      bool
	Reason       string
	Destructive  bool
	Confirmation string
}

func Actions(worker sandbox.Worker) []ActionState {
	running := strings.EqualFold(worker.Status, "running")
	startable := !running && worker.Status != "removing" && worker.Status != "dead"
	return []ActionState{
		{Action: ActionConnect, Enabled: running, Reason: disabledReason(running, "worker is not running")},
		{Action: ActionExec, Enabled: running, Reason: disabledReason(running, "worker is not running")},
		{Action: ActionLogs, Enabled: true},
		{Action: ActionDenials, Enabled: true},
		{Action: ActionStart, Enabled: startable, Reason: disabledReason(startable, "worker is already running or cannot start")},
		{Action: ActionStop, Enabled: running, Reason: disabledReason(running, "worker is not running")},
		{Action: ActionRestart, Enabled: true, Destructive: running, Confirmation: fmt.Sprintf("Restart %s?", worker.Name)},
		{Action: ActionDelete, Enabled: true, Destructive: true, Confirmation: fmt.Sprintf("Delete %s and its managed network?", worker.Name)},
	}
}

func disabledReason(enabled bool, reason string) string {
	if enabled {
		return ""
	}
	return reason
}

func Detail(worker sandbox.Worker, now time.Time) []string {
	lines := []string{
		"[::b]" + Escape(worker.Name) + "[::-]",
		"",
		"Status     " + statusGlyph(worker.Status) + " " + Escape(orDash(worker.Status)),
		"Policy     " + Escape(orDash(worker.AgentPolicy)),
		"Branch     " + Escape(orDash(worker.Branch)),
		"Image      " + Escape(orDash(worker.Image)),
		"ID         " + Escape(shortID(worker.ID)),
		"Started    " + Escape(formatTime(worker.StartedAt)),
		"Uptime     " + Escape(formatAge(worker.StartedAt, now)),
		"Finished   " + Escape(formatTime(worker.FinishedAt)),
		"",
		"[::b]Task[::-]",
		Escape(orInteractive(worker.Task)),
		"",
		"[::b]Available actions[::-]",
	}
	for _, action := range Actions(worker) {
		mark := "[green]●[-]"
		if !action.Enabled {
			mark = "[gray]○[-]"
		}
		line := fmt.Sprintf("%s %-9s", mark, action.Action)
		if action.Reason != "" {
			line += " [gray](" + Escape(action.Reason) + ")[-]"
		}
		lines = append(lines, line)
	}
	return lines
}

func Escape(value string) string {
	value = strings.ReplaceAll(value, "[", "[[]")
	return strings.ReplaceAll(value, "\x00", "")
}

func statusGlyph(status string) string {
	switch strings.ToLower(status) {
	case "running":
		return "[green]●[-]"
	case "exited", "stopped":
		return "[gray]■[-]"
	case "paused":
		return "[yellow]Ⅱ[-]"
	case "dead", "error":
		return "[red]×[-]"
	default:
		return "[blue]◆[-]"
	}
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
func orInteractive(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(interactive)"
	}
	return value
}
func shortID(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return value
}
func formatTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "-"
	}
	return value.Local().Format("2006-01-02 15:04:05 MST")
}
func formatAge(value *time.Time, now time.Time) string {
	if value == nil || value.IsZero() {
		return "-"
	}
	d := now.Sub(*value)
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd%02dh", int(d.Hours()/24), int(d.Hours())%24)
}
