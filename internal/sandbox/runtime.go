package sandbox

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/system"
)

const (
	RuntimeKata        = "kata"
	RuntimeRunc        = "runc"
	DefaultKataRuntime = "io.containerd.kata.v2"
	labelIsolation     = labelPrefix + "isolation"
	labelRuntime       = labelPrefix + "runtime"
	labelProxyHosts    = labelPrefix + "proxy-hosts"
)

// ResolveRuntime deliberately has no automatic fallback to the daemon default.
func ResolveRuntime(isolation, registration string) (string, string, error) {
	if isolation == "" {
		isolation = RuntimeKata
	}
	switch isolation {
	case RuntimeKata:
		if registration == "" {
			registration = DefaultKataRuntime
		}
		if registration == "runc" || strings.ContainsAny(registration, " /\\\r\n\t\x00") {
			return "", "", fmt.Errorf("invalid Kata runtime registration %q", registration)
		}
	case RuntimeRunc:
		registration = "runc"
	default:
		return "", "", fmt.Errorf("invalid runtime %q: choose kata or runc", isolation)
	}
	return isolation, registration, nil
}

// CheckRuntime verifies the local Kata node and pinned daemon/assets. Every
// worker additionally passes host-process and guest checks before release.
// Explicit runc compatibility retains the containerd node service.
func (m *Manager) CheckRuntime(ctx context.Context, opts RunOptions) error {
	isolation, registration, err := m.checkRuntimeMetadata(ctx, opts)
	if err != nil {
		return err
	}
	if isolation == RuntimeKata {
		guard, err := m.kataNode()
		if err != nil {
			return err
		}
		return guard.Validate(ctx, registration)
	}
	return nil
}

// Metadata checks precede either standalone validation or the combined
// asset/host verification used by Exec. Neither path releases user code early.
func (m *Manager) checkRuntimeMetadata(ctx context.Context, opts RunOptions) (string, string, error) {
	isolation, registration, err := ResolveRuntime(opts.Runtime, opts.KataRuntime)
	if err != nil {
		return "", "", err
	}
	profile, err := m.resourceProfile(ctx, opts.ResourceProfile)
	if err != nil {
		return "", "", err
	}
	if isolation != RuntimeKata && profile.Name != "standard" {
		return "", "", fmt.Errorf("invalid resource profile %q for %s runtime", opts.ResourceProfile, isolation)
	}
	info, err := m.cli.Info(ctx)
	if err != nil {
		return "", "", fmt.Errorf("checking containerd runtime: %w", err)
	}
	if err := validateRuntime(info, isolation, registration); err != nil {
		return "", "", err
	}
	if isolation == RuntimeKata {
		if info.CgroupDriver != "cgroupfs" || info.CgroupVersion != "2" {
			return "", "", fmt.Errorf("kata requires a cgroup v2 daemon using cgroupfs")
		}
	}
	return isolation, registration, nil
}

func validateRuntime(info system.Info, isolation, registration string) error {
	if info.OSType != "linux" {
		return fmt.Errorf("%s workers require a Linux execution node", isolation)
	}
	r, ok := info.Runtimes[registration]
	if !ok {
		return fmt.Errorf("runtime %q is not registered on the containerd node; configure Kata/KVM on the execution host (see doc/kata.md); no fallback was attempted", registration)
	}
	if isolation == RuntimeKata {
		// Node API versions differ in the metadata they return. An explicit
		// non-Kata entry must never be accepted, even under a Kata alias.
		if r.Path != "" && filepath.Base(r.Path) != "containerd-shim-kata-v2" && r.Path != DefaultKataRuntime {
			return fmt.Errorf("runtime %q does not point to the Kata v2 shim", registration)
		}
		if r.Type != "" && r.Type != DefaultKataRuntime && filepath.Base(r.Type) != "containerd-shim-kata-v2" {
			return fmt.Errorf("runtime %q has a non-Kata runtimeType", registration)
		}
		if registration != DefaultKataRuntime && r.Path == "" && r.Type == "" {
			return fmt.Errorf("the node does not expose the type of Kata alias %q; use %s or a daemon that exposes registration metadata", registration, DefaultKataRuntime)
		}
	}
	return nil
}

func applyRuntime(hc *container.HostConfig, isolation, registration string, proxyHosts []string) {
	hc.Runtime = registration
	if isolation == RuntimeKata {
		// Preserve the validated per-network proxy mapping for inspection and
		// recovery. Product proxy URLs use the pinned IP because guest hosts
		// mounts are not reliable on every Kata node setup.
		hc.ExtraHosts = proxyHosts
		hc.DNS = []string{"127.0.0.1"}
	}
}
