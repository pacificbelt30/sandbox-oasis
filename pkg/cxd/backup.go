package cxd

import (
	"context"
	"github.com/pacificbelt30/codex-dock/internal/engine"
)

func (c *Client) ExportCheckpoint(ctx context.Context, id, path string) error {
	return engine.ExportCheckpoint(ctx, c.opts.NodeSocket, id, path)
}
func (c *Client) ImportCheckpoint(ctx context.Context, path string) (DiskCheckpoint, error) {
	return engine.ImportCheckpoint(ctx, c.opts.NodeSocket, path)
}
func (c *Client) ExportAgentState(ctx context.Context, worker, path string) error {
	return engine.ExportAgentState(ctx, c.opts.NodeSocket, worker, path)
}

// RestoreAgentState only accepts a never-started worker created with Create.
func (c *Client) RestoreAgentState(ctx context.Context, worker, path string) error {
	return engine.RestoreAgentState(ctx, c.opts.NodeSocket, worker, path)
}
