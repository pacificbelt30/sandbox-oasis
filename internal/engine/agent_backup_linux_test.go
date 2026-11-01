//go:build linux

package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/agentbackup"
)

func TestAgentRestoreCorruptionAndCrashRecovery(t *testing.T) {
	b, r, s := stateDiskFixture(t)
	if err := b.provisionAgentState(context.Background(), r, s); err != nil {
		t.Fatal(err)
	}
	// An opaque disk is deliberately not mounted or parsed on the host.
	disk := bytes.Repeat([]byte{7}, int(r.AgentState.Bytes))
	sum := sha256.Sum256(disk)
	h := agentbackup.Header{Format: "oaiso-agent-state-v1", Bytes: int64(len(disk)), UID: r.AgentState.UID, GID: r.AgentState.GID, SHA256: hex.EncodeToString(sum[:])}
	payload := func() io.Reader {
		var header bytes.Buffer
		if err := agentbackup.WriteHeader(&header, h); err != nil {
			t.Fatal(err)
		}
		return io.MultiReader(&header, bytes.NewReader(disk))
	}
	before, _ := os.ReadFile(b.agentStateImage(r))
	bad := payload()
	all, _ := io.ReadAll(bad)
	all[len(all)-1] ^= 1
	if err := b.restoreAgentBackup(context.Background(), r, bytes.NewReader(all)); err == nil {
		t.Fatal("corrupt backup accepted")
	}
	after, _ := os.ReadFile(b.agentStateImage(r))
	if !bytes.Equal(before, after) {
		t.Fatal("corrupt restore changed target")
	}
	if err := b.restoreAgentBackup(context.Background(), r, payload()); err != nil {
		t.Fatal(err)
	}
	after, _ = os.ReadFile(b.agentStateImage(r))
	if !bytes.Equal(after, disk) {
		t.Fatal("restore lost bytes")
	}
	if err := b.ensureAgentState(r); err != nil {
		t.Fatal("restored identity unavailable", err)
	}
	// Reproduce node death after durable intent but before/after atomic rename.
	for _, renamed := range []bool{false, true} {
		f, err := os.CreateTemp(b.path(r.ID), ".agent-restore-")
		if err != nil {
			t.Fatal(err)
		}
		if err = f.Truncate(r.AgentState.Bytes); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		dev, ino, err := diskIdentity(f.Name(), r.AgentState.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		r.AgentState.Restore = &agentStateRestore{Temporary: filepath.Base(f.Name()), Device: dev, Inode: ino}
		if err = b.save(r); err != nil {
			t.Fatal(err)
		}
		if renamed {
			if err = os.Rename(f.Name(), b.agentStateImage(r)); err != nil {
				t.Fatal(err)
			}
		}
		if err = b.finishAgentRestore(r); err != nil {
			t.Fatal(err)
		}
		if r.AgentState.Restore != nil || r.AgentState.Inode != ino {
			t.Fatal("intent not completed")
		}
	}
}

func TestAgentRestoreRejectsIdentityAndCapacityBeforeReadingPayload(t *testing.T) {
	b, r, s := stateDiskFixture(t)
	if err := b.provisionAgentState(context.Background(), r, s); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"capacity", "uid", "gid"} {
		t.Run(change, func(t *testing.T) {
			h := agentbackup.Header{Format: "oaiso-agent-state-v1", Bytes: r.AgentState.Bytes, UID: r.AgentState.UID, GID: r.AgentState.GID, SHA256: strings.Repeat("a", 64)}
			switch change {
			case "capacity":
				h.Bytes *= 2
			case "uid":
				h.UID++
			case "gid":
				h.GID++
			}
			var header bytes.Buffer
			if err := agentbackup.WriteHeader(&header, h); err != nil {
				t.Fatal(err)
			}
			input := io.MultiReader(&header, unexpectedRestorePayload{})
			err := b.restoreAgentBackup(context.Background(), r, input)
			if err == nil || !strings.Contains(err.Error(), "capacity or UID/GID differs") {
				t.Fatal("mismatch was not rejected before payload", err)
			}
			if r.AgentState.Restore != nil || r.AgentState.Restored {
				t.Fatal("mismatched restore changed state")
			}
			if err := b.ensureAgentState(r); err != nil {
				t.Fatal("mismatch changed target identity", err)
			}
		})
	}
}

type unexpectedRestorePayload struct{}

func (unexpectedRestorePayload) Read([]byte) (int, error) {
	panic("restore read payload after mismatched header")
}
