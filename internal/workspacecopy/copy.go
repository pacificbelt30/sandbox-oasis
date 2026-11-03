// Package workspacecopy prepares reviewed regular-file copies without invoking Git.
package workspacecopy

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/pacificbelt30/codex-dock/internal/managedworkspace"
	"golang.org/x/sys/unix"
)

type Skipped struct{ Path, Reason string }
type Plan struct {
	Source   string
	Manifest managedworkspace.Manifest
	Skipped  []Skipped
}

func directory(source string) (*os.File, error) {
	p, err := filepath.Abs(source)
	if err != nil {
		return nil, err
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		if part == "" {
			continue
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if e != nil {
			return nil, e
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), p), nil
}
func regularAt(parent *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	stat, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || stat.Nlink != 1 {
		_ = f.Close()
		return nil, fmt.Errorf("source must be a regular file with no hardlinks")
	}
	return f, nil
}
func openRelative(source, rel string) (*os.File, error) {
	if err := managedworkspace.FilePath(rel); err != nil {
		return nil, err
	}
	parent, err := directory(filepath.Join(source, filepath.Dir(rel)))
	if err != nil {
		return nil, err
	}
	defer func() { _ = parent.Close() }()
	return regularAt(parent, filepath.Base(rel))
}
func Prepare(ctx context.Context, source string, limit int64) (Plan, error) {
	p, err := filepath.Abs(source)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Source: p, Manifest: managedworkspace.Manifest{Files: []managedworkspace.File{}}}
	if limit < 0 || limit > managedworkspace.MaxSeedBytes {
		return plan, fmt.Errorf("invalid copy byte limit")
	}
	root, err := directory(p)
	if err != nil {
		return plan, err
	}
	defer func() { _ = root.Close() }()
	count := 0
	var walk func(*os.File, string, int) error
	walk = func(dir *os.File, prefix string, depth int) error {
		if depth > 64 {
			return fmt.Errorf("source directory nesting exceeds 64")
		}
		for {
			entries, err := dir.ReadDir(128)
			if err != nil && err != io.EOF {
				return err
			}
			for _, entry := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				count++
				if len(plan.Skipped) >= managedworkspace.MaxFiles {
					return fmt.Errorf("too many excluded paths")
				}
				if count > 100000 {
					return fmt.Errorf("source scan exceeds 100000 entries")
				}
				rel := entry.Name()
				if prefix != "" {
					rel = prefix + "/" + rel
				}
				if e := managedworkspace.FilePath(rel); e != nil {
					return e
				}
				reason := managedworkspace.Excluded(rel)
				if entry.Type()&os.ModeSymlink != 0 {
					reason = "symlink"
				}
				if reason != "" {
					plan.Skipped = append(plan.Skipped, Skipped{rel, reason})
					if len(plan.Skipped) > managedworkspace.MaxFiles {
						return fmt.Errorf("too many excluded paths")
					}
					continue
				}
				if entry.IsDir() {
					fd, e := unix.Openat(int(dir.Fd()), entry.Name(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
					if e != nil {
						return e
					}
					child := os.NewFile(uintptr(fd), rel)
					e = walk(child, rel, depth+1)
					_ = child.Close()
					if e != nil {
						return e
					}
					continue
				}
				file, e := regularAt(dir, entry.Name())
				if e != nil {
					plan.Skipped = append(plan.Skipped, Skipped{rel, "special file/hardlink"})
					continue
				}
				st, e := file.Stat()
				if e != nil {
					_ = file.Close()
					return e
				}
				if st.Size() > managedworkspace.MaxFileBytes {
					_ = file.Close()
					plan.Skipped = append(plan.Skipped, Skipped{rel, "file exceeds 64 MiB"})
					continue
				}
				hash := sha256.New()
				n, e := io.Copy(hash, io.LimitReader(&contextReader{ctx, file}, managedworkspace.MaxFileBytes+1))
				_ = file.Close()
				if e != nil {
					return e
				}
				if n != st.Size() {
					return fmt.Errorf("source changed during copy review: %s", rel)
				}
				mode := uint32(0644)
				if st.Mode().Perm()&0111 != 0 {
					mode = 0755
				}
				plan.Manifest.Files = append(plan.Manifest.Files, managedworkspace.File{Path: rel, Bytes: n, Mode: mode, SHA256: hex.EncodeToString(hash.Sum(nil))})
				plan.Manifest.Bytes += n
				if len(plan.Manifest.Files) > managedworkspace.MaxFiles || plan.Manifest.Bytes > limit {
					return fmt.Errorf("selected files exceed workspace seed byte/file limit; choose a smaller source")
				}
			}
			if err == io.EOF {
				return nil
			}
		}
	}
	if err = walk(root, "", 0); err != nil {
		return plan, err
	}
	sort.Slice(plan.Manifest.Files, func(i, j int) bool { return plan.Manifest.Files[i].Path < plan.Manifest.Files[j].Path })
	sort.Slice(plan.Skipped, func(i, j int) bool { return plan.Skipped[i].Path < plan.Skipped[j].Path })
	plan.Manifest.ID = plan.Manifest.Digest()
	return plan, plan.Manifest.Validate(limit)
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}

// Write reopens every component without following symlinks and rechecks each
// reviewed hash while streaming. No temporary copy is published on the host.
func (p Plan) Write(ctx context.Context, out io.Writer) error {
	if err := p.Manifest.Validate(managedworkspace.MaxSeedBytes); err != nil {
		return err
	}
	writer := tar.NewWriter(out)
	data, err := json.Marshal(p.Manifest)
	if err != nil {
		return err
	}
	if err = writer.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(data))}); err != nil {
		return err
	}
	if _, err = writer.Write(data); err != nil {
		return err
	}
	for _, entry := range p.Manifest.Files {
		file, err := openRelative(p.Source, entry.Path)
		if err != nil {
			return err
		}
		st, err := file.Stat()
		if err != nil || st.Size() != entry.Bytes {
			_ = file.Close()
			return fmt.Errorf("source changed after review: %s", entry.Path)
		}
		if err = writer.WriteHeader(&tar.Header{Name: "files/" + entry.Path, Mode: int64(entry.Mode), Size: entry.Bytes}); err != nil {
			_ = file.Close()
			return err
		}
		hash := sha256.New()
		_, err = io.CopyN(io.MultiWriter(writer, hash), &contextReader{ctx, file}, entry.Bytes)
		_ = file.Close()
		if err != nil {
			return err
		}
		if hex.EncodeToString(hash.Sum(nil)) != entry.SHA256 {
			return fmt.Errorf("source changed after review: %s", entry.Path)
		}
	}
	return writer.Close()
}
