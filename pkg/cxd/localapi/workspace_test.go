package localapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/pacificbelt30/codex-dock/pkg/cxd"
	"io"
	"strings"
	"testing"
)

func (b *fakeBackend) ReadFile(_ context.Context, _, path string, out io.Writer) error {
	b.mu.Lock()
	data, ok := b.files[path]
	b.mu.Unlock()
	if !ok {
		return cxd.ErrNotFound
	}
	_, err := out.Write(data)
	return err
}
func (b *fakeBackend) WriteFile(_ context.Context, _, path string, in io.Reader) error {
	data, err := io.ReadAll(in)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.files == nil {
		b.files = map[string][]byte{}
	}
	b.files[path] = data
	return nil
}
func (b *fakeBackend) ListFiles(_ context.Context, _, _ string, out io.Writer) error {
	_, err := io.WriteString(out, `["binary"]`)
	return err
}
func (*fakeBackend) StartJob(_ context.Context, worker string, r cxd.ExecRequest) (cxd.Job, error) {
	if len(r.Argv) > 0 && r.Argv[0] == "lost-job" {
		return cxd.Job{ID: "job-1", Worker: worker}, cxd.ErrOutcomeUnknown
	}
	return cxd.Job{ID: "job-1", Worker: worker, Running: true}, nil
}
func (*fakeBackend) Job(_ context.Context, worker, id string) (cxd.Job, error) {
	return cxd.Job{ID: id, Worker: worker, ExitCode: 7}, nil
}
func (*fakeBackend) ListJobs(_ context.Context, worker string) ([]cxd.Job, error) {
	return []cxd.Job{{ID: "job-1", Worker: worker}}, nil
}
func (*fakeBackend) JobLogs(_ context.Context, _, _ string, offset int64) (cxd.JobOutput, error) {
	return cxd.JobOutput{Output: "job-log", Next: offset + 7, Truncated: offset < 10}, nil
}
func (*fakeBackend) KillJob(context.Context, string, string) error { return nil }
func (b *fakeBackend) WaitJob(ctx context.Context, worker, id string) (cxd.Job, error) {
	if id == "block" {
		<-ctx.Done()
		return cxd.Job{}, ctx.Err()
	}
	return b.Job(ctx, worker, id)
}
func (*fakeBackend) Checkpoint(_ context.Context, worker string, _ cxd.CheckpointOptions) (cxd.DiskCheckpoint, error) {
	return cxd.DiskCheckpoint{ID: strings.Repeat("a", 64), Source: worker, Image: "checkpoint", Digest: "sha256:fixture"}, nil
}
func (*fakeBackend) Checkpoints(context.Context) (cxd.CheckpointCatalog, error) {
	return cxd.CheckpointCatalog{}, nil
}
func (*fakeBackend) InspectCheckpoint(_ context.Context, id string) (cxd.DiskCheckpoint, error) {
	return cxd.DiskCheckpoint{ID: id, Image: "checkpoint", Digest: "sha256:fixture"}, nil
}
func (*fakeBackend) RemoveCheckpoint(context.Context, string) error { return nil }
func TestWorkspaceValidationBoundsAndMutationSemantics(t *testing.T) {
	b := newFake()
	b.files = map[string][]byte{"large": bytes.Repeat([]byte{1}, MaxFileTransferBytes+1)}
	for _, tc := range []struct {
		method string
		p      any
		want   error
	}{
		{"read_file", FileParams{WorkerID: "w", Path: "../secret"}, errInvalid},
		{"write_file", FileParams{WorkerID: "w", Path: "file", Data: make([]byte, MaxFileTransferBytes+1)}, errInvalid},
		{"read_file", FileParams{WorkerID: "w", Path: "large"}, errOutput},
		{"start_job", ExecParams{WorkerID: "w", Argv: []string{"lost-job"}}, cxd.ErrOutcomeUnknown},
		{"job_logs", JobParams{WorkerID: "w", JobID: "j", Offset: -1}, errInvalid},
		{"inspect_checkpoint", CheckpointParams{ID: "../../bad"}, errInvalid},
	} {
		raw := jsonValue(tc.p)
		_, err := workspaceCall(context.Background(), b, Request{Method: tc.method, Params: raw})
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: %v", tc.method, err)
		}
	}
	if !mutating("start_job") || !mutating("write_file") || mutating("wait_job") {
		t.Fatal("mutation reconciliation contract lost")
	}
}

func jsonValue(v any) []byte { data, _ := json.Marshal(v); return data }
