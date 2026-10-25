//go:build linux

package engine

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pacificbelt30/codex-dock/internal/diskcheckpoint"
)

func TestCheckpointImagesRequireFreshKataAndCompatibleEntrypoint(t *testing.T) {
	id := strings.Repeat("a", 64)
	info := diskcheckpoint.Info{Format: diskcheckpoint.Format, ID: id, Source: strings.Repeat("b", 64), Image: checkpointRef(id), Created: time.Now(), Bytes: 4096, Entries: 1, EntrypointSHA256: entrypointDigest()}
	data, _ := json.Marshal(info)
	ic := v1.Image{Config: v1.ImageConfig{Labels: map[string]string{diskcheckpoint.MetadataLabel: string(data)}}}
	if err := validateCheckpointImage(ic, "io.containerd.kata.v2"); err != nil {
		t.Fatal(err)
	}
	if err := validateCheckpointImage(ic, "runc"); err == nil {
		t.Fatal("checkpoint bypassed Kata through image selection")
	}
	info.EntrypointSHA256 = strings.Repeat("0", 64)
	data, _ = json.Marshal(info)
	ic.Config.Labels[diskcheckpoint.MetadataLabel] = string(data)
	if err := validateCheckpointImage(ic, "io.containerd.kata.v2"); err == nil {
		t.Fatal("incompatible checkpoint accepted")
	}
	if err := validateCheckpointImage(v1.Image{}, "runc"); err != nil {
		t.Fatal("ordinary images changed")
	}
}

func TestCheckpointMetadataMustRemainReadableBeforePublication(t *testing.T) {
	id := strings.Repeat("a", 64)
	info := diskcheckpoint.Info{Format: diskcheckpoint.Format, ID: id, Source: id, Image: checkpointRef(id), Created: time.Now(), Bytes: 4096, Entries: 1, EntrypointSHA256: entrypointDigest()}
	if _, err := encodeCheckpointMetadata(info); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 128; i++ {
		info.Excluded = append(info.Excluded, "/"+strings.Repeat("a", 1023))
	}
	if _, err := encodeCheckpointMetadata(info); err == nil {
		t.Fatal("unreadable metadata was publishable")
	}
}
