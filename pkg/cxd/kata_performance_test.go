//go:build linux

package cxd

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	dn "github.com/docker/docker/api/types/network"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/network"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/credentialstore"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

// Explicitly selected on a disposable node. Wall-time samples exclude control
// handshakes, metric reads, image import, proxy startup and cleanup.
func TestKataPerformance(t *testing.T) {
	if os.Getenv("OAISO_KATA_PERFORMANCE") != "1" {
		t.Skip("set OAISO_KATA_PERFORMANCE=1 on an isolated node")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	control := func(action, label string) {
		t.Helper()
		dir := os.Getenv("OAISO_PERF_CONTROL_DIR")
		if dir == "" {
			return
		}
		_ = os.Remove(filepath.Join(dir, "complete"))
		if err := os.WriteFile(filepath.Join(dir, "request.tmp"), []byte(action+" "+label+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(dir, "request.tmp"), filepath.Join(dir, "request")); err != nil {
			t.Fatal(err)
		}
		for {
			if _, err := os.Stat(filepath.Join(dir, "complete")); err == nil {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(25 * time.Millisecond):
			}
		}
	}
	label := os.Getenv("OAISO_PERF_LABEL")
	if label == "" {
		label = "baseline0"
	}
	control("perf-variant", label)
	node, err := engine.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = node.Close() }()
	if _, err := node.NetworkInspect(ctx, network.EgressNetworkName, dn.InspectOptions{}); errdefs.IsNotFound(err) {
		defer func() { _ = node.NetworkRemove(context.Background(), network.EgressNetworkName) }()
	} else if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err = os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	caseID := fmt.Sprintf("perf-%x", time.Now().UnixNano())
	socket := filepath.Join(root, "store.sock")
	storeCtx, stopStore := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- credentialstore.Serve(storeCtx, socket, caseID, &apiFixtureStore{creds: credentialstore.Credentials{Version: "1", ConnectionVersion: "1", OpenAIAPIKey: "fixture-performance-never-valid"}})
	}()
	defer func() {
		stopStore()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	for i := 0; ; i++ {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if i > 100 {
			t.Fatal("store not ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	proxy, err := EnsureCaseProxy(ctx, CaseProxyOptions{Name: caseID, CaseID: caseID, StoreSocket: socket, AdminPort: port, AdminSecret: strings.Repeat("fixture-performance-admin-", 2), Image: os.Getenv("CODEX_DOCK_KATA_PROXY_IMAGE")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := RemoveCaseProxy(context.Background(), engine.Socket(), caseID, caseID); err != nil {
			t.Error(err)
		}
	}()
	c, err := Open(ctx, Options{NodeSocket: engine.Socket(), ProxyAdminURL: proxy.AdminURL, ProxyContainerURL: proxy.ContainerURL, ProxyAdminSecret: strings.Repeat("fixture-performance-admin-", 2), StateDir: filepath.Join(root, "state")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	var owned []string
	defer func() {
		for _, id := range owned {
			_ = c.Remove(context.Background(), id, true)
		}
	}()
	next := 0
	request := func() LaunchRequest {
		next++
		id := fmt.Sprintf("%s/%d", caseID, next)
		return LaunchRequest{RequestID: id, ProjectDir: t.TempDir(), Image: os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE"), Profile: "compact", User: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), NoInternet: true, NoGitMetadata: true, Identity: identity.Identity{CaseID: caseID, RunID: id, ConnectionVersion: "1", Provider: "openai"}}
	}
	emit := func(kind string, elapsed time.Duration, timing any) {
		raw, _ := json.Marshal(map[string]any{"label": label, "kind": kind, "wall_ns": elapsed.Nanoseconds(), "timings": timing})
		t.Log("perf_sample " + string(raw))
	}
	begin := func(kind string) { control("perf-begin", label+"_"+kind) }
	end := func(kind string) { control("perf-end", label+"_"+kind) }
	// Prepared-image launch samples, with no VM pool or relaxed bootstrap.
	begin("launch")
	for i := 0; i < 3; i++ {
		w, timing, err := c.LaunchWithTimings(ctx, request())
		if w.ID != "" {
			owned = append(owned, w.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
		emit("launch", timing.Duration, timing)
		if err = c.Remove(ctx, w.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	end("launch")
	begin("parallel_launch")
	for n := 0; n < 3; n++ {
		reqs := []LaunchRequest{request(), request()}
		workers := make([]Worker, 2)
		timings := make([]LaunchTimings, 2)
		errs := make([]error, 2)
		started := time.Now()
		var wg sync.WaitGroup
		for i := range 2 {
			wg.Add(1)
			go func() { defer wg.Done(); workers[i], timings[i], errs[i] = c.LaunchWithTimings(ctx, reqs[i]) }()
		}
		wg.Wait()
		elapsed := time.Since(started)
		for i := range 2 {
			if workers[i].ID != "" {
				owned = append(owned, workers[i].ID)
			}
			if errs[i] != nil {
				t.Fatal(errs[i])
			}
		}
		emit("parallel_launch", elapsed, timings)
		for _, w := range workers {
			if err = c.Remove(ctx, w.ID, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	end("parallel_launch")
	workers := make([]Worker, 2)
	for i := range workers {
		w, err := c.Launch(ctx, request())
		if w.ID != "" {
			owned = append(owned, w.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
		workers[i] = w
	}
	for _, w := range workers {
		if code, err := c.Exec(ctx, w.ID, ExecRequest{Argv: []string{"/bin/true"}}); err != nil || code != 0 {
			t.Fatal("warmup", code, err)
		}
	}
	begin("exec")
	var before, after syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &before)
	for i := 0; i < 7; i++ {
		code, timing, err := c.ExecWithTimings(ctx, workers[0].ID, ExecRequest{Argv: []string{"/bin/true"}})
		if err != nil || code != 0 {
			t.Fatal(code, err)
		}
		emit("exec", timing.Duration, timing)
	}
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &after)
	t.Logf("client_cpu label=%s before_user=%v after_user=%v before_sys=%v after_sys=%v", label, before.Utime, after.Utime, before.Stime, after.Stime)
	end("exec")
	begin("parallel_exec")
	for n := 0; n < 5; n++ {
		timings := make([]ExecTimings, 2)
		errs := make([]error, 2)
		codes := make([]int64, 2)
		var wg sync.WaitGroup
		started := time.Now()
		for i := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				codes[i], timings[i], errs[i] = c.ExecWithTimings(ctx, workers[i].ID, ExecRequest{Argv: []string{"/bin/true"}})
			}()
		}
		wg.Wait()
		elapsed := time.Since(started)
		for i := range 2 {
			if errs[i] != nil || codes[i] != 0 {
				t.Fatal(codes[i], errs[i])
			}
		}
		emit("parallel_exec", elapsed, timings)
	}
	end("parallel_exec")
	// Optional supervisor fault injection is outside all performance samples.
	if os.Getenv("OAISO_PERF_TAMPER") == "1" {
		if os.Getenv("OAISO_PERF_CONTROL_DIR") == "" {
			t.Fatal("tamper test requires a disposable-node supervisor")
		}
		control("perf-corrupt", label)
		corrupted := true
		defer func() {
			if corrupted {
				control("perf-restore", label)
			}
		}()
		code, err := c.Exec(ctx, workers[0].ID, ExecRequest{Argv: []string{"sh", "-c", "printf forbidden > /tmp/oaiso-perf-forbidden"}})
		if err == nil {
			t.Fatalf("modified pinned asset allowed Exec: %d", code)
		}
		control("perf-restore", label)
		corrupted = false
		if code, err = c.Exec(ctx, workers[0].ID, ExecRequest{Argv: []string{"sh", "-c", "test ! -e /tmp/oaiso-perf-forbidden"}}); err != nil || code != 0 {
			t.Fatalf("tamper recovery or pre-exec gate failed: %d %v", code, err)
		}
		t.Log("same-size, same-mtime asset corruption rejected before user code; recovery verified")
	}
	for _, w := range workers {
		usage, err := c.ResourceUsage(ctx, w.ID)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(usage)
		t.Log("perf_resources " + string(raw))
		if err = c.Remove(ctx, w.ID, true); err != nil {
			t.Fatal(err)
		}
	}
}
