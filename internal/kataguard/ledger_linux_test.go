//go:build linux

package kataguard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ledgerFixture(t *testing.T) *Guard {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return &Guard{config: Config{StateDir: dir}, manifest: Manifest{Version: 1, Profile: Profile, Runtime: "fixture"}}
}
func TestReservationSurvivesManagerRestartAndRejectsChangedOwnership(t *testing.T) {
	g := ledgerFixture(t)
	id := strings.Repeat("a", 32)
	if err := g.trackAllocation(context.Background(), id, ""); err != nil {
		t.Fatal(err)
	}
	other := &Guard{config: g.config, manifest: g.manifest}
	r, err := other.readRecord(id)
	if err != nil || r.CreatedAt.IsZero() || r.Allocation != id {
		t.Fatalf("lost reservation: %+v %v", r, err)
	}
	info, err := os.Stat(filepath.Join(g.config.StateDir, "allocations", id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatal("record is not private")
	}
	r.Worker = strings.Repeat("b", 64)
	if err := g.writeRecord(r); err != nil {
		t.Fatal(err)
	}
	if err := g.trackAllocation(context.Background(), id, strings.Repeat("c", 64)); err == nil {
		t.Fatal("bound allocation was reassigned")
	}
	other.manifest.Runtime = "different"
	if _, err := other.readRecord(id); err == nil {
		t.Fatal("record reused under another node profile")
	}
	if err := g.removeRecord(id); err != nil {
		t.Fatal(err)
	}
	if err := g.removeRecord(id); err != nil {
		t.Fatal("record removal is not idempotent:", err)
	}
}

func TestSmallResourceProfileIsDurableAndCannotChange(t *testing.T) {
	g := ledgerFixture(t)
	id := strings.Repeat("d", 32)
	if err := g.trackAllocation(context.Background(), id, "", "small"); err != nil {
		t.Fatal(err)
	}
	record, err := g.readRecord(id)
	if err != nil || record.Profile != "small" {
		t.Fatalf("small profile was not persisted: %+v %v", record, err)
	}
	if err := g.trackAllocation(context.Background(), id, "", "standard"); err == nil {
		t.Fatal("reservation profile changed during retry")
	}
}
func TestAdmissionLockHonorsContext(t *testing.T) {
	g := ledgerFixture(t)
	unlock, err := g.admissionLock(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := g.admissionLock(ctx); err == nil {
		t.Fatal("concurrent admission bypassed the lock")
	}
}
func TestAllocationLedgerRejectsSymlinkAndCorruptRecord(t *testing.T) {
	g := ledgerFixture(t)
	id := strings.Repeat("a", 32)
	if err := g.trackAllocation(context.Background(), id, ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(g.config.StateDir, "allocations", id+".json")
	if err := os.WriteFile(path, []byte(`{"Version":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := g.readRecord(id); err == nil {
		t.Fatal("incomplete record accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", path); err != nil {
		t.Fatal(err)
	}
	if _, err := g.readRecord(id); err == nil {
		t.Fatal("symlink record accepted")
	}
}

func TestCustomResourceBudgetIsDurableAndCannotBeReinterpreted(t *testing.T) {
	g := ledgerFixture(t)
	p := customProfile()
	g.profiles = []ResourceProfile{p}
	id := strings.Repeat("e", 32)
	if err := g.trackAllocation(context.Background(), id, "", p.Name); err != nil {
		t.Fatal(err)
	}
	other := &Guard{config: g.config, manifest: g.manifest, profiles: append([]ResourceProfile(nil), g.profiles...)}
	record, err := other.readRecord(id)
	if err != nil || record.Budget == nil || !record.Budget.Equal(p) {
		t.Fatalf("custom budget was not retained: %v", err)
	}
	extra := p
	extra.Name = "another"
	other.profiles = append(other.profiles, extra)
	if other.Stamp() != g.Stamp() {
		t.Fatal("adding a profile invalidated existing asset attestations")
	}
	if _, err := other.readRecord(id); err != nil {
		t.Fatal("additive profile invalidated allocation:", err)
	}
	for _, mutate := range []func(*ResourceProfile){func(p *ResourceProfile) { p.CPUCores = 1 }, func(p *ResourceProfile) { p.PIDLimit = 512 }, func(p *ResourceProfile) { p.GuestMemoryBytes = CompactGuestMemory }, func(p *ResourceProfile) { p.HostMemoryBytes = HostMemory }} {
		other.profiles[0] = p
		mutate(&other.profiles[0])
		if _, err := other.readRecord(id); err == nil {
			t.Fatal("retained budget was reinterpreted")
		}
	}
	other.profiles = nil
	if _, err := other.readRecord(id); err == nil {
		t.Fatal("removed custom definition was accepted")
	}
	other.profiles = []ResourceProfile{p}
	if _, err := other.readRecord(id); err != nil {
		t.Fatal("restoring definition did not permit recovery:", err)
	}
	if err := g.trackAllocation(context.Background(), id, "", "standard"); err == nil {
		t.Fatal("custom allocation changed to builtin")
	}
}
