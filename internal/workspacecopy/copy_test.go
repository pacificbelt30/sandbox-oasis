package workspacecopy_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/managedworkspace"
	"github.com/pacificbelt30/codex-dock/internal/workspacecopy"
)

func TestCurrentFilesCopyExcludesSecretsLinksAndCaches(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string]string{"tracked": "uncommitted", "untracked": "present", "日本語.txt": "[red]literal", ".env": "secret", ".git/config": "git", "node_modules/pkg": "cache"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(root, "untracked"), filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	plan, err := workspacecopy.Prepare(t.Context(), root, 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Manifest.Files) != 2 || len(plan.Skipped) != 6 {
		t.Fatal(plan)
	}
	var archive bytes.Buffer
	if err = plan.Write(t.Context(), &archive); err != nil {
		t.Fatal(err)
	}
	options := managedworkspace.Options{Name: "test", Bytes: 64 << 20, Inodes: 1024, UID: uint32(os.Getuid()), GID: uint32(os.Getgid())}
	target := t.TempDir()
	manifest, err := managedworkspace.ExtractSeed(t.Context(), &archive, target, options)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ID != plan.Manifest.ID {
		t.Fatal("review changed")
	}
	for _, f := range manifest.Files {
		got, e := os.ReadFile(filepath.Join(target, f.Path))
		if e != nil || len(got) != int(f.Bytes) {
			t.Fatal(f, e)
		}
	}
	if _, e := os.Lstat(filepath.Join(target, "escape")); !os.IsNotExist(e) {
		t.Fatal("link imported")
	}
	if e := os.WriteFile(filepath.Join(root, "tracked"), []byte("same-length!"), 0600); e != nil {
		t.Fatal(e)
	}
	if err = plan.Write(t.Context(), &bytes.Buffer{}); err == nil {
		t.Fatal("changed source accepted")
	}
}

func TestReviewedSourceCannotBecomeSymlinkOrGrow(t *testing.T) {
	for _, kind := range []string{"link", "parent", "grow"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			if e := os.Mkdir(filepath.Join(root, "dir"), 0700); e != nil {
				t.Fatal(e)
			}
			p := filepath.Join(root, "dir", "file")
			if e := os.WriteFile(p, []byte("base"), 0600); e != nil {
				t.Fatal(e)
			}
			plan, e := workspacecopy.Prepare(t.Context(), root, 1024)
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "link":
				os.Remove(p)
				os.Symlink("/etc/passwd", p)
			case "parent":
				os.Rename(filepath.Join(root, "dir"), filepath.Join(root, "old"))
				os.Symlink(filepath.Join(root, "old"), filepath.Join(root, "dir"))
			case "grow":
				os.WriteFile(p, []byte("different data"), 0600)
			}
			if e = plan.Write(t.Context(), &bytes.Buffer{}); e == nil {
				t.Fatal("source replacement followed")
			}
		})
	}
}

func TestSeedRejectsArchiveLinksTraversalAndUnreviewedFiles(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "ok"), []byte("data"), 0600)
	p, e := workspacecopy.Prepare(t.Context(), root, 1024)
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"symlink", "traversal", "hash", "extra", "metadata"} {
		t.Run(kind, func(t *testing.T) {
			var buf bytes.Buffer
			w := tar.NewWriter(&buf)
			data, _ := json.Marshal(p.Manifest)
			w.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0600, Size: int64(len(data)), Typeflag: tar.TypeReg})
			w.Write(data)
			h := &tar.Header{Name: "files/ok", Mode: 0644, Size: 4, Typeflag: tar.TypeReg}
			body := "data"
			switch kind {
			case "symlink":
				h.Typeflag = tar.TypeSymlink
				h.Linkname = "/etc/passwd"
				h.Size = 0
				body = ""
			case "traversal":
				h.Name = "files/../outside"
			case "hash":
				body = "evil"
			case "metadata":
				h.PAXRecords = map[string]string{"SCHILY.xattr.security.capability": "evil"}
			}
			w.WriteHeader(h)
			w.Write([]byte(body))
			if kind == "extra" {
				w.WriteHeader(&tar.Header{Name: "files/extra", Mode: 0644, Size: 0})
			}
			w.Close()
			o := managedworkspace.Options{Name: "test", Bytes: 64 << 20, Inodes: 1024, UID: uint32(os.Getuid()), GID: uint32(os.Getgid())}
			if _, e := managedworkspace.ExtractSeed(t.Context(), &buf, t.TempDir(), o); e == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}

func TestCopyLimitsAndCancellation(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "file"), []byte("data"), 0600)
	if _, err := workspacecopy.Prepare(t.Context(), root, 3); err == nil {
		t.Fatal("byte limit ignored")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := workspacecopy.Prepare(ctx, root, 1024); err == nil {
		t.Fatal("cancel ignored")
	}
	if _, err := workspacecopy.Prepare(t.Context(), root, -1); err == nil {
		t.Fatal("negative limit")
	}
}

func FuzzSeedManifest(f *testing.F) {
	f.Add(`{"id":"` + strings.Repeat("a", 64) + `","files":[],"bytes":0}`)
	f.Add(`{"files":[{"path":"../escape"}],"bytes":0}`)
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 1<<20 {
			return
		}
		var m managedworkspace.Manifest
		if json.Unmarshal([]byte(raw), &m) != nil {
			return
		}
		m.ID = m.Digest()
		if err := m.Validate(32 << 20); err == nil {
			for _, entry := range m.Files {
				if managedworkspace.FilePath(entry.Path) != nil || managedworkspace.Excluded(entry.Path) != "" {
					t.Fatal("unsafe path accepted")
				}
			}
		}
	})
}
