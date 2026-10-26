package instanceux

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

type EventKind string

const (
	EventDiscovered EventKind = "discovered"
	EventRemoved    EventKind = "removed"
	EventStatus     EventKind = "status"
	EventRestarted  EventKind = "restarted"
	EventMetadata   EventKind = "metadata"
)

type Event struct {
	Sequence   uint64    `json:"sequence"`
	Time       time.Time `json:"time"`
	Kind       EventKind `json:"kind"`
	WorkerID   string    `json:"worker_id"`
	WorkerName string    `json:"worker_name"`
	Before     string    `json:"before,omitempty"`
	After      string    `json:"after,omitempty"`
	Fields     []string  `json:"fields,omitempty"`
}

type Tracker struct {
	mu          sync.RWMutex
	initialized bool
	sequence    uint64
	limit       int
	workers     map[string]sandbox.Worker
	events      []Event
}

func NewTracker(limit int) *Tracker {
	if limit <= 0 {
		limit = 500
	}
	return &Tracker{limit: limit, workers: map[string]sandbox.Worker{}}
}

func (t *Tracker) Observe(workers []sandbox.Worker, observedAt time.Time) []Event {
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	current := make(map[string]sandbox.Worker, len(workers))
	for _, worker := range workers {
		current[workerIdentity(worker)] = worker
	}
	if !t.initialized {
		t.initialized = true
		t.workers = current
		return nil
	}
	identities := make([]string, 0, len(t.workers)+len(current))
	seen := map[string]struct{}{}
	for identity := range t.workers {
		identities = append(identities, identity)
		seen[identity] = struct{}{}
	}
	for identity := range current {
		if _, ok := seen[identity]; !ok {
			identities = append(identities, identity)
		}
	}
	sort.Strings(identities)
	var observed []Event
	for _, identity := range identities {
		before, hadBefore := t.workers[identity]
		after, hasAfter := current[identity]
		var event *Event
		switch {
		case !hadBefore && hasAfter:
			event = &Event{Time: observedAt, Kind: EventDiscovered, WorkerID: after.ID, WorkerName: after.Name, After: after.Status}
		case hadBefore && !hasAfter:
			event = &Event{Time: observedAt, Kind: EventRemoved, WorkerID: before.ID, WorkerName: before.Name, Before: before.Status}
		case before.Status != after.Status:
			event = &Event{Time: observedAt, Kind: EventStatus, WorkerID: after.ID, WorkerName: after.Name, Before: before.Status, After: after.Status}
		case differentTime(before.StartedAt, after.StartedAt):
			event = &Event{Time: observedAt, Kind: EventRestarted, WorkerID: after.ID, WorkerName: after.Name, Before: timestampString(before.StartedAt), After: timestampString(after.StartedAt)}
		default:
			fields := metadataChanges(before, after)
			if len(fields) > 0 {
				event = &Event{Time: observedAt, Kind: EventMetadata, WorkerID: after.ID, WorkerName: after.Name, Fields: fields}
			}
		}
		if event != nil {
			t.sequence++
			event.Sequence = t.sequence
			observed = append(observed, *event)
			t.events = append(t.events, *event)
		}
	}
	t.workers = current
	if overflow := len(t.events) - t.limit; overflow > 0 {
		copy(t.events, t.events[overflow:])
		t.events = t.events[:t.limit]
	}
	return observed
}

func (t *Tracker) Events() []Event {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return append([]Event(nil), t.events...)
}

func (t *Tracker) ForWorker(reference string) []Event {
	reference = strings.ToLower(strings.TrimSpace(reference))
	t.mu.RLock()
	defer t.mu.RUnlock()
	var events []Event
	for _, event := range t.events {
		if strings.HasPrefix(strings.ToLower(event.WorkerID), reference) || strings.EqualFold(event.WorkerName, reference) {
			events = append(events, event)
		}
	}
	return events
}

func (t *Tracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = nil
}

func workerIdentity(worker sandbox.Worker) string {
	if worker.ID != "" {
		return worker.ID
	}
	return "name:" + worker.Name
}

func differentTime(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left != right
	}
	return !left.Equal(*right)
}

func metadataChanges(before, after sandbox.Worker) []string {
	var fields []string
	if before.Name != after.Name {
		fields = append(fields, "name")
	}
	if before.Image != after.Image {
		fields = append(fields, "image")
	}
	if before.Branch != after.Branch {
		fields = append(fields, "branch")
	}
	if before.Task != after.Task {
		fields = append(fields, "task")
	}
	if before.AgentPolicy != after.AgentPolicy {
		fields = append(fields, "policy")
	}
	if differentTime(before.FinishedAt, after.FinishedAt) {
		fields = append(fields, "finished")
	}
	return fields
}

func FormatEvent(event Event) string {
	prefix := fmt.Sprintf("%s  %-10s  %-20s", event.Time.Local().Format("15:04:05"), strings.ToUpper(string(event.Kind)), event.WorkerName)
	switch event.Kind {
	case EventDiscovered:
		return prefix + "  status=" + orDash(event.After)
	case EventRemoved:
		return prefix + "  last_status=" + orDash(event.Before)
	case EventStatus:
		return prefix + "  " + orDash(event.Before) + " → " + orDash(event.After)
	case EventRestarted:
		return prefix + "  start time changed"
	case EventMetadata:
		return prefix + "  changed=" + strings.Join(event.Fields, ",")
	default:
		return prefix
	}
}
