//go:build linux

package kataguard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
)

const managedLabel = "codex-dock.managed"
const isolationLabel = "codex-dock.isolation"
const allocationLabel = "codex-dock.allocation"
const profileLabel = "codex-dock.verified-profile"
const resourceProfileLabel = "codex-dock.resource-profile"

func (g *Guard) engineClient() (*client.Client, error) {
	return client.NewClientWithOpts(client.WithHost(g.config.Endpoint), client.WithAPIVersionNegotiation())
}
func (g *Guard) containerdVMs(ctx context.Context) ([]VM, error) {
	cli, err := g.engineClient()
	if err != nil {
		return nil, err
	}
	defer func() { _ = cli.Close() }()
	rows, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", managedLabel+"=true"), filters.Arg("label", isolationLabel+"=kata"))})
	if err != nil {
		return nil, err
	}
	var vms []VM
	for _, row := range rows {
		v, err := cli.ContainerInspect(ctx, row.ID)
		if errdefs.IsNotFound(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if v.Config == nil || v.HostConfig == nil || v.Config.Labels[managedLabel] != "true" || v.Config.Labels[isolationLabel] != "kata" {
			return nil, fmt.Errorf("worker identity changed while inspecting %s", row.ID)
		}
		profileName := v.Config.Labels[resourceProfileLabel]
		profile, profileOK := g.resourceProfile(profileName)
		vm := VM{ID: v.ID, ContainerID: v.ID, ContainerPresent: true, Name: strings.TrimPrefix(v.Name, "/"), Allocation: v.Config.Labels[allocationLabel], ResourceProfile: profile.Name}
		if v.State != nil {
			vm.EngineState = v.State.Status
		}
		vm.CreatedAt, _ = time.Parse(time.RFC3339Nano, v.Created)
		parent, err := g.Parent(vm.Allocation)
		if err != nil || !profileOK || v.HostConfig.Memory != profile.GuestMemoryBytes || v.HostConfig.MemorySwap != profile.GuestMemoryBytes || v.HostConfig.NanoCPUs != int64(profile.CPUCores)*1_000_000_000 || v.HostConfig.PidsLimit == nil || *v.HostConfig.PidsLimit != profile.PIDLimit || v.HostConfig.CgroupParent != parent || v.HostConfig.Runtime != g.manifest.Runtime || v.Config.Labels[profileLabel] != g.Stamp() {
			vm.Problems = append(vm.Problems, "containerd worker does not match the node's allocation/profile")
		}
		vms = append(vms, vm)
	}
	return vms, nil
}

func (g *Guard) observeEnvelope(ctx context.Context, id string) VM {
	path, _ := g.group(id)
	vm := VM{ID: id, Allocation: id, Cgroup: path, Resources: map[string]string{}}
	record, err := g.readRecord(id)
	if err == nil {
		vm.Tracked = true
		profile, _ := g.resourceProfile(record.Profile)
		vm.ResourceProfile = profile.Name
		vm.Name = record.Name
		vm.ContainerID = record.Worker
		vm.CreatedAt = record.CreatedAt
	} else if !os.IsNotExist(err) {
		vm.Problems = append(vm.Problems, err.Error())
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return vm
	}
	if err != nil {
		vm.Problems = append(vm.Problems, err.Error())
		return vm
	}
	if !info.IsDir() || !rootOwner(info) || info.Mode().Perm()&0022 != 0 {
		vm.Problems = append(vm.Problems, "untrusted host envelope")
		return vm
	}
	vm.Allocated = true
	if !vm.Tracked {
		vm.Problems = append(vm.Problems, "allocation has no trusted reservation record")
	}
	data, err := os.ReadFile(filepath.Join(path, "cgroup.events"))
	if err != nil {
		vm.Problems = append(vm.Problems, err.Error())
	} else {
		fields := strings.Fields(string(data))
		found := false
		for i := 0; i+1 < len(fields); i += 2 {
			if fields[i] == "populated" {
				found = true
				vm.Populated = fields[i+1] == "1"
				if fields[i+1] != "0" && fields[i+1] != "1" {
					vm.Problems = append(vm.Problems, "invalid populated state")
				}
			}
		}
		if !found {
			vm.Problems = append(vm.Problems, "missing populated state")
		}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		vm.Problems = append(vm.Problems, err.Error())
	} else {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if entry.Name() == "supervisor" {
				continue
			}
			worker := strings.TrimPrefix(entry.Name(), "kata_")
			if !containerID.MatchString(worker) {
				vm.Problems = append(vm.Problems, "unknown child cgroup")
				continue
			}
			if vm.ContainerID != "" && vm.ContainerID != worker {
				vm.Problems = append(vm.Problems, "conflicting worker identity in envelope")
			} else {
				vm.ContainerID = worker
			}
		}
	}
	for _, name := range []string{"cpu.max", "memory.max", "memory.current", "memory.peak", "memory.swap.max", "pids.max", "pids.current", "memory.events"} {
		b, err := os.ReadFile(filepath.Join(path, name))
		if err != nil {
			if name == "memory.peak" && os.IsNotExist(err) {
				continue
			}
			vm.Problems = append(vm.Problems, err.Error())
		} else {
			vm.Resources[name] = strings.TrimSpace(string(b))
		}
	}
	for _, name := range []string{"io.max", "io.stat"} {
		if data, err := os.ReadFile(filepath.Join(path, name)); err == nil {
			vm.Resources[name] = strings.TrimSpace(string(data))
		}
	}
	if vm.Populated {
		vm.Processes, err = g.scanProcesses(ctx, id, vm.ContainerID, true)
		vm.HostVerified = err == nil
	} else {
		err = g.checkLimits(path)
	}
	if err != nil {
		vm.Problems = append(vm.Problems, err.Error())
	}
	return vm
}

// Inventory reconciles containerd's durable records with the dedicated cgroup tree
// and reservation ledger. containerd unavailability is UNKNOWN, never orphanhood.
func (g *Guard) Inventory(ctx context.Context) (Inventory, error) {
	unlock, err := g.admissionLock(ctx)
	if err != nil {
		return Inventory{}, err
	}
	defer unlock()
	return g.inventoryUnlocked(ctx)
}
func (g *Guard) inventoryUnlocked(ctx context.Context) (Inventory, error) {
	result := Inventory{ObservedAt: time.Now().UTC(), VMs: []VM{}}
	hosts := map[string]VM{}
	ids := map[string]bool{}
	entries, err := os.ReadDir(g.config.CgroupRoot)
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "cdx-") {
			id := strings.TrimPrefix(entry.Name(), "cdx-")
			if !entry.IsDir() || !groupID.MatchString(id) {
				result.Problems = append(result.Problems, "unexpected allocation entry: "+entry.Name())
				continue
			}
			ids[id] = true
		}
	}
	dir, err := g.recordDir()
	if err != nil {
		return result, err
	}
	records, err := os.ReadDir(dir)
	if err != nil {
		return result, err
	}
	for _, entry := range records {
		if strings.HasSuffix(entry.Name(), ".json") {
			id := strings.TrimSuffix(entry.Name(), ".json")
			if !groupID.MatchString(id) {
				result.Problems = append(result.Problems, "unexpected allocation record")
				continue
			}
			ids[id] = true
		}
	}
	for id := range ids {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		hosts[id] = g.observeEnvelope(ctx, id)
	}
	workers, err := g.containerdVMs(ctx)
	result.EngineAvailable = err == nil
	if err != nil {
		result.Problems = append(result.Problems, "containerd inventory unavailable: "+err.Error())
	}
	if err := g.Validate(ctx, g.manifest.Runtime); err != nil {
		result.Problems = append(result.Problems, "node verification: "+err.Error())
		for id, h := range hosts {
			h.HostVerified = false
			h.Problems = append(h.Problems, "node assets or daemon verification failed")
			hosts[id] = h
		}
	}
	result.VMs = reconcileVMs(hosts, workers, result.EngineAvailable)
	return result, nil
}

// RecoverOrphan affects only the exact reservation with no remaining containerd
// object. A populated envelope additionally requires --force and reattestation
// of every occupant; cgroup.kill avoids PID reuse and process-tree escapes.
func (g *Guard) RecoverOrphan(ctx context.Context, id string, force bool) error {
	unlock, err := g.admissionLock(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	inventory, err := g.inventoryUnlocked(ctx)
	if err != nil {
		return err
	}
	if !inventory.EngineAvailable {
		return fmt.Errorf("cannot recover an orphan while containerd is unavailable")
	}
	vm, err := ResolveVM(inventory, id)
	if err != nil {
		return err
	}
	if err = canRecoverOrphan(vm, force, time.Now()); err != nil {
		return err
	}
	// A successful list alone is insufficient: recheck the exact immutable ID
	// immediately before changing host resources.
	if vm.ContainerID != "" {
		cli, err := g.engineClient()
		if err != nil {
			return err
		}
		_, err = cli.ContainerInspect(ctx, vm.ContainerID)
		_ = cli.Close()
		if !errdefs.IsNotFound(err) {
			return fmt.Errorf("orphan recovery requires containerd to confirm the exact worker is absent")
		}
	}
	if err = g.Validate(ctx, g.manifest.Runtime); err != nil {
		return err
	}
	if vm.Populated {
		if _, err = g.scanProcesses(ctx, vm.Allocation, vm.ContainerID, false); err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(vm.Cgroup, "cgroup.kill"), []byte("1"), 0600); err != nil {
			return err
		}
	}
	return g.releaseUnlocked(ctx, vm.Allocation, vm.ContainerID)
}
