//go:build linux

package engine

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/containerd/errdefs"
	"golang.org/x/sys/unix"
)

const rootFSBytes uint64 = 4 << 30
const rootFSInodes uint64 = 65536
const rootFSReserve uint64 = 1 << 30
const projectInherit uint32 = 0x200

type rootFSQuota struct {
	Project             uint32
	Device              uint64
	Anchor, Upper, Work string
}

// Linux XFS UAPI: fs_disk_quota (112 bytes), fs_quota_statv and fsxattr.
// quotactl_fd binds every query to the opened filesystem, avoiding device-name
// lookup and mount aliases. Only accounting AND enforcement are accepted.
func quotaCall(fd uintptr, command uint32, project uint32, data []byte) error {
	_, _, errno := unix.Syscall6(unix.SYS_QUOTACTL_FD, fd, uintptr(command<<8|2), uintptr(project), uintptr(unsafe.Pointer(&data[0])), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func openQuotaFS(path string, device uint64) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	var fs unix.Statfs_t
	var stat unix.Stat_t
	err = unix.Fstatfs(int(f.Fd()), &fs)
	if err == nil {
		err = unix.Fstat(int(f.Fd()), &stat)
	}
	if err == nil && (fs.Type != unix.XFS_SUPER_MAGIC || device != 0 && uint64(stat.Dev) != device) {
		err = fmt.Errorf("rootfs requires dedicated XFS storage with project quotas enabled")
	}
	var status [160]byte
	status[0] = 1
	if err == nil {
		err = quotaCall(f.Fd(), 0x5808, 0, status[:])
	}
	if err == nil && binary.NativeEndian.Uint16(status[2:4])&0x30 != 0x30 {
		err = fmt.Errorf("XFS project quota accounting and enforcement must both be enabled")
	}
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("rootfs quota unavailable: %w", err)
	}
	return f, nil
}

func readProjectQuota(fd uintptr, project uint32) ([112]byte, error) {
	var data [112]byte
	data[0], data[1] = 1, 2
	binary.NativeEndian.PutUint32(data[4:8], project)
	err := quotaCall(fd, 0x5803, project, data[:])
	return data, err
}

func setProjectQuota(fd uintptr, project uint32, bytes, inodes uint64) error {
	var data [112]byte
	data[0], data[1] = 1, 2
	binary.NativeEndian.PutUint16(data[2:4], 15)
	binary.NativeEndian.PutUint32(data[4:8], project)
	binary.NativeEndian.PutUint64(data[8:16], bytes/512)
	binary.NativeEndian.PutUint64(data[16:24], bytes/512)
	binary.NativeEndian.PutUint64(data[24:32], inodes)
	binary.NativeEndian.PutUint64(data[32:40], inodes)
	return quotaCall(fd, 0x5804, project, data[:])
}

func projectDirectory(path string, project uint32, assign bool) error {
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var attr [28]byte
	ioctl := func(command uintptr) error {
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), command, uintptr(unsafe.Pointer(&attr[0])))
		if errno != 0 {
			return errno
		}
		return nil
	}
	if err := ioctl(0x801c581f); err != nil {
		return err
	}
	if assign {
		entries, err := f.ReadDir(1)
		if len(entries) != 0 || err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("quota assignment requires an empty new snapshot directory")
		}
		if binary.NativeEndian.Uint32(attr[12:16]) != 0 {
			return fmt.Errorf("snapshot already has a project identity")
		}
		binary.NativeEndian.PutUint32(attr[12:16], project)
		binary.NativeEndian.PutUint32(attr[0:4], binary.NativeEndian.Uint32(attr[0:4])|projectInherit)
		if err := ioctl(0x401c5820); err != nil {
			return err
		}
		if err := ioctl(0x801c581f); err != nil {
			return err
		}
	}
	if binary.NativeEndian.Uint32(attr[12:16]) != project || binary.NativeEndian.Uint32(attr[0:4])&projectInherit == 0 {
		return fmt.Errorf("snapshot project identity or inheritance changed")
	}
	return nil
}

func (b *Backend) provisionRootFSQuota(ctx context.Context, r *record) error {
	mounts, err := b.client.SnapshotService(b.opts.Snapshotter).Mounts(ctx, r.ID)
	if err != nil {
		return err
	}
	var upper, work string
	if len(mounts) != 1 || mounts[0].Type != "overlay" {
		return fmt.Errorf("bounded writable rootfs requires the overlayfs snapshotter")
	}
	for _, option := range mounts[0].Options {
		if strings.HasPrefix(option, "upperdir=") {
			upper = strings.TrimPrefix(option, "upperdir=")
		}
		if strings.HasPrefix(option, "workdir=") {
			work = strings.TrimPrefix(option, "workdir=")
		}
	}
	if !filepath.IsAbs(upper) || filepath.Clean(upper) != upper || filepath.Dir(upper) != filepath.Dir(work) || filepath.Base(upper) != "fs" || filepath.Base(work) != "work" {
		return fmt.Errorf("unexpected overlay snapshot layout")
	}
	anchor := filepath.Dir(filepath.Dir(upper))
	f, err := openQuotaFS(anchor, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var stat unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &stat); err != nil {
		return err
	}
	if err := b.admitRootFS(ctx, r.ID, f, uint64(stat.Dev)); err != nil {
		return err
	}
	for attempt := 0; attempt < 32; attempt++ {
		var random [4]byte
		if _, err := rand.Read(random[:]); err != nil {
			return err
		}
		project := binary.NativeEndian.Uint32(random[:]) & 0x7fffffff
		if project == 0 {
			continue
		}
		q, err := readProjectQuota(f.Fd(), project)
		if err != nil && !errors.Is(err, unix.ENOENT) && !errors.Is(err, unix.ESRCH) {
			return err
		}
		if err == nil && (binary.NativeEndian.Uint64(q[8:16]) != 0 || binary.NativeEndian.Uint64(q[24:32]) != 0 || binary.NativeEndian.Uint64(q[40:48]) != 0 || binary.NativeEndian.Uint64(q[48:56]) != 0) {
			continue
		}
		r.RootFSQuota = &rootFSQuota{project, uint64(stat.Dev), anchor, upper, work}
		// Persist identity before imposing/assigning the quota for retry safety.
		if err := b.save(r); err != nil {
			return err
		}
		if err := setProjectQuota(f.Fd(), project, rootFSBytes, rootFSInodes); err != nil {
			return err
		}
		if err := projectDirectory(upper, project, true); err != nil {
			return err
		}
		if err := projectDirectory(work, project, true); err != nil {
			return err
		}
		return b.validateRootFSQuota(r)
	}
	return fmt.Errorf("cannot allocate an unused rootfs quota identity")
}

func (b *Backend) validateRootFSQuota(r *record) error {
	q := r.RootFSQuota
	if q == nil || q.Project == 0 {
		return fmt.Errorf("worker has no enforced rootfs quota; migrate dedicated storage and recreate this worker")
	}
	f, err := openQuotaFS(q.Anchor, q.Device)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	data, err := readProjectQuota(f.Fd(), q.Project)
	if err != nil {
		return err
	}
	if binary.NativeEndian.Uint64(data[8:16]) != rootFSBytes/512 || binary.NativeEndian.Uint64(data[24:32]) != rootFSInodes {
		return fmt.Errorf("rootfs quota limits changed")
	}
	if err := projectDirectory(q.Upper, q.Project, false); err != nil {
		return err
	}
	return projectDirectory(q.Work, q.Project, false)
}

// The engine lifecycle mutex remains held until task creation. Count full
// ceilings for native tasks, including stopped tasks not yet deleted. Avoid
// subtracting sampled project usage: guests can change it between samples.
// Retained snapshots without a task use actual disk space but no future-write
// reservation; starting them must pass this check again.
func (b *Backend) admitRootFS(ctx context.Context, id string, f *os.File, device uint64) error {
	all, err := b.client.Containers(ctx, `labels."`+owned+`"=="true"`)
	if err != nil {
		return err
	}
	reserved := rootFSBytes + rootFSReserve
	for _, c := range all {
		if c.ID() == id {
			continue
		}
		other, _, err := b.load(ctx, c.ID())
		if err != nil {
			return err
		}
		if other.RootFSQuota == nil || other.RootFSQuota.Device != device {
			continue
		}
		if _, err := c.Task(ctx, nil); errdefs.IsNotFound(err) {
			continue
		} else if err != nil {
			return err
		}
		if reserved > ^uint64(0)-rootFSBytes {
			return fmt.Errorf("rootfs quota reservations overflow")
		}
		reserved += rootFSBytes
	}
	var fs unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &fs); err != nil {
		return err
	}
	if fs.Bavail < (reserved+uint64(fs.Bsize)-1)/uint64(fs.Bsize) {
		return fmt.Errorf("insufficient rootfs capacity after active task reservations and host reserve")
	}
	return nil
}

func (b *Backend) admitRootFSStart(ctx context.Context, r *record) error {
	f, err := openQuotaFS(r.RootFSQuota.Anchor, r.RootFSQuota.Device)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return b.admitRootFS(ctx, r.ID, f, r.RootFSQuota.Device)
}
