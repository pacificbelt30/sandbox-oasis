package sandbox

import (
	"context"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/storageinfo"
)

func (m *Manager) RootFSUsage(ctx context.Context, id string) (storageinfo.Usage, error) {
	return engine.RootFSUsage(ctx, m.cli, id)
}
