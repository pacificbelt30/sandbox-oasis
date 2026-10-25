package cxd

import (
	"context"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"io"
)

type ArtifactOptions = sandbox.ArtifactOptions
type ArtifactPlan = sandbox.ArtifactPlan

func (c *Client) PlanArtifacts(ctx context.Context, id string, o ArtifactOptions) (ArtifactPlan, error) {
	return c.manager.PlanArtifacts(ctx, id, o)
}
func (c *Client) ExportArtifacts(ctx context.Context, id string, o ArtifactOptions, out io.Writer) error {
	return c.manager.ExportArtifacts(ctx, id, o, out)
}
