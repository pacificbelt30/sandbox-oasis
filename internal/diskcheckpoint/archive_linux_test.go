//go:build linux

package diskcheckpoint

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{"opt/data", "home/user/.codex", "workspace", "run", "tmp", "etc"} {
		if err := os.MkdirAll(filepath.Join(root, p), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for p, data := range map[string]string{"opt/data/value": "disk-content", "home/user/.codex/auth.json": "must-not-copy", "workspace/source": "must-not-copy", "run/token": "must-not-copy", "tmp/history": "must-not-copy", "etc/resolv.conf": "must-not-copy"} {
		if err := os.WriteFile(filepath.Join(root, p), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func TestArchiveExcludesCredentialsMountsAndHardlinkAliases(t *testing.T) {
	root := fixture(t)
	if err := os.Link(filepath.Join(root, "home/user/.codex/auth.json"), filepath.Join(root, "opt/data/alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("value", filepath.Join(root, "opt/data/link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/shadow", filepath.Join(root, "opt/data/outside")); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(root, "opt/data/pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	options, _ := Normalize(Options{}, nil)
	var output bytes.Buffer
	size, entries, err := Archive(context.Background(), root, &output, options)
	if err != nil || size != int64(output.Len()) || entries == 0 {
		t.Fatal(size, entries, err)
	}
	found := map[string]string{}
	reader := tar.NewReader(bytes.NewReader(output.Bytes()))
	for {
		h, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		found[h.Name] = string(data)
		if strings.Contains(string(data), "must-not-copy") {
			t.Fatal("secret copied", h.Name)
		}
		if h.Name == "opt/data/link" && h.Linkname != "value" {
			t.Fatal("symlink changed")
		}
	}
	if found["opt/data/value"] != "disk-content" {
		t.Fatal("rootfs data lost")
	}
	for _, p := range []string{"opt/data/alias", "opt/data/pipe", "home/user/.codex/auth.json", "workspace/source", "etc/resolv.conf"} {
		if _, ok := found[p]; ok {
			t.Fatal("excluded entry copied", p)
		}
	}
	if _, ok := found["home/user"]; !ok {
		t.Fatal("fresh non-root HOME skeleton missing")
	}
}
func TestArchiveLimitsCancellationAndAdditionalExclusions(t *testing.T) {
	root := fixture(t)
	options, _ := Normalize(Options{Exclude: []string{"/opt/data"}}, nil)
	var output bytes.Buffer
	if _, _, err := Archive(context.Background(), root, &output, options); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output.Bytes(), []byte("disk-content")) {
		t.Fatal("explicit exclusion ignored")
	}
	options, _ = Normalize(Options{}, nil)
	options.MaxBytes = 512
	output.Reset()
	if _, _, err := Archive(context.Background(), root, &output, options); err == nil || output.Len() > 512 {
		t.Fatal("byte limit ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	output.Reset()
	if _, _, err := Archive(ctx, root, &output, options); err == nil || output.Len() != 0 {
		t.Fatal("canceled archive produced content")
	}
	for _, p := range []string{"/", "/opt/../secret", "relative", "/secret\n"} {
		if _, err := Normalize(Options{Exclude: []string{p}}, nil); err == nil {
			t.Fatal("invalid exclusion accepted", p)
		}
	}
}

func TestExclusionTraversingGuestSymlinkAlsoExcludesItsRealTarget(t *testing.T) {
	root := fixture(t)
	if err := os.Symlink("opt/data", filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	options, _ := Normalize(Options{Exclude: []string{"/alias/value"}}, nil)
	var output bytes.Buffer
	if _, _, err := Archive(context.Background(), root, &output, options); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(output.Bytes(), []byte("disk-content")) {
		t.Fatal("symlink exclusion failed to remove target")
	}
}
