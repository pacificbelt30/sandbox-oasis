package sandboxux

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

// Supervisor is explicit and scoped to this TUI's selected full worker IDs.
// Closing the TUI cancels it; persistent supervision uses oaiso reap --watch.
type Supervisor struct {
	mu        sync.Mutex
	workers   map[string]bool
	lastError string
}

func (s *Supervisor) Watch(id string, enable bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.workers == nil {
		s.workers = map[string]bool{}
	}
	if enable && id != "" {
		s.workers[id] = true
	} else {
		delete(s.workers, id)
	}
}
func (s *Supervisor) State(id string) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.workers[id], s.lastError
}
func (s *Supervisor) IDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.workers))
	for id := range s.workers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
func (s *Supervisor) Run(ctx context.Context, reap func(context.Context, time.Time, bool, []string) ([]sandbox.ReapAction, error)) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			ids := s.IDs()
			if len(ids) == 0 {
				continue
			}
			call, cancel := context.WithTimeout(ctx, 120*time.Second)
			actions, err := reap(call, now, false, ids)
			cancel()
			s.mu.Lock()
			s.lastError = ""
			if err != nil {
				s.lastError = err.Error()
			}
			for _, a := range actions {
				if a.Action == "remove" {
					delete(s.workers, a.Worker)
				}
			}
			s.mu.Unlock()
		}
	}
}
