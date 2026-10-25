package cxd

import (
	"context"
	"github.com/pacificbelt30/codex-dock/internal/usage"
)

type UsageSnapshot = usage.Snapshot

func (c *Client) Usage(ctx context.Context, worker string) (UsageSnapshot, error) {
	s, err := c.manager.Usage(ctx, worker)
	return s, classify(err)
}
