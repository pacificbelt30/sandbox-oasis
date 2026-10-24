// Package workerpolicy defines opt-in, portable worker lifecycle restrictions.
package workerpolicy

import (
	"encoding/hex"
	"fmt"
	"path"
	"strings"
	"time"
)

type Filesystem struct {
	DenyRead  []string `json:"deny_read,omitempty" toml:"deny_read"`
	DenyWrite []string `json:"deny_write,omitempty" toml:"deny_write"`
}

func (f Filesystem) Validate() error {
	for _, paths := range [][]string{f.DenyRead, f.DenyWrite} {
		for _, p := range paths {
			if !strings.HasPrefix(p, "/workspace/") || path.Clean(p) != p || strings.ContainsAny(p, "\x00\n\r") {
				return fmt.Errorf("file restrictions require clean paths below /workspace: %q", p)
			}
		}
	}
	return nil
}

type Lifecycle struct {
	ProtectMain bool          `json:"protect_main,omitempty"`
	MaxLifetime time.Duration `json:"max_lifetime,omitempty"`
	IdleTimeout time.Duration `json:"idle_timeout,omitempty"`
	Retention   time.Duration `json:"retention,omitempty"`
}

func (l Lifecycle) Validate() error {
	for _, d := range []time.Duration{l.MaxLifetime, l.IdleTimeout, l.Retention} {
		if d < 0 {
			return fmt.Errorf("lifecycle durations cannot be negative")
		}
	}
	return nil
}

// Environment settings run inside the worker, after authentication setup and
// before the requested command. Commands are explicit argv, never host shells.
type Environment struct {
	ImageID          string        `json:"image_id,omitempty"`
	StartHooks       [][]string    `json:"start_hooks,omitempty"`
	Readiness        []string      `json:"readiness,omitempty"`
	ReadinessTimeout time.Duration `json:"readiness_timeout,omitempty"`
}

func (e Environment) Validate() error {
	if e.ImageID != "" && (!strings.HasPrefix(e.ImageID, "sha256:") || len(e.ImageID) != 71) {
		return fmt.Errorf("image ID must be sha256:<64 hex digits>")
	}
	if e.ImageID != "" {
		if _, err := hex.DecodeString(strings.TrimPrefix(e.ImageID, "sha256:")); err != nil {
			return fmt.Errorf("invalid image digest")
		}
	}
	if e.ReadinessTimeout < 0 {
		return fmt.Errorf("readiness timeout cannot be negative")
	}
	if len(e.Readiness) > 0 && e.Readiness[0] == "" {
		return fmt.Errorf("readiness probe requires executable argv")
	}
	for _, cmd := range e.StartHooks {
		if len(cmd) == 0 || cmd[0] == "" {
			return fmt.Errorf("start hook requires executable argv")
		}
	}
	return nil
}
