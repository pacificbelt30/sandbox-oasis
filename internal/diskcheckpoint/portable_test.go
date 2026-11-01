package diskcheckpoint

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	digest "github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
)

func portableFixture(t *testing.T, alter func(*tar.Header, *Info, *v1.Image)) ([]byte, string) {
	t.Helper()
	entry := []byte("#!/bin/sh\nexec true\n")
	sum := sha256.Sum256(entry)
	canonical := hex.EncodeToString(sum[:])
	options, _ := Normalize(Options{}, nil)
	id := strings.Repeat("a", 64)
	info := Info{Format: Format, ID: id, Image: "docker.io/library/oaiso-checkpoint:" + id, Source: strings.Repeat("b", 64), Created: time.Unix(1000, 0).UTC(), EntrypointSHA256: canonical, Excluded: options.Exclude, Entries: 1, BuildVersion: "v0.0.6"}
	config := v1.Image{Platform: v1.Platform{OS: "linux", Architecture: "amd64"}, RootFS: v1.RootFS{Type: "layers"}, Config: v1.ImageConfig{Entrypoint: []string{"/entrypoint.sh"}, WorkingDir: "/workspace", Env: []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"}, Labels: map[string]string{"io.codex-dock.start-hooks": "1"}}}
	var layer bytes.Buffer
	tw := tar.NewWriter(&layer)
	h := &tar.Header{Name: "entrypoint.sh", Mode: 0755, Typeflag: tar.TypeReg, Size: int64(len(entry))}
	if alter != nil {
		alter(h, &info, &config)
	}
	if err := tw.WriteHeader(h); err != nil {
		t.Fatal(err)
	}
	if h.Size > 0 {
		if _, err := tw.Write(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	info.Bytes = int64(layer.Len())
	metadata, _ := json.Marshal(info)
	config.Config.Labels[MetadataLabel] = string(metadata)
	layerDesc := v1.Descriptor{MediaType: v1.MediaTypeImageLayer, Digest: digest.FromBytes(layer.Bytes()), Size: int64(layer.Len())}
	config.RootFS.DiffIDs = []digest.Digest{layerDesc.Digest}
	configBytes, _ := json.Marshal(config)
	configDesc := v1.Descriptor{MediaType: v1.MediaTypeImageConfig, Digest: digest.FromBytes(configBytes), Size: int64(len(configBytes))}
	manifest := v1.Manifest{Versioned: imagespec.Versioned{SchemaVersion: 2}, MediaType: v1.MediaTypeImageManifest, Config: configDesc, Layers: []v1.Descriptor{layerDesc}}
	manifestBytes, _ := json.Marshal(manifest)
	target := v1.Descriptor{MediaType: v1.MediaTypeImageManifest, Digest: digest.FromBytes(manifestBytes), Size: int64(len(manifestBytes)), Annotations: map[string]string{"io.containerd.image.name": info.Image}}
	index, _ := json.Marshal(v1.Index{Versioned: imagespec.Versioned{SchemaVersion: 2}, Manifests: []v1.Descriptor{target}})
	var out bytes.Buffer
	w := tar.NewWriter(&out)
	add := func(name string, data []byte) {
		t.Helper()
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0600, Typeflag: tar.TypeReg, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	add("oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`))
	add("index.json", index)
	for _, b := range []struct {
		d v1.Descriptor
		b []byte
	}{{layerDesc, layer.Bytes()}, {configDesc, configBytes}, {target, manifestBytes}} {
		add("blobs/sha256/"+b.d.Digest.Encoded(), b.b)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes(), canonical
}
func TestPortableCheckpointValidatesContentBeforePublication(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alter func(*tar.Header, *Info, *v1.Image)
		good  bool
	}{
		{name: "legacy-compatible", good: true},
		{name: "escaping-path", alter: func(h *tar.Header, _ *Info, _ *v1.Image) { h.Name = "../entrypoint.sh" }},
		{name: "excluded-home", alter: func(h *tar.Header, _ *Info, _ *v1.Image) { h.Name = "home/private" }},
		{name: "entrypoint-symlink", alter: func(h *tar.Header, _ *Info, _ *v1.Image) {
			h.Typeflag = tar.TypeSymlink
			h.Size = 0
			h.Linkname = "/tmp/evil"
		}},
		{name: "forged-entrypoint-label", alter: func(_ *tar.Header, i *Info, _ *v1.Image) { i.EntrypointSHA256 = strings.Repeat("0", 64) }},
		{name: "incomplete-exclusions", alter: func(_ *tar.Header, i *Info, _ *v1.Image) { i.Excluded = []string{"/home"} }},
		{name: "credential-environment", alter: func(_ *tar.Header, _ *Info, c *v1.Image) {
			c.Config.Env = append(c.Config.Env, "OPENAI_API_KEY=secret")
		}},
		{name: "wrong-count", alter: func(_ *tar.Header, i *Info, _ *v1.Image) { i.Entries = 2 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, canonical := portableFixture(t, tc.alter)
			file := filepath.Join(t.TempDir(), "backup.oci")
			if err := os.WriteFile(file, data, 0600); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(file)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			_, err = ValidatePortable(context.Background(), f, canonical)
			if (err == nil) != tc.good {
				t.Fatal(err)
			}
		})
	}
	data, canonical := portableFixture(t, nil)
	for _, bad := range [][]byte{data[:len(data)-1300], append(append([]byte{}, data...), []byte("another archive")...)} {
		f, err := os.CreateTemp(t.TempDir(), "bad-")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(bad); err != nil {
			t.Fatal(err)
		}
		if _, err = ValidatePortable(context.Background(), f, canonical); err == nil {
			t.Fatal("incomplete/appended archive accepted")
		}
		_ = f.Close()
	}
}

func TestPortableLayerRejectsOversizedPathMetadata(t *testing.T) {
	var data bytes.Buffer
	w := tar.NewWriter(&data)
	script := []byte("canonical entrypoint")
	sum := sha256.Sum256(script)
	for _, h := range []*tar.Header{
		{Name: "entrypoint.sh", Typeflag: tar.TypeReg, Size: int64(len(script)), Mode: 0755},
		{Name: "opt", Typeflag: tar.TypeDir, Mode: 0755},
		{Name: "opt/" + strings.Repeat("x", 5000), Typeflag: tar.TypeReg, Mode: 0600},
	} {
		if err := w.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Name == "entrypoint.sh" {
			if _, err := w.Write(script); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	err := validateLayer(context.Background(), bytes.NewReader(data.Bytes()), Info{Entries: 3}, hex.EncodeToString(sum[:]))
	if err == nil || !strings.Contains(err.Error(), "metadata exceeds limit") {
		t.Fatal("unbounded path metadata accepted", err)
	}
}
