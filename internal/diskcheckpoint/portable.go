package diskcheckpoint

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"

	digest "github.com/opencontainers/go-digest"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
)

const MaxArchiveBytes = DefaultMaxBytes + 4<<20

type Portable struct {
	Info   Info
	Target v1.Descriptor
}
type archiveBlob struct {
	offset, size int64
	data         []byte
}
type countingReader struct {
	r io.Reader
	n int64
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, e := r.r.Read(p)
	r.n += int64(n)
	return n, e
}

// ValidatePortable checks a single-image OCI bundle without extracting any
// guest files on the host. All blob digests, references and the real entrypoint
// are checked before the caller may publish a checkpoint image name.
func ValidatePortable(ctx context.Context, f *os.File, canonical string) (Portable, error) {
	var out Portable
	st, err := f.Stat()
	if err != nil {
		return out, err
	}
	if !st.Mode().IsRegular() || st.Size() > MaxArchiveBytes || st.Size() < 1024 {
		return out, fmt.Errorf("checkpoint archive must be a bounded regular OCI file")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return out, err
	}
	cr := &countingReader{r: f}
	tr := tar.NewReader(cr)
	blobs := map[string]archiveBlob{}
	seen := map[string]bool{}
	var index v1.Index
	var layout v1.ImageLayout
	for {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, err
		}
		name := strings.TrimSuffix(h.Name, "/")
		if path.Clean(name) != name || strings.HasPrefix(name, "/") || seen[name] || len(seen) >= 16 || h.Size < 0 || len(h.PAXRecords) > 0 || h.Linkname != "" {
			return out, fmt.Errorf("invalid or duplicate OCI archive entry")
		}
		seen[name] = true
		if h.Typeflag == tar.TypeDir && (name == "blobs" || name == "blobs/sha256") && h.Size == 0 {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return out, fmt.Errorf("OCI archive contains non-file entry")
		}
		if name == "oci-layout" || name == "index.json" {
			if h.Size > 1<<16 {
				return out, fmt.Errorf("OCI metadata too large")
			}
			data, err := io.ReadAll(tr)
			if err != nil {
				return out, err
			}
			if name == "oci-layout" {
				err = json.Unmarshal(data, &layout)
			} else {
				err = json.Unmarshal(data, &index)
			}
			if err != nil {
				return out, err
			}
			continue
		}
		sum := strings.TrimPrefix(name, "blobs/sha256/")
		if name != "blobs/sha256/"+sum || !IDPattern.MatchString(sum) || h.Size > DefaultMaxBytes {
			return out, fmt.Errorf("unexpected OCI archive file")
		}
		blob := archiveBlob{offset: cr.n, size: h.Size}
		hash := sha256.New()
		if h.Size <= 1<<20 {
			blob.data, err = io.ReadAll(tr)
			if err == nil {
				_, err = hash.Write(blob.data)
			}
		} else {
			_, err = io.Copy(hash, tr)
		}
		if err != nil {
			return out, err
		}
		if hex.EncodeToString(hash.Sum(nil)) != sum {
			return out, fmt.Errorf("checkpoint blob checksum mismatch")
		}
		blobs["sha256:"+sum] = blob
	}
	// tar.Reader stops at the end marker; reject appended archives and nonzero
	// trailing bytes, while accepting ordinary tar zero padding up to the bound.
	tail := make([]byte, 32<<10)
	for {
		n, err := cr.Read(tail)
		for _, b := range tail[:n] {
			if b != 0 {
				return out, fmt.Errorf("checkpoint archive has trailing data")
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, err
		}
	}
	if layout.Version != v1.ImageLayoutVersion || index.SchemaVersion != 2 || len(index.Manifests) != 1 || len(blobs) != 3 {
		return out, fmt.Errorf("checkpoint requires one OCI image with exactly three blobs")
	}
	out.Target = index.Manifests[0]
	if p := out.Target.Platform; p != nil && (p.OS != "linux" || p.Architecture != "amd64") {
		return out, fmt.Errorf("checkpoint target platform is incompatible")
	}
	get := func(d v1.Descriptor, media string) (archiveBlob, error) {
		b, ok := blobs[d.Digest.String()]
		if !ok || d.MediaType != media || d.Size != b.size || d.Digest.Algorithm() != digest.SHA256 || len(d.URLs) > 0 || len(d.Data) > 0 {
			return archiveBlob{}, fmt.Errorf("invalid checkpoint descriptor")
		}
		return b, nil
	}
	mb, err := get(out.Target, v1.MediaTypeImageManifest)
	if err != nil {
		return out, err
	}
	var manifest v1.Manifest
	if len(mb.data) == 0 || json.Unmarshal(mb.data, &manifest) != nil || manifest.SchemaVersion != 2 || len(manifest.Layers) != 1 || manifest.Subject != nil {
		return out, fmt.Errorf("invalid checkpoint manifest")
	}
	cb, err := get(manifest.Config, v1.MediaTypeImageConfig)
	if err != nil {
		return out, err
	}
	var config v1.Image
	if len(cb.data) == 0 || json.Unmarshal(cb.data, &config) != nil || config.OS != "linux" || config.Architecture != "amd64" || len(config.RootFS.DiffIDs) != 1 || config.RootFS.Type != "layers" {
		return out, fmt.Errorf("invalid checkpoint configuration")
	}
	raw := config.Config.Labels[MetadataLabel]
	if len(raw) > 1<<16 || json.Unmarshal([]byte(raw), &out.Info) != nil || out.Info.Validate() != nil || out.Info.EntrypointSHA256 != canonical {
		return out, fmt.Errorf("incompatible checkpoint metadata or entrypoint")
	}
	expected := v1.ImageConfig{Entrypoint: []string{"/entrypoint.sh"}, WorkingDir: "/workspace", Env: []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}, Labels: map[string]string{"io.codex-dock.start-hooks": "1", MetadataLabel: raw}}
	a, _ := json.Marshal(config.Config)
	b, _ := json.Marshal(expected)
	if string(a) != string(b) {
		return out, fmt.Errorf("checkpoint configuration includes unexpected runtime settings")
	}
	if name := out.Target.Annotations["io.containerd.image.name"]; name != "" && name != out.Info.Image {
		return out, fmt.Errorf("checkpoint image name differs from metadata")
	}
	mandatory, _ := Normalize(Options{}, nil)
	for _, p := range mandatory.Exclude {
		if !slices.Contains(out.Info.Excluded, p) {
			return out, fmt.Errorf("checkpoint lacks mandatory exclusions")
		}
	}
	lb, err := get(manifest.Layers[0], v1.MediaTypeImageLayer)
	if err != nil {
		return out, err
	}
	if config.RootFS.DiffIDs[0] != manifest.Layers[0].Digest || lb.size != out.Info.Bytes {
		return out, fmt.Errorf("checkpoint layer size or diffID mismatch")
	}
	if err := validateLayer(ctx, io.NewSectionReader(f, lb.offset, lb.size), out.Info, canonical); err != nil {
		return out, err
	}
	out.Info.Digest = out.Target.Digest.String()
	// Keep only the verified target identity; no archive-supplied annotations
	// influence native image naming or platform selection.
	out.Target.Annotations = nil
	return out, nil
}

func validateLayer(ctx context.Context, r io.Reader, info Info, canonical string) error {
	tr := tar.NewReader(r)
	seen := map[string]byte{}
	entrypoint := false
	metadataBytes := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(h.Name, "/")
		metadataBytes += len(name) + len(h.Linkname)
		if len(name) > 4096 || len(h.Linkname) > 4096 || metadataBytes > 64<<20 || strings.ContainsRune(name, 0) || strings.ContainsRune(h.Linkname, 0) {
			return fmt.Errorf("checkpoint layer path metadata exceeds limit")
		}
		if name == "." || path.Clean(name) != name || strings.HasPrefix(name, "/") || name == ".." || strings.HasPrefix(name, "../") || len(seen) >= MaxEntries || seen[name] != 0 || strings.HasPrefix(path.Base(name), ".wh.") {
			return fmt.Errorf("unsafe checkpoint layer path")
		}
		if parent := path.Dir(name); parent != "." && seen[parent] != tar.TypeDir {
			return fmt.Errorf("checkpoint layer parent is not an earlier directory")
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeSymlink {
			return fmt.Errorf("unsafe checkpoint layer file type")
		}
		skeleton := h.Typeflag == tar.TypeDir && (strings.Count(name, "/") == 0 || strings.HasPrefix(name, "home/") && strings.Count(name, "/") == 1)
		if Excluded(name, info.Excluded) && !skeleton {
			return fmt.Errorf("checkpoint layer contains excluded data")
		}
		for k := range h.PAXRecords {
			if k != "path" && k != "linkpath" && k != "mtime" && k != "atime" && k != "ctime" {
				return fmt.Errorf("unexpected checkpoint layer extension")
			}
		}
		seen[name] = h.Typeflag
		if name == "entrypoint.sh" {
			if h.Typeflag != tar.TypeReg || h.Size > 1<<20 {
				return fmt.Errorf("checkpoint entrypoint must be a small regular file")
			}
			hash := sha256.New()
			if _, err = io.Copy(hash, tr); err != nil {
				return err
			}
			entrypoint = hex.EncodeToString(hash.Sum(nil)) == canonical
		}
	}
	if !entrypoint || len(seen) != info.Entries {
		return fmt.Errorf("checkpoint entrypoint or entry count mismatch")
	}
	return nil
}
