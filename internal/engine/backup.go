package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	dockerdefaults "github.com/pacificbelt30/codex-dock/docker"
	"github.com/pacificbelt30/codex-dock/internal/agentbackup"
	"github.com/pacificbelt30/codex-dock/internal/diskcheckpoint"
	"github.com/pacificbelt30/codex-dock/internal/streamfile"
)

func backupRequest(ctx context.Context, socket, method, endpoint string, body io.Reader, receive func(io.Reader) error) error {
	c, err := NewClientAt(socket)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+endpoint, body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	res, err := c.HTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("backup response unavailable; inspect the destination before retrying: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		var detail struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&detail)
		return fmt.Errorf("backup operation failed (HTTP %d): %s", res.StatusCode, strings.TrimSpace(detail.Message))
	}
	return receive(res.Body)
}
func portableCanonical() string {
	s := sha256.Sum256(dockerdefaults.Entrypoint)
	return hex.EncodeToString(s[:])
}
func ExportCheckpoint(ctx context.Context, socket, id, destination string) error {
	if !diskcheckpoint.IDPattern.MatchString(id) {
		return fmt.Errorf("checkpoint requires full ID")
	}
	return streamfile.Create(destination, func(f *os.File) error {
		if err := backupRequest(ctx, socket, http.MethodGet, "/checkpoints/"+id+"/export", nil, func(r io.Reader) error {
			n, err := io.Copy(f, io.LimitReader(r, diskcheckpoint.MaxArchiveBytes+1))
			if err == nil && n > diskcheckpoint.MaxArchiveBytes {
				err = fmt.Errorf("checkpoint export exceeds size limit")
			}
			return err
		}); err != nil {
			return err
		}
		verified, err := diskcheckpoint.ValidatePortable(ctx, f, portableCanonical())
		if err == nil && verified.Info.ID != id {
			return fmt.Errorf("export returned different checkpoint ID")
		}
		return err
	})
}
func openBackup(path string, max int64) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err == nil && (!st.Mode().IsRegular() || st.Size() > max) {
		err = fmt.Errorf("backup input must be a bounded regular file")
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}
func ImportCheckpoint(ctx context.Context, socket, path string) (diskcheckpoint.Info, error) {
	var info diskcheckpoint.Info
	f, err := openBackup(path, diskcheckpoint.MaxArchiveBytes)
	if err != nil {
		return info, err
	}
	defer func() { _ = f.Close() }()
	// Validate before sending so an accidental wrong file never becomes an upload.
	if _, err = diskcheckpoint.ValidatePortable(ctx, f, portableCanonical()); err != nil {
		return info, err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return info, err
	}
	err = backupRequest(ctx, socket, http.MethodPost, "/checkpoints/import", f, func(r io.Reader) error { return json.NewDecoder(io.LimitReader(r, 1<<16)).Decode(&info) })
	return info, err
}
func ExportAgentState(ctx context.Context, socket, id, destination string) error {
	if !hexWorkerID(id) {
		return fmt.Errorf("agent state backup requires full worker ID")
	}
	return streamfile.Create(destination, func(f *os.File) error {
		if err := backupRequest(ctx, socket, http.MethodGet, "/agent-state/"+id+"/export", nil, func(r io.Reader) error {
			n, err := io.Copy(f, io.LimitReader(r, agentbackup.MaxFileBytes+1))
			if err == nil && n > agentbackup.MaxFileBytes {
				err = fmt.Errorf("agent backup exceeds size limit")
			}
			return err
		}); err != nil {
			return err
		}
		_, err := agentbackup.ValidateFile(f)
		return err
	})
}
func RestoreAgentState(ctx context.Context, socket, id, path string) error {
	if !hexWorkerID(id) {
		return fmt.Errorf("agent state restore requires full worker ID")
	}
	f, err := openBackup(path, agentbackup.MaxFileBytes)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err = agentbackup.ValidateFile(f); err != nil {
		return err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return backupRequest(ctx, socket, http.MethodPost, "/agent-state/"+id+"/restore", f, func(r io.Reader) error {
		var result struct {
			Restored bool `json:"restored"`
		}
		if err := json.NewDecoder(io.LimitReader(r, 4096)).Decode(&result); err != nil {
			return err
		}
		if !result.Restored {
			return fmt.Errorf("node did not confirm restore")
		}
		return nil
	})
}
