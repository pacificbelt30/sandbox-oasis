package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/diskcheckpoint"
)

// Checkpoints uses only the existing local node socket and never starts a VM.
func Checkpoints(ctx context.Context, socket, method, id, worker string, options diskcheckpoint.Options, out any) error {
	c, err := NewClientAt(socket)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	info, err := c.Info(ctx)
	if err != nil {
		return err
	}
	supported := false
	for _, f := range info.DriverStatus {
		supported = supported || (f[0] == "cxd.disk-checkpoint" && f[1] == "1")
	}
	if !supported {
		return fmt.Errorf("node does not support disk-checkpoint-v1")
	}
	endpoint := "http://docker/checkpoints"
	if id != "" {
		if !diskcheckpoint.IDPattern.MatchString(id) {
			return fmt.Errorf("checkpoint requires its full ID")
		}
		endpoint += "/" + id
	}
	var body io.Reader
	if method == http.MethodPost {
		data, err := json.Marshal(struct {
			Worker  string                 `json:"worker"`
			Options diskcheckpoint.Options `json:"options"`
		}{worker, options})
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.HTTPClient().Do(request)
	if err != nil {
		return fmt.Errorf("checkpoint response unavailable; inspect checkpoint list before retrying: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		var detail struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&detail)
		return fmt.Errorf("checkpoint operation failed (HTTP %d): %s", response.StatusCode, strings.TrimSpace(detail.Message))
	}
	return json.NewDecoder(io.LimitReader(response.Body, 128<<20)).Decode(out)
}
