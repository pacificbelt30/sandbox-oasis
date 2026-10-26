package instanceux

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

type Snapshot struct {
	GeneratedAt time.Time       `json:"generated_at"`
	Workers     []WorkerRecord  `json:"workers"`
	Summary     SnapshotSummary `json:"summary"`
}

type WorkerRecord struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Image       string     `json:"image"`
	Branch      string     `json:"branch,omitempty"`
	Task        string     `json:"task,omitempty"`
	AgentPolicy string     `json:"agent_policy,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	AgeSeconds  int64      `json:"age_seconds,omitempty"`
	Health      string     `json:"health"`
	Notices     []string   `json:"notices,omitempty"`
}

type SnapshotSummary struct {
	Total           int            `json:"total"`
	Running         int            `json:"running"`
	Stopped         int            `json:"stopped"`
	Unhealthy       int            `json:"unhealthy"`
	Interactive     int            `json:"interactive"`
	ByStatus        map[string]int `json:"by_status"`
	ByImage         map[string]int `json:"by_image"`
	ByPolicy        map[string]int `json:"by_policy"`
	OldestStartedAt *time.Time     `json:"oldest_started_at,omitempty"`
	NewestStartedAt *time.Time     `json:"newest_started_at,omitempty"`
}

func NewSnapshot(workers []sandbox.Worker, now time.Time) Snapshot {
	if now.IsZero() {
		now = time.Now()
	}
	snapshot := Snapshot{GeneratedAt: now, Workers: make([]WorkerRecord, 0, len(workers))}
	snapshot.Summary.ByStatus = map[string]int{}
	snapshot.Summary.ByImage = map[string]int{}
	snapshot.Summary.ByPolicy = map[string]int{}
	for _, worker := range workers {
		record := Record(worker, now)
		snapshot.Workers = append(snapshot.Workers, record)
		snapshot.Summary.Total++
		snapshot.Summary.ByStatus[orUnknown(record.Status)]++
		snapshot.Summary.ByImage[orUnknown(record.Image)]++
		snapshot.Summary.ByPolicy[orUnknown(record.AgentPolicy)]++
		if record.Status == "running" {
			snapshot.Summary.Running++
		} else {
			snapshot.Summary.Stopped++
		}
		if record.Health != "ok" {
			snapshot.Summary.Unhealthy++
		}
		if strings.TrimSpace(record.Task) == "" {
			snapshot.Summary.Interactive++
		}
		if record.StartedAt != nil {
			if snapshot.Summary.OldestStartedAt == nil || record.StartedAt.Before(*snapshot.Summary.OldestStartedAt) {
				value := *record.StartedAt
				snapshot.Summary.OldestStartedAt = &value
			}
			if snapshot.Summary.NewestStartedAt == nil || record.StartedAt.After(*snapshot.Summary.NewestStartedAt) {
				value := *record.StartedAt
				snapshot.Summary.NewestStartedAt = &value
			}
		}
	}
	return snapshot
}

func Record(worker sandbox.Worker, now time.Time) WorkerRecord {
	record := WorkerRecord{ID: worker.ID, Name: worker.Name, Status: worker.Status, Image: worker.Image, Branch: worker.Branch, Task: worker.Task, AgentPolicy: worker.AgentPolicy, StartedAt: worker.StartedAt, FinishedAt: worker.FinishedAt, Health: "ok"}
	if worker.StartedAt != nil {
		age := now.Sub(*worker.StartedAt)
		if age > 0 {
			record.AgeSeconds = int64(age.Seconds())
		}
	}
	switch strings.ToLower(worker.Status) {
	case "running", "created", "exited", "stopped":
	default:
		record.Health = "attention"
		record.Notices = append(record.Notices, "unusual runtime status")
	}
	if worker.Status == "running" && worker.StartedAt == nil {
		record.Health = "attention"
		record.Notices = append(record.Notices, "running worker has no start timestamp")
	}
	if strings.TrimSpace(worker.Image) == "" {
		record.Health = "attention"
		record.Notices = append(record.Notices, "image metadata is missing")
	}
	if strings.TrimSpace(worker.AgentPolicy) == "" {
		record.Notices = append(record.Notices, "created before agent-policy metadata was recorded")
	}
	return record
}

type Column string

const (
	ColumnName     Column = "name"
	ColumnStatus   Column = "status"
	ColumnUptime   Column = "uptime"
	ColumnPolicy   Column = "policy"
	ColumnBranch   Column = "branch"
	ColumnTask     Column = "task"
	ColumnImage    Column = "image"
	ColumnID       Column = "id"
	ColumnStarted  Column = "started"
	ColumnFinished Column = "finished"
	ColumnHealth   Column = "health"
)

var columnOrder = []Column{ColumnName, ColumnStatus, ColumnUptime, ColumnPolicy, ColumnBranch, ColumnTask, ColumnImage, ColumnID, ColumnStarted, ColumnFinished, ColumnHealth}

func DefaultColumns() []Column {
	return []Column{ColumnName, ColumnStatus, ColumnUptime, ColumnBranch, ColumnTask, ColumnImage, ColumnPolicy}
}
func WideColumns() []Column { return append([]Column(nil), columnOrder...) }

func ParseColumns(raw string) ([]Column, error) {
	if strings.TrimSpace(raw) == "" {
		return DefaultColumns(), nil
	}
	known := map[Column]struct{}{}
	for _, column := range columnOrder {
		known[column] = struct{}{}
	}
	seen := map[Column]struct{}{}
	var columns []Column
	for _, item := range strings.Split(raw, ",") {
		column := Column(strings.ToLower(strings.TrimSpace(item)))
		if _, ok := known[column]; !ok {
			return nil, fmt.Errorf("unknown instance column %q", item)
		}
		if _, duplicate := seen[column]; duplicate {
			continue
		}
		seen[column] = struct{}{}
		columns = append(columns, column)
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("at least one instance column is required")
	}
	return columns, nil
}

type RenderOptions struct {
	Columns    []Column
	NoTruncate bool
	TaskWidth  int
	TimeZone   *time.Location
}

func WriteTable(out io.Writer, snapshot Snapshot, options RenderOptions) error {
	columns := options.Columns
	if len(columns) == 0 {
		columns = DefaultColumns()
	}
	writer := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	for index, column := range columns {
		if index > 0 {
			_, _ = io.WriteString(writer, "\t")
		}
		_, _ = io.WriteString(writer, strings.ToUpper(string(column)))
	}
	_, _ = io.WriteString(writer, "\n")
	for _, record := range snapshot.Workers {
		for index, column := range columns {
			if index > 0 {
				_, _ = io.WriteString(writer, "\t")
			}
			value := tableValue(record, column, snapshot.GeneratedAt, options)
			if column == ColumnStatus {
				value = statusIcon(record.Status) + value
			}
			_, _ = io.WriteString(writer, value)
		}
		_, _ = io.WriteString(writer, "\n")
	}
	return writer.Flush()
}

func WriteCSV(out io.Writer, snapshot Snapshot, options RenderOptions) error {
	columns := options.Columns
	if len(columns) == 0 {
		columns = DefaultColumns()
	}
	writer := csv.NewWriter(out)
	header := make([]string, len(columns))
	for index, column := range columns {
		header[index] = string(column)
	}
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, record := range snapshot.Workers {
		row := make([]string, len(columns))
		for index, column := range columns {
			row[index] = tableValue(record, column, snapshot.GeneratedAt, RenderOptions{Columns: columns, NoTruncate: true, TimeZone: options.TimeZone})
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func WriteJSON(out io.Writer, snapshot Snapshot, pretty bool) error {
	encoder := json.NewEncoder(out)
	if pretty {
		encoder.SetIndent("", "  ")
	}
	return encoder.Encode(snapshot)
}

func tableValue(record WorkerRecord, column Column, now time.Time, options RenderOptions) string {
	location := options.TimeZone
	if location == nil {
		location = time.Local
	}
	width := options.TaskWidth
	if width <= 0 {
		width = 36
	}
	switch column {
	case ColumnName:
		return record.Name
	case ColumnStatus:
		return record.Status
	case ColumnUptime:
		if record.StartedAt == nil {
			return "-"
		}
		return formatAge(record.StartedAt, now)
	case ColumnPolicy:
		return orDash(record.AgentPolicy)
	case ColumnBranch:
		return orDash(record.Branch)
	case ColumnTask:
		value := orInteractive(record.Task)
		if options.NoTruncate {
			return value
		}
		return truncateRunes(value, width)
	case ColumnImage:
		return record.Image
	case ColumnID:
		return record.ID
	case ColumnStarted:
		return renderTimestamp(record.StartedAt, location)
	case ColumnFinished:
		return renderTimestamp(record.FinishedAt, location)
	case ColumnHealth:
		return record.Health
	default:
		return ""
	}
}

func renderTimestamp(value *time.Time, location *time.Location) string {
	if value == nil || value.IsZero() {
		return "-"
	}
	return value.In(location).Format(time.RFC3339)
}

func truncateRunes(value string, width int) string {
	runes := []rune(value)
	if width < 4 || len(runes) <= width {
		return value
	}
	return string(runes[:width-3]) + "..."
}

func orUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

func statusIcon(status string) string {
	switch strings.ToLower(status) {
	case "running":
		return "✅ "
	case "stopped", "exited":
		return "⏹ "
	case "dead", "error":
		return "❌ "
	case "paused":
		return "⏸ "
	default:
		return "  "
	}
}

func (s SnapshotSummary) Lines() []string {
	lines := []string{
		fmt.Sprintf("total=%d running=%d stopped=%d attention=%d interactive=%d", s.Total, s.Running, s.Stopped, s.Unhealthy, s.Interactive),
		"status: " + formatCounts(s.ByStatus),
		"policy: " + formatCounts(s.ByPolicy),
		"image: " + formatCounts(s.ByImage),
	}
	return lines
}

func formatCounts(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+strconv.Itoa(counts[key]))
	}
	return strings.Join(values, ", ")
}
