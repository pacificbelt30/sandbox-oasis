package main

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// Runs in the pinned bootstrap before any image entrypoint/user code. Older
// mke2fs releases can set root ownership but cannot set root permissions.
func prepareAgentState() error {
	if os.Getenv("OAISO_AGENT_STATE_DISK") != "1" {
		return nil
	}
	f, err := os.OpenFile("/var/lib/oaiso-agent-state", os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var filesystem unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &filesystem); err != nil {
		return err
	}
	required := int64(unix.ST_NOSUID | unix.ST_NODEV | unix.ST_NOEXEC)
	info, err := f.Stat()
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if filesystem.Type != unix.EXT4_SUPER_MAGIC || filesystem.Flags&required != required || filesystem.Flags&unix.ST_RDONLY != 0 || !ok || owner.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("agent state requires a private owned writable ext4 mount with nosuid,nodev,noexec")
	}
	return errors.Join(f.Chmod(0700), f.Sync())
}
