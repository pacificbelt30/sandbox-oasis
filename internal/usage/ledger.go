// Package usage enforces execution-scoped admission and transfer budgets.
// Accepted work is journaled before upstream I/O. Interrupted requests remain
// indeterminate across restart, never silently granting a fresh budget.
package usage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

var ErrLimit = errors.New("execution usage limit reached")
var ErrUnavailable = errors.New("execution usage state unavailable or indeterminate")

type Snapshot struct {
	Policy            identity.UsagePolicy `json:"policy"`
	Requests          int64                `json:"requests"`
	Active            int64                `json:"active"`
	Bytes             int64                `json:"bytes"`
	Tokens            int64                `json:"tokens"`
	UnmeteredRequests int64                `json:"unmetered_requests"`
	Indeterminate     bool                 `json:"indeterminate"`
}

type account struct {
	Snapshot
	Pending int64 `json:"pending"`
}

type Ledger struct {
	mu       sync.Mutex
	accounts map[string]account
	persist  func(map[string]account) error
	release  func() error
	failed   bool
	closed   bool
}

func Key(worker string, id identity.Identity) string {
	value := []byte("worker:" + worker)
	if !id.Empty() {
		value, _ = json.Marshal(id)
	}
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func (l *Ledger) commit(next map[string]account) error {
	if l.closed || l.failed {
		return ErrUnavailable
	}
	if err := l.persist(next); err != nil {
		l.failed = true
		return ErrUnavailable
	}
	l.accounts = next
	return nil
}

func (l *Ledger) copy() map[string]account {
	next := make(map[string]account, len(l.accounts)+1)
	for k, v := range l.accounts {
		next[k] = v
	}
	return next
}

// Bind inherits an existing policy if omitted. Reissue/restore cannot reset or
// weaken the execution's original limits, even through a legacy admin caller.
func (l *Ledger) Bind(key string, policy *identity.UsagePolicy) (*identity.UsagePolicy, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if a, ok := l.accounts[key]; ok {
		if l.closed || l.failed {
			return nil, ErrUnavailable
		}
		if policy != nil && a.Policy != *policy {
			return nil, fmt.Errorf("execution usage policy is immutable")
		}
		return a.Policy.Clone(), nil
	}
	if policy == nil {
		return nil, nil
	}
	if l.closed || l.failed {
		return nil, ErrUnavailable
	}
	if len(l.accounts) >= 10000 {
		return nil, ErrUnavailable
	}
	next := l.copy()
	next[key] = account{Snapshot: Snapshot{Policy: *policy}}
	if err := l.commit(next); err != nil {
		return nil, err
	}
	return policy.Clone(), nil
}

func (l *Ledger) Inspect(key string) (Snapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.accounts[key]
	if !ok || l.closed || l.failed {
		return Snapshot{}, ErrUnavailable
	}
	return a.Snapshot, nil
}

type Request struct {
	l        *Ledger
	key      string
	once     sync.Once
	finished bool
}

func (l *Ledger) Begin(key string) (*Request, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.accounts[key]
	if !ok || l.closed || l.failed || a.Indeterminate {
		return nil, ErrUnavailable
	}
	p := a.Policy
	concurrent := p.MaxConcurrent
	// One request at a time is the conservative default for observed token caps.
	if concurrent == 0 && p.MaxTokens > 0 {
		concurrent = 1
	}
	if p.MaxRequests > 0 && a.Requests >= p.MaxRequests || concurrent > 0 && a.Active >= concurrent || p.MaxBytes > 0 && a.Bytes >= p.MaxBytes || p.MaxTokens > 0 && a.Tokens >= p.MaxTokens || a.Requests == math.MaxInt64 || a.Active == math.MaxInt64 {
		return nil, ErrLimit
	}
	a.Requests++
	a.Active++
	a.Pending++
	next := l.copy()
	next[key] = a
	if err := l.commit(next); err != nil {
		return nil, err
	}
	return &Request{l: l, key: key}, nil
}

// Consume reserves bytes before they cross the proxy boundary. Active requests
// share one budget. A crash cannot grant a refund: the durable Pending record
// blocks further admissions until the operator starts a new execution.
func (r *Request) Consume(n int) (int, error) {
	l := r.l
	l.mu.Lock()
	defer l.mu.Unlock()
	if n < 0 || l.closed || l.failed || r.finished {
		return 0, ErrUnavailable
	}
	a := l.accounts[r.key]
	allowed := int64(n)
	if allowed > math.MaxInt64-a.Bytes {
		allowed = math.MaxInt64 - a.Bytes
	}
	if a.Policy.MaxBytes > 0 && allowed > a.Policy.MaxBytes-a.Bytes {
		allowed = a.Policy.MaxBytes - a.Bytes
	}
	if allowed < 0 {
		allowed = 0
	}
	a.Bytes += allowed
	l.accounts[r.key] = a
	if allowed < int64(n) {
		return int(allowed), ErrLimit
	}
	return int(allowed), nil
}

func (r *Request) Finish(tokens int64, known bool) error {
	var result error
	r.once.Do(func() {
		l := r.l
		l.mu.Lock()
		defer l.mu.Unlock()
		a := l.accounts[r.key]
		r.finished = true
		a.Active--
		a.Pending--
		if !known {
			a.UnmeteredRequests++
		}
		if tokens < 0 || tokens > math.MaxInt64-a.Tokens {
			a.Indeterminate = true
		} else {
			a.Tokens += tokens
		}
		if a.Policy.MaxTokens > 0 && !known {
			a.Indeterminate = true
		}
		next := l.copy()
		next[r.key] = a
		result = l.commit(next)
	})
	return result
}

func (l *Ledger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	return l.release()
}
