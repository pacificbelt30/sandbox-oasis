package cxd

import (
	"context"
	"time"
)

// Heartbeat protects a running worker from idle reaping for at most five
// minutes per renewal. ttl=0 releases the lease, recording current activity.
// It never extends MaxLifetime. No credentials or management socket enter VMs.
func (c *Client) Heartbeat(ctx context.Context, worker string, ttl time.Duration) (time.Time, error) {
	until, err := c.manager.Heartbeat(ctx, worker, ttl)
	return until, classify(err)
}
