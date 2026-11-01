package taskboard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Serialize lifecycle and review operations across CLI/TUI processes. The
// board's separate optimistic lock protects records; this lock spans runtime
// side effects so stop/retry/test cannot race a pending launch.
func (s Store) operation(ctx context.Context, id string) (func(), error) {
	if !idPattern.MatchString(id) {
		return nil, fmt.Errorf("full task ID required")
	}
	if _, _, e := s.read(); e != nil {
		return nil, e
	}
	if e := os.MkdirAll(s.Root, 0700); e != nil {
		return nil, e
	}
	st, e := os.Lstat(s.Root)
	if e != nil {
		return nil, e
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.IsDir() || st.Mode().Perm()&0077 != 0 || owner.Uid != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("task directory must be owner-private")
	}
	f, e := os.OpenFile(filepath.Join(s.Root, id+".operation"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if e != nil {
		return nil, e
	}
	st, e = f.Stat()
	if e != nil {
		_ = f.Close()
		return nil, e
	}
	owner, ok = st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
		_ = f.Close()
		return nil, fmt.Errorf("unsafe task operation lock")
	}
	for {
		e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if e == nil {
			return func() { _ = f.Close() }, nil
		}
		if e != syscall.EWOULDBLOCK && e != syscall.EAGAIN {
			_ = f.Close()
			return nil, e
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func privateDirectory(dir string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return fmt.Errorf("private directory requires a clean absolute path")
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	st, e := os.Lstat(dir)
	if e != nil {
		return e
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.IsDir() || st.Mode().Perm()&0077 != 0 || owner.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("directory must be owner-private")
	}
	return nil
}
