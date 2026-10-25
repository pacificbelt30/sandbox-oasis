package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types/mount"
	"slices"
	"sort"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/network"
)

// Recheck on creation and every restart. A changed proxy IP is a reason to
// recreate the worker, not to silently change the network of an existing VM.
func (m *Manager) validateKataWorker(ctx context.Context, id string) error {
	v, err := m.cli.ContainerInspect(ctx, id)
	if err != nil {
		return err
	}
	if v.Config == nil || v.HostConfig == nil || v.Config.Labels[labelIsolation] != RuntimeKata || v.HostConfig.Runtime != v.Config.Labels[labelRuntime] {
		return fmt.Errorf("kata runtime identity mismatch")
	}
	h := v.HostConfig
	g, err := m.kataNode()
	if err != nil {
		return err
	}
	parent, err := g.Parent(v.Config.Labels[labelAllocation])
	if err != nil {
		return err
	}
	if v.Config.Labels[labelProfile] != g.Stamp() || h.CgroupParent != parent || len(v.Config.Entrypoint) != 3 || v.Config.Entrypoint[0] != guestGuard || v.Config.Entrypoint[1] != "wait" || v.Config.Entrypoint[2] != v.Config.Labels[labelAllocation] {
		return fmt.Errorf("kata worker requires the pinned verification bootstrap; recreate it")
	}
	profile, err := m.resourceProfile(ctx, v.Config.Labels[labelPrefix+"resource-profile"])
	if err != nil {
		return err
	}
	if h.Memory != profile.GuestMemoryBytes || h.MemorySwap != profile.GuestMemoryBytes || h.NanoCPUs != int64(profile.CPUCores)*1_000_000_000 || h.PidsLimit == nil || *h.PidsLimit != profile.PIDLimit {
		return fmt.Errorf("kata workload resource limits changed")
	}
	if profile.FileDescriptorLimit != 0 && (len(h.Ulimits) != 1 || h.Ulimits[0] == nil || h.Ulimits[0].Name != "nofile" || h.Ulimits[0].Soft != int64(profile.FileDescriptorLimit) || h.Ulimits[0].Hard != int64(profile.FileDescriptorLimit)) {
		return fmt.Errorf("kata file descriptor limit changed")
	}
	if _, err := validateSavedTemporaryStorage(v, profile.GuestMemoryBytes); err != nil {
		return err
	}
	launcher := false
	var userMounts []mount.Mount
	for _, bind := range h.Mounts {
		if bind.Target == guestGuard && bind.Source == g.Launcher() && bind.ReadOnly {
			launcher = true
		} else {
			userMounts = append(userMounts, bind)
		}
	}
	if err := m.protectKataMounts(userMounts); err != nil {
		return err
	}
	if !launcher || h.Tmpfs["/run/codex-dock-control"] == "" {
		return fmt.Errorf("trusted Kata bootstrap mount is missing")
	}
	if v.Config.Labels[labelPrefix+"read-only-rootfs"] == "true" {
		if !h.ReadonlyRootfs || h.Tmpfs["/tmp"] == "" || h.Tmpfs["/var/tmp"] == "" {
			return fmt.Errorf("read-only rootfs setting changed")
		}
	}
	if h.Privileged || len(h.CapAdd) != 0 || !slices.Contains(h.CapDrop, "ALL") || !slices.Contains(h.SecurityOpt, "no-new-privileges:true") {
		return fmt.Errorf("kata worker security settings changed")
	}
	if !slices.Equal(h.DNS, []string{"127.0.0.1"}) || len(h.PortBindings) != 0 || h.PublishAllPorts {
		return fmt.Errorf("kata worker must not expose ports or use external DNS")
	}
	if string(h.NetworkMode) != network.WorkerNetworkName(strings.TrimPrefix(v.Name, "/")) {
		return fmt.Errorf("kata worker network identity mismatch")
	}
	netID := v.Config.Labels[labelPrefix+"network-id"]
	if netID == "" {
		return fmt.Errorf("kata worker is missing its network ID")
	}
	if v.NetworkSettings != nil {
		if v.State != nil && v.State.Running && len(v.NetworkSettings.Networks) != 1 {
			return fmt.Errorf("running Kata worker must have exactly one network")
		}
		for name, endpoint := range v.NetworkSettings.Networks {
			// Docker can leave the endpoint ID empty until the first start,
			// and again after stop. The configured network name must still
			// match; a live worker must expose the exact materialized ID.
			if name != string(h.NetworkMode) || endpoint == nil || (endpoint.NetworkID != "" && endpoint.NetworkID != netID) || (v.State != nil && v.State.Running && endpoint.NetworkID == "") {
				return fmt.Errorf("kata worker has an unexpected attached network")
			}
		}
	}
	var proxies []string
	if err := json.Unmarshal([]byte(v.Config.Labels[labelProxyHosts]), &proxies); err != nil {
		return fmt.Errorf("invalid Kata proxy identity record: %w", err)
	}
	netMgr := m.network
	if netMgr == nil {
		netMgr = network.NewManagerWithClient(m.cli)
	}
	actual, err := netMgr.KataProxyHosts(ctx, netID, v.ID, proxies)
	if err != nil {
		return err
	}
	expected := slices.Clone(h.ExtraHosts)
	sort.Strings(expected)
	if !slices.Equal(actual, expected) {
		return fmt.Errorf("kata proxy addresses changed; recreate the worker to refresh its pinned hosts")
	}
	return nil
}
