//go:build linux

package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHostCgroupDiagnosticsAndMissingCounters(t *testing.T) {
	root := t.TempDir()
	group := filepath.Join(root, "worker")
	if err := os.Mkdir(group, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"cpu.stat": "usage_usec 1250000\nuser_usec 12\n", "memory.current": "500000000\n", "memory.max": "512000000\n", "memory.events": "low 0\noom 2\noom_kill 1\n"} {
		if err := os.WriteFile(filepath.Join(group, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sample := readCgroupResources(root, "/worker")
	if sample.Error != "" || sample.CPUUsec == nil || *sample.CPUUsec != 1250000 || sample.MemoryBytes == nil || *sample.MemoryBytes != 500000000 || sample.OOMKills == nil || *sample.OOMKills != 1 {
		t.Fatal(sample)
	}
	if err := os.Remove(filepath.Join(group, "memory.current")); err != nil {
		t.Fatal(err)
	}
	sample = readCgroupResources(root, "/worker")
	if sample.MemoryBytes != nil || sample.Error == "" {
		t.Fatal("missing observation became zero", sample)
	}
	for _, path := range []string{"", "/", "/worker/../escape", "relative"} {
		s := readCgroupResources(root, path)
		if s.Error == "" || s.CPUUsec != nil {
			t.Fatal("unsafe cgroup path", path, s)
		}
	}
}
