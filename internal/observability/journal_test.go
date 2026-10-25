package observability

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/diagnostics"
	"github.com/pacificbelt30/codex-dock/internal/launchtiming"
	"github.com/pacificbelt30/codex-dock/internal/policyevents"
)

func TestDiagnosticRecordsCannotCopySecretsFromReportsEventsOrPhases(t *testing.T) {
	secret := "fixture-secret-access-token"
	records := []Record{
		Status(diagnostics.Report{Checks: []diagnostics.Check{{Name: "node", Status: "error", Detail: secret, Fix: secret}, {Name: secret, Status: secret, Detail: secret}}}),
		Denial(secret, policyevents.Event{Destination: secret, Source: secret, Worker: secret, RunID: secret, Code: secret}),
		Launch(secret, time.Second, []launchtiming.Phase{{Name: secret, Duration: time.Second}}, false),
	}
	for _, r := range records {
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(r)
		if strings.Contains(string(data), secret) {
			t.Fatalf("secret copied to exported diagnostics: %s", data)
		}
	}
	if r := Resources("worker", nil); r.Status != "unknown" || r.Memory != nil {
		t.Fatal("unavailable memory treated as zero")
	}
	if r := Status(diagnostics.Report{}); r.Status != "unknown" {
		t.Fatal("no checks treated as healthy")
	}
}

func TestJournalBoundsHistoryAndSerializesConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	j := Journal{Directory: dir, MaxBytes: 2048}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 20 {
				if err := j.Append(Resources("worker", nil)); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	records, err := j.Read()
	if err != nil || len(records) == 0 || len(records) >= 80 {
		t.Fatalf("rotation did not bound history: count=%d err=%v", len(records), err)
	}
	for _, name := range []string{"events.jsonl", "events.previous.jsonl"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Size() > 2048 || info.Mode().Perm() != 0600 {
			t.Fatalf("unbounded or public diagnostic file: %s %v", name, err)
		}
	}
}

func TestJournalRejectsSymlinksHardlinksAndMalformedExports(t *testing.T) {
	for _, name := range []string{".lock", "events.jsonl", "events.previous.jsonl"} {
		dir := t.TempDir()
		_ = os.Chmod(dir, 0700)
		target := filepath.Join(t.TempDir(), "private")
		if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
		j := Journal{Directory: dir}
		if err := j.Append(Resources("worker", nil)); err == nil {
			t.Fatal("symlink accepted")
		}
		if data, _ := os.ReadFile(target); string(data) != "unchanged" {
			t.Fatal("symlink target modified")
		}
	}
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	target := filepath.Join(t.TempDir(), "private")
	_ = os.WriteFile(target, []byte("unchanged"), 0600)
	if err := os.Link(target, filepath.Join(dir, "events.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := (Journal{Directory: dir}).Append(Resources("worker", nil)); err == nil {
		t.Fatal("file with multiple links file accepted")
	}
	_ = os.Remove(filepath.Join(dir, "events.jsonl"))
	_ = os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(`{"schema":1,"secret":"do-not-export"}`+"\n"), 0600)
	if _, err := (Journal{Directory: dir}).Read(); err == nil || strings.Contains(err.Error(), "do-not-export") {
		t.Fatal("malformed export accepted or leaked data")
	}
}
