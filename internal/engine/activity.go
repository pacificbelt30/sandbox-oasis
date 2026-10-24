//go:build linux

package engine

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
)

func (b *Backend) activeWork(r *record, mainRunning bool, now time.Time) (int, error) {
	n := b.activeExecutions(r.ID)
	if now.Before(r.ActivityUntil) {
		n++
	}
	var policy workerpolicy.Lifecycle
	if value := r.Config.Labels["codex-dock.lifecycle"]; value != "" {
		if err := json.Unmarshal([]byte(value), &policy); err != nil || policy.Validate() != nil {
			return 0, fmt.Errorf("worker lifecycle policy is invalid; idle stop refused")
		}
	}
	if policy.ProtectMain && mainRunning {
		n++
	}
	return n, nil
}

// Called with b.mu held. Pending execs protect the worker until their bounded
// creation lease expires; detached jobs are restored before the node serves.
func (b *Backend) activeExecutions(worker string) int {
	n := 0
	for _, e := range b.execs {
		if e.Container == worker && (!e.Started || e.Running) {
			n++
		}
	}
	return n
}

func idleEligible(last, before time.Time, active int) bool {
	return !last.IsZero() && !last.After(before) && active == 0
}
