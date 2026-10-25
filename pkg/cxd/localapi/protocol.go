// Package localapi exposes a versioned, pipe-only adapter to the public Go API.
// It never listens on a network or management socket.
package localapi

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/pacificbelt30/codex-dock/pkg/cxd"
)

const (
	ProtocolVersion      = 1
	MaxRequestBytes      = 1 << 20
	MaxResponseBytes     = 4 << 20
	MaxOutputBytes       = 256 << 10 // Per exec stream; buffers are never unbounded.
	MaxConcurrentCalls   = 16
	MaxTimeoutMS         = 1200000
	MaxFileTransferBytes = 512 << 10
)

type Request struct {
	Version   int             `json:"version"`
	ID        uint64          `json:"id"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params"`
	TimeoutMS int64           `json:"timeout_ms,omitempty"`
}

type Response struct {
	Version int    `json:"version"`
	ID      uint64 `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   *Error `json:"error,omitempty"`
}

// Error contains fixed diagnostic categories, never an underlying error string.
// A partial result (notably Worker.ID) may accompany an error.
type Error struct {
	Code      string   `json:"code"`
	Codes     []string `json:"codes"`
	Message   string   `json:"message"`
	Reconcile bool     `json:"requires_reconciliation"`
}

type OpenParams struct {
	Options cxd.Options `json:"options"`
}
type LaunchParams struct {
	Request cxd.LaunchRequest `json:"request"`
}
type WorkerParams struct {
	WorkerID string `json:"worker_id"`
}
type RequestParams struct {
	RequestID string `json:"request_id"`
}
type RemoveParams struct {
	WorkerID string `json:"worker_id"`
	Force    bool   `json:"force"`
}
type RemoveRequestParams struct {
	RequestID string `json:"request_id"`
	Force     bool   `json:"force"`
}
type StopParams struct {
	WorkerID       string `json:"worker_id"`
	TimeoutSeconds *int64 `json:"timeout_seconds,omitempty"`
}
type CancelParams struct {
	CallID uint64 `json:"call_id"`
}
type CancelResult struct {
	Accepted bool `json:"accepted"`
}
type ExecParams struct {
	WorkerID   string   `json:"worker_id"`
	Argv       []string `json:"argv"`
	WorkingDir string   `json:"working_dir,omitempty"`
	// Env is guest-only. Provider credentials belong in the existing proxy.
	Env   []string `json:"env,omitempty"`
	Stdin []byte   `json:"stdin,omitempty"`
}
type ExecResult struct {
	ExitCode int64  `json:"exit_code"`
	Stdout   []byte `json:"stdout"`
	Stderr   []byte `json:"stderr"`
}
type ExitResult struct {
	ExitCode int64 `json:"exit_code"`
}
type Success struct {
	OK bool `json:"ok"`
}
type Capabilities struct {
	MaxFileTransferBytes int              `json:"max_file_transfer_bytes"`
	ProtocolVersion      int              `json:"protocol_version"`
	Node                 cxd.Capabilities `json:"node"`
	Methods              []string         `json:"methods"`
	MaxRequestBytes      int              `json:"max_request_bytes"`
	MaxResponseBytes     int              `json:"max_response_bytes"`
	MaxOutputBytes       int              `json:"max_output_bytes"`
	MaxConcurrentCalls   int              `json:"max_concurrent_calls"`
}

// Backend deliberately calls the same exported operations as an embedded Go
// product. No raw node HTTP route or credential-returning operation is exposed.
type Backend interface {
	Close() error
	Capabilities() cxd.Capabilities
	ProxyCapabilities(context.Context) (cxd.ProxyCapabilities, error)
	Create(context.Context, cxd.LaunchRequest) (cxd.Worker, error)
	Launch(context.Context, cxd.LaunchRequest) (cxd.Worker, error)
	Get(context.Context, string) (cxd.Worker, error)
	List(context.Context) ([]cxd.Worker, error)
	Request(context.Context, string) (cxd.RequestStatus, error)
	RemoveRequest(context.Context, string, bool) error
	Start(context.Context, string) error
	Stop(context.Context, string, time.Duration) error
	Remove(context.Context, string, bool) error
	Quarantine(context.Context, string) error
	Wait(context.Context, string) (int64, error)
	Exec(context.Context, string, cxd.ExecRequest) (int64, error)
	Profiles(context.Context) ([]cxd.Profile, error)
	ResourcePolicies(context.Context) ([]cxd.ResourcePolicy, error)
	ResourceUsage(context.Context, string) (cxd.ResourceUsage, error)
}

type Factory func(context.Context, cxd.Options) (Backend, error)

type FileParams struct {
	WorkerID string `json:"worker_id"`
	Path     string `json:"path"`
	Data     []byte `json:"data,omitempty"`
}
type DataResult struct {
	Data []byte `json:"data"`
}
type JobParams struct {
	WorkerID string `json:"worker_id"`
	JobID    string `json:"job_id"`
	Offset   int64  `json:"offset,omitempty"`
}
type CheckpointParams struct {
	WorkerID string                `json:"worker_id,omitempty"`
	ID       string                `json:"checkpoint_id,omitempty"`
	Options  cxd.CheckpointOptions `json:"options"`
}

// WorkspaceBackend extends v1 additively without changing lifecycle contracts.
type WorkspaceBackend interface {
	ReadFile(context.Context, string, string, io.Writer) error
	WriteFile(context.Context, string, string, io.Reader) error
	ListFiles(context.Context, string, string, io.Writer) error
	StartJob(context.Context, string, cxd.ExecRequest) (cxd.Job, error)
	Job(context.Context, string, string) (cxd.Job, error)
	ListJobs(context.Context, string) ([]cxd.Job, error)
	JobLogs(context.Context, string, string, int64) (cxd.JobOutput, error)
	KillJob(context.Context, string, string) error
	WaitJob(context.Context, string, string) (cxd.Job, error)
	Checkpoint(context.Context, string, cxd.CheckpointOptions) (cxd.DiskCheckpoint, error)
	Checkpoints(context.Context) (cxd.CheckpointCatalog, error)
	InspectCheckpoint(context.Context, string) (cxd.DiskCheckpoint, error)
	RemoveCheckpoint(context.Context, string) error
}
