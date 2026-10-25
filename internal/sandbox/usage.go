package sandbox

import (
	"context"
	"fmt"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/usage"
)

func (m *Manager) Usage(ctx context.Context, worker string) (usage.Snapshot, error) {
	v, err := m.cli.ContainerInspect(ctx, worker)
	if err != nil {
		return usage.Snapshot{}, err
	}
	p, err := m.workerProxy(v)
	if err != nil {
		return usage.Snapshot{}, err
	}
	reporter, ok := p.(interface {
		UsageContext(context.Context, string) (usage.Snapshot, error)
	})
	if !ok {
		return usage.Snapshot{}, fmt.Errorf("auth proxy does not support usage reporting")
	}
	return reporter.UsageContext(ctx, strings.TrimPrefix(v.Name, "/"))
}
