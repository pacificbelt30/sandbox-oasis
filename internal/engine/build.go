package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/docker/docker/api/types/image"
	"github.com/pacificbelt30/codex-dock/internal/buildinfo"
	"github.com/pacificbelt30/codex-dock/internal/proxyimage"
)

var codexVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

// Build retains Dockerfile/custom/template builds using standalone BuildKit.
// OCI output is streamed into the same containerd namespace as workers. Neither
// Docker Engine nor its CLI is involved, and no large archive is left on disk.
func Build(ctx context.Context, tag, dockerfile, buildContext string, log io.Writer) error {
	return BuildAt(ctx, Socket(), tag, dockerfile, buildContext, log)
}

// BuildAt imports the built image into an explicitly selected local node.
func BuildAt(ctx context.Context, socket, tag, dockerfile, buildContext string, log io.Writer) error {
	return buildAt(ctx, socket, tag, dockerfile, buildContext, log, false, false)
}

// BuildPinned builds a product-owned image using the embedded recipe's CLI
// versions. Development version overrides must not change the login CLI.
func BuildPinned(ctx context.Context, tag, dockerfile, buildContext string, log io.Writer) error {
	return buildAt(ctx, Socket(), tag, dockerfile, buildContext, log, true, false)
}

// BuildProxyAt authorizes only the output of this explicit trusted build. A
// random reserved staging reference prevents an in-flight project build from
// substituting the target tag between build completion and digest capture.
func BuildProxyAt(ctx context.Context, socket, tag, dockerfile, buildContext string, log io.Writer) error {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	staging := "oaiso-proxy-build:" + hex.EncodeToString(random[:])
	if err := buildAt(ctx, socket, staging, dockerfile, buildContext, log, true, true); err != nil {
		return err
	}
	c, err := NewClientAt(socket)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	info, err := c.ImageInspect(ctx, staging)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"Source": staging, "Target": tag, "Digest": info.ID})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker/images/promote", bytes.NewReader(body))
	if err != nil {
		return err
	}
	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("trusted proxy image promotion failed (HTTP %d); upgrade node", resp.StatusCode)
	}
	if err := proxyimage.Record(ctx, socket, tag, info.ID); err != nil {
		return err
	}
	_, err = c.ImageRemove(ctx, staging, image.RemoveOptions{})
	return err
}

func buildAt(ctx context.Context, socket, tag, dockerfile, buildContext string, log io.Writer, pinned, proxy bool) error {
	name, err := normalizeBuildName(tag)
	if err != nil {
		return err
	}
	if !proxy {
		if err := proxyimage.CheckWorkerTag(ctx, socket, name, pinned); err != nil {
			return err
		}
	}
	df, err := filepath.Abs(dockerfile)
	if err != nil {
		return err
	}
	dir, err := filepath.Abs(buildContext)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	addr := os.Getenv("BUILDKIT_HOST")
	if addr == "" {
		addr = "unix:///run/codex-dock-buildkit/buildkitd.sock"
	}
	c := exec.CommandContext(ctx, "buildctl", "--addr", addr, "build", "--progress=plain", "--frontend", "dockerfile.v0", "--local", "context="+dir, "--local", "dockerfile="+filepath.Dir(df), "--opt", "filename="+filepath.Base(df), "--opt", "image-resolve-mode=local", "--output", "type=oci,name="+name+",dest=-")
	c.Args = append(c.Args, "--opt", "build-arg:CODEX_DOCK_BUILD_VERSION="+buildinfo.Version)
	for _, agent := range []string{"CODEX", "CLAUDE"} {
		if pinned {
			continue
		}
		key := "CODEX_DOCK_" + agent + "_VERSION"
		if version := os.Getenv(key); version != "" {
			if !codexVersionPattern.MatchString(version) {
				return fmt.Errorf("invalid %s %q: expected an exact semver", key, version)
			}
			c.Args = append(c.Args, "--opt", "build-arg:"+agent+"_CLI_VERSION="+version)
		}
	}
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "no_proxy", "all_proxy"} {
		if value := os.Getenv(key); value != "" {
			c.Args = append(c.Args, "--opt", "build-arg:"+key+"="+value)
		}
	}
	if cert := os.Getenv("CODEX_PROXY_CERT"); cert != "" {
		c.Args = append(c.Args, "--secret", "id=proxy_ca,src="+cert)
	}
	c.Stderr = log
	output, err := c.StdoutPipe()
	if err != nil {
		return err
	}
	client, err := NewClientAt(socket)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if err = c.Start(); err != nil {
		return fmt.Errorf("standalone BuildKit (buildctl) is required: %w", err)
	}
	response, importErr := client.ImageLoad(ctx, output)
	if importErr == nil {
		_, importErr = io.Copy(log, response.Body)
		importErr = errors.Join(importErr, response.Body.Close())
	}
	if importErr != nil {
		cancel()
	}
	buildErr := c.Wait()
	if err = errors.Join(buildErr, importErr); err != nil {
		return fmt.Errorf("BuildKit build/import failed: %w", err)
	}
	return nil
}
