package instanceux

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const SnapshotVersion = 1
const MaxSnapshotBytes = 8 << 20

type SnapshotFile struct {
	Version  int      `json:"version"`
	Snapshot Snapshot `json:"snapshot"`
}

type ChangeKind string

const (
	ChangeAdded     ChangeKind = "added"
	ChangeRemoved   ChangeKind = "removed"
	ChangeStarted   ChangeKind = "started"
	ChangeStopped   ChangeKind = "stopped"
	ChangeRestarted ChangeKind = "restarted"
	ChangeUpdated   ChangeKind = "updated"
)

type ValueChange struct {
	Field  string `json:"field"`
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

type WorkerChange struct {
	Kind   ChangeKind    `json:"kind"`
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Before *WorkerRecord `json:"before,omitempty"`
	After  *WorkerRecord `json:"after,omitempty"`
	Values []ValueChange `json:"values,omitempty"`
}

type SnapshotDiff struct {
	Before    time.Time      `json:"before"`
	After     time.Time      `json:"after"`
	Changes   []WorkerChange `json:"changes"`
	Added     int            `json:"added"`
	Removed   int            `json:"removed"`
	Started   int            `json:"started"`
	Stopped   int            `json:"stopped"`
	Restarted int            `json:"restarted"`
	Updated   int            `json:"updated"`
}

func SaveSnapshot(path string, snapshot Snapshot) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("snapshot path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving snapshot path: %w", err)
	}
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating snapshot directory: %w", err)
	}
	data, err := json.MarshalIndent(SnapshotFile{Version: SnapshotVersion, Snapshot: snapshot}, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding instance snapshot: %w", err)
	}
	if len(data) > MaxSnapshotBytes {
		return fmt.Errorf("instance snapshot exceeds %d bytes", MaxSnapshotBytes)
	}
	temporary, err := os.CreateTemp(dir, ".oaiso-snapshot-*")
	if err != nil {
		return fmt.Errorf("creating snapshot staging file: %w", err)
	}
	temporaryName := temporary.Name()
	defer func() { _ = temporary.Close(); _ = os.Remove(temporaryName) }()
	if err := temporary.Chmod(0600); err != nil {
		return fmt.Errorf("protecting snapshot staging file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("writing snapshot: %w", err)
	}
	if _, err := temporary.Write([]byte("\n")); err != nil {
		return fmt.Errorf("writing snapshot terminator: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("syncing snapshot: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("closing snapshot: %w", err)
	}
	if err := os.Rename(temporaryName, abs); err != nil {
		return fmt.Errorf("publishing snapshot: %w", err)
	}
	return nil
}

func LoadSnapshot(path string) (Snapshot, error) {
	file, err := os.Open(path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("opening instance snapshot: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return Snapshot{}, fmt.Errorf("inspecting instance snapshot: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Snapshot{}, fmt.Errorf("instance snapshot is not a regular file")
	}
	if info.Size() > MaxSnapshotBytes {
		return Snapshot{}, fmt.Errorf("instance snapshot exceeds %d bytes", MaxSnapshotBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxSnapshotBytes+1))
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading instance snapshot: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var envelope SnapshotFile
	if err := decoder.Decode(&envelope); err != nil {
		return Snapshot{}, fmt.Errorf("decoding instance snapshot: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Snapshot{}, fmt.Errorf("decoding instance snapshot: trailing data")
	}
	if envelope.Version != SnapshotVersion {
		return Snapshot{}, fmt.Errorf("unsupported instance snapshot version %d", envelope.Version)
	}
	if envelope.Snapshot.GeneratedAt.IsZero() {
		return Snapshot{}, fmt.Errorf("instance snapshot has no generation timestamp")
	}
	if envelope.Snapshot.Workers == nil {
		envelope.Snapshot.Workers = []WorkerRecord{}
	}
	return envelope.Snapshot, nil
}

func CompareSnapshots(before, after Snapshot) SnapshotDiff {
	diff := SnapshotDiff{Before: before.GeneratedAt, After: after.GeneratedAt}
	oldByIdentity := indexRecords(before.Workers)
	newByIdentity := indexRecords(after.Workers)
	identities := map[string]struct{}{}
	for identity := range oldByIdentity {
		identities[identity] = struct{}{}
	}
	for identity := range newByIdentity {
		identities[identity] = struct{}{}
	}
	ordered := make([]string, 0, len(identities))
	for identity := range identities {
		ordered = append(ordered, identity)
	}
	sort.Strings(ordered)
	for _, identity := range ordered {
		oldRecord, oldExists := oldByIdentity[identity]
		newRecord, newExists := newByIdentity[identity]
		switch {
		case !oldExists && newExists:
			value := newRecord
			diff.Changes = append(diff.Changes, WorkerChange{Kind: ChangeAdded, ID: newRecord.ID, Name: newRecord.Name, After: &value})
			diff.Added++
		case oldExists && !newExists:
			value := oldRecord
			diff.Changes = append(diff.Changes, WorkerChange{Kind: ChangeRemoved, ID: oldRecord.ID, Name: oldRecord.Name, Before: &value})
			diff.Removed++
		default:
			values := compareRecord(oldRecord, newRecord)
			if len(values) == 0 {
				continue
			}
			kind := classifyChange(oldRecord, newRecord)
			oldValue, newValue := oldRecord, newRecord
			diff.Changes = append(diff.Changes, WorkerChange{Kind: kind, ID: newRecord.ID, Name: newRecord.Name, Before: &oldValue, After: &newValue, Values: values})
			switch kind {
			case ChangeStarted:
				diff.Started++
			case ChangeStopped:
				diff.Stopped++
			case ChangeRestarted:
				diff.Restarted++
			default:
				diff.Updated++
			}
		}
	}
	return diff
}

func indexRecords(records []WorkerRecord) map[string]WorkerRecord {
	out := make(map[string]WorkerRecord, len(records))
	for _, record := range records {
		identity := record.ID
		if identity == "" {
			identity = "name:" + record.Name
		}
		out[identity] = record
	}
	return out
}

func compareRecord(before, after WorkerRecord) []ValueChange {
	var values []ValueChange
	add := func(field, oldValue, newValue string) {
		if oldValue != newValue {
			values = append(values, ValueChange{Field: field, Before: oldValue, After: newValue})
		}
	}
	add("name", before.Name, after.Name)
	add("status", before.Status, after.Status)
	add("image", before.Image, after.Image)
	add("branch", before.Branch, after.Branch)
	add("task", before.Task, after.Task)
	add("agent_policy", before.AgentPolicy, after.AgentPolicy)
	add("health", before.Health, after.Health)
	add("started_at", timestampString(before.StartedAt), timestampString(after.StartedAt))
	add("finished_at", timestampString(before.FinishedAt), timestampString(after.FinishedAt))
	return values
}

func classifyChange(before, after WorkerRecord) ChangeKind {
	wasRunning, isRunning := before.Status == "running", after.Status == "running"
	if !wasRunning && isRunning {
		return ChangeStarted
	}
	if wasRunning && !isRunning {
		return ChangeStopped
	}
	if wasRunning && isRunning && timestampString(before.StartedAt) != timestampString(after.StartedAt) {
		return ChangeRestarted
	}
	return ChangeUpdated
}

func timestampString(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func WriteDiff(out io.Writer, diff SnapshotDiff, jsonOutput bool) error {
	if jsonOutput {
		encoder := json.NewEncoder(out)
		encoder.SetIndent("", "  ")
		return encoder.Encode(diff)
	}
	if len(diff.Changes) == 0 {
		_, err := fmt.Fprintln(out, "No instance changes.")
		return err
	}
	for _, change := range diff.Changes {
		if _, err := fmt.Fprintf(out, "%s\t%s\t%s", strings.ToUpper(string(change.Kind)), change.Name, shortID(change.ID)); err != nil {
			return err
		}
		if len(change.Values) > 0 {
			parts := make([]string, 0, len(change.Values))
			for _, value := range change.Values {
				parts = append(parts, fmt.Sprintf("%s:%s→%s", value.Field, orDash(value.Before), orDash(value.After)))
			}
			if _, err := fmt.Fprintf(out, "\t%s", strings.Join(parts, ", ")); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(out); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(out, "added=%d removed=%d started=%d stopped=%d restarted=%d updated=%d\n", diff.Added, diff.Removed, diff.Started, diff.Stopped, diff.Restarted, diff.Updated)
	return err
}
