package sandbox

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuestFileProgramRejectsSymlinksAndWritesAtomically(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python guest-program fixture unavailable")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err = os.WriteFile(filepath.Join(outside, "private"), []byte("outside-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(outside, "private"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(root, "dirlink")); err != nil {
		t.Fatal(err)
	}
	program := strings.Replace(fileProgram, "os.open('/workspace',", "os.open(sys.argv[3],", 1)
	run := func(op, path string, in io.Reader) (string, error) {
		cmd := exec.CommandContext(context.Background(), python, "-I", "-c", program, op, path, root)
		cmd.Dir = root
		cmd.Stdin = in
		var out, stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr
		err := cmd.Run()
		return out.String(), err
	}
	if err = os.WriteFile(filepath.Join(root, "json.py"), []byte("raise RuntimeError('project module must not execute')"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../private", "/etc/passwd", "link", "dirlink/private"} {
		if out, err := run("read", path, nil); err == nil || strings.Contains(out, "outside-secret") {
			t.Fatalf("unsafe path %s accepted", path)
		}
	}
	if _, err = run("write", "normal", strings.NewReader("replacement")); err != nil {
		t.Fatal(err)
	}
	if out, err := run("read", "normal", nil); err != nil || out != "replacement" {
		t.Fatalf("read=%q %v", out, err)
	}
	if _, err = run("write", "link", strings.NewReader("attacker")); err == nil {
		t.Fatal("symlink destination replaced")
	}
	if data, _ := os.ReadFile(filepath.Join(outside, "private")); string(data) != "outside-secret" {
		t.Fatal("outside file modified")
	}
	if out, err := run("list", ".", nil); err != nil || !strings.Contains(out, "normal") {
		t.Fatalf("root listing %q %v", out, err)
	}
	// A failed oversized upload cannot replace the previous destination.
	if _, err = run("write", "normal", io.LimitReader(zeroFileReader{}, MaxFileBytes+1)); err == nil {
		t.Fatal("oversized upload accepted")
	}
	if out, _ := run("read", "normal", nil); out != "replacement" {
		t.Fatal("failed upload modified destination")
	}
	entries, _ := os.ReadDir(root)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".cxd-upload-") {
			t.Fatal("temporary upload retained")
		}
	}
}

type zeroFileReader struct{}

func (zeroFileReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestGuestFileExclusiveCreateAndEntryTypes(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	program := strings.Replace(fileProgram, "os.open('/workspace',", "os.open(sys.argv[3],", 1)
	run := func(op, name, input string) ([]byte, error) {
		c := exec.CommandContext(t.Context(), python, "-I", "-c", program, op, name, root)
		c.Stdin = strings.NewReader(input)
		return c.CombinedOutput()
	}
	if b, e := run("create", "new.txt", "original"); e != nil {
		t.Fatalf("create: %v %s", e, b)
	}
	if _, e := run("create", "new.txt", "replacement"); e == nil {
		t.Fatal("exclusive create replaced a file")
	}
	if b, _ := os.ReadFile(filepath.Join(root, "new.txt")); string(b) != "original" {
		t.Fatal("existing content lost")
	}
	if e := os.Symlink("new.txt", filepath.Join(root, "link")); e != nil {
		t.Fatal(e)
	}
	if _, e := run("create", "link", "replacement"); e == nil {
		t.Fatal("symlink replaced")
	}
	b, e := run("entries", ".", "")
	if e != nil || !strings.Contains(string(b), `"kind": "symlink"`) || !strings.Contains(string(b), `"kind": "file"`) {
		t.Fatalf("metadata: %s %v", b, e)
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".cxd-upload-") {
			t.Fatal("failed create left a temporary file")
		}
	}
}
