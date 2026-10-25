package usage

import (
	"errors"
	"sync"
	"syscall"
	"testing"

	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

func TestDiskFullPoisonsAdmissionAcrossConcurrentRetries(t *testing.T) {
	ledger := openTest(t)
	key := Key("worker", identity.Identity{})
	if _, err := ledger.Bind(key, &identity.UsagePolicy{MaxRequests: 100, MaxConcurrent: 100}); err != nil {
		t.Fatal(err)
	}
	ledger.persist = func(map[string]account) error { return syscall.ENOSPC }
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			if lease, err := ledger.Begin(key); lease != nil || !errors.Is(err, ErrUnavailable) {
				t.Errorf("disk-full admission granted: %v", err)
			}
		})
	}
	wg.Wait()
	// Recovery of the filesystem must not silently reopen a poisoned ledger.
	ledger.persist = func(map[string]account) error { return nil }
	if lease, err := ledger.Begin(key); lease != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatal("failure granted fresh budget")
	}
}
