package localapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pacificbelt30/codex-dock/internal/strictjson"
	"github.com/pacificbelt30/codex-dock/pkg/cxd"
)

var (
	errInvalid = errors.New("invalid argument")
	errOutput  = errors.New("output limit")
	errNotOpen = errors.New("not initialized")
	errBusy    = errors.New("too many calls")
)

// Serve owns input until it returns. EOF, a broken output pipe or cancellation
// cancels active calls and closes Go handles, not the workers they created.
func Serve(parent context.Context, input io.ReadCloser, output io.Writer, factory Factory) error {
	if factory == nil {
		factory = func(ctx context.Context, o cxd.Options) (Backend, error) { return cxd.Open(ctx, o) }
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	s := &server{ctx: ctx, cancel: cancel, output: output, factory: factory, active: map[uint64]context.CancelFunc{}}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = input.Close()
		case <-done:
		}
	}()
	defer close(done)
	defer func() {
		cancel()
		s.wg.Wait()
		if s.backend != nil {
			_ = s.backend.Close()
		}
	}()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), MaxRequestBytes+1)
	var previous uint64
	for scanner.Scan() {
		line := scanner.Bytes()
		var req Request
		if len(line) > MaxRequestBytes || !utf8.Valid(line) || strictjson.Decode(bytes.NewReader(line), MaxRequestBytes, &req) != nil || req.ID != previous+1 || req.ID > 1<<53-1 {
			s.reply(Response{ID: 0, Error: wireError(errInvalid, false)})
			return errors.New("invalid local API frame")
		}
		previous = req.ID
		if req.Version != ProtocolVersion {
			s.reply(Response{ID: req.ID, Error: wireError(cxd.ErrVersionMismatch, false)})
			continue
		}
		if req.TimeoutMS < 0 || req.TimeoutMS > MaxTimeoutMS || len(req.Params) == 0 || req.Params[0] != '{' || !slices.Contains(methodNames(), req.Method) {
			s.reply(Response{ID: req.ID, Error: wireError(errInvalid, false)})
			continue
		}
		if req.Method == "cancel" {
			var p CancelParams
			if decode(req.Params, &p) != nil || p.CallID == 0 {
				s.reply(Response{ID: req.ID, Error: wireError(errInvalid, false)})
				continue
			}
			s.mu.Lock()
			stop, ok := s.active[p.CallID]
			s.mu.Unlock()
			if ok {
				stop()
			}
			s.reply(Response{ID: req.ID, Result: CancelResult{Accepted: ok}})
			continue
		}
		s.mu.Lock()
		if len(s.active) >= MaxConcurrentCalls {
			s.mu.Unlock()
			s.reply(Response{ID: req.ID, Error: wireError(errBusy, false)})
			continue
		}
		timeout := req.TimeoutMS
		if timeout == 0 {
			timeout = 180000
		}
		callCtx, stop := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
		s.active[req.ID] = stop
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			defer stop()
			defer func() { s.mu.Lock(); delete(s.active, req.ID); s.mu.Unlock() }()
			result, err := s.dispatch(callCtx, req)
			// Keep backpressure within the active-call bound until the reply
			// is written. A caller that stops reading cannot queue unbounded
			// completed responses and their goroutines.
			s.reply(Response{ID: req.ID, Result: result, Error: wireError(err, mutating(req.Method))})
		}()
	}
	if scanner.Err() != nil && ctx.Err() == nil {
		return errors.New("local API input failed or exceeded limit")
	}
	return nil
}

type server struct {
	ctx      context.Context
	cancel   context.CancelFunc
	output   io.Writer
	factory  Factory
	outputMu sync.Mutex
	mu       sync.Mutex
	wg       sync.WaitGroup
	active   map[uint64]context.CancelFunc
	backend  Backend
	opening  bool
}

func (s *server) reply(r Response) {
	r.Version = ProtocolVersion
	data, err := json.Marshal(r)
	if err != nil || len(data) > MaxResponseBytes {
		data, _ = json.Marshal(Response{Version: ProtocolVersion, ID: r.ID, Error: wireError(errOutput, true)})
	}
	s.outputMu.Lock()
	defer s.outputMu.Unlock()
	frame := append(data, '\n')
	if n, err := s.output.Write(frame); err != nil || n != len(frame) {
		s.cancel()
	}
}
func decode(raw json.RawMessage, v any) error {
	if strictjson.Decode(bytes.NewReader(raw), MaxRequestBytes, v) != nil {
		return errInvalid
	}
	return nil
}
func (s *server) dispatch(ctx context.Context, r Request) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if r.Method == "open" {
		var p OpenParams
		if decode(r.Params, &p) != nil {
			return nil, errInvalid
		}
		if p.Options.NodeSocket != "" && (!filepath.IsAbs(p.Options.NodeSocket) || filepath.Clean(p.Options.NodeSocket) != p.Options.NodeSocket) {
			return nil, errInvalid
		}
		s.mu.Lock()
		if s.backend != nil || s.opening {
			s.mu.Unlock()
			return nil, errInvalid
		}
		s.opening = true
		s.mu.Unlock()
		b, err := s.factory(ctx, p.Options)
		s.mu.Lock()
		s.opening = false
		if err == nil {
			s.backend = b
		}
		s.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return capabilities(b), nil
	}
	s.mu.Lock()
	b := s.backend
	s.mu.Unlock()
	if b == nil {
		return nil, errNotOpen
	}
	switch r.Method {
	case "capabilities", "proxy_capabilities", "profiles", "resource_policies", "list":
		if decode(r.Params, &struct{}{}) != nil {
			return nil, errInvalid
		}
		switch r.Method {
		case "capabilities":
			return capabilities(b), nil
		case "proxy_capabilities":
			return b.ProxyCapabilities(ctx)
		case "profiles":
			return b.Profiles(ctx)
		case "resource_policies":
			return b.ResourcePolicies(ctx)
		default:
			return b.List(ctx)
		}
	case "create", "launch":
		var p LaunchParams
		if decode(r.Params, &p) != nil || p.Request.RequestID == "" || len(p.Request.RequestID) > 256 || !filepath.IsAbs(p.Request.ProjectDir) {
			return nil, errInvalid
		}
		if r.Method == "create" {
			return b.Create(ctx, p.Request)
		}
		return b.Launch(ctx, p.Request)
	case "request":
		var p RequestParams
		if decode(r.Params, &p) != nil || p.RequestID == "" || len(p.RequestID) > 256 {
			return nil, errInvalid
		}
		return b.Request(ctx, p.RequestID)
	case "remove_request":
		var p RemoveRequestParams
		if decode(r.Params, &p) != nil || p.RequestID == "" || len(p.RequestID) > 256 {
			return nil, errInvalid
		}
		return success(b.RemoveRequest(ctx, p.RequestID, p.Force))
	case "stop":
		var p StopParams
		seconds := int64(10)
		if err := decode(r.Params, &p); err != nil {
			return nil, err
		}
		if p.TimeoutSeconds != nil {
			seconds = *p.TimeoutSeconds
		}
		if p.WorkerID == "" || seconds < 0 || seconds > math.MaxInt32 {
			return nil, errInvalid
		}
		return success(b.Stop(ctx, p.WorkerID, time.Duration(seconds)*time.Second))
	case "remove":
		var p RemoveParams
		if decode(r.Params, &p) != nil || p.WorkerID == "" {
			return nil, errInvalid
		}
		return success(b.Remove(ctx, p.WorkerID, p.Force))
	case "exec":
		var p ExecParams
		if decode(r.Params, &p) != nil || p.WorkerID == "" || len(p.Argv) == 0 {
			return nil, errInvalid
		}
		execCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		stdout := &limitedOutput{cancel: cancel}
		stderr := &limitedOutput{cancel: cancel}
		var input io.Reader
		if p.Stdin != nil {
			input = bytes.NewReader(p.Stdin)
		}
		code, err := b.Exec(execCtx, p.WorkerID, cxd.ExecRequest{Argv: p.Argv, Env: p.Env, WorkingDir: p.WorkingDir, Stdin: input, Stdout: stdout, Stderr: stderr})
		if stdout.exceeded || stderr.exceeded {
			err = errors.Join(errOutput, err)
		}
		return ExecResult{ExitCode: code, Stdout: append([]byte{}, stdout.buffer.Bytes()...), Stderr: append([]byte{}, stderr.buffer.Bytes()...)}, err
	default:
		var p WorkerParams
		if decode(r.Params, &p) != nil || p.WorkerID == "" {
			return nil, errInvalid
		}
		switch r.Method {
		case "get":
			return b.Get(ctx, p.WorkerID)
		case "resource_usage":
			return b.ResourceUsage(ctx, p.WorkerID)
		case "wait":
			code, err := b.Wait(ctx, p.WorkerID)
			return ExitResult{ExitCode: code}, err
		case "start":
			return success(b.Start(ctx, p.WorkerID))
		case "quarantine":
			return success(b.Quarantine(ctx, p.WorkerID))
		}
	}
	return nil, errInvalid
}
func capabilities(b Backend) Capabilities {
	return Capabilities{ProtocolVersion: ProtocolVersion, Node: b.Capabilities(), Methods: methodNames(), MaxRequestBytes: MaxRequestBytes, MaxResponseBytes: MaxResponseBytes, MaxOutputBytes: MaxOutputBytes, MaxConcurrentCalls: MaxConcurrentCalls}
}
func methodNames() []string {
	names := make([]string, 0, len(methods()))
	for _, m := range methods() {
		names = append(names, m.Name)
	}
	return names
}
func mutating(name string) bool {
	for _, m := range methods() {
		if m.Name == name {
			return m.Mutation
		}
	}
	return false
}

type limitedOutput struct {
	buffer   bytes.Buffer
	exceeded bool
	cancel   context.CancelFunc
}

func (w *limitedOutput) Write(p []byte) (int, error) {
	remaining := MaxOutputBytes - w.buffer.Len()
	if len(p) <= remaining {
		return w.buffer.Write(p)
	}
	_, _ = w.buffer.Write(p[:remaining])
	w.exceeded = true
	w.cancel()
	return remaining, errOutput
}
func wireError(err error, mutation bool) *Error {
	if err == nil {
		return nil
	}
	var codes []string
	for _, category := range []struct {
		code string
		err  error
	}{
		{"invalid_argument", errInvalid}, {"version_mismatch", cxd.ErrVersionMismatch}, {"conflict", cxd.ErrConflict}, {"cleanup_pending", cxd.ErrCleanupPending}, {"outcome_unknown", cxd.ErrOutcomeUnknown}, {"request_gone", cxd.ErrRequestGone}, {"in_progress", cxd.ErrInProgress}, {"capacity", cxd.ErrCapacity}, {"not_found", cxd.ErrNotFound}, {"canceled", context.Canceled}, {"deadline_exceeded", context.DeadlineExceeded}, {"permission_denied", os.ErrPermission}, {"output_limit", errOutput}, {"not_initialized", errNotOpen}, {"busy", errBusy},
	} {
		if errors.Is(err, category.err) {
			codes = append(codes, category.code)
		}
	}
	if len(codes) == 0 {
		codes = []string{"internal"}
	}
	reconcile := mutation
	if slices.Contains(codes, "invalid_argument") || slices.Contains(codes, "version_mismatch") || slices.Contains(codes, "conflict") || slices.Contains(codes, "request_gone") || slices.Contains(codes, "not_initialized") || slices.Contains(codes, "busy") {
		reconcile = false
	}
	if slices.Contains(codes, "cleanup_pending") || slices.Contains(codes, "outcome_unknown") {
		reconcile = mutation
	}
	// Never serialize wrapped errors: filesystem paths, command arguments and
	// provider errors can carry credentials. Codes retain joined Go categories.
	return &Error{Code: codes[0], Codes: codes, Message: strings.ReplaceAll(codes[0], "_", " "), Reconcile: reconcile}
}

func success(err error) (any, error) {
	if err != nil {
		return nil, err
	}
	return Success{OK: true}, nil
}
