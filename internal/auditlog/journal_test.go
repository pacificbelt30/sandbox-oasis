package auditlog

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestJournalChainConcurrencyAndSecrets(t *testing.T) {
	j := Journal{Directory: filepath.Join(t.TempDir(), "audit")}
	secret := "sk-secret-not-a-log-field"
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			finish, e := j.Begin("auth_change", secret)
			if e != nil {
				t.Error(e)
				return
			}
			if e = finish(errors.New(secret)); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	report, e := j.Read()
	if e != nil || len(report.Records) != 24 || len(report.Pending) != 0 {
		t.Fatalf("%+v %v", report, e)
	}
	data, _ := os.ReadFile(filepath.Join(j.Directory, "events.jsonl"))
	if bytes.Contains(data, []byte(secret)) {
		t.Fatal("secret retained")
	}
	// Removing even complete trailing records must disagree with the independent head.
	lines := bytes.Split(data, []byte("\n"))
	_ = os.WriteFile(filepath.Join(j.Directory, "events.jsonl"), bytes.Join(lines[:len(lines)-2], []byte("\n")), 0600)
	if _, e = j.Read(); e == nil {
		t.Fatal("truncation accepted")
	}
}
func TestPendingAndCapacityFailClosed(t *testing.T) {
	j := Journal{Directory: filepath.Join(t.TempDir(), "audit"), MaxBytes: 1024}
	finish, e := j.Begin("create", "worker")
	if e != nil {
		t.Fatal(e)
	}
	r, e := j.Read()
	if e != nil || len(r.Pending) != 1 {
		t.Fatalf("pending %v %v", r, e)
	}
	if e = finish(nil); e != nil {
		t.Fatal(e)
	}
	if _, e = j.Begin("create", "next"); e == nil {
		t.Fatal("capacity accepted")
	}
}
func TestUnsafeOrBrokenStorage(t *testing.T) {
	for _, kind := range []string{"symlink", "partial", "head"} {
		t.Run(kind, func(t *testing.T) {
			j := Journal{Directory: filepath.Join(t.TempDir(), "audit")}
			f, e := j.Begin("issue", "worker")
			if e != nil {
				t.Fatal(e)
			}
			if e = f(nil); e != nil {
				t.Fatal(e)
			}
			p := filepath.Join(j.Directory, "events.jsonl")
			switch kind {
			case "symlink":
				_ = os.Remove(p)
				_ = os.Symlink("/dev/full", p)
			case "partial":
				h, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
				_, _ = h.WriteString("{")
				_ = h.Close()
			case "head":
				_ = os.WriteFile(filepath.Join(j.Directory, "head"), []byte(strings.Repeat("0", 64)), 0600)
			}
			if _, e = j.Begin("issue", "worker"); e == nil {
				t.Fatal("broken evidence allowed authority")
			}
		})
	}
}

func TestLegacyAuditRecordsRemainVerifiableWithReasonCodes(t *testing.T) {
	j := Journal{Directory: filepath.Join(t.TempDir(), "audit")}
	if err := os.Mkdir(j.Directory, 0700); err != nil {
		t.Fatal(err)
	}
	const legacy = `{"schema":1,"sequence":1,"time":"2026-10-25T00:00:00Z","transaction":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","action":"create","subject_hash":"f16d05ec6b29248d2c61adb1e9263f78e4f7bace1b955014a2d17872cfe4064d","result":"intent","previous":"","hash":"66bd7e51d1b0eeceab63be24be688ba69d3ce4265aa0490a8d0d818898c99306"}
{"schema":1,"sequence":2,"time":"2026-10-25T00:00:00Z","transaction":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","action":"create","subject_hash":"f16d05ec6b29248d2c61adb1e9263f78e4f7bace1b955014a2d17872cfe4064d","result":"ok","previous":"66bd7e51d1b0eeceab63be24be688ba69d3ce4265aa0490a8d0d818898c99306","hash":"18e02733709765e755b1d3ac0490d244f42c02a858dd7609edd46319cfa62ddc"}
`
	if err := os.WriteFile(filepath.Join(j.Directory, "events.jsonl"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(j.Directory, "head"), []byte("18e02733709765e755b1d3ac0490d244f42c02a858dd7609edd46319cfa62ddc"), 0600); err != nil {
		t.Fatal(err)
	}
	finish, err := j.Begin("stop", "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err = finish(context.Canceled); err != nil {
		t.Fatal(err)
	}
	if err = j.Denial("policy", "private-secret-not-a-reason"); err != nil {
		t.Fatal(err)
	}
	report, err := j.Read()
	if err != nil || len(report.Records) != 6 || report.Records[3].Reason != "canceled" || report.Records[5].Reason != "policy_denied" {
		t.Fatalf("legacy/typed reason record failed: %v %+v", err, report)
	}
	data, _ := os.ReadFile(filepath.Join(j.Directory, "events.jsonl"))
	if bytes.Contains(data, []byte("private-secret-not-a-reason")) {
		t.Fatal("unrecognized reason value leaked")
	}
}
