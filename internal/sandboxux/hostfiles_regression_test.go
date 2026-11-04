package sandboxux

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestExistingDownloadDestinationNeverReadsGuest(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			name := filepath.Join(t.TempDir(), "existing")
			var err error
			switch kind {
			case "file":
				err = os.WriteFile(name, []byte("keep"), 0600)
			case "directory":
				err = os.Mkdir(name, 0700)
			case "symlink":
				err = os.Symlink("absent", name)
			case "fifo":
				err = unix.Mkfifo(name, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			called := false
			err = SaveNewHostFile(name, func(io.Writer) error { called = true; return errors.New("guest must not be contacted") })
			if err == nil || called {
				t.Fatalf("existing destination started a guest transfer: called=%t err=%v", called, err)
			}
		})
	}
}

func TestDownloadPublicationRetainsNoClobberAfterPreflight(t *testing.T) {
	root := t.TempDir()
	name := filepath.Join(root, "result")
	err := SaveNewHostFile(name, func(out io.Writer) error {
		if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("partial destination visible: %v", err)
		}
		if err := os.WriteFile(name, []byte("concurrent winner"), 0600); err != nil {
			return err
		}
		_, err := io.WriteString(out, "must not replace winner")
		return err
	})
	if err == nil {
		t.Fatal("concurrent destination replaced")
	}
	data, err := os.ReadFile(name)
	if err != nil || string(data) != "concurrent winner" {
		t.Fatal(string(data), err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 {
		t.Fatal("staging retained", entries, err)
	}
}
