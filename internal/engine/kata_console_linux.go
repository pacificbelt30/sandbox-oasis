//go:build linux

package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Kata 4.3 leaves its runtime sockets behind after abrupt supervisor death.
// Only the node's owned, independently attested empty envelope may reach this
// cleanup, after containerd has confirmed that its previous task is absent.
func (b *Backend) cleanupKataSockets(ctx context.Context, r *record) error {
	if r.Host.Runtime == "runc" || r.StartedAt.IsZero() {
		return nil
	}
	if !hexID.MatchString(r.ID) {
		return fmt.Errorf("invalid Kata console identity")
	}
	found := false
	for _, path := range []string{"console.sock", "shim-monitor.sock", "root/kata.hvsock"} {
		if _, err := os.Lstat(filepath.Join("/run/kata", r.ID, path)); err == nil {
			found = true
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if !found {
		return nil
	}
	if b.opts.ConfirmStopped == nil {
		return fmt.Errorf("kata restart requires stopped-envelope verifier")
	}
	if err := b.opts.ConfirmStopped(ctx, r.Config.Labels["codex-dock.allocation"], r.ID); err != nil {
		return err
	}
	return removeStaleKataSockets("/run/kata", r.ID)
}

func removeStaleKataSockets(root, id string) error {
	if !hexID.MatchString(id) {
		return fmt.Errorf("invalid Kata console identity")
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err == unix.ENOENT {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	trusted := func(fd int) error {
		var st unix.Stat_t
		if err := unix.Fstat(fd, &st); err != nil {
			return err
		}
		if st.Uid != uint32(os.Geteuid()) || st.Mode&0022 != 0 {
			return fmt.Errorf("untrusted Kata console directory")
		}
		return nil
	}
	if err := trusted(fd); err != nil {
		return err
	}
	dir, err := unix.Openat(fd, id, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err == unix.ENOENT {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(dir) }()
	if err := trusted(dir); err != nil {
		return err
	}
	type socket struct {
		dir        int
		name, path string
	}
	base := filepath.Join(root, id)
	candidates := []socket{{dir, "console.sock", filepath.Join(base, "console.sock")}, {dir, "shim-monitor.sock", filepath.Join(base, "shim-monitor.sock")}}
	guest, err := unix.Openat(dir, "root", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil && err != unix.ENOENT {
		return err
	}
	if err == nil {
		defer func() { _ = unix.Close(guest) }()
		if err := trusted(guest); err != nil {
			return err
		}
		candidates = append(candidates, socket{guest, "kata.hvsock", filepath.Join(base, "root", "kata.hvsock")})
	}
	// Preflight all entries before removing any of them. Other runtime data,
	// including state.json, is retained. No recursive directory removal.
	var stale []socket
	for _, candidate := range candidates {
		var st unix.Stat_t
		if err := unix.Fstatat(candidate.dir, candidate.name, &st, unix.AT_SYMLINK_NOFOLLOW); err == unix.ENOENT {
			continue
		} else if err != nil {
			return err
		}
		if st.Mode&unix.S_IFMT != unix.S_IFSOCK || st.Uid != uint32(os.Geteuid()) {
			return fmt.Errorf("unexpected Kata socket entry")
		}
		stale = append(stale, candidate)
	}
	// Even an owned empty envelope is insufficient if another live endpoint
	// remains. Never unlink a live or foreign runtime listener.
	table, err := os.ReadFile("/proc/net/unix")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(table), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		for _, candidate := range stale {
			if fields[7] == candidate.path {
				return fmt.Errorf("kata socket still has a live endpoint")
			}
		}
	}
	for _, candidate := range stale {
		if err := unix.Unlinkat(candidate.dir, candidate.name, 0); err != nil {
			return err
		}
	}
	return nil
}
