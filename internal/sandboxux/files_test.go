package sandboxux

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

type fakeFiles struct {
	listing string
	data    map[string]string
	readErr error
}

func (f *fakeFiles) ListFileEntries(_ context.Context, _, _ string, w io.Writer) error {
	_, e := io.WriteString(w, f.listing)
	return e
}
func (f *fakeFiles) ReadFile(_ context.Context, _, name string, w io.Writer) error {
	_, e := io.WriteString(w, f.data[name])
	if e != nil {
		return e
	}
	return f.readErr
}
func (f *fakeFiles) CreateFile(_ context.Context, _, name string, r io.Reader) error {
	if _, ok := f.data[name]; ok {
		return fmt.Errorf("exists")
	}
	b, e := io.ReadAll(r)
	if e == nil {
		f.data[name] = string(b)
	}
	return e
}
func putFile(t *testing.T, p, s string) {
	t.Helper()
	if e := os.WriteFile(p, []byte(s), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestTransfersKeepOriginalsAndNeverPublishPartialDownloads(t *testing.T) {
	root := t.TempDir()
	guest := &fakeFiles{data: map[string]string{"result": "guest bytes"}}
	files := Files{Guest: guest}
	output := filepath.Join(root, "result")
	if e := files.Download(t.Context(), "worker", "result", output); e != nil {
		t.Fatal(e)
	}
	st, e := os.Stat(output)
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("download not private", e)
	}
	if e = files.Download(t.Context(), "worker", "result", output); e == nil {
		t.Fatal("existing host file replaced")
	}
	guest.readErr = fmt.Errorf("connection lost")
	partial := filepath.Join(root, "partial")
	if e = files.Download(t.Context(), "worker", "result", partial); e == nil {
		t.Fatal("partial download accepted")
	}
	if _, e = os.Stat(partial); !os.IsNotExist(e) {
		t.Fatal("failed transfer published output")
	}
	entries, _ := os.ReadDir(root)
	for _, v := range entries {
		if strings.HasPrefix(v.Name(), ".oaiso-download-") {
			t.Fatal("download staging retained")
		}
	}
}
func TestHostTransferRejectsFinalAndAncestorSymlinksAndFIFO(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	source := filepath.Join(outside, "private")
	putFile(t, source, "private")
	if e := os.Symlink(source, filepath.Join(root, "link")); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(root, "dirlink")); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{filepath.Join(root, "link"), filepath.Join(root, "dirlink", "private")} {
		if u, e := StageUpload(p); e == nil {
			_ = u.Close()
			t.Fatal("symlink upload accepted", p)
		}
	}
	files := Files{Guest: &fakeFiles{data: map[string]string{"file": "replacement"}}}
	if e := files.Download(t.Context(), "worker", "file", filepath.Join(root, "dirlink", "new")); e == nil {
		t.Fatal("download followed ancestor symlink")
	}
	if _, e := os.Stat(filepath.Join(outside, "new")); !os.IsNotExist(e) {
		t.Fatal("outside path written")
	}
	fifo := filepath.Join(root, "fifo")
	if e := unix.Mkfifo(fifo, 0600); e != nil {
		t.Fatal(e)
	}
	if u, e := StageUpload(fifo); e == nil {
		_ = u.Close()
		t.Fatal("FIFO upload accepted")
	}
}
func TestUploadUsesReviewedSnapshotAndNewGuestDestination(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	putFile(t, source, "reviewed")
	upload, e := StageUpload(source)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = upload.Close() }()
	putFile(t, source, "changed after review")
	guest := &fakeFiles{data: map[string]string{}}
	files := Files{Guest: guest}
	if e = files.Upload(t.Context(), "worker", "new", upload); e != nil {
		t.Fatal(e)
	}
	if guest.data["new"] != "reviewed" || upload.Size != 8 || len(upload.SHA256) != 64 {
		t.Fatal("upload did not preserve reviewed bytes")
	}
	if e = files.Upload(t.Context(), "worker", "new", upload); e == nil {
		t.Fatal("existing guest file replaced")
	}
}
func TestGuestMetadataAndTextAreBoundedAndNotInterpreted(t *testing.T) {
	guest := &fakeFiles{listing: `[{"name":"../escape","kind":"file","size":1}]`, data: map[string]string{}}
	files := Files{Guest: guest}
	if _, e := files.List(t.Context(), "worker", "."); e == nil {
		t.Fatal("unsafe guest metadata accepted")
	}
	guest.listing = `[{"name":"normal","kind":"file","size":1},{"name":"dir","kind":"directory","size":0}]`
	entries, e := files.List(t.Context(), "worker", ".")
	if e != nil || entries[0].Kind != "directory" {
		t.Fatal(entries, e)
	}
	guest.data["file"] = "[red]literal\x1b[31m\r\n日本語"
	text, e := files.Text(t.Context(), "worker", "file")
	if e != nil || strings.ContainsRune(text.Content, 27) || !strings.Contains(text.Content, "[red]literal\\x1b[31m\n日本語") {
		t.Fatal(text, e)
	}
	guest.data["file"] = "\u009b31m"
	text, e = files.Text(t.Context(), "worker", "file")
	if e != nil || text.Content != "\\x9b31m" || GuestPath("file\u009b", false) == nil {
		t.Fatal("C1 terminal control was accepted", text, e)
	}
	guest.data["file"] = strings.Repeat("a", TextLimit+100)
	text, e = files.Text(t.Context(), "worker", "file")
	if e != nil || !text.Truncated || len(text.Content) != TextLimit {
		t.Fatal("text preview not bounded", len(text.Content), e)
	}
	guest.data["file"] = "binary\x00"
	text, e = files.Text(t.Context(), "worker", "file")
	if e != nil || !text.Binary {
		t.Fatal(text, e)
	}
}
func FuzzGuestPath(f *testing.F) {
	for _, s := range []string{".", "file", "../escape", "sub/../../file", "/etc/passwd"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, p string) {
		if len(p) > 8192 {
			return
		}
		if GuestPath(p, false) == nil {
			unsafe := filepath.IsAbs(p) || p == "."
			for _, part := range strings.Split(p, "/") {
				unsafe = unsafe || part == ".."
			}
			if unsafe {
				t.Fatalf("unsafe path accepted: %q", p)
			}
		}
	})
}
