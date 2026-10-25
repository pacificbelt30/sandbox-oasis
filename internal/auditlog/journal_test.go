package auditlog

import (
	"bytes"
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
