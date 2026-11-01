//go:build linux

package engine

import (
	"github.com/docker/docker/api/types/container"
	"testing"
)

func TestStorageDiagnosticsRequireDedicatedStorage(t *testing.T) {
	b := &Backend{opts: Options{StorageRoot: "/"}}
	status := b.storageStatus()
	if status.Enforced || status.Error == "" {
		t.Fatal("OS filesystem reported as dedicated storage", status)
	}
	usage := b.rootFSUsage(&record{})
	if usage.Available || usage.Error == "" || usage.UsedBytes != 0 {
		t.Fatal("unavailable usage treated as an observed zero", usage)
	}
	usage = b.rootFSUsage(&record{Host: container.HostConfig{ReadonlyRootfs: true}})
	if !usage.Available || !usage.ReadOnly {
		t.Fatal(usage)
	}
}
