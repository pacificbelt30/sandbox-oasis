// Package diskcheckpoint defines the local, filesystem-only checkpoint format.
package diskcheckpoint

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

const Format = "oaiso-disk-v1"
const MetadataLabel = "io.oaiso.checkpoint.metadata"
const OwnerLabel = "io.oaiso.checkpoint.id"
const DefaultMaxBytes int64 = 4 << 30
const CapacityBytes int64 = 16 << 30
const MaxEntries = 500000

var IDPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Options only narrows exclusions; mandatory exclusions cannot be overridden.
type Options struct {
	MaxBytes int64    `json:"max_bytes"`
	Exclude  []string `json:"exclude,omitempty"`
}
type Info struct {
	Pinned           bool      `json:"pinned"`
	Format           string    `json:"format"`
	ID               string    `json:"id"`
	Image            string    `json:"image"`
	Digest           string    `json:"digest,omitempty"`
	Created          time.Time `json:"created"`
	Source           string    `json:"source"`
	BuildVersion     string    `json:"build_version"`
	EntrypointSHA256 string    `json:"entrypoint_sha256"`
	Bytes            int64     `json:"bytes"`
	Entries          int       `json:"entries"`
	Excluded         []string  `json:"excluded"`
}
type Catalog struct {
	CapacityBytes int64  `json:"capacity_bytes"`
	UsedBytes     int64  `json:"used_bytes"`
	Items         []Info `json:"items"`
}

func Normalize(options Options, mounts []string) (Options, error) {
	if options.MaxBytes == 0 {
		options.MaxBytes = DefaultMaxBytes
	}
	if options.MaxBytes < 1<<20 || options.MaxBytes > DefaultMaxBytes {
		return Options{}, fmt.Errorf("checkpoint max bytes must be between 1 MiB and 4 GiB")
	}
	if len(options.Exclude) > 128 || len(mounts) > 256 {
		return Options{}, fmt.Errorf("too many checkpoint exclusions")
	}
	excluded := []string{"/dev", "/proc", "/sys", "/run", "/tmp", "/var/tmp", "/home", "/root", "/workspace", "/etc/hosts", "/etc/hostname", "/etc/resolv.conf", "/var/log", "/.dockerenv"}
	excluded = append(excluded, mounts...)
	excluded = append(excluded, options.Exclude...)
	set := map[string]bool{}
	for _, p := range excluded {
		if !strings.HasPrefix(p, "/") || path.Clean(p) != p || p == "/" || len(p) > 1024 || strings.ContainsAny(p, "\x00\r\n") {
			return Options{}, fmt.Errorf("checkpoint exclusions must be clean absolute guest paths other than /")
		}
		set[p] = true
	}
	options.Exclude = nil
	for p := range set {
		options.Exclude = append(options.Exclude, p)
	}
	sort.Strings(options.Exclude)
	return options, nil
}
func Excluded(name string, excluded []string) bool {
	p := "/" + strings.TrimPrefix(name, "/")
	for _, e := range excluded {
		if p == e || strings.HasPrefix(p, e+"/") {
			return true
		}
	}
	return false
}
func (i Info) Validate() error {
	if i.Format != Format || !IDPattern.MatchString(i.ID) || !IDPattern.MatchString(i.Source) || i.Image != "docker.io/library/oaiso-checkpoint:"+i.ID || i.Created.IsZero() || i.Bytes < 1024 || i.Bytes > DefaultMaxBytes || i.Entries < 1 || i.Entries > MaxEntries || len(i.Excluded) > 512 || !IDPattern.MatchString(i.EntrypointSHA256) {
		return fmt.Errorf("invalid disk checkpoint metadata")
	}
	for _, p := range i.Excluded {
		if !strings.HasPrefix(p, "/") || path.Clean(p) != p || p == "/" || strings.ContainsRune(p, 0) {
			return fmt.Errorf("invalid checkpoint exclusion")
		}
	}
	return nil
}
