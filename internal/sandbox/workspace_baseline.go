package sandbox

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/workerstatus"
)

func validGitCommit(head string) bool {
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

// Capture exactly once while the bootstrap gate still blocks user code. All
// Git execution stays inside the guest; no guest .git is resolved on the host.
func (m *Manager) captureWorkspaceBaseline(ctx context.Context, id string) error {
	v, err := m.cli.ContainerInspect(ctx, id)
	if err != nil {
		return err
	}
	if v.Config == nil || v.Config.Labels[workerstatus.BaselineStateLabel] != "pending" {
		return nil
	}
	data, probeErr := m.execTrusted(ctx, id, []string{"git", "--no-pager", "-c", "core.fsmonitor=false", "-C", "/workspace", "rev-parse", "--verify", "HEAD"})
	head := strings.TrimSpace(string(data))
	if probeErr != nil || !validGitCommit(head) {
		head = ""
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.jobRequest(ctx, "POST", "/containers/"+url.PathEscape(v.ID)+"/workspace-baseline?head="+url.QueryEscape(head), nil)
}

// WorkspaceBaseline reports an immutable launch reference. Legacy/non-Git
// workers remain usable, but never substitute the current HEAD as a baseline.
func (m *Manager) WorkspaceBaseline(ctx context.Context, id string) (string, error) {
	v, err := m.cli.ContainerInspect(ctx, id)
	if err != nil {
		return "", err
	}
	if v.Config == nil || v.Config.Labels[workerstatus.BaselineStateLabel] != "ready" {
		return "", fmt.Errorf("launch Git baseline unavailable for this worker")
	}
	head := v.Config.Labels[workerstatus.BaselineHeadLabel]
	if !validGitCommit(head) {
		return "", fmt.Errorf("invalid launch Git baseline")
	}
	return head, nil
}

func (m *Manager) Diagnostics(ctx context.Context, id string) (workerstatus.Diagnostics, error) {
	v, err := m.InspectContext(ctx, id)
	if err != nil {
		return workerstatus.Diagnostics{}, err
	}
	var result workerstatus.Diagnostics
	err = m.jobRequest(ctx, "GET", "/containers/"+url.PathEscape(v.ID)+"/diagnostics", &result)
	return result, err
}

func (m *Manager) stopForReason(ctx context.Context, id string, timeout int, reason string) error {
	return m.jobRequest(ctx, "POST", fmt.Sprintf("/containers/%s/stop?t=%d&reason=%s", url.PathEscape(id), timeout, url.QueryEscape(reason)), nil)
}
