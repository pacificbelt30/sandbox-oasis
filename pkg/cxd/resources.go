package cxd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/kataguard"
)

// ResourceUsage distinguishes the pinned capacity contract from observed host
// cgroup usage. Peak is optional on older kernels; neither value is guest RSS.
// ResourcePolicy includes optional process and block I/O limits. The existing
// Profile type remains comparable and retains its CPU/memory/PID contract.
type ResourcePolicy = kataguard.ResourceProfile
type BlockIOLimit = kataguard.IOLimit

func (c *Client) ResourcePolicies(ctx context.Context) ([]ResourcePolicy, error) {
	policies, err := c.node.Profiles(ctx)
	if err != nil {
		return nil, classify(err)
	}
	return policies, nil
}

type ResourceUsage struct {
	Policy                 *ResourcePolicy
	Worker                 string
	ObservedAt             time.Time
	Profile                Profile
	HostMemoryCurrentBytes int64
	HostMemoryPeakBytes    int64
	PeakAvailable          bool
	OOMKills               uint64
}

func (c *Client) ResourceUsage(ctx context.Context, worker string) (ResourceUsage, error) {
	w, err := c.Get(ctx, worker)
	if err != nil {
		return ResourceUsage{}, err
	}
	inv, err := c.node.Inventory(ctx)
	if err != nil {
		return ResourceUsage{}, classify(err)
	}
	if !inv.EngineAvailable {
		return ResourceUsage{}, fmt.Errorf("node inventory is unavailable")
	}
	v, err := kataguard.ResolveVM(inv, w.ID)
	if err != nil {
		return ResourceUsage{}, err
	}
	if !v.HostVerified || !v.Populated || len(v.Problems) != 0 {
		return ResourceUsage{}, fmt.Errorf("worker does not have a verified running resource envelope")
	}

	p, ok := kataguard.LookupResourceProfile(v.ResourceProfile)
	if !ok {
		profiles, err := c.node.Profiles(ctx)
		if err != nil {
			return ResourceUsage{}, classify(err)
		}
		for _, candidate := range profiles {
			if candidate.Name == v.ResourceProfile {
				p = candidate
				ok = true
				break
			}
		}
	}
	if !ok {
		return ResourceUsage{}, fmt.Errorf("unknown resource profile")
	}

	current, err := strconv.ParseInt(v.Resources["memory.current"], 10, 64)
	if err != nil || current < 0 {
		return ResourceUsage{}, fmt.Errorf("invalid observed memory usage")
	}
	r := ResourceUsage{Policy: &p, Worker: w.ID, ObservedAt: inv.ObservedAt, Profile: Profile{p.Name, p.CPUCores, p.GuestMemoryBytes, p.HostMemoryBytes, p.PIDLimit}, HostMemoryCurrentBytes: current}
	if peak, ok := v.Resources["memory.peak"]; ok {
		r.HostMemoryPeakBytes, err = strconv.ParseInt(peak, 10, 64)
		if err != nil || r.HostMemoryPeakBytes < 0 {
			return ResourceUsage{}, fmt.Errorf("invalid observed memory peak")
		}
		r.PeakAvailable = true
	}
	for _, line := range strings.Split(v.Resources["memory.events"], "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "oom_kill" {
			r.OOMKills, err = strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return ResourceUsage{}, fmt.Errorf("invalid observed OOM count")
			}
		}
	}
	return r, nil
}
