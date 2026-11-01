//go:build linux

package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/containerd/errdefs"
	"github.com/pacificbelt30/codex-dock/internal/agentbackup"
	"golang.org/x/sys/unix"
)

type agentStateRestore struct {
	Temporary     string
	Device, Inode uint64
}

func diskIdentity(path string, bytes int64) (uint64, uint64, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return 0, 0, err
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || s.Uid != uint32(os.Geteuid()) || s.Nlink != 1 || st.Size() != bytes {
		return 0, 0, fmt.Errorf("untrusted agent disk identity or permissions")
	}
	return uint64(s.Dev), s.Ino, nil
}

// The intent is durable before rename. Recovery can finish either side of that
// rename without discarding an existing worker's data or accepting a new disk
// solely because a pathname matches.
func (b *Backend) finishAgentRestore(r *record) error {
	s := r.AgentState
	p := s.Restore
	if p == nil {
		return nil
	}
	if !r.StartedAt.IsZero() || r.FailedCreate || p.Temporary != filepath.Base(p.Temporary) || !strings.HasPrefix(p.Temporary, ".agent-restore-") || p.Device == 0 || p.Inode == 0 {
		return fmt.Errorf("invalid pending agent state restore")
	}
	if err := b.kernelIdle(r); err != nil {
		return err
	}
	canonical := b.agentStateImage(r)
	temporary := filepath.Join(b.path(r.ID), p.Temporary)
	dev, ino, err := diskIdentity(canonical, s.Bytes)
	if err != nil {
		return err
	}
	if dev == s.Device && ino == s.Inode {
		td, ti, err := diskIdentity(temporary, s.Bytes)
		if err != nil {
			return err
		}
		if td != p.Device || ti != p.Inode {
			return fmt.Errorf("pending restore disk identity changed")
		}
		if err = os.Rename(temporary, canonical); err != nil {
			return err
		}
	} else if dev != p.Device || ino != p.Inode {
		return fmt.Errorf("restore target identity changed")
	} else if _, err := os.Lstat(temporary); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("completed restore still has an unexpected staging path")
	}
	if err := syncStateDirectory(b.path(r.ID)); err != nil {
		return err
	}
	oldDevice, oldInode, oldRestored := s.Device, s.Inode, s.Restored
	s.Device, s.Inode, s.Restore, s.Restored = p.Device, p.Inode, nil, true
	if err := b.save(r); err != nil {
		s.Device, s.Inode, s.Restore, s.Restored = oldDevice, oldInode, p, oldRestored
		return err
	}
	return nil
}

func (b *Backend) stoppedAgentState(ctx context.Context, id string, restore bool) (*record, error) {
	r, c, err := b.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if r.Config.Labels["codex-dock.managed"] != "true" || r.Host.Runtime != "io.containerd.kata.v2" || r.Config.Labels["codex-dock.authentication-session"] == "true" || r.Quarantined || r.AgentState == nil || !r.AgentState.Ready {
		return nil, fmt.Errorf("backup requires a regular Kata worker with a private agent state disk")
	}
	if restore && (!r.StartedAt.IsZero() || r.FailedCreate) {
		return nil, fmt.Errorf("restore requires a newly created, never-started worker; use run --create-only: %w", errdefs.ErrConflict)
	}
	if _, err = c.Task(ctx, nil); err == nil {
		return nil, fmt.Errorf("stop the worker before agent state backup: %w", errdefs.ErrConflict)
	} else if !errdefs.IsNotFound(err) {
		return nil, err
	}
	if b.activeExecutions(r.ID) > 0 || b.checkpointing[r.ID] {
		return nil, fmt.Errorf("worker has pending operations: %w", errdefs.ErrConflict)
	}
	if err = b.kernelIdle(r); err != nil {
		return nil, err
	}
	if err = b.ensureAgentState(r); err != nil {
		return nil, err
	}
	if restore && r.AgentState.Restored {
		return nil, fmt.Errorf("agent state already restored; choose another newly created worker: %w", errdefs.ErrConflict)
	}
	if b.checkpointing == nil {
		b.checkpointing = map[string]bool{}
	}
	b.checkpointing[r.ID] = true
	return r, nil
}
func (b *Backend) agentBackupHTTP(w http.ResponseWriter, request *http.Request, ctx context.Context, path string) {
	parts := strings.Split(strings.TrimPrefix(path, "/agent-state/"), "/")
	if len(parts) != 2 || !hexID.MatchString(parts[0]) || (parts[1] != "export" && parts[1] != "restore") {
		http.NotFound(w, request)
		return
	}
	restore := parts[1] == "restore"
	if !restore && request.Method != http.MethodGet || restore && request.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !b.checkpointMu.TryLock() {
		failure(w, fmt.Errorf("backup operation active: %w", errdefs.ErrConflict))
		return
	}
	defer b.checkpointMu.Unlock()
	b.mu.Lock()
	r, err := b.stoppedAgentState(ctx, parts[0], restore)
	b.mu.Unlock()
	if err != nil {
		failure(w, err)
		return
	}
	defer func() { b.mu.Lock(); delete(b.checkpointing, r.ID); b.mu.Unlock() }()
	if restore {
		err = b.restoreAgentBackup(ctx, r, http.MaxBytesReader(w, request.Body, agentbackup.MaxFileBytes))
		if err != nil {
			failure(w, err)
			return
		}
		reply(w, map[string]bool{"restored": true})
		return
	}
	f, err := os.OpenFile(b.agentStateImage(r), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		failure(w, err)
		return
	}
	defer func() { _ = f.Close() }()
	hash := sha256.New()
	if _, err = io.Copy(hash, &contextReader{ctx, f}); err != nil {
		failure(w, err)
		return
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		failure(w, err)
		return
	}
	h := agentbackup.Header{Format: "oaiso-agent-state-v1", Bytes: r.AgentState.Bytes, UID: r.AgentState.UID, GID: r.AgentState.GID, SHA256: hex.EncodeToString(hash.Sum(nil))}
	w.Header().Set("Content-Type", "application/octet-stream")
	if err = agentbackup.WriteHeader(w, h); err != nil {
		return
	}
	_, _ = io.CopyN(w, &contextReader{ctx, f}, h.Bytes)
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func (b *Backend) restoreAgentBackup(ctx context.Context, r *record, input io.Reader) error {
	h, err := agentbackup.ReadHeader(input)
	if err != nil {
		return err
	}
	s := r.AgentState
	if h.Bytes != s.Bytes || h.UID != s.UID || h.GID != s.GID {
		return fmt.Errorf("agent state capacity or UID/GID differs from the new worker")
	}
	f, err := os.CreateTemp(b.path(r.ID), ".agent-restore-")
	if err != nil {
		return err
	}
	retain := false
	defer func() {
		_ = f.Close()
		if !retain {
			_ = os.Remove(f.Name())
		}
	}()
	if err = unix.Fallocate(int(f.Fd()), 0, 0, h.Bytes); err != nil {
		return fmt.Errorf("reserving restore capacity: %w", err)
	}
	if err = agentbackup.CopyPayload(f, &contextReader{ctx, input}, h); err != nil {
		return err
	}
	if err = errors.Join(f.Sync(), f.Close()); err != nil {
		return err
	}
	dev, ino, err := diskIdentity(f.Name(), s.Bytes)
	if err != nil {
		return err
	}
	if err = syncStateDirectory(b.path(r.ID)); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	s.Restore = &agentStateRestore{Temporary: filepath.Base(f.Name()), Device: dev, Inode: ino}
	// A save error can occur after record rename but before directory sync.
	// Retain the staged disk even when the durability outcome is unknown.
	retain = true
	if err = b.save(r); err != nil {
		return err
	}
	return b.finishAgentRestore(r)
}
