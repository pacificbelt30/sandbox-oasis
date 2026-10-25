//go:build linux

package kataguard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// ConfirmStopped only reads the attested allocation. Do not take admissionLock:
// engine callers hold its mutex while inventory can hold the admission lock.
func (g *Guard) ConfirmStopped(ctx context.Context, allocation, worker string) error {
	if err := g.Validate(ctx, g.manifest.Runtime); err != nil {
		return err
	}
	r, err := g.readRecord(allocation)
	if err != nil {
		return err
	}
	if r.Worker != worker || worker == "" {
		return fmt.Errorf("stopped allocation ownership mismatch")
	}
	processes, err := g.scanProcesses(ctx, allocation, worker, false)
	if err != nil {
		return err
	}
	if len(processes) != 0 {
		return fmt.Errorf("kata envelope still has processes")
	}
	path, err := g.group(allocation)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(path, "cgroup.events"))
	if err != nil {
		return err
	}
	confirmed := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "populated" {
			if confirmed || len(fields) != 2 || fields[1] != "0" {
				return fmt.Errorf("kata envelope is not confirmed empty")
			}
			confirmed = true
		}
	}
	if !confirmed {
		return fmt.Errorf("kata envelope is not confirmed empty")
	}
	return nil
}

// Retire prepares the supervisor before user code is released or stop is sent,
// and is safe to repeat after exit. Kata 4.2 may destroy its own sandbox cgroup
// while serving Wait, before reporting the guest's exit. A sibling leaf
// empties that sandbox while keeping every thread inside the SAME CPU/memory/
// swap/PID envelope. This never moves a process outside its allocation.
//
// The engine serializes task mutations. Do not take admissionLock here: an
// inventory request can hold it while waiting for the engine's task lock.
func (g *Guard) Retire(ctx context.Context, allocation, worker string) error {
	if err := g.Validate(ctx, g.manifest.Runtime); err != nil {
		return err
	}
	return g.retireVerified(ctx, allocation, worker)
}

// Only call after full asset/runtime validation in this operation. Standalone
// engine stop/delete callbacks must continue to use Retire, including validation.
func (g *Guard) retireVerified(ctx context.Context, allocation, worker string) error {
	r, err := g.readRecord(allocation)
	if err != nil {
		return err
	}
	if r.Worker != "" && r.Worker != worker {
		return fmt.Errorf("retirement allocation ownership mismatch")
	}
	processes, err := g.scanProcesses(ctx, allocation, worker, false)
	if err != nil {
		return err
	}
	if len(processes) == 0 {
		return nil
	}
	parent, err := g.group(allocation)
	if err != nil {
		return err
	}
	target := filepath.Join(parent, "supervisor")
	if err = os.Mkdir(target, 0755); err != nil && !os.IsExist(err) {
		return err
	}
	if err = trustedPath(target, true); err != nil {
		return err
	}
	for _, process := range processes {
		if process.Kind != "runtime" && process.Kind != "logger" {
			return fmt.Errorf("unexpected Dragonball occupant")
		}
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", process.PID))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(data)) == "0::"+strings.TrimPrefix(target, "/sys/fs/cgroup") {
			continue
		}
		if err = g.moveRetiredProcess(ctx, allocation, worker, process.PID, target); err != nil {
			return err
		}
	}

	return g.checkLimits(parent)
}

func (g *Guard) moveRetiredProcess(ctx context.Context, allocation, worker string, pid int, target string) error {
	fd, err := unix.PidfdOpen(pid, 0)
	if err == unix.ESRCH {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	// Reattest this exact occupant after opening its pidfd. A process from the
	// initial scan may have exited; never stop or move a replacement elsewhere.
	processes, err := g.scanProcesses(ctx, allocation, worker, false)
	if err != nil {
		return err
	}
	found := false
	for _, process := range processes {
		if process.PID == pid {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	if err = unix.PidfdSendSignal(fd, unix.SIGSTOP, nil, 0); err == unix.ESRCH {
		return nil
	} else if err != nil {
		return err
	}
	defer func() { _ = unix.PidfdSendSignal(fd, unix.SIGCONT, nil, 0) }()
	if _, err = g.scanProcesses(ctx, allocation, worker, false); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(target, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0600); err != nil {
		return err
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(data)) != "0::"+strings.TrimPrefix(target, "/sys/fs/cgroup") {
		return fmt.Errorf("retired process escaped its envelope")
	}
	return nil
}
