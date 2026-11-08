//go:build linux

package engine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/workerstatus"
)

func readCgroupResources(root, parent string) workerstatus.Resources {
	s := workerstatus.Resources{ObservedAt: time.Now().UTC()}
	if parent == "" || parent == "/" || filepath.Clean(parent) != parent || !filepath.IsAbs(parent) {
		s.Error = "worker host cgroup is unavailable"
		return s
	}
	dir := filepath.Join(root, strings.TrimPrefix(parent, "/"))
	read := func(name string) (string, error) {
		f, e := os.Open(filepath.Join(dir, name))
		if e != nil {
			return "", e
		}
		defer func() { _ = f.Close() }()
		data, e := io.ReadAll(io.LimitReader(f, 4097))
		if e != nil || len(data) > 4096 {
			return "", fmt.Errorf("invalid cgroup observation")
		}
		return strings.TrimSpace(string(data)), nil
	}
	number := func(raw string) *uint64 {
		n, e := strconv.ParseUint(raw, 10, 64)
		if e != nil {
			return nil
		}
		return &n
	}
	value := func(name string) *uint64 {
		raw, e := read(name)
		if e != nil {
			return nil
		}
		return number(raw)
	}
	counter := func(name, key string) *uint64 {
		raw, e := read(name)
		if e != nil {
			return nil
		}
		for _, l := range strings.Split(raw, "\n") {
			v := strings.Fields(l)
			if len(v) == 2 && v[0] == key {
				return number(v[1])
			}
		}
		return nil
	}
	s.CPUUsec = counter("cpu.stat", "usage_usec")
	s.MemoryBytes = value("memory.current")
	s.MemoryLimit = value("memory.max")
	s.OOMKills = counter("memory.events", "oom_kill")
	if s.CPUUsec == nil || s.MemoryBytes == nil || s.OOMKills == nil {
		s.Error = "some host cgroup counters are unavailable"
	}
	return s
}

func (b *Backend) observeResources(r *record) workerstatus.Resources {
	sample := readCgroupResources("/sys/fs/cgroup", r.Host.CgroupParent)
	if sample.CPUUsec != nil || sample.MemoryBytes != nil || sample.OOMKills != nil {
		r.LastResources = sample
	}
	return sample
}

func validBaseline(head string) bool {
	if len(head) != 40 && len(head) != 64 {
		return false
	}
	for _, r := range head {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
