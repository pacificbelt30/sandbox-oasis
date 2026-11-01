//go:build linux

package engine

import (
	"encoding/binary"
	"fmt"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/storageinfo"
	"golang.org/x/sys/unix"
)

func (b *Backend) storageStatus() storageinfo.Status {
	out := storageinfo.Status{WorkerLimitBytes: rootFSBytes, WorkerLimitInodes: rootFSInodes}
	if b.opts.StorageRoot == "" {
		out.Error = "node has no configured dedicated containerd storage root"
		return out
	}
	f, err := openQuotaFS(b.opts.StorageRoot, 0)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer func() { _ = f.Close() }()
	var fs unix.Statfs_t
	var st, root unix.Stat_t
	if err = unix.Fstatfs(int(f.Fd()), &fs); err == nil {
		err = unix.Fstat(int(f.Fd()), &st)
	}
	if err == nil {
		err = unix.Stat("/", &root)
	}
	if err == nil && (st.Dev == root.Dev || fs.Flags&unix.ST_RDONLY != 0) {
		err = fmt.Errorf("storage must be writable dedicated XFS, separate from the OS filesystem")
	}
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Enforced = true
	out.TotalBytes = fs.Blocks * uint64(fs.Bsize)
	out.AvailableBytes = fs.Bavail * uint64(fs.Bsize)
	return out
}
func (b *Backend) rootFSUsage(r *record) storageinfo.Usage {
	out := storageinfo.Usage{ObservedAt: time.Now().UTC(), ReadOnly: r.Host.ReadonlyRootfs}
	if out.ReadOnly {
		out.Available = true
		return out
	}
	if err := b.validateRootFSQuota(r); err != nil {
		out.Error = err.Error()
		return out
	}
	f, err := openQuotaFS(r.RootFSQuota.Anchor, r.RootFSQuota.Device)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer func() { _ = f.Close() }()
	data, err := readProjectQuota(f.Fd(), r.RootFSQuota.Project)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Available = true
	out.UsedBytes = binary.NativeEndian.Uint64(data[40:48]) * 512
	out.LimitBytes = binary.NativeEndian.Uint64(data[8:16]) * 512
	out.UsedInodes = binary.NativeEndian.Uint64(data[48:56])
	out.LimitInodes = binary.NativeEndian.Uint64(data[24:32])
	return out
}
