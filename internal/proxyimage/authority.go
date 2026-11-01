// Package proxyimage separates operator-authorized proxy images from project builds.
package proxyimage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/distribution/reference"
	"github.com/opencontainers/go-digest"
	"github.com/pacificbelt30/codex-dock/internal/buildinfo"
	"github.com/pacificbelt30/codex-dock/internal/privatefile"
)

type Authority struct{ Name, Digest, Build string }

func Normalize(name string) (string, error) {
	n, err := reference.ParseNormalizedNamed(name)
	if err != nil {
		return "", err
	}
	return reference.TagNameOnly(n).String(), nil
}
func authorityPath(directory, image string) (string, error) {
	name, err := Normalize(image)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(name))
	return filepath.Join(directory, hex.EncodeToString(sum[:])+".json"), nil
}

// Save and Load run in the root node, inside its protected state root.
// HOME and guest-writable project configuration never grant image authority.
func Save(directory string, a Authority) error {
	if digest.Digest(a.Digest).Validate() != nil || a.Build == "" {
		return fmt.Errorf("invalid proxy image authority")
	}
	name, err := Normalize(a.Name)
	if err != nil {
		return err
	}
	a.Name = name
	p, err := authorityPath(directory, name)
	if err != nil {
		return err
	}
	old, err := privatefile.Read(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return privatefile.Update(p, privatefile.Revision(old), func([]byte) ([]byte, error) { return data, nil })
}
func Load(directory, image string) (Authority, error) {
	var a Authority
	p, err := authorityPath(directory, image)
	if err != nil {
		return a, err
	}
	data, err := privatefile.Read(p)
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(data, &a)
	return a, err
}
func nodeAuthority(ctx context.Context, socket, method, path string, body any) (Authority, error) {
	var a Authority
	if !filepath.IsAbs(socket) || filepath.Clean(socket) != socket {
		return a, fmt.Errorf("invalid authority node socket")
	}
	var input io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return a, err
		}
		input = bytes.NewReader(data)
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	defer transport.CloseIdleConnections()
	c := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, input)
	if err != nil {
		return a, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return a, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return a, os.ErrNotExist
	}
	if resp.StatusCode != http.StatusOK {
		return a, fmt.Errorf("node proxy authority failed (HTTP %d); upgrade the node or explicitly rebuild the proxy", resp.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&a)
	return a, err
}
func Record(ctx context.Context, socket, image, id string) error {
	_, err := nodeAuthority(ctx, socket, http.MethodPost, "/authority/images", Authority{image, id, buildinfo.Version})
	return err
}
func Verify(ctx context.Context, socket, image, id string) error {
	name, err := Normalize(image)
	if err != nil {
		return err
	}
	a, err := nodeAuthority(ctx, socket, http.MethodGet, "/authority/images?name="+url.QueryEscape(name), nil)
	if err != nil {
		return fmt.Errorf("proxy image lacks node-owned build authority; run oaiso proxy build or explicitly trust a reviewed import: %w", err)
	}
	if a.Name != name || a.Digest != id || a.Build != buildinfo.Version || digest.Digest(id).Validate() != nil {
		return fmt.Errorf("proxy image differs from its node-authorized build")
	}
	return nil
}
func CheckWorkerTag(ctx context.Context, socket, image string, login bool) error {
	name, err := Normalize(image)
	if err != nil {
		return err
	}
	n, _ := reference.ParseNormalizedNamed(name)
	base := reference.FamiliarName(n)
	if strings.HasPrefix(base, "oaiso-proxy-build") || base == "codex-dock-proxy" || !login && base == "oaiso-login" {
		return fmt.Errorf("image %s is reserved for trusted product code; choose a worker tag", name)
	}
	_, err = nodeAuthority(ctx, socket, http.MethodGet, "/authority/images?name="+url.QueryEscape(name), nil)
	if err == nil {
		return fmt.Errorf("image %s is reserved by node proxy authority", name)
	}
	if !os.IsNotExist(err) {
		return err
	}
	return nil
}
