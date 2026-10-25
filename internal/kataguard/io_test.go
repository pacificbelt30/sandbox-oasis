//go:build linux

package kataguard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIOLimitsVerifyEveryDimensionWithoutDependingOnOrder(t *testing.T) {
	limit, err := ParseIOLimit("8:0 wbps=1048576 riops=100")
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyIOLimits("8:0 wiops=max riops=100 wbps=1048576 rbps=max\n", []IOLimit{limit}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "8:1 rbps=max wbps=1048576 riops=100 wiops=max", "8:0 rbps=max wbps=max riops=100 wiops=max", "8:0 rbps=max wbps=1048576 riops=100", "8:0 rbps=max wbps=1048576 riops=100 wiops=max wbps=max"} {
		if VerifyIOLimits(bad, []IOLimit{limit}) == nil {
			t.Fatalf("weakened/ambiguous I/O accepted: %q", bad)
		}
	}
	for _, bad := range []string{"8:0 wbps=0", "8:0 wbps=-1", "8:0 wbps=1 wbps=2", "8:0 rbps=1\n8:1 wbps=1", "0:1 rbps=1", "4096:0 rbps=1", "8:0 rbps=max", "8:0 latency=1", "8:0 wbps=18446744073709551615"} {
		if _, err := ParseIOLimit(bad); err == nil {
			t.Fatalf("bad I/O input accepted: %q", bad)
		}
	}
}

func TestAdditionalLimitsRemainPinnedAcrossReopen(t *testing.T) {
	g := ledgerFixture(t)
	p := customProfile()
	p.FileDescriptorLimit = 512
	p.BlockIO = []IOLimit{{Major: 8, Minor: 0, WriteBytesPerSecond: 1048576}}
	g.profiles = []ResourceProfile{p}
	id := strings.Repeat("b", 32)
	if err := g.trackAllocation(context.Background(), id, "", p.Name); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*ResourceProfile){func(v *ResourceProfile) { v.FileDescriptorLimit++ }, func(v *ResourceProfile) { v.BlockIO = []IOLimit{{Major: 8, Minor: 0, WriteBytesPerSecond: 2097152}} }} {
		changed := p
		mutate(&changed)
		other := &Guard{config: g.config, manifest: g.manifest, profiles: []ResourceProfile{changed}}
		if _, err := other.readRecord(id); err == nil {
			t.Fatal("retained additional limits reinterpreted")
		}
	}
	if _, err := g.readRecord(id); err != nil {
		t.Fatal(err)
	}
}

func TestProfileRejectsInvalidAdditionalLimitsAndAcceptsLegacy(t *testing.T) {
	p := customProfile()
	legacy, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy), "file_descriptor") || strings.Contains(string(legacy), "block_io") {
		t.Fatal("legacy encoding changed")
	}
	for _, n := range []uint64{1, 127, 1<<20 + 1} {
		p.FileDescriptorLimit = n
		if ValidateCustomResourceProfile(p) == nil {
			t.Fatal("bad nofile limit accepted")
		}
	}
	p.FileDescriptorLimit = 512
	p.BlockIO = []IOLimit{{Major: 8, WriteIOPS: 10}, {Major: 8, WriteIOPS: 20}}
	if ValidateCustomResourceProfile(p) == nil {
		t.Fatal("duplicate device accepted")
	}
}

func TestRequestedIOFailsBeforeReservationWhenControllerMissing(t *testing.T) {
	g := ledgerFixture(t)
	g.config.CgroupRoot = t.TempDir()
	p := customProfile()
	p.BlockIO = []IOLimit{{Major: 8, WriteIOPS: 10}}
	g.profiles = []ResourceProfile{p}
	if err := os.WriteFile(filepath.Join(g.config.CgroupRoot, "cgroup.subtree_control"), []byte("cpu memory pids"), 0600); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("c", 32)
	if _, err := g.ReserveProfile(context.Background(), id, p.Name); err == nil || !strings.Contains(err.Error(), "io controller") {
		t.Fatalf("unsupported I/O silently ignored: %v", err)
	}
	path, _ := g.group(id)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("unsupported I/O allocated resources")
	}
}
