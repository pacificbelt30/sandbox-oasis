//go:build linux

package diskcheckpoint

import (
	"archive/tar"
	"context"
	"fmt"
	continuityfs "github.com/containerd/continuity/fs"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

type boundedWriter struct {
	out    io.Writer
	max, n int64
	ctx    context.Context
}

func (w *boundedWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(p)) > w.max-w.n {
		return 0, fmt.Errorf("checkpoint exceeds byte limit")
	}
	n, err := w.out.Write(p)
	w.n += int64(n)
	return n, err
}

type inode struct{ dev, ino uint64 }

func key(info fs.FileInfo) (inode, error) {
	s, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return inode{}, fmt.Errorf("unsupported filesystem metadata")
	}
	return inode{uint64(s.Dev), s.Ino}, nil
}

// Archive reads an immutable, read-only rootfs, never executes image programs,
// never follows symlink entries and never visits host bind mounts. Hard links
// to excluded files are also excluded. All ordinary hard links are materialized
// independently so no tar link can reintroduce an excluded target.
func Archive(ctx context.Context, directory string, out io.Writer, options Options) (size int64, entries int, resultErr error) {

	// Guest paths may traverse directory symlinks (for example /var/run).
	// Exclude both their names and their resolved targets inside this rootfs.
	paths := append([]string(nil), options.Exclude...)
	for _, excluded := range paths {
		resolved, err := continuityfs.RootPath(directory, excluded)
		if err != nil {
			return 0, 0, fmt.Errorf("cannot resolve checkpoint exclusion")
		}
		rel, err := filepath.Rel(directory, resolved)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
			return 0, 0, fmt.Errorf("checkpoint exclusion resolves to filesystem root or outside it")
		}
		options.Exclude = append(options.Exclude, "/"+filepath.ToSlash(rel))
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = root.Close() }()
	denied := map[inode]bool{}
	count := 0
	// Inspect metadata even below excluded directories to detect aliases. No
	// excluded regular-file content is opened or copied.
	err = fs.WalkDir(&boundedTree{root: root}, ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > MaxEntries {
			return fmt.Errorf("checkpoint exceeds entry limit")
		}
		if Excluded(name, options.Exclude) && d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			k, err := key(info)
			if err != nil {
				return err
			}
			denied[k] = true
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	w := &boundedWriter{out: out, max: options.MaxBytes, ctx: ctx}
	tw := tar.NewWriter(w)
	err = fs.WalkDir(&boundedTree{root: root}, ".", func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if name == "." {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		// Keep only top-level excluded directories and immediate home skeletons,
		// with their original ownership, so a fresh non-root boot has a valid HOME.
		excluded := Excluded(name, options.Exclude)
		skeleton := info.IsDir() && (strings.Count(name, "/") == 0 || strings.HasPrefix(name, "home/") && strings.Count(name, "/") == 1)
		if excluded && !skeleton {
			return nil
		}
		if !info.Mode().IsRegular() && !info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		if info.Mode().IsRegular() {
			k, err := key(info)
			if err != nil {
				return err
			}
			if denied[k] {
				return nil
			}
		}
		link := ""
		if info.Mode()&os.ModeSymlink != 0 {
			link, err = root.Readlink(name)
			if err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(name)
		header.Uname, header.Gname = "", ""
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		entries++
		if info.Mode().IsRegular() {
			f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			stat, err := f.Stat()
			if err != nil {
				_ = f.Close()
				return err
			}
			if !os.SameFile(info, stat) || !stat.Mode().IsRegular() {
				_ = f.Close()
				return fmt.Errorf("checkpoint source changed")
			}
			_, err = io.CopyN(tw, f, info.Size())
			closeErr := f.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	})
	if err != nil {
		return w.n, entries, err
	}
	if err := tw.Close(); err != nil {
		return w.n, entries, err
	}
	return w.n, entries, nil
}

// Bound directory enumeration before allocating all entries, even when one
// guest directory contains more entries than the total checkpoint allowance.
type boundedTree struct {
	root *os.Root
	read int
}

func (t *boundedTree) Open(name string) (fs.File, error) { return t.root.Open(name) }
func (t *boundedTree) ReadDir(name string) ([]fs.DirEntry, error) {
	f, err := t.root.OpenFile(name, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var all []fs.DirEntry
	for {
		entries, err := f.ReadDir(256)
		t.read += len(entries)
		if t.read > MaxEntries {
			return nil, fmt.Errorf("checkpoint exceeds entry limit")
		}
		all = append(all, entries...)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Name() < all[j].Name() })
	return all, nil
}
