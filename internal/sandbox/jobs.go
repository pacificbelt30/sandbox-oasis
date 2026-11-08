package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
)

type Job struct {
	ID       string `json:"id"`
	Worker   string `json:"worker"`
	Running  bool   `json:"running"`
	ExitCode int    `json:"exit_code"`
}
type JobOutput struct {
	Output    string `json:"output"`
	Next      int64  `json:"next"`
	Running   bool   `json:"running"`
	Truncated bool   `json:"truncated"`
}

func (m *Manager) StartJob(ctx context.Context, name string, opts ExecOptions) (Job, error) {
	if err := validateExecEnv(opts.Env); err != nil {
		return Job{}, err
	}
	if len(opts.Argv) == 0 || opts.TTY || opts.Stdin != nil {
		return Job{}, fmt.Errorf("background job needs argv and cannot attach stdin/PTY")
	}
	if opts.WorkingDir == "" {
		opts.WorkingDir = "/workspace"
	}
	if !path.IsAbs(opts.WorkingDir) || path.Clean(opts.WorkingDir) != opts.WorkingDir {
		return Job{}, fmt.Errorf("invalid guest working directory")
	}
	id, err := m.verifyExecWorker(ctx, name)
	if err != nil {
		return Job{}, err
	}
	info, err := m.cli.Info(ctx)
	if err != nil {
		return Job{}, err
	}
	supported := false
	for _, f := range info.DriverStatus {
		if f == [2]string{"cxd.jobs", "1"} {
			supported = true
		}
	}
	if !supported {
		return Job{}, fmt.Errorf("background jobs require an updated node and logger bundle")
	}
	created, err := m.cli.ContainerExecCreate(ctx, id, container.ExecOptions{Cmd: opts.Argv, Env: opts.Env, WorkingDir: opts.WorkingDir})
	if err != nil {
		return Job{}, err
	}
	if err = m.cli.ContainerExecStart(ctx, created.ID, container.ExecStartOptions{Detach: true}); err != nil {
		return Job{ID: created.ID, Worker: id}, err
	}
	return Job{ID: created.ID, Worker: id, Running: true}, nil
}
func (m *Manager) Job(ctx context.Context, worker, eid string) (Job, error) {
	w, err := m.InspectContext(ctx, worker)
	if err != nil {
		return Job{}, err
	}
	v, err := m.cli.ContainerExecInspect(ctx, eid)
	if err != nil {
		return Job{}, err
	}
	if v.ContainerID != w.ID {
		return Job{}, fmt.Errorf("job does not belong to selected worker")
	}
	return Job{ID: eid, Worker: w.ID, Running: v.Running, ExitCode: v.ExitCode}, nil
}
func (m *Manager) jobRequest(ctx context.Context, method, endpoint string, target any) error {
	return m.jobRequestLimit(ctx, method, endpoint, target, 2<<20)
}
func (m *Manager) jobRequestLimit(ctx context.Context, method, endpoint string, target any, limit int64) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := m.cli.HTTPClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("job operation failed (HTTP %d)", resp.StatusCode)
	}
	if target != nil {
		return json.NewDecoder(io.LimitReader(resp.Body, limit)).Decode(target)
	}
	return nil
}
func (m *Manager) ListJobs(ctx context.Context, worker string) ([]Job, error) {
	w, err := m.InspectContext(ctx, worker)
	if err != nil {
		return nil, err
	}
	var records []container.ExecInspect
	if err = m.jobRequest(ctx, "GET", "/containers/"+url.PathEscape(w.ID)+"/jobs", &records); err != nil {
		return nil, err
	}
	jobs := make([]Job, 0, len(records))
	for _, v := range records {
		jobs = append(jobs, Job{v.ExecID, v.ContainerID, v.Running, v.ExitCode})
	}
	return jobs, nil
}
func (m *Manager) JobLogs(ctx context.Context, worker, eid string, offset int64) (JobOutput, error) {
	if _, err := m.Job(ctx, worker, eid); err != nil {
		return JobOutput{}, err
	}
	var out JobOutput
	err := m.jobRequestLimit(ctx, "GET", "/exec/"+url.PathEscape(eid)+"/logs?offset="+strconv.FormatInt(offset, 10), &out, 8<<20)
	return out, err
}
func (m *Manager) KillJob(ctx context.Context, worker, eid string) error {
	job, err := m.Job(ctx, worker, eid)
	if err != nil {
		return err
	}
	if !job.Running {
		return nil
	}
	return m.jobRequest(ctx, "POST", "/exec/"+url.PathEscape(eid)+"/kill", nil)
}
func (m *Manager) ResizeSession(ctx context.Context, worker, eid string, width, height uint) error {
	if _, err := m.Job(ctx, worker, eid); err != nil {
		return err
	}
	return m.cli.ContainerExecResize(ctx, eid, container.ResizeOptions{Width: width, Height: height})
}
