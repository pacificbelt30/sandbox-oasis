package privatefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReadRetiredInodeIsRetryableAndUnsafeFilesStayRejected(t *testing.T) {
	root := t.TempDir()
	name := filepath.Join(root, "config")
	if err := os.WriteFile(name, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if data, err := readOpened(f); !errors.Is(err, errReplaced) || len(data) != 0 {
		t.Fatal("retired inode was accepted or misclassified", string(data), err)
	}
	if err := os.WriteFile(name, []byte("current"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := Read(name); err != nil || string(data) != "current" {
		t.Fatal(string(data), err)
	}
	if err := os.Link(name, filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(name); err == nil {
		t.Fatal("hardlink accepted")
	}
	if err := os.Remove(filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(name, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(name); err == nil {
		t.Fatal("non-private file accepted")
	}
}
