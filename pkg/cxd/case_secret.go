package cxd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/docker/docker/client"
)

// The node keeps authority outside all guest-shareable paths. A shared host
// HOME never contains the secret or its persistent source.
func prepareCaseAuthority(ctx context.Context, c *client.Client, opts CaseProxyOptions) (string, error) {
	body, err := json.Marshal(struct {
		Name, CaseID, Secret string
		UID, GID             int
	}{opts.Name, opts.CaseID, opts.AdminSecret, os.Geteuid(), os.Getegid()})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker/authority/cases", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("node case authority unavailable or differs (HTTP %d); drain old proxy before replacement", resp.StatusCode)
	}
	var result struct{ Path string }
	err = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result)
	if err == nil && result.Path == "" {
		err = fmt.Errorf("node returned no case authority path")
	}
	return result.Path, err
}
func removeCaseAuthority(ctx context.Context, c *client.Client, name, caseID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, "http://docker/authority/cases?name="+url.QueryEscape(name)+"&case="+url.QueryEscape(caseID), nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("node case authority cleanup failed (HTTP %d)", resp.StatusCode)
	}
	return nil
}
