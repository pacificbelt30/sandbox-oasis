// Package streamfile publishes private backup files without overwriting user data.
package streamfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Create stages in the destination directory, syncs, then links atomically with
// no replacement. A failed stream leaves no apparently complete backup.
func Create(destination string, write func(*os.File) error) error {
	if destination == "" {
		return fmt.Errorf("backup output path is required")
	}
	dir := filepath.Dir(destination)
	f, err := os.CreateTemp(dir, ".oaiso-backup-")
	if err != nil {
		return err
	}
	defer func() { _ = f.Close(); _ = os.Remove(f.Name()) }()
	if err = write(f); err != nil {
		return err
	}
	if err = errors.Join(f.Sync(), f.Close()); err != nil {
		return err
	}
	if err = os.Link(f.Name(), destination); err != nil {
		return fmt.Errorf("publishing backup without replacement: %w", err)
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
