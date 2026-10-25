package localapi

import (
	"bytes"
	"context"
	"io"
	"path"
	"strings"

	"github.com/pacificbelt30/codex-dock/pkg/cxd"
)

func workspaceDispatch(ctx context.Context, backend Backend, r Request) (any, bool, error) {
	switch r.Method {
	case "read_file", "write_file", "list_files", "start_job", "job", "list_jobs", "job_logs", "kill_job", "wait_job", "checkpoint", "checkpoints", "inspect_checkpoint", "remove_checkpoint":
	default:
		return nil, false, nil
	}
	b, ok := backend.(WorkspaceBackend)
	if !ok {
		return nil, true, errInvalid
	}
	result, err := workspaceCall(ctx, b, r)
	return result, true, err
}
func workspaceCall(ctx context.Context, b WorkspaceBackend, r Request) (any, error) {
	switch r.Method {
	case "read_file", "write_file", "list_files":
		var p FileParams
		if decode(r.Params, &p) != nil || p.WorkerID == "" || p.Path == "" || strings.HasPrefix(p.Path, "/") || path.Clean(p.Path) != p.Path || p.Path == ".." || strings.HasPrefix(p.Path, "../") || strings.ContainsRune(p.Path, 0) || len(p.Data) > MaxFileTransferBytes {
			return nil, errInvalid
		}
		if r.Method == "write_file" {
			return success(b.WriteFile(ctx, p.WorkerID, p.Path, bytes.NewReader(p.Data)))
		}
		var buffer bytes.Buffer
		out := &boundedData{out: &buffer, remaining: MaxFileTransferBytes}
		var err error
		if r.Method == "read_file" {
			err = b.ReadFile(ctx, p.WorkerID, p.Path, out)
		} else {
			err = b.ListFiles(ctx, p.WorkerID, p.Path, out)
		}
		if err != nil {
			return nil, err
		}
		return DataResult{Data: append([]byte{}, buffer.Bytes()...)}, nil
	case "start_job":
		var p ExecParams
		if decode(r.Params, &p) != nil || p.WorkerID == "" || len(p.Argv) == 0 || p.Stdin != nil {
			return nil, errInvalid
		}
		return b.StartJob(ctx, p.WorkerID, cxd.ExecRequest{Argv: p.Argv, Env: p.Env, WorkingDir: p.WorkingDir})
	case "list_jobs":
		var p WorkerParams
		if decode(r.Params, &p) != nil || p.WorkerID == "" {
			return nil, errInvalid
		}
		return b.ListJobs(ctx, p.WorkerID)
	case "job", "job_logs", "kill_job", "wait_job":
		var p JobParams
		if decode(r.Params, &p) != nil || p.WorkerID == "" || p.JobID == "" || p.Offset < 0 {
			return nil, errInvalid
		}
		switch r.Method {
		case "job":
			return b.Job(ctx, p.WorkerID, p.JobID)
		case "job_logs":
			return b.JobLogs(ctx, p.WorkerID, p.JobID, p.Offset)
		case "kill_job":
			return success(b.KillJob(ctx, p.WorkerID, p.JobID))
		default:
			return b.WaitJob(ctx, p.WorkerID, p.JobID)
		}
	case "checkpoints":
		if decode(r.Params, &struct{}{}) != nil {
			return nil, errInvalid
		}
		return b.Checkpoints(ctx)
	default:
		var p CheckpointParams
		if decode(r.Params, &p) != nil {
			return nil, errInvalid
		}
		if r.Method == "checkpoint" {
			if p.WorkerID == "" || p.ID != "" {
				return nil, errInvalid
			}
			return b.Checkpoint(ctx, p.WorkerID, p.Options)
		}
		if len(p.ID) != 64 || strings.Trim(p.ID, "0123456789abcdef") != "" {
			return nil, errInvalid
		}
		if r.Method == "inspect_checkpoint" {
			return b.InspectCheckpoint(ctx, p.ID)
		}
		return success(b.RemoveCheckpoint(ctx, p.ID))
	}
}

type boundedData struct {
	out       io.Writer
	remaining int
}

func (w *boundedData) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, errOutput
	}
	n, err := w.out.Write(p)
	w.remaining -= n
	return n, err
}
