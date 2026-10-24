package projectconfig

import (
	"bytes"
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/workeregress"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/kataguard"
	"github.com/pacificbelt30/codex-dock/internal/networkpolicy"
	"github.com/pacificbelt30/codex-dock/internal/template"
	"github.com/pelletier/go-toml/v2"
)

const Filename = "oaiso.toml"

// Config contains only portable worker settings. Host credentials,
// runtime selection, proxy endpoints and extra host mounts remain global or
// explicit CLI options so cloning a repository cannot silently change them.
type Config struct {
	Root string `toml:"-"`
	Run  struct {
		PolicyPreset       string     `toml:"policy_preset"`
		DenyRead           []string   `toml:"deny_read"`
		DenyWrite          []string   `toml:"deny_write"`
		MaxLifetime        string     `toml:"max_lifetime"`
		IdleTimeout        string     `toml:"idle_timeout"`
		ProtectMain        bool       `toml:"protect_main"`
		Retention          string     `toml:"retention"`
		StartHooks         [][]string `toml:"start_hooks"`
		Readiness          []string   `toml:"readiness"`
		ReadinessTimeout   string     `toml:"readiness_timeout"`
		Image              string     `toml:"image"`
		Template           string     `toml:"template"`
		Agent              string     `toml:"agent"`
		Model              string     `toml:"model"`
		ProfileSize        string     `toml:"profile_size"`
		ApprovalMode       string     `toml:"approval_mode"`
		WatchPolling       *bool      `toml:"watch_polling"`
		ReadOnly           *bool      `toml:"read_only"`
		ReadOnlyRootFS     *bool      `toml:"read_only_rootfs"`
		NoGitMetadata      *bool      `toml:"no_git_metadata"`
		NoInternet         *bool      `toml:"no_internet"`
		EgressAllowDomains []string   `toml:"egress_allow_domains"`
	} `toml:"run"`
	Build struct {
		Dockerfile string `toml:"dockerfile"`
		Template   string `toml:"template"`
		Tag        string `toml:"tag"`
	} `toml:"build"`
}

var projectKeys = map[string]map[string]bool{
	"run": {
		"image": true, "template": true, "agent": true, "model": true, "profile_size": true,
		"approval_mode": true, "watch_polling": true,
		"policy_preset": true, "deny_read": true, "deny_write": true, "max_lifetime": true, "idle_timeout": true, "retention": true, "start_hooks": true, "readiness": true, "readiness_timeout": true,
		"read_only": true, "read_only_rootfs": true, "no_git_metadata": true, "no_internet": true, "egress_allow_domains": true,
	},
	"build": {"dockerfile": true, "template": true, "tag": true},
}

func Load(dir string) (*Config, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	// Do not silently discard restrictions left in the previous filename.
	legacy := filepath.Join(root, "codex-dock.toml")
	if _, err := os.Lstat(legacy); err == nil {
		return nil, fmt.Errorf("obsolete project config %s: move its settings to %s and remove the old filename before continuing; if both files exist, merge them first", legacy, filepath.Join(root, Filename))
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("checking old project config %s: %w", legacy, err)
	}
	path := filepath.Join(root, Filename)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading project config %s: %w", path, err)
	}
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing project config %s: %w", path, err)
	}
	for section, value := range raw {
		allowed, ok := projectKeys[section]
		if !ok {
			return nil, fmt.Errorf("%s: %q is a host setting or unknown section; use the global config for host settings", path, section)
		}
		entries, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: %q must be a TOML table", path, section)
		}
		for key := range entries {
			if !allowed[key] {
				return nil, fmt.Errorf("%s: unsupported project setting %s.%s", path, section, key)
			}
		}
	}
	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid project config %s: %w", path, err)
	}
	cfg.Root = root
	if _, _, _, err := cfg.Policies(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if _, _, err := workeregress.ResolvePreset(networkpolicy.Rules{}, cfg.Run.PolicyPreset, cfg.Run.EgressAllowDomains); err != nil {
		return nil, err
	}
	if cfg.Run.Image != "" && cfg.Run.Template != "" {
		return nil, fmt.Errorf("%s: choose either run.image or run.template", path)
	}
	if cfg.Run.Agent != "" && !validAgentChoice(cfg.Run.Agent) {
		return nil, fmt.Errorf("%s: run.agent must be shell, codex, or claude", path)
	}
	if cfg.Run.ProfileSize != "" {
		if _, ok := kataguard.LookupResourceProfile(cfg.Run.ProfileSize); !ok {
			return nil, fmt.Errorf("%s: run.profile_size must be standard or small", path)
		}
	}
	if cfg.Build.Dockerfile != "" && cfg.Build.Template != "" {
		return nil, fmt.Errorf("%s: choose either build.dockerfile or build.template", path)
	}
	if cfg.Build.Dockerfile != "" && !filepath.IsLocal(cfg.Build.Dockerfile) {
		return nil, fmt.Errorf("%s: build.dockerfile must be a path inside the project", path)
	}
	if cfg.Build.Dockerfile != "" {
		dockerfile := filepath.Join(root, cfg.Build.Dockerfile)
		resolved, err := filepath.EvalSymlinks(dockerfile)
		if err != nil {
			return nil, fmt.Errorf("%s: build.dockerfile: %w", path, err)
		}
		canonicalRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(canonicalRoot, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("%s: build.dockerfile must resolve inside the project", path)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s: build.dockerfile must be a regular file", path)
		}
	}
	if cfg.Run.ApprovalMode != "" && cfg.Run.ApprovalMode != "suggest" && cfg.Run.ApprovalMode != "auto-edit" {
		return nil, fmt.Errorf("%s: project approval_mode must be suggest or auto-edit; set stronger modes explicitly on the CLI or in the global config", path)
	}
	if cfg.Run.ReadOnly != nil && !*cfg.Run.ReadOnly {
		return nil, fmt.Errorf("%s: project read_only can only be true; a project cannot loosen a host restriction", path)
	}
	if cfg.Run.NoInternet != nil && !*cfg.Run.NoInternet {
		return nil, fmt.Errorf("%s: project no_internet can only be true; a project cannot loosen a host restriction", path)
	}
	if len(cfg.Run.EgressAllowDomains) > 0 {
		if _, err := networkpolicy.Compile(networkpolicy.Rules{AllowDomains: cfg.Run.EgressAllowDomains}); err != nil {
			return nil, fmt.Errorf("%s: run.egress_allow_domains: %w", path, err)
		}
	}
	for _, entry := range []struct{ section, name string }{{"run", cfg.Run.Template}, {"build", cfg.Build.Template}} {
		if entry.name != "" {
			if err := validatePortableTemplate(entry.name); err != nil {
				return nil, fmt.Errorf("%s: %s.template: %w", path, entry.section, err)
			}
		}
	}
	if cfg.Build.Dockerfile != "" && cfg.Build.Tag == "" {
		return nil, fmt.Errorf("%s: set build.tag to run.image for a project Dockerfile", path)
	}
	runImage := cfg.Run.Image
	if cfg.Run.Template != "" {
		tmpl, _ := template.Get(cfg.Run.Template)
		runImage = tmpl.Tag("")
	}
	if name := template.MatchTag(runImage); name != "" && cfg.Build.Dockerfile == "" {
		if err := validatePortableTemplate(name); err != nil {
			return nil, fmt.Errorf("%s: run image: %w", path, err)
		}
	}
	buildImage := cfg.Build.Tag
	if buildImage == "" && cfg.Build.Template != "" {
		tmpl, _ := template.Get(cfg.Build.Template)
		buildImage = tmpl.Tag("")
	}
	if runImage != "" && buildImage != "" && runImage != buildImage {
		return nil, fmt.Errorf("%s: build image %q must match run image %q so a fresh clone builds the image it runs", path, buildImage, runImage)
	}
	if runImage != "" && template.MatchTag(runImage) == "" && cfg.Build.Dockerfile == "" && cfg.Build.Template == "" {
		return nil, fmt.Errorf("%s: custom run.image requires build.dockerfile or build.template and a matching build.tag", path)
	}
	return &cfg, nil
}

func validatePortableTemplate(name string) error {
	visited := make(map[string]bool)
	for name != "" {
		if visited[name] {
			return fmt.Errorf("template %q has a cyclic base image", name)
		}
		visited[name] = true
		current, err := template.Get(name)
		if err != nil {
			return err
		}
		bundled, err := template.BuiltIn(name)
		if err != nil || !bytes.Equal(current.Dockerfile, bundled.Dockerfile) {
			return fmt.Errorf("template %q differs from the bundled version; commit a project Dockerfile instead", name)
		}
		name = template.MatchTag(bundled.BaseImage)
	}
	return nil
}

func validAgentChoice(value string) bool {
	return value == "shell" || value == "codex" || value == "claude"
}

// DockerfileUsesBase reports whether an actual FROM instruction names image.
func DockerfileUsesBase(data []byte, image string) bool {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.EqualFold(fields[0], "FROM") {
			continue
		}
		for _, field := range fields[1:] {
			if strings.HasPrefix(field, "--") {
				continue
			}
			if field == image {
				return true
			}
			break
		}
	}
	return false
}

func (c *Config) Policies() (workerpolicy.Filesystem, workerpolicy.Lifecycle, workerpolicy.Environment, error) {
	f := workerpolicy.Filesystem{DenyRead: c.Run.DenyRead, DenyWrite: c.Run.DenyWrite}
	l := workerpolicy.Lifecycle{ProtectMain: c.Run.ProtectMain}
	e := workerpolicy.Environment{StartHooks: c.Run.StartHooks, Readiness: c.Run.Readiness}
	for _, value := range []struct {
		text   string
		target *time.Duration
	}{{c.Run.MaxLifetime, &l.MaxLifetime}, {c.Run.IdleTimeout, &l.IdleTimeout}, {c.Run.Retention, &l.Retention}, {c.Run.ReadinessTimeout, &e.ReadinessTimeout}} {
		if value.text != "" {
			d, err := time.ParseDuration(value.text)
			if err != nil || d < 0 {
				return f, l, e, fmt.Errorf("invalid project duration %q", value.text)
			}
			*value.target = d
		}
	}
	if err := f.Validate(); err != nil {
		return f, l, e, err
	}
	if err := l.Validate(); err != nil {
		return f, l, e, err
	}
	return f, l, e, e.Validate()
}
