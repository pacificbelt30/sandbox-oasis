package cxd

import (
	"context"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"time"
)

type Preview = sandbox.Preview

func (c *Client) Preview(ctx context.Context, worker string, port int, lifetime time.Duration) (*Preview, error) {
	return c.manager.Preview(ctx, worker, port, lifetime)
}
