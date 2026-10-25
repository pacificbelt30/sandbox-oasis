package sandbox_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/pacificbelt30/codex-dock/internal/auditlog"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/kataguard"
	"github.com/pacificbelt30/codex-dock/internal/network"
	"github.com/pacificbelt30/codex-dock/pkg/cxd"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/credentialstore"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

// This runs in the same disposable Linux/KVM node as the CLI acceptance suite.
// The credential fixtures are deliberately invalid; no external AI is contacted.
func TestKataGoAPI(t *testing.T) {
	if os.Getenv("CODEX_DOCK_KATA_E2E") != "1" {
		t.Skip("set CODEX_DOCK_KATA_E2E=1 on a configured Linux/KVM host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	image := os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE")
	if image == "" {
		image = "codex-dock:latest"
	}
	proxyImage := os.Getenv("CODEX_DOCK_KATA_PROXY_IMAGE")
	if proxyImage == "" {
		proxyImage = "codex-dock-proxy:latest"
	}
	node, err := engine.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { node.Close() })
	if _, err := node.NetworkInspect(ctx, network.EgressNetworkName, dockernetwork.InspectOptions{}); errdefs.IsNotFound(err) {
		t.Cleanup(func() {
			clean, stop := context.WithTimeout(context.Background(), 30*time.Second)
			defer stop()
			if err := node.NetworkRemove(clean, network.EgressNetworkName); err != nil && !errdefs.IsNotFound(err) {
				t.Error(err)
			}
		})
	} else if err != nil {
		t.Fatal(err)
	}
	if _, err := node.Ping(ctx); err != nil {
		t.Fatalf("real Kata node required: %v", err)
	}
	verifier, err := kataguard.Connect(engine.Socket(), "unix://"+engine.Socket())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { verifier.Close() })
	prefix := fmt.Sprintf("api-kata-%d", time.Now().UnixNano())
	hostBoot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	type fixture struct {
		api      *cxd.Client
		endpoint string
		id       identity.Identity
		worker   cxd.Worker
		req      cxd.LaunchRequest
		token    string
		usage    string
	}
	cases := make([]fixture, 2)
	secrets := []string{}
	for i := range cases {
		caseID := fmt.Sprintf("%s-%d", prefix, i)
		key, access, refresh := caseID+"-fake-key", caseID+"-fake-access", caseID+"-fake-refresh"
		secrets = append(secrets, key, access, refresh)
		store := &kataCaseStore{id: caseID, creds: credentialstore.Credentials{Version: "1", ConnectionVersion: "1", OpenAIAPIKey: key, AnthropicOAuth: &credentialstore.AnthropicOAuth{AccessToken: access, RefreshToken: refresh, ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}}}
		dir := t.TempDir()
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
		socket := filepath.Join(dir, "store.sock")
		storeCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- credentialstore.Serve(storeCtx, socket, caseID, store) }()
		t.Cleanup(func() {
			stop()
			if err := <-done; err != nil {
				t.Error(err)
			}
		})
		for attempts := 0; ; attempts++ {
			if info, err := os.Stat(socket); err == nil && info.Mode().Perm() == 0600 {
				break
			}
			if attempts == 100 {
				t.Fatal("credential fixture did not start")
			}
			time.Sleep(20 * time.Millisecond)
		}
		storeClient, err := credentialstore.NewUnixClient(socket)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := storeClient.Load(ctx, "another-case"); !errors.Is(err, credentialstore.ErrNotFound) {
			t.Fatalf("cross-case store load: %v", err)
		}
		if _, err := storeClient.SaveRefresh(ctx, "another-case", "1", "openai", credentialstore.Credentials{}); !errors.Is(err, credentialstore.ErrNotFound) {
			t.Fatalf("cross-case store update: %v", err)
		}
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		listener.Close()
		t.Cleanup(func() {
			clean, stop := context.WithTimeout(context.Background(), 30*time.Second)
			defer stop()
			if err := cxd.RemoveCaseProxy(clean, "", caseID, caseID); err != nil {
				t.Error(err)
			}
		})
		proxy, err := cxd.EnsureCaseProxy(ctx, cxd.CaseProxyOptions{Image: proxyImage, Name: caseID, CaseID: caseID, StoreSocket: socket, UsageStateDir: filepath.Join(dir, "usage"), AdminPort: port, AdminSecret: prefix + "-private-admin-secret"})
		if err != nil {
			t.Fatal(err)
		}
		opts := cxd.Options{ProxyAdminURL: proxy.AdminURL, ProxyContainerURL: proxy.ContainerURL, ProxyAdminSecret: prefix + "-private-admin-secret", StateDir: filepath.Join(dir, "state")}
		api, err := cxd.Open(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { api.Close() })
		profiles, err := api.Profiles(ctx)
		if err != nil || len(profiles) < 2 {
			t.Fatalf("profiles: %v %+v", err, profiles)
		}
		profile := "small"
		if i == 1 {
			profile = "standard"
		}
		id := identity.Identity{CaseID: caseID, RunID: "run", ConnectionVersion: "1", Provider: "openai"}
		req := cxd.LaunchRequest{RequestID: caseID, ProjectDir: t.TempDir(), Image: image, Profile: profile, NoInternet: true, Identity: id, User: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())}
		// A canceled call must not allocate a worker or claim a request.
		canceled, stopCanceled := context.WithCancel(ctx)
		stopCanceled()
		if _, _, err := api.LaunchWithTimings(canceled, req); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled launch: %v", err)
		}
		worker, timings, err := api.LaunchWithTimings(ctx, req)
		if worker.ID != "" {
			t.Cleanup(func() {
				clean, stop := context.WithTimeout(context.Background(), 45*time.Second)
				defer stop()
				if err := api.Remove(clean, worker.ID, true); err != nil && !errors.Is(err, cxd.ErrNotFound) {
					t.Error(err)
				}
			})
		}
		if err != nil {
			t.Fatal(err)
		}
		var measured time.Duration
		phaseIndex := map[string]int{}
		for index, phase := range timings.Phases {
			measured += phase.Duration
			phaseIndex[phase.Name] = index
		}
		if measured != timings.Duration || timings.Duration <= 0 {
			t.Fatalf("launch timing did not partition elapsed time: %+v", timings)
		}
		previous := -1
		for _, name := range []string{"request_claim", "preflight", "vm_start", "host_guest_verification", "token_activation", "bootstrap_release", "request_confirmation", "final_inspect"} {
			index, ok := phaseIndex[name]
			if !ok || index <= previous {
				t.Fatalf("missing/out-of-order measured phase %s: %+v", name, timings)
			}
			previous = index
		}
		inspect, err := node.ContainerInspect(ctx, worker.ID)
		if err != nil {
			t.Fatal(err)
		}
		var expected cxd.Profile
		for _, p := range profiles {
			if p.Name == profile {
				expected = p
			}
		}
		if expected.Name == "" || inspect.HostConfig.Memory != expected.GuestMemoryBytes || inspect.HostConfig.MemorySwap != expected.GuestMemoryBytes || inspect.HostConfig.NanoCPUs != int64(expected.CPUCores)*1_000_000_000 {
			t.Fatalf("%s reservation differs from container limits", profile)
		}
		inventory, err := verifier.Inventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		vm, err := kataguard.ResolveVM(inventory, worker.ID)
		if err != nil || !vm.HostVerified || vm.ResourceProfile != profile || vm.Resources["memory.max"] != fmt.Sprint(expected.HostMemoryBytes) || vm.Resources["pids.max"] != fmt.Sprint(expected.PIDLimit) {
			t.Fatalf("%s host reservation not attested: %+v %v", profile, vm, err)
		}
		var limits bytes.Buffer
		limitCode, limitErr := api.Exec(ctx, worker.ID, cxd.ExecRequest{Argv: []string{"sh", "-c", "cat /sys/fs/cgroup/memory.max /sys/fs/cgroup/pids.max"}, Stdout: &limits})
		if limitErr != nil || limitCode != 0 || strings.TrimSpace(limits.String()) != fmt.Sprintf("%d\n%d", expected.GuestMemoryBytes, expected.PIDLimit) {
			t.Fatalf("%s guest limits: exit=%d err=%v %q", profile, limitCode, limitErr, limits.String())
		}
		token, endpoint := "", ""
		for _, v := range inspect.Config.Env {
			if strings.HasPrefix(v, "CODEX_AUTH_PROXY_URL=") {
				endpoint = strings.TrimPrefix(v, "CODEX_AUTH_PROXY_URL=")
			}
			if strings.HasPrefix(v, "CODEX_TOKEN=") {
				token = strings.TrimPrefix(v, "CODEX_TOKEN=")
			}
		}
		if token == "" || endpoint == "" {
			t.Fatal("worker has no issued proxy token or guest endpoint")
		}
		cases[i] = fixture{api: api, endpoint: endpoint, id: id, worker: worker, req: req, token: token, usage: filepath.Join(dir, "usage")}
		var output bytes.Buffer
		code, err := api.Exec(ctx, worker.ID, cxd.ExecRequest{Argv: []string{"cat", "/proc/sys/kernel/random/boot_id"}, Stdout: &output})
		if err != nil || code != 0 || strings.TrimSpace(output.String()) == strings.TrimSpace(string(hostBoot)) {
			t.Fatalf("guest must have a different boot ID: exit=%d err=%v", code, err)
		}
		if i == 0 {
			checkKataExecInputAndCancellation(t, ctx, api, worker.ID, req.ProjectDir)
		}
		again, err := api.Launch(ctx, req)
		if err != nil || again.ID != worker.ID {
			t.Fatalf("replay created another worker: %v", err)
		}
		// Reconnect without relying on the first Launch response and reconcile by ID.
		reconnect, err := cxd.Open(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		status, statusErr := reconnect.Request(ctx, req.RequestID)
		replay, replayErr := reconnect.Launch(ctx, req)
		reconnect.Close()
		if statusErr != nil || replayErr != nil || status.State != "complete" || status.WorkerID != worker.ID || replay.ID != worker.ID {
			t.Fatalf("reconnect reconciliation failed: %v %v %+v", statusErr, replayErr, status)
		}
		changed := req
		changed.ReadOnly = true
		if _, err := api.Launch(ctx, changed); !errors.Is(err, cxd.ErrConflict) {
			t.Fatalf("changed request accepted: %v", err)
		}
		t.Logf("verified: %s real VM, canceled call, replay, reconnect reconciliation, request conflict", profile)
	}
	// Inspect both guests after both cases exist, so either case's secrets are forbidden.
	for i, f := range cases {
		other := cases[1-i]
		input, _ := json.Marshal(map[string]any{"secrets": secrets, "url": f.endpoint, "other_token": other.token, "token": f.token})
		script := `import json,os,pathlib,urllib.request,urllib.error
v=json.load(__import__('sys').stdin)
data=str(dict(os.environ))
for root in ['/home','/root','/workspace','/run']:
 for p in pathlib.Path(root).rglob('*'):
  try:
   if p.is_file() and p.stat().st_size < 1048576: data+=p.read_text(errors='replace')
  except (OSError,UnicodeError): pass
assert not any(s in data for s in v['secrets']), 'real fixture credential leaked to VM'
assert not pathlib.Path('/run/cxd-credential-store.sock').exists()
def status(url, token):
 r=urllib.request.Request(url+'/token',headers={'X-Codex-Token':token})
 try:
  with urllib.request.urlopen(r,timeout=10) as response: return response.status
 except urllib.error.HTTPError as e: return e.code
assert status(v['url'],v['token'])==200, 'own token rejected'
try: result=status(v['url'],v['other_token'])
except (OSError,urllib.error.URLError): result=0
assert result==401, ('cross-case token rejection was not authenticated',result)
print('guest credential and case isolation PASS')`
		var output bytes.Buffer
		code, err := f.api.Exec(ctx, f.worker.ID, cxd.ExecRequest{Argv: []string{"python3", "-c", script}, Stdin: bytes.NewReader(input), Stdout: &output, Stderr: &output})
		if err != nil || code != 0 {
			t.Fatalf("case isolation: exit=%d err=%v %s", code, err, &output)
		}
		if err := f.api.RevokeRun(ctx, f.id); err != nil {
			t.Fatal(err)
		}
		revoked := `import sys,json,urllib.request,urllib.error
v=json.load(sys.stdin)
r=urllib.request.Request(v['url']+'/token',headers={'X-Codex-Token':v['token']})
try: urllib.request.urlopen(r,timeout=10); raise AssertionError('revoked token accepted')
except urllib.error.HTTPError as e: assert e.code==401, e.code`
		output.Reset()
		code, err = f.api.Exec(ctx, f.worker.ID, cxd.ExecRequest{Argv: []string{"python3", "-c", revoked}, Stdin: bytes.NewReader(input), Stdout: &output, Stderr: &output})
		if err != nil || code != 0 {
			t.Fatalf("revocation: exit=%d err=%v %s", code, err, &output)
		}
		if err := f.api.Stop(ctx, f.worker.ID, 5*time.Second); err != nil {
			t.Fatal(err)
		}
		if err := f.api.RemoveRequest(ctx, f.req.RequestID, true); err != nil {
			t.Fatal(err)
		}
		if err := f.api.RemoveRequest(ctx, f.req.RequestID, true); err != nil {
			t.Fatalf("repeated request cleanup: %v", err)
		}
		if _, err := node.ContainerInspect(ctx, f.worker.ID); !errdefs.IsNotFound(err) {
			t.Fatalf("worker remains after RemoveRequest: %v", err)
		}
		inventory, inventoryErr := verifier.Inventory(ctx)
		if inventoryErr != nil {
			t.Fatal(inventoryErr)
		}
		if _, err := kataguard.ResolveVM(inventory, f.worker.ID); err == nil {
			t.Fatal("VM allocation remains after RemoveRequest")
		}
		if _, err := f.api.Launch(ctx, f.req); !errors.Is(err, cxd.ErrRequestGone) {
			t.Fatalf("removed request resurrected: %v", err)
		}
		report, err := (auditlog.Journal{Directory: filepath.Join(f.usage, "audit")}).Read()
		if err != nil || len(report.Records) == 0 || len(report.Pending) != 0 {
			t.Fatalf("case operation audit incomplete: %v", err)
		}
		t.Log("verified: guest credentials absent, case token isolation, run revocation, Stop and RemoveRequest")
	}
}

type kataCaseStore struct {
	id    string
	creds credentialstore.Credentials
}

func (s *kataCaseStore) Load(_ context.Context, id string) (credentialstore.Credentials, error) {
	if id != s.id {
		return credentialstore.Credentials{}, credentialstore.ErrNotFound
	}
	return s.creds, nil
}
func (s *kataCaseStore) SaveRefresh(context.Context, string, string, string, credentialstore.Credentials) (string, error) {
	return "", errors.New("refresh is outside this lifecycle fixture")
}

// A half-close is input EOF; a canceled call must still kill a silent process.
func checkKataExecInputAndCancellation(t *testing.T, ctx context.Context, api *cxd.Client, workerID, project string) {
	t.Helper()
	checkKataExecWithoutStdinEOF(t, ctx, workerID)
	var output bytes.Buffer
	code, err := api.Exec(ctx, workerID, cxd.ExecRequest{Argv: []string{"python3", "-c", "import sys; print(sys.stdin.read().upper())"}, Stdin: strings.NewReader("stdin eof"), Stdout: &output, Stderr: &output})
	if err != nil || code != 0 || strings.TrimSpace(output.String()) != "STDIN EOF" {
		t.Fatalf("exec output after stdin EOF: exit=%d err=%v %q", code, err, output.String())
	}
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := api.Exec(commandCtx, workerID, cxd.ExecRequest{Argv: []string{"python3", "-c", "import os,time,pathlib; pathlib.Path('/workspace/exec-cancel.pid').write_text(str(os.getpid())); time.sleep(60)"}})
		done <- err
	}()
	var pid []byte
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for len(pid) == 0 {
		select {
		case err := <-done:
			t.Fatalf("exec ended before cancellation: %v", err)
		case <-deadline.C:
			t.Fatal("exec did not publish its PID")
		case <-time.After(20 * time.Millisecond):
			pid, _ = os.ReadFile(filepath.Join(project, "exec-cancel.pid"))
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("exec cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled exec did not return")
	}
	script := `import os,sys,time
pid=int(sys.argv[1])
for _ in range(30):
 try: os.kill(pid,0)
 except ProcessLookupError: sys.exit(0)
 time.sleep(.1)
raise AssertionError('canceled exec process remains alive')`
	output.Reset()
	code, err = api.Exec(ctx, workerID, cxd.ExecRequest{Argv: []string{"python3", "-c", script, strings.TrimSpace(string(pid))}, Stdout: &output, Stderr: &output})
	if err != nil || code != 0 {
		t.Fatalf("exec process cleanup: exit=%d err=%v %s", code, err, &output)
	}
	t.Log("verified: output after stdin EOF; context cancellation kills a silent exec")
}

// Closing only the client's write side must also preserve commands which do
// not attach stdin. Use the node protocol directly so the test controls EOF.
func checkKataExecWithoutStdinEOF(t *testing.T, parent context.Context, workerID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	node, err := engine.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	created, err := node.ContainerExecCreate(ctx, workerID, container.ExecOptions{
		Cmd:          []string{"python3", "-c", "import time; time.sleep(.3); print('output-after-unused-stdin-eof')"},
		AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := node.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	stop := context.AfterFunc(ctx, stream.Close)
	defer stop()
	if err := stream.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	var output, stderr bytes.Buffer
	_, copyErr := stdcopy.StdCopy(&output, &stderr, stream.Reader)
	status, inspectErr := node.ContainerExecInspect(ctx, created.ID)
	if copyErr != nil || inspectErr != nil || status.Running || status.ExitCode != 0 || strings.TrimSpace(output.String()) != "output-after-unused-stdin-eof" {
		t.Fatalf("exec without stdin canceled on EOF: exit=%d running=%t copy=%v inspect=%v stdout=%q stderr=%q", status.ExitCode, status.Running, copyErr, inspectErr, &output, &stderr)
	}
	t.Log("verified: unused stdin EOF preserves exec output and exit status")
}
