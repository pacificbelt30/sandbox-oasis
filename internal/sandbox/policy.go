package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
	"net/url"
	"strconv"
	"time"
)

const lifecycleLabel = labelPrefix + "lifecycle"

func addWorkerPolicyLabels(labels map[string]string, opts RunOptions) error {
	if len(opts.TemporaryStorage) > 0 {
		data, err := json.Marshal(opts.TemporaryStorage)
		if err != nil {
			return err
		}
		labels[workerpolicy.TemporaryStorageLabel] = string(data)
	}
	if opts.Lifecycle != (workerpolicy.Lifecycle{}) {
		data, err := json.Marshal(opts.Lifecycle)
		if err != nil {
			return err
		}
		labels[lifecycleLabel] = string(data)
	}
	if len(opts.Filesystem.DenyRead)+len(opts.Filesystem.DenyWrite) > 0 {
		data, err := json.Marshal(opts.Filesystem)
		if err != nil {
			return err
		}
		if len(data) > 4096 {
			return fmt.Errorf("filesystem policy exceeds label limit")
		}
		labels[labelPrefix+"filesystem"] = string(data)
	}
	return nil
}

type ReapAction struct {
	Worker string `json:"worker"`
	Action string `json:"action"`
	Reason string `json:"reason"`
}

// Reap applies explicit lifecycle settings only. Idle means time since the last
// control-plane exec/session activity, not guest CPU or filesystem activity.
// dryRun returns the same decisions without changing workers.
func (m *Manager) Reap(ctx context.Context, now time.Time, dryRun bool) ([]ReapAction, error) {
	return m.reap(ctx, now, dryRun, nil, nil)
}

// ReapWorkers applies lifecycle policies only to the supplied full worker IDs.
// An empty selection never expands to all managed workers.
func (m *Manager) ReapWorkers(ctx context.Context, now time.Time, dryRun bool, ids []string) ([]ReapAction, error) {
	selected := make(map[string]bool, len(ids))
	for _, id := range ids {
		selected[id] = true
	}
	return m.reap(ctx, now, dryRun, selected, nil)
}

// ApplyReapActions refuses a different action/reason from the reviewed plan.
// In particular, a reviewed stop must never turn into retention removal while
// the operator is reading the confirmation page.
func (m *Manager) ApplyReapActions(ctx context.Context, now time.Time, expected []ReapAction) ([]ReapAction, error) {
	selected := map[string]bool{}
	approved := map[string]ReapAction{}
	for _, a := range expected {
		selected[a.Worker] = true
		approved[a.Worker] = a
	}
	return m.reap(ctx, now, false, selected, approved)
}

func (m *Manager) reap(ctx context.Context, now time.Time, dryRun bool, selected map[string]bool, approved map[string]ReapAction) ([]ReapAction, error) {
	workers, err := m.ListContext(ctx, true)
	if err != nil {
		return nil, err
	}
	var actions []ReapAction
	var failures []error
	for _, w := range workers {
		if selected != nil && !selected[w.ID] {
			continue
		}
		if err := ctx.Err(); err != nil {
			return actions, err
		}
		v, err := m.cli.ContainerInspect(ctx, w.ID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if v.Config == nil || v.State == nil || v.Config.Labels[lifecycleLabel] == "" {
			continue
		}
		var p workerpolicy.Lifecycle
		if err = json.Unmarshal([]byte(v.Config.Labels[lifecycleLabel]), &p); err != nil || p.Validate() != nil {
			failures = append(failures, fmt.Errorf("worker %s has invalid lifecycle policy", w.ID))
			continue
		}
		created, err := time.Parse(time.RFC3339Nano, v.Created)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		a := ReapAction{Worker: w.ID}
		if v.State.Running {
			if p.MaxLifetime > 0 && now.Sub(created) >= p.MaxLifetime {
				a.Action, a.Reason = "stop", "max_lifetime"
			} else if p.IdleTimeout > 0 {
				var active time.Time
				if value := v.Config.Labels[labelPrefix+"last-activity"]; value != "" {
					if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
						active = t
					} else {
						failures = append(failures, err)
						continue
					}
				} else {
					failures = append(failures, fmt.Errorf("worker %s: node does not report idle activity; upgrade the node", w.ID))
					continue
				}
				count, err := strconv.Atoi(v.Config.Labels[labelPrefix+"active-executions"])
				if err != nil || count < 0 {
					failures = append(failures, fmt.Errorf("worker %s: safe idle collection requires an updated node", w.ID))
					continue
				}
				if count == 0 && now.Sub(active) >= p.IdleTimeout {
					a.Action, a.Reason = "stop", "idle_timeout"
				}
			}
		} else if v.State.Status == "exited" && p.Retention > 0 {
			finished, err := time.Parse(time.RFC3339Nano, v.State.FinishedAt)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if now.Sub(finished) >= p.Retention {
				a.Action, a.Reason = "remove", "retention"
			}
		}
		if a.Action == "" {
			continue
		}
		if dryRun {
			actions = append(actions, a)
			continue
		}
		if approved != nil && a != approved[w.ID] {
			failures = append(failures, fmt.Errorf("worker %s lifecycle action changed; review the due action again", w.ID))
			continue
		}
		if a.Reason == "idle_timeout" {
			var stopped bool
			stopped, err = m.stopIdle(ctx, v.ID, now.Add(-p.IdleTimeout))
			if err == nil && !stopped {
				continue // New activity appeared after inspection; no token revoked.
			}
		} else if a.Action == "stop" {
			err = m.stopContext(ctx, w.ID, 10, "max_lifetime")
		} else {
			err = m.RemoveContext(ctx, w.ID, false)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s %s: %w", a.Action, w.ID, err))
		} else {
			actions = append(actions, a)
		}
	}
	return actions, errors.Join(failures...)
}

func (m *Manager) stopIdle(parent context.Context, id string, before time.Time) (bool, error) {
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()
	unlock, err := m.lockCleanup(ctx, id)
	if err != nil {
		return false, err
	}
	defer unlock()
	v, err := m.cli.ContainerInspect(ctx, id)
	if err != nil {
		return false, err
	}
	var result struct {
		Stopped bool `json:"stopped"`
	}
	err = m.jobRequest(ctx, "POST", "/containers/"+url.PathEscape(id)+"/idle-stop?before="+url.QueryEscape(before.Format(time.RFC3339Nano)), &result)
	if err == nil && !result.Stopped {
		return false, nil
	}
	// An uncertain stop must also fail closed for credentials. Leave durable
	// allocation recovery to the normal Stop/Remove retry if the node failed.
	revokeErr := m.revokeWorkerTokenChecked(v)
	if err != nil {
		return false, errors.Join(err, revokeErr)
	}
	if v.Config.Labels[labelIsolation] == RuntimeKata {
		err = m.releaseKataAllocation(ctx, v.Config.Labels[labelAllocation], v.ID)
	}
	return true, errors.Join(err, revokeErr)
}
