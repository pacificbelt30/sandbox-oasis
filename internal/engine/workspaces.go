package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/docker/docker/client"
	"github.com/pacificbelt30/codex-dock/internal/managedworkspace"
)

type WorkspaceClient struct{ Socket string }

func (w WorkspaceClient) request(ctx context.Context, method, endpoint string, body io.Reader, out any) error {
	socket := w.Socket
	if socket == "" {
		socket = Socket()
	}
	c, err := NewClientAt(socket)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	return workspaceRequest(ctx, c, method, endpoint, body, out)
}
func workspaceRequest(ctx context.Context, c *client.Client, method, endpoint string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+endpoint, body)
	if err != nil {
		return err
	}
	response, err := c.HTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("workspace response unavailable; inspect the workspace catalog before retrying: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		var detail struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&detail)
		return fmt.Errorf("workspace operation failed (HTTP %d): %s", response.StatusCode, detail.Message)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(response.Body, 16<<20)).Decode(out)
}
func workspaceEndpoint(id string) (string, error) {
	if !managedworkspace.ID.MatchString(id) {
		return "", fmt.Errorf("managed workspace requires its full ID")
	}
	return "/workspaces/" + id, nil
}
func (w WorkspaceClient) List(ctx context.Context) (managedworkspace.Catalog, error) {
	var out managedworkspace.Catalog
	err := w.request(ctx, http.MethodGet, "/workspaces", nil, &out)
	return out, err
}
func (w WorkspaceClient) Create(ctx context.Context, options managedworkspace.Options) (managedworkspace.Info, error) {
	var out managedworkspace.Info
	if err := options.Validate(); err != nil {
		return out, err
	}
	data, err := json.Marshal(options)
	if err != nil {
		return out, err
	}
	err = w.request(ctx, http.MethodPost, "/workspaces", bytes.NewReader(data), &out)
	return out, err
}
func (w WorkspaceClient) Inspect(ctx context.Context, id string) (managedworkspace.Info, error) {
	var out managedworkspace.Info
	endpoint, err := workspaceEndpoint(id)
	if err == nil {
		err = w.request(ctx, http.MethodGet, endpoint, nil, &out)
	}
	return out, err
}

func InspectManagedWorkspace(ctx context.Context, c *client.Client, id string) (managedworkspace.Info, error) {
	var out managedworkspace.Info
	endpoint, err := workspaceEndpoint(id)
	if err == nil {
		err = workspaceRequest(ctx, c, http.MethodGet, endpoint, nil, &out)
	}
	return out, err
}

func ManagedWorkspaceManifest(ctx context.Context, c *client.Client, id string) (managedworkspace.Manifest, error) {
	var out managedworkspace.Manifest
	endpoint, err := workspaceEndpoint(id)
	if err == nil {
		err = workspaceRequest(ctx, c, http.MethodGet, endpoint+"/manifest", nil, &out)
	}
	if err == nil {
		err = out.Validate(managedworkspace.MaxSeedBytes)
	}
	return out, err
}
func (w WorkspaceClient) Seed(ctx context.Context, id string, input io.Reader) (managedworkspace.Info, error) {
	var out managedworkspace.Info
	endpoint, err := workspaceEndpoint(id)
	if err == nil {
		err = w.request(ctx, http.MethodPut, endpoint+"/seed", input, &out)
	}
	return out, err
}
func (w WorkspaceClient) Manifest(ctx context.Context, id string) (managedworkspace.Manifest, error) {
	var out managedworkspace.Manifest
	endpoint, err := workspaceEndpoint(id)
	if err == nil {
		err = w.request(ctx, http.MethodGet, endpoint+"/manifest", nil, &out)
	}
	return out, err
}
func (w WorkspaceClient) Remove(ctx context.Context, id string) error {
	endpoint, err := workspaceEndpoint(id)
	if err != nil {
		return err
	}
	return w.request(ctx, http.MethodDelete, endpoint, nil, nil)
}
func (w WorkspaceClient) Clone(ctx context.Context, id, name string) (managedworkspace.Info, error) {
	var out managedworkspace.Info
	endpoint, err := workspaceEndpoint(id)
	if err != nil {
		return out, err
	}
	data, _ := json.Marshal(map[string]string{"name": name})
	err = w.request(ctx, http.MethodPost, endpoint+"/clone", bytes.NewReader(data), &out)
	return out, err
}
func (w WorkspaceClient) Points(ctx context.Context) ([]managedworkspace.RecoveryPoint, error) {
	var out []managedworkspace.RecoveryPoint
	err := w.request(ctx, http.MethodGet, "/recovery-points", nil, &out)
	return out, err
}
func (w WorkspaceClient) SavePoint(ctx context.Context, options managedworkspace.SaveOptions) (managedworkspace.RecoveryPoint, error) {
	var out managedworkspace.RecoveryPoint
	data, err := json.Marshal(options)
	if err != nil {
		return out, err
	}
	err = w.request(ctx, http.MethodPost, "/recovery-points", bytes.NewReader(data), &out)
	return out, err
}
func (w WorkspaceClient) RemovePoint(ctx context.Context, id string) error {
	if !managedworkspace.ID.MatchString(id) {
		return fmt.Errorf("recovery point requires its full ID")
	}
	return w.request(ctx, http.MethodDelete, "/recovery-points/"+url.PathEscape(id), nil, nil)
}
