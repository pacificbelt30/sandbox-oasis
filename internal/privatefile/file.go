// Package privatefile handles small owner-private local configuration files.
package privatefile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const Limit = 1 << 20

func safe(info os.FileInfo) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && s.Uid == uint32(os.Geteuid()) && s.Nlink == 1 && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0
}

func Read(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if os.IsNotExist(err) {
		return nil, os.ErrNotExist
	}
	if err != nil {
		return nil, fmt.Errorf("private configuration unavailable")
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !safe(info) || info.Size() > Limit {
		return nil, fmt.Errorf("configuration must be an owner-private regular file (0600, at most 1 MiB)")
	}
	data, err := io.ReadAll(io.LimitReader(f, Limit+1))
	if err != nil || len(data) > Limit {
		return nil, fmt.Errorf("private configuration read failed")
	}
	return data, nil
}

func Revision(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// Update uses a cooperative lock plus optimistic revision. The caller retains
// responsibility for keeping the directory outside guest/shared workspaces.
func Update(path, expected string, transform func([]byte) ([]byte, error)) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return fmt.Errorf("private configuration requires a clean absolute path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("private configuration directory unavailable")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return fmt.Errorf("configuration directory must be private (0700)")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("configuration directory has a different owner")
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return fmt.Errorf("private configuration lock unavailable")
	}
	defer func() { _ = lock.Close() }()
	info, err = lock.Stat()
	if err != nil || !safe(info) {
		return fmt.Errorf("unsafe configuration lock")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("configuration lock unavailable")
	}
	data, err := Read(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if Revision(data) != expected {
		return fmt.Errorf("configuration changed; reload before saving")
	}
	output, err := transform(data)
	if err != nil {
		return err
	}
	if len(output) > Limit {
		return fmt.Errorf("private configuration exceeds limit")
	}
	f, err := os.CreateTemp(dir, ".broker-*")
	if err != nil {
		return fmt.Errorf("private configuration staging failed")
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if _, err := f.Write(output); err != nil {
		return fmt.Errorf("private configuration write failed")
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("private configuration sync failed")
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("private configuration close failed")
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return fmt.Errorf("private configuration replacement failed")
	}
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("private configuration directory unavailable after save")
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("private configuration directory sync failed")
	}
	return nil
}
