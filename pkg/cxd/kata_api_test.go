package cxd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/workeregress"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/credentialstore"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

const apiTemporaryStorage = `
import errno, os, stat, subprocess
for target, capacity, executable in [('/run/build-cache', 8 << 20, True), ('/run/data-cache', 1 << 20, False)]:
    info = os.stat(target)
    assert (info.st_uid, info.st_gid) == (os.getuid(), os.getgid())
    assert stat.S_IMODE(info.st_mode) == 0o700
    fs = os.statvfs(target)
    assert fs.f_blocks * fs.f_frsize == capacity
    script = target + '/run.sh'
    with open(script, 'w') as out:
        out.write('#!/bin/sh\nprintf tmpfs-ok')
    os.chmod(script, 0o700)
    if executable:
        assert subprocess.check_output([script]) == b'tmpfs-ok'
    else:
        try:
            subprocess.run([script], check=True)
        except PermissionError:
            pass
        else:
            raise AssertionError('noexec mount allowed direct execution')
    os.remove(script)
    try:
        with open(target + '/full', 'wb', buffering=0) as out:
            chunk = b'x' * 65536
            for _ in range(capacity // len(chunk) + 2):
                out.write(chunk)
    except OSError as exc:
        assert exc.errno == errno.ENOSPC, exc
    else:
        raise AssertionError('filesystem capacity was not enforced')
    finally:
        os.remove(target + '/full')
with open('/run/build-cache/marker', 'w') as out:
    out.write('survive node restart only')
`

type apiFixtureStore struct {
	mu           sync.Mutex
	creds        credentialstore.Credentials
	mode         string
	loads, saves int
}

func (s *apiFixtureStore) Load(context.Context, string) (credentialstore.Credentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	return s.creds, nil
}
func (s *apiFixtureStore) SaveRefresh(_ context.Context, _ string, version, provider string, next credentialstore.Credentials) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	if s.mode == "save-failure" {
		return "", fmt.Errorf("synthetic storage failure")
	}
	if s.mode == "conflict" {
		s.creds.Version = "concurrent-version"
	}
	if version != s.creds.Version {
		return "", credentialstore.ErrConflict
	}
	if provider != "openai" && provider != "anthropic" {
		return "", fmt.Errorf("unexpected provider")
	}
	next.Version = version + "-next"
	s.creds = next
	return next.Version, nil
}

// TestKataAPIIntegration drives the public Go API against the real dedicated
// node, VM, proxy container and Unix credential adapter. The authproxy test
// executable supplies synthetic upstreams; no external AI or real key is used.
func TestKataAPIIntegration(t *testing.T) {
	if os.Getenv("CODEX_DOCK_KATA_API_E2E") != "1" {
		t.Skip("set CODEX_DOCK_KATA_API_E2E=1 on an isolated matching node with the fixture proxy image")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	workerImage := os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE")
	if workerImage == "" {
		workerImage = "codex-dock:latest"
	}
	proxyImage := os.Getenv("CODEX_DOCK_KATA_API_PROXY_IMAGE")
	if proxyImage == "" {
		proxyImage = "codex-dock-proxy:api-fixture"
	}
	engineClient, err := engine.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	defer engineClient.Close()
	if os.Getenv("CODEX_DOCK_KATA_API_OWN_IMAGE") == "1" {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if _, err := engineClient.ImageRemove(cleanup, proxyImage, image.RemoveOptions{}); err != nil {
				t.Error("fixture image cleanup:", err)
			} else if _, err := engineClient.ImageInspect(cleanup, proxyImage); !errdefs.IsNotFound(err) {
				t.Errorf("fixture image reference survived cleanup: %v", err)
			}
		}()
	}
	modes := []string{"codex-http", "claude-api", "claude-oauth", "claude-direct-api", "claude-direct-oauth", "compact", "lifecycle", "bandwidth-revoke", "save-failure", "conflict", "restart", "temporary-storage", "embedding-example", "egress-zero-value", "egress-default-deny", "egress-exact", "quarantine"}
	if os.Getenv("CODEX_DOCK_KATA_CUSTOM_PROFILE") != "" {
		modes = append(modes, "custom-profile")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			project := filepath.Join(root, "project")
			if err := os.Mkdir(project, 0755); err != nil {
				t.Fatal(err)
			}
			store := &apiFixtureStore{mode: mode, creds: credentialstore.Credentials{Version: "version-1", ConnectionVersion: "connection-1", OpenAIOAuth: &credentialstore.OpenAIOAuth{AccessToken: "fixture-access-initial", RefreshToken: "fixture-refresh-initial"}}}
			claude := strings.HasPrefix(mode, "claude-")
			if claude {
				store.creds.OpenAIOAuth = nil
				store.creds.AnthropicAPIKey = "fixture-anthropic-key"
				if strings.HasSuffix(mode, "oauth") {
					store.creds.AnthropicAPIKey = ""
					store.creds.AnthropicOAuth = &credentialstore.AnthropicOAuth{AccessToken: "fixture-anthropic-expired", RefreshToken: "fixture-anthropic-refresh", ExpiresAt: time.Now().Add(-time.Minute).UnixMilli(), SubscriptionType: "max", Scopes: []string{"user:inference"}}
				}
			}
			storeCtx, stopStore := context.WithCancel(ctx)
			done := make(chan error, 1)
			socket := filepath.Join(root, "c.sock")
			go func() { done <- credentialstore.Serve(storeCtx, socket, "fixture-case", store) }()
			defer func() {
				stopStore()
				if err := <-done; err != nil {
					t.Error(err)
				}
			}()
			awaitAPI(t, ctx, func() bool { _, err := os.Stat(socket); return err == nil })
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			port := ln.Addr().(*net.TCPAddr).Port
			_ = ln.Close()
			proxyOpts := CaseProxyOptions{NodeSocket: engine.Socket(), Name: fmt.Sprintf("cxd-api-%x", time.Now().UnixNano()), CaseID: "fixture-case", StoreSocket: socket, AdminPort: port, AdminSecret: strings.Repeat("fixture-", 5), Image: proxyImage, AllowedModels: []string{"allowed", "costly"}}
			if mode == "bandwidth-revoke" {
				proxyOpts.NetworkResources = ProxyResourceLimits{BytesPerSecond: 1024}
			}
			if claude {
				proxyOpts.AllowedModels = append(proxyOpts.AllowedModels, "claude-sonnet-4-6")
			}
			proxy, err := EnsureCaseProxy(ctx, proxyOpts)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				if err := RemoveCaseProxy(cleanup, engine.Socket(), proxy.Name, "fixture-case"); err != nil {
					t.Error(err)
				}
			}()
			options := Options{NodeSocket: engine.Socket(), ProxyAdminURL: proxy.AdminURL, ProxyContainerURL: proxy.ContainerURL, ProxyAdminSecret: proxyOpts.AdminSecret, StateDir: filepath.Join(root, "state")}
			if strings.HasPrefix(mode, "egress-") || mode == "quarantine" {
				if mode != "egress-zero-value" {
					options.EgressPolicy.DefaultDeny = true
				}
				if mode == "egress-exact" {
					options.EgressPolicy.AllowDomains = []string{"=example.com"}
					options.EgressPolicy.AllowPorts = []int{443}
					options.EgressProxyImage = os.Getenv("CODEX_DOCK_KATA_PROXY_IMAGE")
				}
				if mode == "quarantine" {
					options.EgressPolicy.AllowCIDRs = []string{"203.0.113.1/32"}
					options.EgressPolicy.AllowPorts = []int{80, 443}
					options.EgressProxyImage = proxyImage
				}
				if mode == "egress-exact" || mode == "quarantine" {
					rules := options.EgressPolicy.rules()
					name := workeregress.Name(rules)
					if _, err := engineClient.ContainerInspect(ctx, name); !errdefs.IsNotFound(err) {
						t.Fatalf("isolated egress fixture name must be unused: %v", err)
					}
					defer func() {
						clean, stop := context.WithTimeout(context.Background(), time.Minute)
						defer stop()
						v, err := engineClient.ContainerInspect(clean, name)
						if errdefs.IsNotFound(err) {
							return
						}
						if err != nil || v.Config == nil || v.Config.Labels["io.codex-dock.forward-policy"] != workeregress.ID(rules) {
							t.Errorf("egress fixture identity changed; retain resources: %v", err)
							return
						}
						if err := engineClient.ContainerRemove(clean, v.ID, container.RemoveOptions{Force: true}); err != nil {
							t.Error("egress fixture cleanup:", err)
						}
					}()
				}
			}
			c, err := Open(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if _, err := engineClient.ImageRemove(ctx, proxyImage, image.RemoveOptions{}); !errdefs.IsConflict(err) {
				t.Fatalf("referenced proxy image removal was not rejected: %v", err)
			}
			caps, err := c.ProxyCapabilities(ctx)
			if err != nil || !caps.ModelAuthorization || !caps.ModelPolicyActive || !caps.TokenModelPolicy {
				t.Fatalf("model capabilities: %+v %v", caps, err)
			}
			runIdentity := identity.Identity{CaseID: "fixture-case", RunID: "run-" + mode, ConnectionVersion: "connection-1", Provider: "openai"}
			if claude {
				runIdentity.Provider = "anthropic"
			}
			if mode == "embedding-example" {
				binary := os.Getenv("CODEX_DOCK_KATA_EMBEDDING_BINARY")
				if binary == "" {
					t.Fatal("build the embedding example with scripts/verify-go-api.py")
				}
				requestID := proxy.Name + "/example"
				defer func() {
					cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
					defer cancel()
					if state, err := c.Request(cleanup, requestID); err == nil && state.WorkerID != "" {
						if err := c.RemoveRequest(cleanup, requestID, true); err != nil {
							t.Error("embedding fixture cleanup:", err)
						}
					} else if err != nil && !errors.Is(err, ErrNotFound) {
						t.Error("embedding fixture request:", err)
					}
				}()
				command := exec.CommandContext(ctx, binary, "-project", project, "-request-id", requestID,
					"-image", workerImage, "-profile", "compact", "-temporary-mib", "8", "-model", "allowed", "-prompt", "Say fixture complete.")
				command.Env = append(os.Environ(),
					"CODEX_DOCK_KATA_NODE_SOCKET="+engine.Socket(), "CXD_EXAMPLE_PROXY_ADMIN_URL="+proxy.AdminURL,
					"CXD_EXAMPLE_PROXY_CONTAINER_URL="+proxy.ContainerURL, "CODEX_DOCK_PROXY_ADMIN_SECRET="+proxyOpts.AdminSecret,
					"CXD_EXAMPLE_STATE_DIR="+filepath.Join(root, "example-state"), "CXD_EXAMPLE_CASE_ID="+runIdentity.CaseID,
					"CXD_EXAMPLE_RUN_ID="+runIdentity.RunID, "CXD_EXAMPLE_CONNECTION_VERSION="+runIdentity.ConnectionVersion)
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("embedding example: %v\n%s", err, output)
				}
				for _, marker := range []string{"CREATE_OK", "START_OK", "streaming before cancellation", "EXEC_CANCEL_OK", "CXD_HTTP_SSE_OK", "CODEX_HTTP_OK", "REMOVE_OK"} {
					if !strings.Contains(string(output), marker) {
						t.Fatalf("embedding example missing %s: %s", marker, output)
					}
				}
				state, err := c.Request(ctx, requestID)
				if err != nil || state.WorkerID == "" {
					t.Fatalf("embedding request record missing: %+v %v", state, err)
				}
				if _, err := c.Get(ctx, state.WorkerID); !errors.Is(err, ErrNotFound) {
					t.Fatalf("embedding example left its worker: %v", err)
				}
				t.Log("verified actual embedding binary: staged start, streaming exec cancellation, bounded tmpfs, Codex HTTP/SSE and durable removal")
				return
			}
			req := LaunchRequest{RequestID: proxy.Name + "/worker", ProjectDir: project, Image: workerImage, Profile: "small", NoInternet: true, User: strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()), Identity: runIdentity, ModelPolicy: &identity.ModelPolicy{AllowedModels: []string{"allowed", "outside-global"}}}
			if strings.HasPrefix(mode, "egress-") || mode == "quarantine" {
				// No worker allowlist: the host maximum must still be enforced.
				req.NoInternet = false
				req.Profile = "compact"
			}
			if claude {
				req.ModelPolicy.AllowedModels = append(req.ModelPolicy.AllowedModels, "claude-sonnet-4-6")
				if strings.HasPrefix(mode, "claude-direct-") {
					req.Agent = "claude"
					req.AgentArgs = []string{"--setting-sources", "", "-p", "Run the fixture command.", "--model", "claude-sonnet-4-6", "--tools", "Bash", "--allowedTools", "Bash", "--max-turns", "3"}
				}
			}
			if mode == "custom-profile" {
				req.Profile = os.Getenv("CODEX_DOCK_KATA_CUSTOM_PROFILE")
			}
			if mode == "compact" || mode == "bandwidth-revoke" {
				req.Profile = "compact"
			}
			if mode == "lifecycle" {
				req.Lifecycle.IdleTimeout = 500 * time.Millisecond
				req.Environment = EnvironmentPolicy{StartHooks: [][]string{{"sh", "-c", "printf ready > /workspace/hook"}}, Readiness: []string{"test", "-f", "/workspace/hook"}}
			}
			if mode == "temporary-storage" {
				req.ReadOnlyRootFS = true
				req.TemporaryStorage = []TemporaryStorage{
					{Target: "/run/build-cache", MaxBytes: 8 << 20, Executable: true},
					{Target: "/run/data-cache", MaxBytes: 1 << 20},
				}
				req.Environment = EnvironmentPolicy{StartHooks: [][]string{{"sh", "-c", "printf ready > /run/build-cache/hook"}}, Readiness: []string{"test", "-f", "/run/build-cache/hook"}}
			}
			w, err := c.Create(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				if err := c.Remove(cleanup, w.ID, true); err != nil {
					t.Error(err)
				}
			}()
			if w.Status != "created" {
				t.Fatalf("create started worker: %s", w.Status)
			}
			if _, err := os.Stat(filepath.Join(project, "hook")); !os.IsNotExist(err) {
				t.Fatal("hook ran before Start")
			}
			again, err := c.Create(ctx, req)
			if err != nil || again.ID != w.ID {
				t.Fatalf("create replay: %+v %v", again, err)
			}
			changedPolicy := req
			changedPolicy.ModelPolicy = req.ModelPolicy.Clone()
			changedPolicy.ModelPolicy.AllowedModels = []string{"different"}
			if _, err := c.Create(ctx, changedPolicy); !errors.Is(err, ErrConflict) {
				t.Fatalf("model policy changed on replay: %v", err)
			}
			if mode == "temporary-storage" {
				changed := req
				changed.TemporaryStorage = append([]TemporaryStorage(nil), req.TemporaryStorage...)
				changed.TemporaryStorage[0].MaxBytes /= 2
				if _, err := c.Create(ctx, changed); !errors.Is(err, ErrConflict) {
					t.Fatalf("temporary storage changed on replay: %v", err)
				}
			}
			if _, err := c.Launch(ctx, req); err == nil {
				t.Fatal("Create and Launch shared a request identity")
			}
			if err := c.Start(ctx, w.ID); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(mode, "claude-direct-") {
				waitCtx, stopWait := context.WithTimeout(ctx, time.Minute)
				code, err := c.Wait(waitCtx, w.ID)
				stopWait()
				if err != nil || code != 0 {
					t.Fatalf("direct Claude exit=%d err=%v", code, err)
				}
				logs, err := engineClient.ContainerLogs(ctx, w.ID, container.LogsOptions{ShowStdout: true, ShowStderr: true})
				if err != nil {
					t.Fatal(err)
				}
				output, err := io.ReadAll(logs)
				_ = logs.Close()
				if err != nil || !strings.Contains(string(output), "CXD_CLAUDE_SSE_OK") {
					t.Fatalf("direct Claude response: %s %v", output, err)
				}
				marker, err := os.ReadFile(filepath.Join(project, "cxd-claude-tool.txt"))
				if err != nil || string(marker) != "CXD_CLAUDE_TOOL_OK" {
					t.Fatalf("direct Claude tool: %q %v", marker, err)
				}
				t.Log("verified Claude as the worker's main process with native argv, tool execution and final SSE response")
				return
			}
			usage, err := c.ResourceUsage(ctx, w.ID)
			if err != nil || usage.OOMKills != 0 {
				t.Fatalf("resource usage: %+v %v", usage, err)
			}
			t.Logf("%s idle: host current=%d peak=%d guest budget=%d host ceiling=%d", req.Profile, usage.HostMemoryCurrentBytes, usage.HostMemoryPeakBytes, usage.Profile.GuestMemoryBytes, usage.Profile.HostMemoryBytes)
			if strings.HasPrefix(mode, "egress-") {
				snapshot, err := c.Policy(ctx, w.ID)
				if err != nil || snapshot.GeneralEgress != (mode == "egress-exact") {
					t.Fatalf("host policy was ignored without a worker list: %+v %v", snapshot, err)
				}
				probe := `import os; assert not os.environ.get('HTTP_PROXY') and not os.environ.get('HTTPS_PROXY')`
				if mode == "egress-exact" {
					if !snapshot.Rules.DefaultDeny || len(snapshot.Rules.AllowDomains) != 1 || snapshot.Rules.AllowDomains[0] != "=example.com" {
						t.Fatalf("running strict/exact policy mismatch: %+v", snapshot)
					}
					allowed, err := c.ExplainPolicy(ctx, w.ID, "example.com:443", false)
					if err != nil || !allowed.Allowed || allowed.Complete {
						t.Fatalf("exact name preflight: %+v %v", allowed, err)
					}
					probe = `import os,socket,urllib.parse
u=urllib.parse.urlsplit(os.environ['HTTP_PROXY'])
for line in ['CONNECT sub.example.com:443 HTTP/1.1', 'GET http://sub.example.com:443/ HTTP/1.1']:
    with socket.create_connection((u.hostname,u.port),timeout=5) as s:
        s.sendall((line+'\r\nHost: sub.example.com:443\r\nConnection: close\r\n\r\n').encode())
        assert s.makefile('rb').readline().split()[1] == b'403'
`
				}
				var output bytes.Buffer
				if code, err := c.Exec(ctx, w.ID, ExecRequest{Argv: []string{"python3", "-c", probe}, Stdout: &output, Stderr: &output}); err != nil || code != 0 {
					t.Fatalf("strict/exact guest probe: code=%d err=%v output=%s", code, err, output.String())
				}
				t.Log("verified host policy without worker allowlist, default deny/exact HTTP+CONNECT; fixed AI routes checked below")
			}
			if claude {
				verifyClaudeAPI(t, ctx, c, w.ID, project, mode == "claude-oauth", store)
				return
			}
			if mode == "bandwidth-revoke" {
				streamCtx, cancelStream := context.WithCancel(ctx)
				defer cancelStream()
				streamDone := make(chan error, 1)
				probe := strings.Replace(apiHTTPStream, `"stream":true`, `"stream":true,"input":"bandwidth-hold"`, 1)
				go func() {
					var output bytes.Buffer
					code, err := c.Exec(streamCtx, w.ID, ExecRequest{Argv: []string{"python3", "-c", probe}, Stdout: &output, Stderr: &output})
					if err == nil && code != 0 {
						err = fmt.Errorf("throttled stream exit %d: %s", code, output.String())
					}
					streamDone <- err
				}()
				awaitAPI(t, ctx, func() bool { _, err := os.Stat(filepath.Join(project, "stream-started")); return err == nil })
				// Exhaust the TCP burst so revocation happens during a rate wait.
				time.Sleep(300 * time.Millisecond)
				select {
				case err := <-streamDone:
					t.Fatalf("throttled stream ended before revocation: %v", err)
				default:
				}
				started := time.Now()
				if err := c.RevokeRun(ctx, runIdentity); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-streamDone:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("revocation did not interrupt the guest stream's bandwidth wait")
				}
				t.Logf("verified guest stream closure at 1 KiB/s after revocation in %s", time.Since(started))
				return
			}
			request := func(path, body string, expectedErrorBody ...string) string {
				t.Helper()
				var out, stderr bytes.Buffer
				args := append([]string{"python3", "-c", apiHTTPRequest, path, body}, expectedErrorBody...)
				code, err := c.Exec(ctx, w.ID, ExecRequest{Argv: args, Stdout: &out, Stderr: &stderr})
				if err != nil || code != 0 {
					t.Fatalf("guest HTTP: code=%d err=%v stderr=%s", code, err, stderr.String())
				}
				return strings.TrimSpace(out.String())
			}
			if got := request("/v1/responses", `{"model":"other"}`); got != "403" {
				t.Fatalf("model bypass: %s", got)
			}
			if got := request("/v1/responses", `{"model":"costly"}`); got != "403" {
				t.Fatalf("token policy bypass: %s", got)
			}
			if got := request("/v1/responses", `{"model":"allowed"}`); got != "204" {
				t.Fatalf("allowed model: %s", got)
			}
			if mode == "quarantine" {
				verifyQuarantineAPI(t, ctx, c, engineClient, options, req, w, project)
				return
			}
			if strings.HasPrefix(mode, "egress-") {
				return
			}
			if mode == "temporary-storage" {
				execOK := func(args ...string) {
					t.Helper()
					var out bytes.Buffer
					code, err := c.Exec(ctx, w.ID, ExecRequest{Argv: args, Stdout: &out, Stderr: &out})
					if err != nil || code != 0 {
						t.Fatalf("temporary storage assertion: code=%d err=%v output=%s", code, err, out.String())
					}
				}
				execOK("python3", "-c", apiTemporaryStorage)
				if requestDir := os.Getenv("CODEX_DOCK_NODE_RESTART_REQUEST_DIR"); requestDir != "" {
					if err := os.Remove(filepath.Join(requestDir, "complete")); err != nil && !os.IsNotExist(err) {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(requestDir, "request"), []byte("restart\n"), 0600); err != nil {
						t.Fatal(err)
					}
					awaitAPI(t, ctx, func() bool { _, err := os.Stat(filepath.Join(requestDir, "complete")); return err == nil })
					execOK("test", "-f", "/run/build-cache/marker")
				}
				if err := c.Stop(ctx, w.ID, 5*time.Second); err != nil {
					t.Fatal(err)
				}
				if err := c.Start(ctx, w.ID); err != nil {
					t.Fatal(err)
				}
				execOK("sh", "-c", "test ! -e /run/build-cache/marker && test -f /run/build-cache/hook")
				execOK("python3", "-c", apiTemporaryStorage)
				if got := request("/v1/responses", `{"model":"allowed"}`); got != "204" {
					t.Fatalf("temporary storage restart auth: %s", got)
				}
				t.Log("verified bounded tmpfs capacity, ENOSPC, owner/mode, exec/noexec, node persistence and VM lifetime")
				return
			}
			if mode == "custom-profile" {
				profiles, err := c.Profiles(ctx)
				if err != nil {
					t.Fatal(err)
				}
				var expected Profile
				for _, p := range profiles {
					if p.Name == req.Profile {
						expected = p
						break
					}
				}
				if expected.Name == "" || usage.Profile != expected {
					t.Fatalf("custom profile was not attested: %+v", usage.Profile)
				}
				checkLimits := func() {
					t.Helper()
					var out bytes.Buffer
					code, err := c.Exec(ctx, w.ID, ExecRequest{Argv: []string{"sh", "-c", "cat /sys/fs/cgroup/cpu.max /sys/fs/cgroup/pids.max /sys/fs/cgroup/memory.max"}, Stdout: &out})
					want := fmt.Sprintf("%d 100000\n%d\n%d", int64(expected.CPUCores)*100000, expected.PIDLimit, expected.GuestMemoryBytes)
					if err != nil || code != 0 || strings.TrimSpace(out.String()) != want {
						t.Fatalf("custom guest budget: %q %v", out.String(), err)
					}
				}
				checkLimits()
				if requestDir := os.Getenv("CODEX_DOCK_NODE_RESTART_REQUEST_DIR"); requestDir != "" {
					if err := os.Remove(filepath.Join(requestDir, "complete")); err != nil && !os.IsNotExist(err) {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(requestDir, "request"), []byte("restart\n"), 0600); err != nil {
						t.Fatal(err)
					}
					awaitAPI(t, ctx, func() bool { _, err := os.Stat(filepath.Join(requestDir, "complete")); return err == nil })
					checkLimits()
				}
				if err := c.Stop(ctx, w.ID, 5*time.Second); err != nil {
					t.Fatal(err)
				}
				if err := c.Start(ctx, w.ID); err != nil {
					t.Fatal(err)
				}
				checkLimits()
				if got := request("/v1/responses", `{"model":"allowed"}`); got != "204" {
					t.Fatalf("custom profile restart auth: %s", got)
				}
				t.Logf("verified custom budget after node and VM restart: CPU=%d guest=%d host=%d PIDs=%d", expected.CPUCores, expected.GuestMemoryBytes, expected.HostMemoryBytes, expected.PIDLimit)
				return
			}
			if mode == "codex-http" {
				eofCtx, cancelEOF := context.WithTimeout(ctx, 5*time.Second)
				code, eofErr := c.Exec(eofCtx, w.ID, ExecRequest{Argv: []string{"python3", "-c", "import sys; assert sys.stdin.read() == ''"}})
				cancelEOF()
				if eofErr != nil || code != 0 {
					t.Fatalf("unattached guest stdin did not reach EOF: %d %v", code, eofErr)
				}
				job, err := c.StartJob(ctx, w.ID, ExecRequest{Argv: []string{"python3", "-c", "import sys; assert sys.stdin.read() == ''"}})
				if err != nil {
					t.Fatal(err)
				}
				awaitAPI(t, ctx, func() bool {
					status, err := c.Job(ctx, w.ID, job.ID)
					return err == nil && !status.Running && status.ExitCode == 0
				})
				connections, err := c.Connections(ctx, w.ID)
				if err != nil || len(connections) != 1 || !connections[0].ModelPolicyActive {
					t.Fatalf("connection descriptor: %+v %v", connections, err)
				}
				for _, transport := range connections[0].Transports {
					if transport == TransportWebSocket {
						t.Fatal("restricted connection advertised WebSocket")
					}
				}
				var version bytes.Buffer
				code, err = c.Exec(ctx, w.ID, ExecRequest{Argv: []string{"codex", "--version"}, Stdout: &version})
				if err != nil || code != 0 || strings.TrimSpace(version.String()) != "codex-cli 0.163.0" {
					t.Fatalf("pinned CLI: %q %v", version.String(), err)
				}
				command, err := c.CodexHTTPCommand(ctx, w.ID)
				if err != nil {
					t.Fatal(err)
				}
				// An empty CODEX_HOME prevents relying on generated user config. All
				// connection settings must come from the helper's explicit CLI overrides.
				var setup bytes.Buffer
				code, err = c.Exec(ctx, w.ID, ExecRequest{Argv: []string{"mkdir", "-p", "/var/tmp/cxd-http-empty-config"}, Stderr: &setup})
				if err != nil || code != 0 {
					t.Fatalf("empty config directory: %d %v %s", code, err, setup.String())
				}
				command.Env = append(command.Env, "CODEX_HOME=/var/tmp/cxd-http-empty-config")
				command.Argv = append(command.Argv, "exec", "--skip-git-repo-check", "-m", "allowed", "Say fixture complete.")
				var output bytes.Buffer
				callCtx, cancelCall := context.WithTimeout(ctx, 60*time.Second)
				code, err = c.Exec(callCtx, w.ID, ExecRequest{Argv: command.Argv, Env: command.Env, Stdout: &output, Stderr: &output})
				cancelCall()
				if err != nil || code != 0 || !strings.Contains(output.String(), "CXD_HTTP_SSE_OK") {
					t.Fatalf("Codex HTTP/SSE: code=%d err=%v output=%s", code, err, output.String())
				}
				return
			}
			if mode == "compact" {
				job, err := c.StartJob(ctx, w.ID, ExecRequest{Argv: []string{"python3", "-c", "import time; x=bytearray(256*1024*1024); print('allocated',flush=True); time.sleep(60)"}})
				if err != nil {
					t.Fatal(err)
				}
				awaitAPI(t, ctx, func() bool {
					logs, err := c.JobLogs(ctx, w.ID, job.ID, 0)
					return err == nil && strings.Contains(logs.Output, "allocated")
				})
				otherReq := req
				otherReq.RequestID += "/concurrent"
				otherReq.Identity.RunID = "compact-concurrent"
				otherReq.ModelPolicy = req.ModelPolicy.Clone()
				otherReq.ModelPolicy.AllowedModels = nil
				other, err := c.Launch(ctx, otherReq)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					cleanup, cancel := context.WithTimeout(context.Background(), time.Minute)
					defer cancel()
					if err := c.Remove(cleanup, other.ID, true); err != nil {
						t.Error(err)
					}
				}()
				var denied bytes.Buffer
				code, err := c.Exec(ctx, other.ID, ExecRequest{Argv: []string{"python3", "-c", apiHTTPRequest, "/v1/responses", `{"model":"allowed"}`}, Stdout: &denied})
				if err != nil || code != 0 || strings.TrimSpace(denied.String()) != "403" {
					t.Fatalf("per-token policy: %s %v", denied.String(), err)
				}
				for _, id := range []string{w.ID, other.ID} {
					u, err := c.ResourceUsage(ctx, id)
					if err != nil || u.OOMKills != 0 || u.HostMemoryCurrentBytes >= u.Profile.HostMemoryBytes {
						t.Fatalf("compact envelope: %+v %v", u, err)
					}
					var out bytes.Buffer
					code, err := c.Exec(ctx, id, ExecRequest{Argv: []string{"sh", "-c", "cat /sys/fs/cgroup/memory.max; cat /sys/fs/cgroup/memory.events"}, Stdout: &out})
					lines := strings.Split(strings.TrimSpace(out.String()), "\n")
					if err != nil || code != 0 || lines[0] != strconv.FormatInt(512<<20, 10) {
						t.Fatalf("compact guest bound: %q %v", out.String(), err)
					}
					guestOOMFound := false
					for _, line := range lines[1:] {
						fields := strings.Fields(line)
						if len(fields) == 2 && (fields[0] == "oom" || fields[0] == "oom_kill") {
							if fields[1] != "0" {
								t.Fatalf("compact guest OOM: %s", line)
							}
							if fields[0] == "oom_kill" {
								guestOOMFound = true
							}
						}
					}
					if !guestOOMFound {
						t.Fatal("guest OOM observation missing")
					}
					t.Logf("compact concurrent: current=%d peak=%d OOM=%d", u.HostMemoryCurrentBytes, u.HostMemoryPeakBytes, u.OOMKills)
				}
				if current, err := c.Job(ctx, w.ID, job.ID); err != nil || !current.Running {
					t.Fatalf("compact load job exited unexpectedly: %+v %v", current, err)
				}
				if err := c.KillJob(ctx, w.ID, job.ID); err != nil {
					t.Fatal(err)
				}
				return
			}
			if mode == "lifecycle" {
				job, err := c.StartJob(ctx, w.ID, ExecRequest{Argv: []string{"python3", "-c", "import time; x=bytearray(256*1024*1024); print('allocated',flush=True); time.sleep(60)"}})
				if err != nil {
					t.Fatal(err)
				}
				awaitAPI(t, ctx, func() bool {
					logs, err := c.JobLogs(ctx, w.ID, job.ID, 0)
					return err == nil && strings.Contains(logs.Output, "allocated")
				})
				time.Sleep(750 * time.Millisecond)
				actions, err := c.Reap(ctx, false)
				if err != nil || len(actions) != 0 {
					t.Fatalf("running job reaped: %+v %v", actions, err)
				}
				usage, err = c.ResourceUsage(ctx, w.ID)
				if err != nil || usage.OOMKills != 0 {
					t.Fatalf("loaded usage: %+v %v", usage, err)
				}
				t.Logf("small +256MiB job: host current=%d peak=%d OOM=%d", usage.HostMemoryCurrentBytes, usage.HostMemoryPeakBytes, usage.OOMKills)
				if err := c.KillJob(ctx, w.ID, job.ID); err != nil {
					t.Fatal(err)
				}
				awaitAPI(t, ctx, func() bool { j, err := c.Job(ctx, w.ID, job.ID); return err == nil && !j.Running })
				streamDone := make(chan error, 1)
				go func() {
					code, err := c.Exec(ctx, w.ID, ExecRequest{Argv: []string{"python3", "-c", apiHTTPStream}, Stdout: io.Discard, Stderr: io.Discard})
					if err == nil && code != 0 {
						err = fmt.Errorf("stream exit %d", code)
					}
					streamDone <- err
				}()
				awaitAPI(t, ctx, func() bool { _, err := os.Stat(filepath.Join(project, "stream-started")); return err == nil })
				time.Sleep(750 * time.Millisecond)
				if actions, err := c.Reap(ctx, false); err != nil || len(actions) != 0 {
					t.Fatalf("streaming exec reaped: %+v %v", actions, err)
				}
				select {
				case err := <-streamDone:
					t.Fatalf("stream ended before revocation: %v", err)
				default:
				}
				if err := c.RevokeRun(ctx, runIdentity); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-streamDone:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(15 * time.Second):
					t.Fatal("revocation did not close stream")
				}
				if got := request("/v1/responses", `{"model":"allowed"}`); got != "401" {
					t.Fatalf("revoked token reused: %s", got)
				}
				time.Sleep(750 * time.Millisecond)
				if actions, err := c.Reap(ctx, false); err != nil || len(actions) != 1 || actions[0].Reason != "idle_timeout" {
					t.Fatalf("idle worker not collected: %+v %v", actions, err)
				}
				// Launch cancellation after a hook began must stop the VM, revoke
				// credentials and leave its durable request reconcilable.
				cancelReq := req
				cancelReq.RequestID = proxy.Name + "/cancel"
				cancelReq.Identity.RunID = "cancel-run"
				cancelReq.Lifecycle = LifecyclePolicy{}
				cancelReq.Environment = EnvironmentPolicy{StartHooks: [][]string{{"sh", "-c", "touch /workspace/launch-started; sleep 120"}}, Readiness: []string{"false"}, ReadinessTimeout: 150 * time.Second}
				launchCtx, stopLaunch := context.WithCancel(ctx)
				launched := make(chan error, 1)
				go func() { _, err := c.Launch(launchCtx, cancelReq); launched <- err }()
				awaitAPI(t, ctx, func() bool { _, err := os.Stat(filepath.Join(project, "launch-started")); return err == nil })
				replay, replayErr := c.Launch(ctx, cancelReq)
				inFlight, inFlightErr := c.Request(ctx, cancelReq.RequestID)
				stopLaunch()
				select {
				case err := <-launched:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("cancel: %v", err)
					}
				case <-time.After(60 * time.Second):
					t.Fatal("launch cleanup timed out")
				}
				if !errors.Is(replayErr, ErrInProgress) || inFlightErr != nil || inFlight.State != "pending" {
					t.Fatalf("retry completed an active launch: worker=%+v err=%v state=%+v stateErr=%v", replay, replayErr, inFlight, inFlightErr)
				}
				status, err := c.Request(ctx, cancelReq.RequestID)
				if err != nil || status.State == "pending" || status.State == "cleanup-pending" {
					t.Fatalf("unreconciled cancellation: %+v %v", status, err)
				}
				workers, err := c.List(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for _, other := range workers {
					if other.ID != w.ID {
						t.Fatalf("canceled VM retained: %+v", other)
					}
				}
				inventory, err := c.node.Inventory(ctx)
				if err != nil || !inventory.EngineAvailable {
					t.Fatalf("post-cancel inventory: %v", err)
				}
				for _, vm := range inventory.VMs {
					if vm.Allocated || vm.Populated || vm.ContainerID != w.ID && vm.ContainerPresent {
						t.Fatalf("canceled host resources survived: %+v", vm)
					}
				}
				return
			}
			refreshPath := "/oauth/token?cdx="
			if got := request(refreshPath, `{"grant_type":"refresh_token"}`); mode == "restart" && got != "200" || mode != "restart" && got != "502" {
				t.Fatalf("refresh %s: %s", mode, got)
			}
			store.mu.Lock()
			saves := store.saves
			creds := store.creds
			loads := store.loads
			store.mu.Unlock()
			if saves != 1 {
				t.Fatalf("refresh save count %d", saves)
			}
			if mode != "restart" {
				if creds.OpenAIOAuth.AccessToken != "fixture-access-initial" {
					t.Fatal("failed CAS/save published credentials")
				}
				if got := request("/v1/responses", `{"model":"allowed"}`); got != "401" {
					t.Fatalf("failed refresh retained token: %s", got)
				}
				return
			}
			if creds.OpenAIOAuth.AccessToken != "fixture-access-rotated" {
				t.Fatal("refresh not persisted")
			}
			seconds := 5
			if err := engineClient.ContainerStop(ctx, proxy.Name, container.StopOptions{Timeout: &seconds}); err != nil {
				t.Fatal(err)
			}
			if _, err := EnsureCaseProxy(ctx, proxyOpts); err != nil {
				t.Fatal(err)
			}
			store.mu.Lock()
			reloaded := store.loads > loads
			store.mu.Unlock()
			if !reloaded {
				t.Fatal("proxy did not reload durable credentials")
			}
			// Restart creates a new TLS authority. The guest relay must reject the
			// stale certificate before its token can reach the new proxy.
			if got := request("/v1/responses", `{"model":"allowed"}`, "worker TLS connection unavailable"); got != "502" {
				t.Fatalf("proxy restart accepted stale TLS credentials: %s", got)
			}
			if err := c.Stop(ctx, w.ID, 5*time.Second); err != nil {
				t.Fatal(err)
			}
			if err := c.Start(ctx, w.ID); err != nil {
				t.Fatal(err)
			}
			if got := request("/v1/responses", `{"model":"costly"}`); got != "403" {
				t.Fatalf("token policy bypass: %s", got)
			}
			if got := request("/v1/responses", `{"model":"allowed"}`); got != "204" {
				t.Fatalf("restored worker: %s", got)
			}
		})
	}
}

func awaitAPI(t *testing.T, ctx context.Context, ready func() bool) {
	t.Helper()
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-timer.C:
			t.Fatal("fixture readiness timeout")
		case <-ticker.C:
		}
	}
}

const apiHTTPRequest = `import os,sys,urllib.request,urllib.error
path=sys.argv[1]
if path.endswith('?cdx='): path+=os.environ['CODEX_TOKEN']
req=urllib.request.Request(os.environ['CODEX_AUTH_PROXY_URL']+path,data=sys.argv[2].encode(),headers={'Authorization':'Bearer '+os.environ['CODEX_TOKEN'],'Content-Type':'application/json'})
try:
 with urllib.request.urlopen(req,timeout=15) as resp: print(resp.status)
except urllib.error.HTTPError as e:
 if len(sys.argv)>3: assert e.read().decode().strip()==sys.argv[3]
 print(e.code)
`

const apiHTTPStream = `import os,urllib.request,http.client
req=urllib.request.Request(os.environ['CODEX_AUTH_PROXY_URL']+'/v1/responses',data=b'{"model":"allowed","stream":true}',headers={'Authorization':'Bearer '+os.environ['CODEX_TOKEN'],'Content-Type':'application/json'})
with urllib.request.urlopen(req,timeout=20) as resp:
 assert resp.readline().startswith(b'data:')
 open('/workspace/stream-started','w').close()
 try:
  while resp.read(1): pass
 except (OSError,ValueError,http.client.IncompleteRead): pass
`
