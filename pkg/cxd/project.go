package cxd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	dockerdefaults "github.com/pacificbelt30/codex-dock/docker"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/projectconfig"
	"github.com/pacificbelt30/codex-dock/internal/template"
)

// BuildRequest imports an OCI image built from a Dockerfile into the same
// dedicated node that the client uses for workers.
type BuildRequest struct {
	Tag        string
	Dockerfile string
	ContextDir string
	Log        io.Writer
}

func (c *Client) Build(ctx context.Context, req BuildRequest) error {
	if req.Tag == "" || req.Dockerfile == "" || req.ContextDir == "" {
		return fmt.Errorf("build tag, Dockerfile and context directory are required")
	}
	if req.Log == nil {
		req.Log = io.Discard
	}
	return engine.BuildAt(ctx, c.opts.NodeSocket, req.Tag, req.Dockerfile, req.ContextDir, req.Log)
}

// BuildTemplate builds a bundled or installed worker image template with the
// same BuildKit pipeline used by the CLI.
func (c *Client) BuildTemplate(ctx context.Context, name, tag string, log io.Writer) error {
	return c.buildTemplate(ctx, name, tag, log, map[string]bool{})
}

func (c *Client) buildTemplate(ctx context.Context, name, tag string, log io.Writer, seen map[string]bool) error {
	if seen[name] {
		return fmt.Errorf("cyclic image template %q", name)
	}
	seen[name] = true
	tmpl, err := template.Get(name)
	if err != nil {
		return err
	}
	if result := template.Validate(tmpl); !result.IsValid() {
		return fmt.Errorf("invalid image template %q: %s", name, strings.Join(result.Failed, "; "))
	}
	if tag == "" {
		tag = tmpl.Tag("")
	}
	if tmpl.IsDerived() {
		exists, err := c.manager.ImageExistsContext(ctx, tmpl.BaseImage)
		if err != nil {
			return err
		}
		if !exists {
			base := template.MatchTag(tmpl.BaseImage)
			if base == "" {
				return fmt.Errorf("base image %q is not available", tmpl.BaseImage)
			}
			if err := c.buildTemplate(ctx, base, tmpl.BaseImage, log, seen); err != nil {
				return err
			}
		}
	}
	dir, err := os.MkdirTemp("", "cxd-template-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	dockerfile := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dockerfile, tmpl.Dockerfile, 0600); err != nil {
		return err
	}
	if tmpl.IsBase {
		if err := os.WriteFile(filepath.Join(dir, "entrypoint.sh"), dockerdefaults.Entrypoint, 0700); err != nil {
			return err
		}
	}
	return c.Build(ctx, BuildRequest{Tag: tag, Dockerfile: dockerfile, ContextDir: dir, Log: log})
}

// PrepareProject validates the portable oaiso.toml, builds its image if
// missing or stale, and returns a launch request for the product to review and
// submit. Host credentials and runtime settings remain the product's choice.
func (c *Client) PrepareProject(ctx context.Context, dir, requestID string, log io.Writer) (LaunchRequest, error) {
	cfg, err := projectconfig.Load(dir)
	if err != nil {
		return LaunchRequest{}, err
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return LaunchRequest{}, err
	}
	req := LaunchRequest{RequestID: requestID, ProjectDir: root, Image: "codex-dock:latest"}
	if cfg != nil {
		if cfg.Run.Image != "" {
			req.Image = cfg.Run.Image
		} else if cfg.Run.Template != "" {
			tmpl, err := template.Get(cfg.Run.Template)
			if err != nil {
				return LaunchRequest{}, err
			}
			req.Image = tmpl.Tag("")
		}
		if cfg.Run.Agent != "shell" {
			req.Agent = cfg.Run.Agent
		}
		req.Filesystem, req.Lifecycle, req.Environment, _ = cfg.Policies()
		req.UsagePolicy, err = cfg.UsagePolicy()
		if err != nil {
			return LaunchRequest{}, err
		}
		req.PolicyPreset = cfg.Run.PolicyPreset
		req.Model, req.Profile, req.ApprovalMode = cfg.Run.Model, cfg.Run.ProfileSize, cfg.Run.ApprovalMode
		req.ReadOnly = cfg.Run.ReadOnly != nil && *cfg.Run.ReadOnly
		req.ReadOnlyRootFS = cfg.Run.ReadOnlyRootFS != nil && *cfg.Run.ReadOnlyRootFS
		req.NoGitMetadata = cfg.Run.NoGitMetadata != nil && *cfg.Run.NoGitMetadata
		req.NoInternet = cfg.Run.NoInternet != nil && *cfg.Run.NoInternet
		req.EgressAllowDomains = append([]string(nil), cfg.Run.EgressAllowDomains...)
		req.WatchPolling = cfg.Run.WatchPolling != nil && *cfg.Run.WatchPolling
	}
	exists, err := c.manager.ImageExistsContext(ctx, req.Image)
	if err != nil {
		return LaunchRequest{}, err
	}
	stale := false
	if exists && cfg != nil && cfg.Build.Dockerfile != "" {
		if created, err := c.manager.ImageCreatedAtContext(ctx, req.Image); err == nil {
			if info, err := os.Stat(filepath.Join(root, cfg.Build.Dockerfile)); err == nil {
				stale = info.ModTime().After(created)
			}
		}
	} else if exists {
		if name := template.MatchTag(req.Image); name != "" {
			if tmpl, err := template.Get(name); err == nil {
				if created, err := c.manager.ImageCreatedAtContext(ctx, req.Image); err == nil {
					stale, _ = tmpl.StaleAgainst(created)
				}
			}
		}
	}
	if exists && !stale {
		return req, nil
	}
	if cfg != nil && cfg.Build.Dockerfile != "" {
		path := filepath.Join(root, cfg.Build.Dockerfile)
		data, err := os.ReadFile(path)
		if err != nil {
			return LaunchRequest{}, err
		}
		if projectconfig.DockerfileUsesBase(data, "codex-dock:latest") {
			baseExists, err := c.manager.ImageExistsContext(ctx, "codex-dock:latest")
			if err != nil {
				return LaunchRequest{}, err
			}
			if !baseExists {
				if err := c.BuildTemplate(ctx, "plain", "codex-dock:latest", log); err != nil {
					return LaunchRequest{}, err
				}
			}
		}
		return req, c.Build(ctx, BuildRequest{Tag: req.Image, Dockerfile: path, ContextDir: root, Log: log})
	}
	name := template.MatchTag(req.Image)
	if cfg != nil && cfg.Build.Template != "" {
		name = cfg.Build.Template
	}
	if name == "" {
		return LaunchRequest{}, fmt.Errorf("image %q is missing and project has no matching build source", req.Image)
	}
	return req, c.BuildTemplate(ctx, name, req.Image, log)
}
