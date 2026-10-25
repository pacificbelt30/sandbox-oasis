//go:build linux

package kataguard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestVerificationWaitsForAdmissionBeforeReadingAssets(t *testing.T) {
	state := t.TempDir()
	g := &Guard{config: Config{StateDir: state, Manifest: filepath.Join(state, "missing-manifest.json")}}
	unlock, err := g.admissionLock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	// Missing assets would fail immediately if validation ran before admission.
	// A queued verification must wait (and remain cancellable) before attesting.
	if err = g.VerifyRuntime(ctx, "io.containerd.kata.v2", "allocation", "worker"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("verification did not wait for admission: %v", err)
	}
	// Standalone engine retirement must validate independently and never acquire
	// admission while the engine may hold its own mutation lock.
	if err = g.Retire(context.Background(), "allocation", "worker"); !os.IsNotExist(err) {
		t.Fatalf("standalone retirement skipped asset validation: %v", err)
	}
	unlock()
	unlock = nil
	if err = g.VerifyRuntime(context.Background(), "io.containerd.kata.v2", "allocation", "worker"); !os.IsNotExist(err) {
		t.Fatalf("assets not re-read after admission: %v", err)
	}
	entries, err := os.ReadDir(state)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "admission.lock" {
		t.Fatalf("failed validation changed allocation state: %v", entries)
	}
}
