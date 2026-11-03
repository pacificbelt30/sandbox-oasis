package sandboxux

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/privatefile"
	"github.com/pacificbelt30/codex-dock/internal/strictjson"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
)

// Environment deliberately excludes identities, credentials, project/clone
// paths and extra mounts. Loading one never launches code or enables AI.
type Environment struct {
	Image, Command, Profile, Network, Workspace string
	Domains                                     []string
	ReadOnly, ReadOnlyRoot                      *bool
	Lifecycle                                   workerpolicy.Lifecycle
	LifecycleSet                                bool
}
type EnvironmentFile struct {
	Version      int                    `json:"version"`
	Environments map[string]Environment `json:"environments"`
}
type EnvironmentSnapshot struct {
	File     EnvironmentFile
	Revision string
}
type EnvironmentStore struct{ Path string }

var environmentName = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}\p{M}_.-]{0,63}$`)

func DefaultEnvironmentStore() (*EnvironmentStore, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	return &EnvironmentStore{Path: filepath.Join(dir, "codex-dock", "sandbox-environments", "config.json")}, nil
}
func EnvironmentFrom(s LaunchSpec) Environment {
	return Environment{Image: s.Image, Command: s.Command, Profile: s.Profile, Network: s.Network, Workspace: s.Workspace, Domains: append([]string(nil), s.Domains...), ReadOnly: s.ReadOnly, ReadOnlyRoot: s.ReadOnlyRoot, Lifecycle: s.Lifecycle, LifecycleSet: s.LifecycleSet}
}
func (e Environment) Apply(s LaunchSpec) LaunchSpec {
	s.Image, s.Command, s.Profile, s.Network, s.Workspace = e.Image, e.Command, e.Profile, e.Network, e.Workspace
	s.Domains, s.ReadOnly, s.ReadOnlyRoot, s.Lifecycle = append([]string(nil), e.Domains...), e.ReadOnly, e.ReadOnlyRoot, e.Lifecycle
	s.LifecycleSet = e.LifecycleSet
	// A saved command always goes through the normal launch review. Credentials
	// and mounts are not inherited from an earlier form or from the saved file.
	s.AI, s.Mounts, s.CloneDir, s.CloneRevision = false, nil, "", ""
	return s
}
func (e Environment) validate() error {
	if len(e.Command) > 32768 || len(e.Image) > 512 || len(e.Profile) > 128 || len(e.Domains) > 64 || e.Lifecycle.Validate() != nil || bytes.IndexByte([]byte(e.Command), 0) >= 0 {
		return fmt.Errorf("invalid environment settings")
	}
	if e.Network != "offline" && e.Network != "host" && e.Network != "dependencies" && e.Network != "allowlist" {
		return fmt.Errorf("invalid environment network mode")
	}
	if e.Workspace != "" && e.Workspace != "shared" && e.Workspace != "copy" {
		return fmt.Errorf("invalid environment workspace mode")
	}
	for _, d := range e.Domains {
		if len(d) == 0 || len(d) > 253 || bytes.ContainsAny([]byte(d), "\x00\r\n\t ") {
			return fmt.Errorf("invalid environment domain")
		}
	}
	if e.Network == "allowlist" && len(e.Domains) == 0 {
		return fmt.Errorf("explicit domains are required")
	}
	return nil
}
func (s *EnvironmentStore) Load() (EnvironmentSnapshot, error) {
	data, err := privatefile.Read(s.Path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return EnvironmentSnapshot{}, err
	}
	f := EnvironmentFile{Version: 1, Environments: map[string]Environment{}}
	if len(data) > 0 {
		if err = strictjson.Decode(bytes.NewReader(data), privatefile.Limit, &f); err != nil {
			return EnvironmentSnapshot{}, fmt.Errorf("invalid saved environments: %w", err)
		}
	}
	if err = validateEnvironmentFile(f); err != nil {
		return EnvironmentSnapshot{}, err
	}
	return EnvironmentSnapshot{File: f, Revision: privatefile.Revision(data)}, nil
}
func validateEnvironmentFile(f EnvironmentFile) error {
	if f.Version != 1 || f.Environments == nil || len(f.Environments) > 32 {
		return fmt.Errorf("saved environments require version 1 and at most 32 entries")
	}
	var seen []string
	for name, e := range f.Environments {
		if !environmentName.MatchString(name) {
			return fmt.Errorf("environment names use 1–64 letters, digits, dots, underscores or hyphens")
		}
		for _, previous := range seen {
			if strings.EqualFold(name, previous) {
				return fmt.Errorf("environment names must be unique ignoring case")
			}
		}
		seen = append(seen, name)
		if err := e.validate(); err != nil {
			return err
		}
	}
	return nil
}
func (s *EnvironmentStore) Save(expected string, f EnvironmentFile) error {
	if err := validateEnvironmentFile(f); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return privatefile.Update(s.Path, expected, func([]byte) ([]byte, error) { return append(data, '\n'), nil })
}
func (s EnvironmentSnapshot) Names() []string {
	names := make([]string, 0, len(s.File.Environments))
	for n := range s.File.Environments {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
