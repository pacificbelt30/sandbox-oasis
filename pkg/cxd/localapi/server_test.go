package localapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pacificbelt30/codex-dock/pkg/cxd"
)

const fixtureSecret = "fixture-private-secret-must-never-be-serialized"

type fakeBackend struct {
	Backend
	mu       sync.Mutex
	launches map[string]cxd.LaunchRequest
	workers  map[string]cxd.Worker
}

func newFake() *fakeBackend {
	return &fakeBackend{launches: map[string]cxd.LaunchRequest{}, workers: map[string]cxd.Worker{}}
}
func (*fakeBackend) Close() error { return nil }
func (*fakeBackend) Capabilities() cxd.Capabilities {
	return cxd.Capabilities{BuildVersion: "fixture", ProtocolVersion: 2, Features: []string{"staged-create-v1"}}
}
func (*fakeBackend) ProxyCapabilities(context.Context) (cxd.ProxyCapabilities, error) {
	return cxd.ProxyCapabilities{WorkerMTLS: true}, nil
}
func (b *fakeBackend) Create(ctx context.Context, r cxd.LaunchRequest) (cxd.Worker, error) {
	return b.Launch(ctx, r)
}
func (b *fakeBackend) Launch(ctx context.Context, r cxd.LaunchRequest) (cxd.Worker, error) {
	if r.RequestID == "block" {
		<-ctx.Done()
		return cxd.Worker{ID: "partial-worker"}, ctx.Err()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if old, ok := b.launches[r.RequestID]; ok {
		if !reflect.DeepEqual(old, r) {
			return cxd.Worker{}, cxd.ErrConflict
		}
		w, ok := b.workers[r.RequestID]
		if !ok {
			return cxd.Worker{ID: r.RequestID}, cxd.ErrRequestGone
		}
		return w, nil
	}
	b.launches[r.RequestID] = r
	w := cxd.Worker{ID: r.RequestID, Name: "fixture", Status: "created", Image: r.Image}
	b.workers[r.RequestID] = w
	if r.RequestID == "lost-result" {
		return w, errors.Join(cxd.ErrOutcomeUnknown, errors.New(fixtureSecret))
	}
	return w, nil
}
func (b *fakeBackend) Get(_ context.Context, id string) (cxd.Worker, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	w, ok := b.workers[id]
	if !ok {
		return cxd.Worker{}, cxd.ErrNotFound
	}
	return w, nil
}
func (b *fakeBackend) List(context.Context) ([]cxd.Worker, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := []cxd.Worker{}
	for _, w := range b.workers {
		r = append(r, w)
	}
	return r, nil
}
func (b *fakeBackend) Request(_ context.Context, id string) (cxd.RequestStatus, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.launches[id]; !ok {
		return cxd.RequestStatus{}, cxd.ErrNotFound
	}
	return cxd.RequestStatus{State: "complete", WorkerID: id, Name: "fixture", Attempt: 1}, nil
}
func (b *fakeBackend) Start(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	w, ok := b.workers[id]
	if !ok {
		return cxd.ErrNotFound
	}
	w.Status = "running"
	b.workers[id] = w
	return nil
}
func (b *fakeBackend) Stop(_ context.Context, id string, _ time.Duration) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	w, ok := b.workers[id]
	if !ok {
		return cxd.ErrNotFound
	}
	w.Status = "exited"
	b.workers[id] = w
	return nil
}
func (b *fakeBackend) Remove(_ context.Context, id string, _ bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.workers[id]; !ok {
		return cxd.ErrNotFound
	}
	delete(b.workers, id)
	return nil
}
func (b *fakeBackend) RemoveRequest(ctx context.Context, id string, force bool) error {
	if _, err := b.Request(ctx, id); err != nil {
		return err
	}
	err := b.Remove(ctx, id, force)
	if errors.Is(err, cxd.ErrNotFound) {
		return nil
	}
	return err
}
func (b *fakeBackend) Quarantine(ctx context.Context, id string) error { return b.Stop(ctx, id, 0) }
func (b *fakeBackend) Wait(ctx context.Context, id string) (int64, error) {
	if id == "block" {
		<-ctx.Done()
		return -1, ctx.Err()
	}
	return 37, nil
}
func (b *fakeBackend) Exec(ctx context.Context, _ string, r cxd.ExecRequest) (int64, error) {
	switch r.Argv[0] {
	case "stdin-mode":
		if r.Stdin == nil {
			_, _ = io.WriteString(r.Stdout, "absent")
		} else {
			value, _ := io.ReadAll(r.Stdin)
			if len(value) == 0 {
				_, _ = io.WriteString(r.Stdout, "empty")
			} else {
				_, _ = io.WriteString(r.Stdout, "present")
			}
		}
		return 0, nil
	case "block":
		<-ctx.Done()
		return -1, ctx.Err()
	case "private-error":
		return -1, errors.New(fixtureSecret)
	case "overflow":
		_, err := io.WriteString(r.Stdout, strings.Repeat("x", MaxOutputBytes+1))
		return -1, err
	default:
		var err error
		if r.Stdin != nil {
			_, err = io.Copy(r.Stdout, r.Stdin)
		}
		if err != nil {
			return -1, err
		}
		_, err = io.WriteString(r.Stderr, "fixture-stderr")
		return 37, err
	}
}
func (*fakeBackend) Profiles(context.Context) ([]cxd.Profile, error) { return cxd.Profiles(), nil }
func (*fakeBackend) ResourcePolicies(context.Context) ([]cxd.ResourcePolicy, error) {
	return []cxd.ResourcePolicy{{Name: "compact", CPUCores: 1, GuestMemoryBytes: 512 << 20, HostMemoryBytes: 768 << 20, PIDLimit: 256}}, nil
}
func (*fakeBackend) ResourceUsage(context.Context, string) (cxd.ResourceUsage, error) {
	return cxd.ResourceUsage{Worker: "fixture", ObservedAt: time.Now().UTC(), Profile: cxd.Profiles()[0]}, nil
}

type harness struct {
	c       net.Conn
	decoder *json.Decoder
	next    uint64
	done    chan error
}

func startHarness(t *testing.T, f Factory) *harness {
	t.Helper()
	client, server := net.Pipe()
	h := &harness{c: client, decoder: json.NewDecoder(client), done: make(chan error, 1)}
	go func() { h.done <- Serve(context.Background(), server, server, f); _ = server.Close() }()
	t.Cleanup(func() {
		_ = client.Close()
		select {
		case <-h.done:
		case <-time.After(3 * time.Second):
			t.Error("server did not cancel outstanding calls on EOF")
		}
	})
	return h
}
func (h *harness) send(t *testing.T, method string, p any) uint64 {
	t.Helper()
	h.next++
	raw, _ := json.Marshal(p)
	if err := json.NewEncoder(h.c).Encode(Request{Version: 1, ID: h.next, Method: method, Params: raw}); err != nil {
		t.Fatal(err)
	}
	return h.next
}
func (h *harness) read(t *testing.T) Response {
	t.Helper()
	_ = h.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var r Response
	if err := h.decoder.Decode(&r); err != nil {
		t.Fatal(err)
	}
	return r
}
func fakeFactory(b *fakeBackend) Factory {
	return func(_ context.Context, o cxd.Options) (Backend, error) {
		if o.NodeSocket == "/permission-denied" {
			return nil, errors.Join(os.ErrPermission, errors.New(fixtureSecret))
		}
		if o.NodeSocket == "/version-mismatch" {
			return nil, errors.Join(cxd.ErrVersionMismatch, errors.New(fixtureSecret))
		}
		return b, nil
	}
}
func openHarness(t *testing.T, b *fakeBackend) *harness {
	h := startHarness(t, fakeFactory(b))
	h.send(t, "open", OpenParams{})
	if r := h.read(t); r.Error != nil {
		t.Fatal(r.Error)
	}
	return h
}

func TestProtocolLifecycleReplayAndReconciliation(t *testing.T) {
	b := newFake()
	h := openHarness(t, b)
	req := cxd.LaunchRequest{RequestID: "launch-1", ProjectDir: "/workspace", NoInternet: true}
	for range 2 {
		h.send(t, "create", LaunchParams{Request: req})
		if r := h.read(t); r.Error != nil {
			t.Fatal(r)
		}
	}
	b.mu.Lock()
	count := len(b.workers)
	b.mu.Unlock()
	if count != 1 {
		t.Fatal("replay duplicated worker")
	}
	req.Image = "different"
	h.send(t, "create", LaunchParams{Request: req})
	if r := h.read(t); r.Error == nil || r.Error.Code != "conflict" || r.Error.Reconcile {
		t.Fatal(r)
	}
	h.send(t, "remove_request", RemoveRequestParams{RequestID: "launch-1", Force: true})
	if r := h.read(t); r.Error != nil {
		t.Fatal(r)
	}
	h.send(t, "remove_request", RemoveRequestParams{RequestID: "launch-1", Force: true})
	if r := h.read(t); r.Error != nil {
		t.Fatal(r)
	}
	req.Image = ""
	h.send(t, "create", LaunchParams{Request: req})
	if r := h.read(t); r.Error == nil || r.Error.Code != "request_gone" {
		t.Fatal(r)
	}
}
func TestCancelRetainsPartialWorkerAndDoesNotCancelOtherCalls(t *testing.T) {
	h := openHarness(t, newFake())
	id := h.send(t, "launch", LaunchParams{Request: cxd.LaunchRequest{RequestID: "block", ProjectDir: "/workspace"}})
	h.send(t, "cancel", CancelParams{CallID: id})
	seen := map[uint64]Response{}
	for range 2 {
		r := h.read(t)
		seen[r.ID] = r
	}
	if r := seen[id]; r.Error == nil || r.Error.Code != "canceled" || !r.Error.Reconcile || r.Result != nil && r.Result.(map[string]any)["ID"] != "partial-worker" {
		t.Fatal(r)
	}
	h.send(t, "profiles", struct{}{})
	if r := h.read(t); r.Error != nil {
		t.Fatal(r)
	}
}
func TestDeadlineAndOutputLimitAreExplicit(t *testing.T) {
	h := openHarness(t, newFake())
	raw, _ := json.Marshal(ExecParams{WorkerID: "worker", Argv: []string{"block"}})
	h.next++
	if err := json.NewEncoder(h.c).Encode(Request{Version: 1, ID: h.next, Method: "exec", Params: raw, TimeoutMS: 10}); err != nil {
		t.Fatal(err)
	}
	if r := h.read(t); r.Error == nil || r.Error.Code != "deadline_exceeded" || !r.Error.Reconcile {
		t.Fatal(r)
	}
	h.send(t, "exec", ExecParams{WorkerID: "worker", Argv: []string{"overflow"}})
	r := h.read(t)
	if r.Error == nil || !slices.Contains(r.Error.Codes, "output_limit") || !r.Error.Reconcile {
		t.Fatalf("output limit not reported: error=%+v", r.Error)
	}
	output := r.Result.(map[string]any)["stdout"].(string)
	if len(output) > MaxOutputBytes*2 {
		t.Fatal("unbounded output")
	}
}
func TestVersionValidationAndPrivateErrors(t *testing.T) {
	h := startHarness(t, fakeFactory(newFake()))
	h.send(t, "open", OpenParams{Options: cxd.Options{NodeSocket: "/version-mismatch", ProxyAdminSecret: fixtureSecret}})
	r := h.read(t)
	data, _ := json.Marshal(r)
	if r.Error == nil || r.Error.Code != "version_mismatch" || bytes.Contains(data, []byte(fixtureSecret)) {
		t.Fatal("version error was not safely classified")
	}
	h.send(t, "open", OpenParams{})
	if r := h.read(t); r.Error != nil {
		t.Fatal(r)
	}
	h.send(t, "exec", ExecParams{WorkerID: "worker", Argv: []string{"private-error"}, Env: []string{"PRIVATE=" + fixtureSecret}})
	r = h.read(t)
	data, _ = json.Marshal(r)
	if r.Error == nil || r.Error.Code != "internal" || bytes.Contains(data, []byte(fixtureSecret)) {
		t.Fatal("underlying error/command leaked")
	}
}
func TestMalformedFramesCannotOpenOrMutate(t *testing.T) {
	for _, line := range []string{
		`{"version":1,"id":1,"method":"open","params":{"options":{}},"id":2}`,
		`{"version":1,"id":1,"method":"open","params":{"options":{"NodeSocket":"/a","nodesocket":"/b"}}}`,
		`{"version":1,"id":2,"method":"open","params":{"options":{}}}`,
		`{"version":1,"id":1,"method":"open","params":{"options":{}},"unknown":true}`,
	} {
		t.Run(line, func(t *testing.T) {
			called := false
			h := startHarness(t, func(context.Context, cxd.Options) (Backend, error) { called = true; return newFake(), nil })
			_, _ = io.WriteString(h.c, line+"\n")
			r := h.read(t)
			if r.Error == nil || called {
				t.Fatal("malformed frame reached backend")
			}
		})
	}
	h := openHarness(t, newFake())
	h.send(t, "create", map[string]any{"request": map[string]any{"RequestID": "r", "ProjectDir": "/workspace", "Unexpected": "secret"}})
	if r := h.read(t); r.Error == nil || r.Error.Code != "invalid_argument" || r.Error.Reconcile {
		t.Fatal(r)
	}
}
func TestSchemaMatchesDTOsAndMethodSemantics(t *testing.T) {
	data, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile("../../../api/local-v1/schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, saved) {
		t.Fatal("regenerate spec/types with go run ./api/generate")
	}
	for _, m := range methods() {
		if !slices.Contains(methodNames(), m.Name) || mutating(m.Name) != m.Mutation {
			t.Fatal("method contract drift", m.Name)
		}
	}
}
func TestSDKFixture(t *testing.T) {
	if os.Getenv("OAISO_SDK_FIXTURE") != "1" {
		return
	}
	err := Serve(context.Background(), os.Stdin, os.Stdout, fakeFactory(newFake()))
	if err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestVersionIsCheckedBeforeFactoryAndCallsAreBounded(t *testing.T) {
	h := startHarness(t, fakeFactory(newFake()))
	_, _ = io.WriteString(h.c, `{"version":999,"id":1,"method":"open","params":{"options":{}}}`+"\n")
	h.next = 1
	if r := h.read(t); r.Error == nil || r.Error.Code != "version_mismatch" || r.Error.Reconcile {
		t.Fatal("wire version was not rejected")
	}
	h.send(t, "open", OpenParams{})
	if r := h.read(t); r.Error != nil {
		t.Fatal(r.Error)
	}
	ids := []uint64{}
	for range MaxConcurrentCalls {
		ids = append(ids, h.send(t, "wait", WorkerParams{WorkerID: "block"}))
	}
	h.send(t, "get", WorkerParams{WorkerID: "worker"})
	if r := h.read(t); r.Error == nil || r.Error.Code != "busy" || r.Error.Reconcile {
		t.Fatal("inflight limit was not enforced")
	}
	for _, id := range ids {
		h.send(t, "cancel", CancelParams{CallID: id})
		for range 2 {
			_ = h.read(t)
		}
	}
	h.send(t, "profiles", struct{}{})
	if r := h.read(t); r.Error != nil {
		t.Fatal("cancel failed to release capacity")
	}
}
func TestDisconnectCancelsWorkWithoutInventingCleanup(t *testing.T) {
	b := newFake()
	h := openHarness(t, b)
	h.send(t, "launch", LaunchParams{Request: cxd.LaunchRequest{RequestID: "kept-after-disconnect", ProjectDir: "/workspace"}})
	// Wait until the response is ready but do not read it. This drops the actual
	// success response, rather than asking the fake backend to return an error.
	deadline := time.Now().Add(time.Second)
	for {
		b.mu.Lock()
		_, ok := b.workers["kept-after-disconnect"]
		b.mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("launch never committed")
		}
		time.Sleep(time.Millisecond)
	}
	_ = h.c.Close()
	next := openHarness(t, b)
	next.send(t, "request", RequestParams{RequestID: "kept-after-disconnect"})
	if r := next.read(t); r.Error != nil || r.Result.(map[string]any)["State"] != "complete" {
		t.Fatal("lost response could not be reconciled")
	}
	next.send(t, "launch", LaunchParams{Request: cxd.LaunchRequest{RequestID: "kept-after-disconnect", ProjectDir: "/workspace"}})
	if r := next.read(t); r.Error != nil {
		t.Fatal("lost response caused duplicate/rejection")
	}
	next.send(t, "remove_request", RemoveRequestParams{RequestID: "kept-after-disconnect", Force: true})
	if r := next.read(t); r.Error != nil {
		t.Fatal("lost response cleanup failed")
	}
}

func TestUnreadRepliesRemainInsideTheInflightBound(t *testing.T) {
	b := newFake()
	h := openHarness(t, b)
	_ = h.c.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
	for i := 0; i < MaxConcurrentCalls*3; i++ {
		h.next++
		raw, _ := json.Marshal(LaunchParams{Request: cxd.LaunchRequest{RequestID: fmt.Sprintf("backpressure-%d", i), ProjectDir: "/workspace"}})
		if err := json.NewEncoder(h.c).Encode(Request{Version: 1, ID: h.next, Method: "create", Params: raw}); err != nil {
			break
		}
	}
	b.mu.Lock()
	count := len(b.workers)
	b.mu.Unlock()
	if count > MaxConcurrentCalls {
		t.Fatalf("unread output admitted %d completed mutations; want <=%d", count, MaxConcurrentCalls)
	}
}
