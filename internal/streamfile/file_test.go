package streamfile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPublishNeverOverwritesOrRetainsFailedStream(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "backup")
	if err := Create(dest, func(f *os.File) error { _, err := f.WriteString("original"); return err }); err != nil {
		t.Fatal(err)
	}
	if err := Create(dest, func(f *os.File) error { _, err := f.WriteString("replacement"); return err }); err == nil {
		t.Fatal("overwrote backup")
	}
	b, _ := os.ReadFile(dest)
	st, _ := os.Stat(dest)
	if string(b) != "original" || st.Mode().Perm() != 0600 {
		t.Fatal("private backup changed")
	}
	failed := dest + "-failed"
	if err := Create(failed, func(f *os.File) error { _, _ = f.WriteString("partial"); return errors.New("lost stream") }); err == nil {
		t.Fatal("lost error")
	}
	if _, err := os.Stat(failed); !os.IsNotExist(err) {
		t.Fatal("partial backup published")
	}
	entries, _ := os.ReadDir(filepath.Dir(dest))
	if len(entries) != 1 {
		t.Fatal("staging file retained")
	}
}
