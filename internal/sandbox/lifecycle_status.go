package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
)

type LifecycleStatus struct {
	Policy                              workerpolicy.Lifecycle
	CreatedAt, FinishedAt, LastActivity time.Time
	ActiveExecutions                    int
	Running                             bool
}

// InspectLifecycle reads control-plane timestamps; guest CPU and filesystem
// activity are deliberately not used as evidence for idle collection.
func (m *Manager) InspectLifecycle(ctx context.Context, id string) (LifecycleStatus, error) {
	v, err := m.cli.ContainerInspect(ctx, id)
	if err != nil {
		return LifecycleStatus{}, err
	}
	if v.Config == nil || v.State == nil || v.Config.Labels[labelManaged] != "true" {
		return LifecycleStatus{}, fmt.Errorf("lifecycle requires a managed worker")
	}
	s := LifecycleStatus{Running: v.State.Running}
	if raw := v.Config.Labels[lifecycleLabel]; raw != "" {
		if err = json.Unmarshal([]byte(raw), &s.Policy); err != nil {
			return s, err
		}
		if err = s.Policy.Validate(); err != nil {
			return s, err
		}
	}
	if s.CreatedAt, err = time.Parse(time.RFC3339Nano, v.Created); err != nil {
		return s, err
	}
	if !s.Running && v.State.Status == "exited" {
		if s.FinishedAt, err = time.Parse(time.RFC3339Nano, v.State.FinishedAt); err != nil {
			return s, err
		}
	}
	if s.Policy.IdleTimeout > 0 {
		if s.LastActivity, err = time.Parse(time.RFC3339Nano, v.Config.Labels[labelPrefix+"last-activity"]); err != nil {
			return s, fmt.Errorf("node does not report idle activity: %w", err)
		}
		if s.ActiveExecutions, err = strconv.Atoi(v.Config.Labels[labelPrefix+"active-executions"]); err != nil || s.ActiveExecutions < 0 {
			return s, fmt.Errorf("node does not report safe idle protection")
		}
	}
	return s, nil
}
