package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/managedworkspace"
)

func (m *Manager) ManagedWorkspaceInfo(ctx context.Context, id string) (*managedworkspace.Info, error) {
	v, err := m.cli.ContainerInspect(ctx, id)
	if err != nil {
		return nil, err
	}
	if v.Config == nil {
		return nil, fmt.Errorf("worker configuration unavailable")
	}
	wid := v.Config.Labels[managedworkspace.Label]
	if wid == "" {
		return nil, nil
	}
	info, err := engine.InspectManagedWorkspace(ctx, m.cli, wid)
	if err != nil {
		return nil, err
	}
	if info.Lease != v.ID {
		return nil, fmt.Errorf("managed workspace lease does not match VM")
	}
	return &info, nil
}

// This is a guest observation, never evidence for host reservation enforcement.
func (m *Manager) ManagedWorkspaceUsage(ctx context.Context, id string) (string, error) {
	var out, stderr bytes.Buffer
	code, err := m.Exec(ctx, id, []string{"python3", "-I", "-c", `import os,json;s=os.statvfs('/workspace');print(json.dumps({'bytes':(s.f_blocks-s.f_bfree)*s.f_frsize,'inodes':s.f_files-s.f_ffree}))`}, nil, &fileOutput{out: &out, remaining: 4096}, &fileOutput{out: &stderr, remaining: 4096})
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("workspace usage observation failed (exit %d)", code)
	}
	var v struct {
		Bytes  uint64 `json:"bytes"`
		Inodes uint64 `json:"inodes"`
	}
	if err = json.Unmarshal(out.Bytes(), &v); err != nil {
		return "", err
	}
	return fmt.Sprintf("Guest-observed usage (not host accounting): %.1f MiB / %d inodes", float64(v.Bytes)/(1<<20), v.Inodes), nil
}
