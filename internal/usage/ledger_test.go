package usage

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

func openTest(t *testing.T) *Ledger {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func TestConcurrentAdmissionsAndTransfersCannotExceedBudget(t *testing.T) {
	l := openTest(t)
	key := Key("worker", identity.Identity{})
	if _, err := l.Bind(key, &identity.UsagePolicy{MaxRequests: 10, MaxConcurrent: 2, MaxBytes: 100}); err != nil {
		t.Fatal(err)
	}
	var admitted atomic.Int64
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			r, err := l.Begin(key)
			if err != nil {
				if !errors.Is(err, ErrLimit) {
					t.Error(err)
				}
				return
			}
			admitted.Add(1)
			if _, err := r.Consume(30); err != nil && !errors.Is(err, ErrLimit) {
				t.Error(err)
			}
			if err := r.Finish(0, false); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	s, err := l.Inspect(key)
	if err != nil || s.Requests > 10 || s.Active != 0 || s.Bytes > 100 || admitted.Load() != s.Requests {
		t.Fatalf("budget bypass: %+v %v", s, err)
	}
	// A burst can admit only two requests before the remaining goroutines
	// observe the concurrency cap. That valid schedule leaves bytes unspent.
	// Exhaust those bytes explicitly before checking admission at the byte cap.
	if s.Bytes < 100 {
		r, err := l.Begin(key)
		if err != nil {
			t.Fatal(err)
		}
		if n, err := r.Consume(int(100 - s.Bytes)); err != nil || int64(n) != 100-s.Bytes {
			t.Fatal("remaining byte reservation failed", n, err)
		}
		if err := r.Finish(0, false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.Begin(key); !errors.Is(err, ErrLimit) {
		t.Fatalf("exhausted byte budget accepted: %v", err)
	}
}

func TestConcurrentRequestAdmissionsEnforceExactRequestCeiling(t *testing.T) {
	l := openTest(t)
	key := Key("worker", identity.Identity{})
	if _, err := l.Bind(key, &identity.UsagePolicy{MaxRequests: 10}); err != nil {
		t.Fatal(err)
	}
	var admitted atomic.Int64
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			r, err := l.Begin(key)
			if errors.Is(err, ErrLimit) {
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			admitted.Add(1)
			if err := r.Finish(0, false); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	s, err := l.Inspect(key)
	if err != nil || admitted.Load() != 10 || s.Requests != 10 || s.Active != 0 {
		t.Fatalf("request ceiling: %+v admitted=%d err=%v", s, admitted.Load(), err)
	}
}

func TestConcurrencyAndObservedTokenCaps(t *testing.T) {
	l := openTest(t)
	key := Key("worker", identity.Identity{})
	_, err := l.Bind(key, &identity.UsagePolicy{MaxTokens: 10})
	if err != nil {
		t.Fatal(err)
	}
	r, err := l.Begin(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Begin(key); !errors.Is(err, ErrLimit) {
		t.Fatal("token metering allowed implicit concurrent work")
	}
	if err := r.Finish(12, true); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Begin(key); !errors.Is(err, ErrLimit) {
		t.Fatal("observed token ceiling ignored")
	}
	if _, err := r.Consume(1); !errors.Is(err, ErrUnavailable) {
		t.Fatal("finished request consumed budget")
	}
}

func TestPersistenceReissueAndInterruptedWorkFailClosed(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	l, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	key := Key("worker", identity.Identity{})
	policy := &identity.UsagePolicy{MaxRequests: 2}
	if _, err := l.Bind(key, policy); err != nil {
		t.Fatal(err)
	}
	r, err := l.Begin(key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Consume(4); err != nil {
		t.Fatal(err)
	}
	if err := r.Finish(0, false); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Bind(key, &identity.UsagePolicy{MaxRequests: 3}); err == nil {
		t.Fatal("reissue changed immutable policy")
	}
	if _, err := Open(dir); err == nil {
		t.Fatal("two proxies opened one budget")
	}
	_ = l.Close()
	l, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if inherited, err := l.Bind(key, nil); err != nil || *inherited != *policy {
		t.Fatal("legacy restore lost limits")
	}
	s, _ := l.Inspect(key)
	if s.Requests != 1 || s.Bytes != 4 || s.Indeterminate {
		t.Fatalf("finished usage lost: %+v", s)
	}
	if _, err := l.Begin(key); err != nil {
		t.Fatal(err)
	}
	_ = l.Close() // Crash/forced stop before Finish.
	l, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if _, err := l.Begin(key); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("interrupted usage silently reset: %v", err)
	}
	if s, _ := l.Inspect(key); !s.Indeterminate || s.Active != 0 || s.Requests != 2 {
		t.Fatalf("interrupted state concealed: %+v", s)
	}
}

func TestUnknownUsageAndStorageFailureDenyFurtherWork(t *testing.T) {
	for _, diskFailure := range []bool{false, true} {
		l := openTest(t)
		key := Key("worker", identity.Identity{})
		_, _ = l.Bind(key, &identity.UsagePolicy{MaxTokens: 10})
		r, err := l.Begin(key)
		if err != nil {
			t.Fatal(err)
		}
		if diskFailure {
			l.persist = func(map[string]account) error { return errors.New("disk full") }
		}
		_ = r.Finish(0, false)
		if _, err := l.Begin(key); !errors.Is(err, ErrUnavailable) {
			t.Fatal("unknown usage or failed storage permitted work")
		}
	}
	for _, name := range []string{"ledger.json", "ledger.lock"} {
		dir := t.TempDir()
		_ = os.Chmod(dir, 0700)
		target := filepath.Join(t.TempDir(), "target")
		if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
		if l, err := Open(dir); err == nil {
			_ = l.Close()
			t.Fatal("symlink accepted")
		}
		if data, _ := os.ReadFile(target); string(data) != "untouched" {
			t.Fatal("symlink target modified")
		}
	}
}
