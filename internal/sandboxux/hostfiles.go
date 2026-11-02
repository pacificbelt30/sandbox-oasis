package sandboxux

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Open each parent through its held descriptor. Neither a final symlink nor a
// swapped ancestor may redirect a transfer into another host directory.
func hostParent(name string) (*os.File, string, error) {
	abs, err := filepath.Abs(name)
	if err != nil {
		return nil, "", err
	}
	if filepath.Clean(abs) != abs || abs == "/" || strings.ContainsRune(abs, 0) {
		return nil, "", fmt.Errorf("choose a regular host file path")
	}
	parts := strings.Split(strings.TrimPrefix(abs, "/"), "/")
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", err
	}
	for _, part := range parts[:len(parts)-1] {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if e != nil {
			return nil, "", fmt.Errorf("host parent must be a real directory without symlinks: %w", e)
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), "transfer-directory"), parts[len(parts)-1], nil
}
func openHostSource(name string) (*os.File, error) {
	dir, base, err := hostParent(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	fd, err := unix.Openat(int(dir.Fd()), base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("upload source must be a regular file without symlinks")
	}
	return f, nil
}
func createHostFile(name string, write func(io.Writer) error) error {
	dir, base, err := hostParent(name)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	temp := ".oaiso-download-" + hex.EncodeToString(random[:])
	fd, err := unix.Openat(int(dir.Fd()), temp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), temp)
	defer func() { _ = f.Close(); _ = unix.Unlinkat(int(dir.Fd()), temp, 0) }()
	if err = write(f); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = unix.Linkat(int(dir.Fd()), temp, int(dir.Fd()), base, 0); err != nil {
		return fmt.Errorf("download requires a new host file: %w", err)
	}
	return dir.Sync()
}
