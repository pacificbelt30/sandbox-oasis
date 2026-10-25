package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func CheckpointMaintenance(ctx context.Context, socket, action string, options, out any) error {
	if action != "retention" && action != "pin" {
		return fmt.Errorf("invalid checkpoint maintenance action")
	}
	c, err := NewClientAt(socket)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	info, err := c.Info(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, f := range info.DriverStatus {
		if f == [2]string{"cxd.checkpoint-retention", "1"} {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("node does not support checkpoint retention")
	}
	data, err := json.Marshal(options)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker/checkpoints/"+action, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("checkpoint outcome unavailable; inspect catalog before retrying: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("checkpoint maintenance failed (HTTP %d); inspect and review a new plan", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}
