package managedworkspace

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"syscall"

	"github.com/pacificbelt30/codex-dock/internal/strictjson"
)

// ExtractSeed accepts exactly the reviewed regular files. Archive ownership,
// links, device nodes, PAX extensions and arbitrary metadata are not applied.
func ExtractSeed(ctx context.Context, input io.Reader, directory string, options Options) (Manifest, error) {
	var manifest Manifest
	reader := tar.NewReader(input)
	header, err := reader.Next()
	if err != nil || header.Name != "manifest.json" || header.Typeflag != tar.TypeReg || header.Size > MaxManifestBytes || header.Size < 1 {
		return manifest, fmt.Errorf("workspace seed requires a bounded manifest first")
	}
	if err = strictjson.Decode(reader, MaxManifestBytes, &manifest); err != nil {
		return manifest, err
	}
	if err = manifest.Validate(options.SeedLimit()); err != nil {
		return manifest, err
	}
	if uint64(len(manifest.Files))+128 > options.Inodes {
		return manifest, fmt.Errorf("selected files exceed workspace inode capacity")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return manifest, err
	}
	defer func() { _ = root.Close() }()
	dirs := map[string]bool{".": true}
	for _, entry := range manifest.Files {
		if err = ctx.Err(); err != nil {
			return manifest, err
		}
		header, err = reader.Next()
		if err != nil || header.Name != "files/"+entry.Path || header.Typeflag != tar.TypeReg || header.Size != entry.Bytes || header.Mode != int64(entry.Mode) || header.Linkname != "" {
			return manifest, fmt.Errorf("workspace archive differs from the reviewed file manifest")
		}
		for key, value := range header.PAXRecords {
			if key != "path" || value != header.Name {
				return manifest, fmt.Errorf("unsupported workspace archive metadata")
			}
		}
		parent := path.Dir(entry.Path)
		if parent != "." {
			current := ""
			for _, part := range strings.Split(parent, "/") {
				if current != "" {
					current += "/"
				}
				current += part
				if dirs[current] {
					continue
				}
				if err = root.Mkdir(current, 0700); err != nil {
					return manifest, err
				}
				if err = root.Chown(current, int(options.UID), int(options.GID)); err != nil {
					return manifest, err
				}
				dirs[current] = true
				if uint64(len(dirs)+len(manifest.Files))+32 > options.Inodes {
					return manifest, fmt.Errorf("directory count exceeds workspace inode capacity")
				}
			}
		}
		file, err := root.OpenFile(entry.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			return manifest, err
		}
		hash := sha256.New()
		n, writeErr := io.Copy(io.MultiWriter(file, hash), reader)
		if writeErr == nil && (n != entry.Bytes || hex.EncodeToString(hash.Sum(nil)) != entry.SHA256) {
			writeErr = fmt.Errorf("source changed after workspace review")
		}
		if writeErr == nil {
			writeErr = file.Chown(int(options.UID), int(options.GID))
		}
		if writeErr == nil {
			writeErr = file.Chmod(os.FileMode(entry.Mode))
		}
		if writeErr == nil {
			writeErr = file.Sync()
		}
		closeErr := file.Close()
		if writeErr != nil {
			return manifest, writeErr
		}
		if closeErr != nil {
			return manifest, closeErr
		}
	}
	if _, err = reader.Next(); err != io.EOF {
		return manifest, fmt.Errorf("workspace archive contains unreviewed files or invalid trailing data")
	}
	return manifest, ctx.Err()
}
