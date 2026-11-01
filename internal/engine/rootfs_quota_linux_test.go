//go:build linux

package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRootFSQuotaRejectsUnsupportedStorage(t *testing.T) {
	f, err := openQuotaFS(t.TempDir(), ^uint64(0))
	if err == nil {
		_ = f.Close()
		t.Fatal("wrong filesystem identity accepted")
	}
	if err := (&Backend{}).validateRootFSQuota(&record{}); err == nil {
		t.Fatal("unbounded legacy snapshot accepted")
	}
}

// Run explicitly as root on an isolated XFS project-quota mount. The ordinary
// test suite never formats storage or changes mounts to satisfy prerequisites.
func TestRootFSQuotaKernelEnforcement(t *testing.T) {
	root := os.Getenv("OAISO_QUOTA_TEST_ROOT")
	if root == "" {
		t.Skip("requires explicitly supplied isolated XFS project-quota mount")
	}
	f, err := openQuotaFS(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i := uint32(1); i <= 2; i++ {
		project := uint32(os.Getpid())*16 + i
		dir, err := os.MkdirTemp(root, "quota-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dir)
		if err := setProjectQuota(f.Fd(), project, 16<<20, 32); err != nil {
			t.Fatal(err)
		}
		defer setProjectQuota(f.Fd(), project, 0, 0)
		if err := projectDirectory(dir, project, true); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(dir, "output"))
		if err != nil {
			t.Fatal(err)
		}
		if err := unix.Fallocate(int(file.Fd()), 0, 0, 1<<20); err != nil {
			t.Fatal("normal write rejected:", err)
		}
		// XFS project limits return ENOSPC, unlike user/group EDQUOT.
		if err := unix.Fallocate(int(file.Fd()), 0, 0, 32<<20); !errors.Is(err, unix.EDQUOT) && !errors.Is(err, unix.ENOSPC) {
			t.Fatalf("bytes exceeded quota: %v", err)
		}
		_ = file.Close()
		if err := projectDirectory(dir, project, false); err != nil {
			t.Fatal(err)
		}
		var exceeded bool
		for n := 0; n < 40; n++ {
			p, err := os.Create(filepath.Join(dir, fmt.Sprint(n)))
			if errors.Is(err, unix.EDQUOT) || errors.Is(err, unix.ENOSPC) {
				exceeded = true
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			_ = p.Close()
		}
		if !exceeded {
			t.Fatal("inode count exceeded quota")
		}
		q, err := readProjectQuota(f.Fd(), project)
		if err != nil || binary.NativeEndian.Uint64(q[8:16]) != 16<<20/512 || binary.NativeEndian.Uint64(q[24:32]) != 32 {
			t.Fatalf("persistent limits mismatch: %v", err)
		}
		// The next independently charged project must still create/write after
		// this project hits both limits. Reopen verifies the kernel-held policy.
	}
	again, err := openQuotaFS(root, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Close()
}
