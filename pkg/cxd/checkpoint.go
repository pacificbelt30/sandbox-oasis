package cxd

import (
	"context"
	"fmt"
	"net/http"

	"github.com/pacificbelt30/codex-dock/internal/diskcheckpoint"
	"github.com/pacificbelt30/codex-dock/internal/engine"
)

type CheckpointOptions = diskcheckpoint.Options
type DiskCheckpoint = diskcheckpoint.Info
type CheckpointCatalog = diskcheckpoint.Catalog

// Checkpoint captures a stopped regular Kata worker's filtered root filesystem.
// No RAM, mounts, environment, token, jobs or execution identity is copied.
func (c *Client) Checkpoint(ctx context.Context, worker string, options CheckpointOptions) (DiskCheckpoint, error) {
	var out DiskCheckpoint
	err := engine.Checkpoints(ctx, c.opts.NodeSocket, http.MethodPost, "", worker, options, &out)
	return out, err
}
func (c *Client) Checkpoints(ctx context.Context) (CheckpointCatalog, error) {
	var out CheckpointCatalog
	err := engine.Checkpoints(ctx, c.opts.NodeSocket, http.MethodGet, "", "", CheckpointOptions{}, &out)
	return out, err
}
func (c *Client) InspectCheckpoint(ctx context.Context, id string) (DiskCheckpoint, error) {
	var out DiskCheckpoint
	err := engine.Checkpoints(ctx, c.opts.NodeSocket, http.MethodGet, id, "", CheckpointOptions{}, &out)
	return out, err
}
func (c *Client) RemoveCheckpoint(ctx context.Context, id string) error {
	var out struct {
		Removed bool `json:"removed"`
	}
	return engine.Checkpoints(ctx, c.opts.NodeSocket, http.MethodDelete, id, "", CheckpointOptions{}, &out)
}

// RestoreCheckpoint starts a NEW worker with ordinary launch/bootstrap/token
// verification. Caller supplies a new RequestID, mounts, identity and policy.
func (c *Client) RestoreCheckpoint(ctx context.Context, id string, request LaunchRequest) (Worker, error) {
	if request.Runtime != "" && request.Runtime != "kata" {
		return Worker{}, fmt.Errorf("checkpoint restoration requires Kata")
	}
	info, err := c.InspectCheckpoint(ctx, id)
	if err != nil {
		return Worker{}, err
	}
	if request.Image != "" && request.Image != info.Image {
		return Worker{}, fmt.Errorf("restore image conflicts with checkpoint")
	}
	if request.Environment.ImageID != "" && request.Environment.ImageID != info.Digest {
		return Worker{}, fmt.Errorf("restore image digest conflicts with checkpoint")
	}
	request.Image = info.Image
	request.Environment.ImageID = info.Digest
	return c.Launch(ctx, request)
}
