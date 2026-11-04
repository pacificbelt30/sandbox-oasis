//go:build linux

package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	cd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/containerd/errdefs"
	digest "github.com/opencontainers/go-digest"
	imagespec "github.com/opencontainers/image-spec/specs-go"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pacificbelt30/codex-dock/internal/diskcheckpoint"
	"github.com/pacificbelt30/codex-dock/internal/managedworkspace"
)

type catalogImages struct {
	images.Store
	items map[string]images.Image
}

func (s *catalogImages) Get(_ context.Context, name string) (images.Image, error) {
	v, ok := s.items[name]
	if !ok {
		return v, errdefs.ErrNotFound
	}
	return v, nil
}
func (s *catalogImages) List(context.Context, ...string) ([]images.Image, error) {
	out := make([]images.Image, 0, len(s.items))
	for _, v := range s.items {
		out = append(out, v)
	}
	return out, nil
}

func checkpointCatalogFixture(tb testing.TB, count int) (*Backend, *catalogImages, []managedworkspace.RecoveryPoint) {
	tb.Helper()
	store, err := local.NewStore(tb.TempDir())
	if err != nil {
		tb.Fatal(err)
	}
	imageStore := &catalogImages{items: map[string]images.Image{}}
	client, err := cd.New("", cd.WithServices(cd.WithImageStore(imageStore), cd.WithContentStore(store)))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = client.Close() })
	b := &Backend{client: client, opts: Options{State: tb.TempDir()}}
	write := func(value any, media string) v1.Descriptor {
		tb.Helper()
		data, err := json.Marshal(value)
		if err != nil {
			tb.Fatal(err)
		}
		d := v1.Descriptor{MediaType: media, Digest: digest.FromBytes(data), Size: int64(len(data))}
		if err = content.WriteBlob(tb.Context(), store, d.Digest.Encoded(), bytes.NewReader(data), d); err != nil {
			tb.Fatal(err)
		}
		return d
	}
	points := make([]managedworkspace.RecoveryPoint, 0, count)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("%064x", i+1)
		info := diskcheckpoint.Info{Format: diskcheckpoint.Format, ID: id, Source: strings.Repeat("a", 64), Image: checkpointRef(id), Created: time.Unix(1700000000, 0).UTC(), Bytes: 4096, Entries: 1, EntrypointSHA256: entrypointDigest()}
		metadata, err := encodeCheckpointMetadata(info)
		if err != nil {
			tb.Fatal(err)
		}
		config := write(v1.Image{Platform: v1.Platform{OS: "linux", Architecture: "amd64"}, RootFS: v1.RootFS{Type: "layers", DiffIDs: []digest.Digest{digest.FromString("fixture-layer")}}, Config: v1.ImageConfig{Labels: map[string]string{diskcheckpoint.MetadataLabel: string(metadata)}}}, v1.MediaTypeImageConfig)
		target := write(v1.Manifest{Versioned: imagespec.Versioned{SchemaVersion: 2}, MediaType: v1.MediaTypeImageManifest, Config: config}, v1.MediaTypeImageManifest)
		imageStore.items[info.Image] = images.Image{Name: info.Image, Target: target, Labels: map[string]string{diskcheckpoint.OwnerLabel: id}}
		info.Digest = target.Digest.String()
		p := managedworkspace.RecoveryPoint{ID: fmt.Sprintf("%064x", 10000+i), Name: "catalog fixture", State: "ready", Created: info.Created, Source: info.Source, Checkpoint: info}
		if err = b.saveRecoveryPoint(p); err != nil {
			tb.Fatal(err)
		}
		points = append(points, p)
	}
	return b, imageStore, points
}

func TestCheckpointCatalogRevalidatesPointBindings(t *testing.T) {
	b, _, points := checkpointCatalogFixture(t, 3)
	catalog, err := b.checkpointCatalog(t.Context())
	if err != nil || len(catalog.Items) != 3 {
		t.Fatal(catalog, err)
	}
	for _, info := range catalog.Items {
		if !info.Pinned {
			t.Fatal("point reference was not retained")
		}
	}
	point := points[1]
	point.Checkpoint.Digest = "sha256:" + strings.Repeat("f", 64)
	if err = b.saveRecoveryPoint(point); err != nil {
		t.Fatal(err)
	}
	if _, err = b.checkpointCatalog(t.Context()); err == nil {
		t.Fatal("next catalog request reused stale reference authority")
	}
	if err = b.saveRecoveryPoint(points[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = b.checkpointCatalog(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkCheckpointCatalog(b *testing.B) {
	for _, count := range []int{8, 32, 128} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			backend, _, _ := checkpointCatalogFixture(b, count)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				catalog, err := backend.checkpointCatalog(b.Context())
				if err != nil || len(catalog.Items) != count {
					b.Fatal(err)
				}
			}
		})
	}
}
