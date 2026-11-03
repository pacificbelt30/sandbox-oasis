// Package managedworkspace defines node-owned, capacity-bounded workspace disks.
package managedworkspace

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pacificbelt30/codex-dock/internal/diskcheckpoint"
)

const Label = "codex-dock.managed-workspace"
const ReadOnlyLabel = "codex-dock.managed-workspace-readonly"
const DefaultBytes int64 = 1 << 30
const MaxBytes int64 = 4 << 30
const CapacityBytes int64 = 16 << 30
const MaxItems = 128
const MaxFiles = 8192
const MaxManifestBytes = 1 << 20
const MaxSeedBytes int64 = 512 << 20
const MaxFileBytes int64 = 64 << 20

var ID = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Options struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	Inodes uint64 `json:"inodes"`
	UID    uint32 `json:"uid"`
	GID    uint32 `json:"gid"`
}

func (o Options) Validate() error {
	if !utf8.ValidString(o.Name) || len([]rune(o.Name)) < 1 || len([]rune(o.Name)) > 64 || strings.IndexFunc(o.Name, unicode.IsControl) >= 0 {
		return fmt.Errorf("workspace name requires 1–64 printable characters")
	}
	if o.Bytes < 64<<20 || o.Bytes > MaxBytes || o.Bytes%(64<<20) != 0 {
		return fmt.Errorf("workspace size must be a multiple of 64 MiB between 64 MiB and 4 GiB")
	}
	if o.Inodes < 1024 || o.Inodes > 65536 || o.Inodes&(o.Inodes-1) != 0 {
		return fmt.Errorf("workspace inode limit must be a power of two between 1024 and 65536")
	}
	if o.UID == 0 {
		return fmt.Errorf("workspace requires a non-root numeric owner")
	}
	return nil
}
func (o Options) SeedLimit() int64 {
	n := o.Bytes / 2
	if n > MaxSeedBytes {
		n = MaxSeedBytes
	}
	return n
}

type File struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Mode   uint32 `json:"mode"`
	SHA256 string `json:"sha256"`
}
type Manifest struct {
	ID    string `json:"id"`
	Files []File `json:"files"`
	Bytes int64  `json:"bytes"`
}

func FilePath(p string) error {
	if p == "" || len(p) > 512 || !utf8.ValidString(p) || path.Clean(p) != p || strings.HasPrefix(p, "/") || p == ".." || strings.HasPrefix(p, "../") || strings.IndexFunc(p, unicode.IsControl) >= 0 {
		return fmt.Errorf("workspace paths must be clean relative UTF-8 paths, at most 512 bytes")
	}
	for _, part := range strings.Split(p, "/") {
		if part == "." || part == ".." || part == "" {
			return fmt.Errorf("dot path components are forbidden")
		}
	}
	return nil
}

type Info struct {
	ID         string    `json:"id"`
	Options    Options   `json:"options"`
	Kind       string    `json:"kind"` // workspace or snapshot; snapshots are never attached directly
	State      string    `json:"state"`
	Created    time.Time `json:"created"`
	Lease      string    `json:"lease,omitempty"`
	ReadOnly   bool      `json:"read_only,omitempty"`
	Operation  string    `json:"operation,omitempty"`
	SeedID     string    `json:"seed_id,omitempty"`
	SeedFiles  int       `json:"seed_files"`
	SeedBytes  int64     `json:"seed_bytes"`
	DiskSHA256 string    `json:"disk_sha256,omitempty"`
}
type Catalog struct {
	CapacityBytes int64  `json:"capacity_bytes"`
	ReservedBytes int64  `json:"reserved_bytes"`
	Items         []Info `json:"items"`
}
type RecoveryPoint struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	State           string              `json:"state"`
	Created         time.Time           `json:"created"`
	Source          string              `json:"source"`
	SourceWorkspace string              `json:"source_workspace,omitempty"`
	Workspace       string              `json:"workspace,omitempty"`
	Checkpoint      diskcheckpoint.Info `json:"checkpoint"`
	Profile         string              `json:"profile,omitempty"`
	Error           string              `json:"error,omitempty"`
}
type SaveOptions struct {
	Worker           string                 `json:"worker"`
	Name             string                 `json:"name"`
	IncludeWorkspace bool                   `json:"include_workspace"`
	Checkpoint       diskcheckpoint.Options `json:"checkpoint"`
}
