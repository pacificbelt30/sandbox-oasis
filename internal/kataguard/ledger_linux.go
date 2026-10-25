//go:build linux

package kataguard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type allocationRecord struct {
	Version                                  int
	Allocation, Worker, Stamp, Name, Profile string
	CreatedAt                                time.Time
	Budget                                   *ResourceProfile `json:",omitempty"`
}

func (g *Guard) admissionLock(ctx context.Context) (func(), error) {
	f, err := os.OpenFile(filepath.Join(g.config.StateDir, "admission.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	for {
		if err = ctx.Err(); err != nil {
			_ = f.Close()
			return nil, err
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			_ = f.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
func (g *Guard) recordDir() (string, error) {
	dir := filepath.Join(g.config.StateDir, "allocations")
	if err := os.Mkdir(dir, 0700); err != nil {
		if !os.IsExist(err) {
			return "", err
		}
	} else if err := syncDirectory(g.config.StateDir); err != nil {
		return "", err
	}
	return dir, trustedPath(dir, true)
}
func (g *Guard) readRecord(id string) (allocationRecord, error) {
	var r allocationRecord
	if !groupID.MatchString(id) {
		return r, fmt.Errorf("invalid allocation")
	}
	dir, err := g.recordDir()
	if err != nil {
		return r, err
	}
	path := filepath.Join(dir, id+".json")
	if err = trustedPath(path, false); err != nil {
		return r, err
	}
	f, err := os.Open(path)
	if err != nil {
		return r, err
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(io.LimitReader(f, 4096))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&r); err != nil {
		return r, err
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return r, fmt.Errorf("trailing allocation data")
	}
	if r.Version != 1 || r.Allocation != id || r.Stamp != g.Stamp() || r.CreatedAt.IsZero() || r.Worker != "" && !containerID.MatchString(r.Worker) {
		return r, fmt.Errorf("untrusted allocation record")
	}
	profile, ok := g.resourceProfile(r.Profile)
	if !ok {
		return r, fmt.Errorf("unknown allocation resource profile; restore its pinned definition before recovery")
	}
	_, builtin := LookupResourceProfile(r.Profile)
	if !builtin && r.Budget == nil || r.Budget != nil && !r.Budget.Equal(profile) {
		return r, fmt.Errorf("allocation resource profile definition changed; restore its pinned definition")
	}

	return r, nil
}
func syncDirectory(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}
func (g *Guard) writeRecord(r allocationRecord) error {
	if !groupID.MatchString(r.Allocation) {
		return fmt.Errorf("invalid allocation")
	}
	dir, err := g.recordDir()
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".allocation-")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if err = json.NewEncoder(f).Encode(r); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), filepath.Join(dir, r.Allocation+".json")); err != nil {
		return err
	}
	return syncDirectory(dir)
}
func (g *Guard) removeRecord(id string) error {
	if !groupID.MatchString(id) {
		return fmt.Errorf("invalid allocation")
	}
	dir, err := g.recordDir()
	if err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(dir, id+".json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return syncDirectory(dir)
}
func (g *Guard) trackAllocation(ctx context.Context, id, worker string, requested ...string) error {
	r, err := g.readRecord(id)
	if os.IsNotExist(err) {
		profile := "standard"
		if len(requested) != 0 {
			profile = requested[0]
		}
		budget, ok := g.resourceProfile(profile)
		if !ok {
			return fmt.Errorf("unknown allocation resource profile")
		}
		r = allocationRecord{Version: 1, Allocation: id, Stamp: g.Stamp(), Profile: budget.Name, CreatedAt: time.Now().UTC()}
		if _, builtin := LookupResourceProfile(budget.Name); !builtin {
			r.Budget = &budget
		}
	} else if err != nil {
		return err
	}
	if len(requested) != 0 && requested[0] != "" {
		existing, _ := g.resourceProfile(r.Profile)
		if requested[0] != existing.Name {
			return fmt.Errorf("allocation resource profile changed")
		}
	}
	if r.Worker != "" && worker != "" && r.Worker != worker {
		return fmt.Errorf("allocation is bound to another worker")
	}
	if worker != "" {
		cli, err := g.engineClient()
		if err != nil {
			return err
		}
		info, err := cli.ContainerInspect(ctx, worker)
		_ = cli.Close()
		if err != nil {
			return err
		}
		parent, _ := g.Parent(id)
		profile, _ := g.resourceProfile(r.Profile)
		if info.Config == nil || info.HostConfig == nil || info.Config.Labels[managedLabel] != "true" || info.Config.Labels[isolationLabel] != "kata" || info.Config.Labels[allocationLabel] != id || info.Config.Labels[profileLabel] != g.Stamp() || info.Config.Labels[resourceProfileLabel] != profile.Name || info.HostConfig.CgroupParent != parent || info.HostConfig.Runtime != g.manifest.Runtime || info.HostConfig.Memory != profile.GuestMemoryBytes || info.HostConfig.MemorySwap != profile.GuestMemoryBytes || info.HostConfig.NanoCPUs != int64(profile.CPUCores)*1_000_000_000 || info.HostConfig.PidsLimit == nil || *info.HostConfig.PidsLimit != profile.PIDLimit {
			return fmt.Errorf("containerd worker does not own the verified allocation")
		}
		r.Worker = worker
		r.Name = strings.TrimPrefix(info.Name, "/")
	}
	return g.writeRecord(r)
}
