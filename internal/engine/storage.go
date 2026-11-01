package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/docker/docker/client"
	"github.com/pacificbelt30/codex-dock/internal/storageinfo"
)

func nodeGet(ctx context.Context, c *client.Client, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker"+endpoint, nil)
	if err != nil {
		return err
	}
	res, err := c.HTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("node %s unavailable (HTTP %d); update the node or inspect runtime-check", endpoint, res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(out)
}
func StorageStatus(ctx context.Context, socket string) (storageinfo.Status, error) {
	var out storageinfo.Status
	c, err := NewClientAt(socket)
	if err != nil {
		return out, err
	}
	defer func() { _ = c.Close() }()
	err = nodeGet(ctx, c, "/storage", &out)
	if err == nil && !out.Enforced {
		err = fmt.Errorf("rootfs storage quota unavailable: %s", out.Error)
	}
	return out, err
}
func RootFSUsage(ctx context.Context, c *client.Client, id string) (storageinfo.Usage, error) {
	var out storageinfo.Usage
	if !hexWorkerID(id) {
		return out, fmt.Errorf("storage query requires full worker ID")
	}
	err := nodeGet(ctx, c, "/containers/"+id+"/storage", &out)
	return out, err
}
func hexWorkerID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
