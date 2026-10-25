package sandbox

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/pacificbelt30/codex-dock/internal/authproxy"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/kataguard"
	"github.com/pacificbelt30/codex-dock/internal/launchtiming"
	"github.com/pacificbelt30/codex-dock/internal/observability"
	"github.com/pacificbelt30/codex-dock/internal/workeregress"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

//go:embed testdata/network-revocation.py
var networkRevocationFixture string

//go:embed testdata/resource-controls.py
var resourceControlsFixture string

// Real production handlers/dialer over Kata networking, with synthetic upstreams.
// The observer proves peer-side closure, rather than inferring it from an API ACK.
func TestKataNetworkRevocation(t *testing.T) { testKataNetworkRevocation(t, false, false) }
func TestKataWorkerMTLS(t *testing.T)        { testKataNetworkRevocation(t, true, false) }
func TestKataResourceControls(t *testing.T)  { testKataNetworkRevocation(t, true, true) }
func testKataNetworkRevocation(t *testing.T, secure, resources bool) {
	if os.Getenv("CODEX_DOCK_KATA_E2E") != "1" {
		t.Skip("set CODEX_DOCK_KATA_E2E=1 on an isolated matching node")
	}
	fixtureImage := os.Getenv("CODEX_DOCK_KATA_NETWORK_PROXY_IMAGE")
	binary := os.Getenv("CODEX_DOCK_KATA_TEST_BINARY")
	if fixtureImage == "" || binary == "" {
		t.Fatal("network acceptance requires the matching test-only proxy image and CLI binary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	node, err := engine.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = node.Close() })
	prefix := "net-kata-" + randomSuffix()
	egress, err := node.NetworkCreate(ctx, prefix, dockernetwork.CreateOptions{Driver: "bridge"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := node.NetworkRemove(context.Background(), egress.ID); err != nil {
			t.Error(err)
		}
	})
	observerPort, adminPort := nat.Port("18083/tcp"), nat.Port("18081/tcp")
	brokerDirectory := t.TempDir()
	if err := os.Chmod(brokerDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	brokerFile := filepath.Join(brokerDirectory, "routes.json")
	createRoute := exec.CommandContext(ctx, binary, "broker", "--file", brokerFile, "set", "service", "--origin", "https://broker.fixture", "--path", "/v1/read", "--exact-path", "/v1/exact", "--method", "GET", "--secret-stdin")
	createRoute.Stdin = strings.NewReader("fixture-broker-secret\n")
	if output, err := createRoute.CombinedOutput(); err != nil {
		t.Fatalf("broker settings: %s %v", output, err)
	}
	writeRoute := exec.CommandContext(ctx, binary, "broker", "--file", brokerFile, "set", "service-write", "--origin", "https://broker.fixture", "--exact-path", "/v1/write", "--method", "POST", "--secret-stdin")
	writeRoute.Stdin = strings.NewReader("fixture-broker-secret\n")
	if output, err := writeRoute.CombinedOutput(); err != nil {
		t.Fatalf("broker write settings: %s %v", output, err)
	}

	usageDirectory := t.TempDir()
	if err := os.Chmod(usageDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	start := func(name string, args []string, auth bool) (string, string) {
		t.Helper()
		if resources {
			args = append(args, "--max-connections", "64", "--max-connections-per-source", "8", "--network-bytes-per-second", "65536")
		}
		ports := nat.PortSet{observerPort: {}}
		bindings := nat.PortMap{observerPort: {{HostIP: "127.0.0.1"}}}
		env := []string{"CXD_KATA_NETWORK_FIXTURE=1"}
		host := &container.HostConfig{Runtime: "runc", NetworkMode: container.NetworkMode(egress.ID), PortBindings: bindings, CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}}
		if auth {
			ports[adminPort] = struct{}{}
			bindings[adminPort] = []nat.PortBinding{{HostIP: "127.0.0.1"}}
			if secure {
				args = append(args, "--worker-mtls")
			}
			args = append(args, "--usage-state-dir", "/run/cxd-usage")
			args = append(args, "--credential-routes", "/run/cxd-broker.json")
			env = append(env, "OPENAI_API_KEY=sk-network-fixture-invalid", "ANTHROPIC_API_KEY=fixture-anthropic-key")
			host.Mounts = []mount.Mount{{Type: mount.TypeBind, Source: usageDirectory, Target: "/run/cxd-usage"}, {Type: mount.TypeBind, Source: brokerFile, Target: "/run/cxd-broker.json", ReadOnly: true}}
		}
		labels := map[string]string{"io.codex-dock.proxy-layout": "network-acceptance"}
		if !auth {
			rules, err := workeregress.ParseProxyRules(args)
			if err != nil {
				t.Fatal(err)
			}
			labels["io.codex-dock.forward-policy"] = workeregress.ID(rules)
		}
		created, err := node.ContainerCreate(ctx, &container.Config{Image: fixtureImage, Cmd: args,
			Env: env, User: strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()),
			Labels: labels, ExposedPorts: ports},
			host, nil, nil, name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := node.ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
				t.Error(err)
			}
		})
		if err = node.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
			t.Fatal(err)
		}
		info, err := node.ContainerInspect(ctx, created.ID)
		if err != nil {
			t.Fatal(err)
		}
		endpoint := func(port nat.Port) string {
			published := info.NetworkSettings.Ports[port]
			if len(published) != 1 || published[0].HostIP != "127.0.0.1" {
				t.Fatal("observer/admin must be published only to loopback")
			}
			return "http://127.0.0.1:" + published[0].HostPort
		}
		admin := ""
		if auth {
			admin = endpoint(adminPort)
		}
		return endpoint(observerPort), admin
	}
	authName, httpName := prefix+"-auth", prefix+"-egress"
	authObserver, adminURL := start(authName, []string{"proxy", "serve", "--role", "auth", "--listen", "0.0.0.0:18080", "--admin-listen", "egress:18081", "--admin-secret", "network-acceptance-admin-fixture-32-bytes"}, true)
	egressObserver, _ := start(httpName, []string{"proxy", "serve", "--role", "egress", "--listen", "0.0.0.0:18082", "--block-private", "--forward-default-deny", "--forward-allow-cidr", "0.0.0.0/0", "--forward-allow-cidr", "::/0", "--forward-allow-port", "80", "--forward-allow-port", "443"}, false)
	var proxy *authproxy.RemoteProxy
	for attempt := 0; attempt < 100; attempt++ {
		proxy, err = authproxy.NewRemoteProxy(adminURL, "http://"+authName+":18080", "network-acceptance-admin-fixture-32-bytes")
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if secure && !proxy.WorkerTransportEnabled() {
		t.Fatal("worker TLS not advertised")
	}
	project, state := t.TempDir(), t.TempDir()
	if resources {
		root := os.Getenv("CODEX_DOCK_RESOURCE_WORKSPACE")
		if root == "" {
			t.Fatal("resource acceptance requires an explicit disk-backed workspace")
		}
		project, err = os.MkdirTemp(root, "run-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.RemoveAll(project); err != nil {
				t.Error(err)
			}
		})
	}
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "network.py"), []byte(networkRevocationFixture), 0600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{cli: node, proxy: proxy, proxyAdminURL: adminURL, httpProxyURL: "http://" + httpName + ":18082", stateDir: state}
	t.Cleanup(func() { _ = m.Close() })
	name := prefix + "-worker"
	launchCtx, timer := launchtiming.Begin(ctx)
	resourceProfile := ""
	if resources {
		resourceProfile = "resource-test"
	}
	id, err := m.RunContext(launchCtx, RunOptions{ResourceProfile: resourceProfile, Name: name, ProjectDir: project, Image: os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE"), Runtime: RuntimeKata,
		ContainerUser: strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()), Detach: true, ExecCmd: "exec python3 -u /workspace/network.py"})
	total, phases := timer.Finish()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := m.RemoveContext(context.Background(), id, true); err != nil {
			t.Error(err)
		}
	})
	for attempt := 0; ; attempt++ {
		if _, err := os.Stat(filepath.Join(project, "connections-ready")); err == nil {
			break
		}
		if attempt == 150 {
			logs, err := node.ContainerLogs(ctx, id, container.LogsOptions{ShowStdout: true, ShowStderr: true, Tail: "all"})
			if err == nil {
				defer logs.Close()
				var out bytes.Buffer
				_, _ = out.ReadFrom(logs)
				t.Fatalf("guest did not establish checked connections: %s", &out)
			}
			t.Fatal("guest did not establish checked connections:", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	waitConnections := func(endpoint string, active int64) {
		t.Helper()
		client := &http.Client{Timeout: time.Second}
		var last struct{ Active, Opened int64 }
		for attempt := 0; attempt < 100; attempt++ {
			response, err := client.Get(endpoint)
			if err == nil {
				err = json.NewDecoder(response.Body).Decode(&last)
				_ = response.Body.Close()
				if err == nil && response.StatusCode == 200 && last.Active == active && last.Opened > 0 {
					return
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("peer connections did not reach %d: %+v", active, last)
	}
	waitConnections(authObserver, 2)
	waitConnections(egressObserver, 3)
	t.Log("verified: guest HTTP/CONNECT reject private IPv4/IPv6, mapped metadata and forbidden port; redirect to metadata rejected; public IPv4/IPv6 CONNECT echo and streams established")

	if resources {
		if err := os.WriteFile(filepath.Join(project, "resources.py"), []byte(resourceControlsFixture), 0600); err != nil {
			t.Fatal(err)
		}
		verifier, err := kataguard.Connect(engine.Socket(), "unix://"+engine.Socket())
		if err != nil {
			t.Fatal(err)
		}
		policy, err := m.resourceProfile(ctx, resourceProfile)
		if err != nil || policy.FileDescriptorLimit != 512 || len(policy.BlockIO) == 0 {
			t.Fatalf("resource policy missing: %+v %v", policy, err)
		}
		before, err := verifier.Inventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		beforeVM, err := kataguard.ResolveVM(before, id)
		if err != nil || !beforeVM.HostVerified || kataguard.VerifyIOLimits(beforeVM.Resources["io.max"], policy.BlockIO) != nil {
			t.Fatalf("unverified host I/O policy: %+v %v", beforeVM, err)
		}
		output := kataExec(t, ctx, node, id, false, []string{"python3", "/workspace/resources.py"})
		t.Logf("resource controls in real Kata: %s", output)

		waitConnections(authObserver, 2)
		waitConnections(egressObserver, 3)
		after, err := verifier.Inventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		afterVM, err := kataguard.ResolveVM(after, id)
		if err != nil || !afterVM.HostVerified || kataguard.VerifyIOLimits(afterVM.Resources["io.max"], policy.BlockIO) != nil {
			t.Fatalf("host I/O policy changed: %+v %v", afterVM, err)
		}
		t.Logf("host block I/O observations before=%q after=%q", beforeVM.Resources["io.stat"], afterVM.Resources["io.stat"])
		var observed struct {
			WriteSeconds float64 `json:"write_seconds"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &observed); err != nil || observed.WriteSeconds < 1 {
			t.Fatalf("host I/O throttling was not observed: %s %v", output, err)
		}
	}

	t.Run("diagnostic-capture-export", func(t *testing.T) {
		directory := t.TempDir()
		if err := os.Chmod(directory, 0700); err != nil {
			t.Fatal(err)
		}
		journal := observability.Journal{Directory: directory}
		if err := journal.Append(observability.Launch(id, total, phases, false)); err != nil {
			t.Fatal(err)
		}
		// Status may report missing independent credentials in this synthetic test;
		// the verified worker memory and retained denial export must still succeed.
		config := filepath.Join(t.TempDir(), "config.toml")
		content := fmt.Sprintf("[proxy]\nadmin_url = %q\ncontainer_url = %q\nadmin_secret = %q\n", adminURL, "http://"+authName+":18080", "network-acceptance-admin-fixture-32-bytes")
		if err := os.WriteFile(config, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		output, captureErr := exec.CommandContext(ctx, binary, "--config", config, "observe", "capture", "--directory", directory, "--worker", id).CombinedOutput()
		if captureErr != nil && !bytes.Contains(output, []byte("diagnostics saved with failed or unavailable checks")) {
			t.Fatalf("capture: %s %v", output, captureErr)
		}
		output, err := exec.CommandContext(ctx, binary, "observe", "export", "--directory", directory).CombinedOutput()
		if err != nil {
			t.Fatalf("export: %s %v", output, err)
		}
		records, err := journal.Read()
		if err != nil {
			t.Fatal(err)
		}
		kinds := map[string]bool{}
		for _, r := range records {
			kinds[r.Kind] = true
			if r.Kind == "resources" && (r.Memory == nil || r.Memory.Current <= 0 || r.Memory.HostLimit <= 0) {
				t.Fatal("live Kata memory missing", r)
			}
		}
		for _, kind := range []string{"status", "resources", "denial", "launch"} {
			if !kinds[kind] {
				t.Fatal("missing diagnostic kind", kind, string(output))
			}
		}
		if bytes.Contains(output, []byte("network-acceptance-admin-fixture-32-bytes")) || bytes.Contains(output, []byte("Bearer ")) || bytes.Contains(output, []byte(project)) {
			t.Fatal("sensitive diagnostic data exported")
		}
		t.Log("PASS: real Kata status, proxy denials, host memory and launch phases saved/exported without credential or workspace values")
	})
	if err := proxy.RevokeTokenChecked(name); err != nil {
		t.Fatal(err)
	}
	waitConnections(authObserver, 0)
	waitConnections(egressObserver, 3)
	info, err := node.ContainerInspect(ctx, id)
	if err != nil || info.State == nil || !info.State.Running {
		t.Fatalf("token revocation unexpectedly stopped guest: %v", err)
	}
	t.Log("verified: token revocation closes existing SSE and WebSocket upstream peers while general CONNECT remains independently active")
	if err := m.QuarantineContext(ctx, id); err != nil {
		t.Fatal(err)
	}
	waitConnections(egressObserver, 0)
	info, err = node.ContainerInspect(ctx, id)
	if err != nil || info.State == nil || info.State.Running || info.Config.Labels[workerpolicy.QuarantinedLabel] != "true" {
		t.Fatalf("quarantine stop not confirmed: %v", err)
	}
	if err := m.StartContext(ctx, id); err == nil {
		t.Fatal("quarantined guest restarted")
	}
	for _, action := range [][]string{{"show"}, {"explain", "203.0.113.1:443"}} {
		args := append([]string{"policy"}, action...)
		args = append(args, "--worker", id, "--json")
		out, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
		if err != nil || action[0] == "show" && (!bytes.Contains(out, []byte(`"quarantined":true`)) || bytes.Contains(out, []byte(`"quarantine_pending":true`))) || action[0] == "explain" && !bytes.Contains(out, []byte("worker_quarantined")) {
			t.Fatalf("CLI quarantine status: %s %v", out, err)
		}
	}
	if _, err := os.Stat(filepath.Join(project, "connections-ready")); err != nil {
		t.Fatal("quarantine lost workspace:", err)
	}
	t.Log("PASS: real Kata token revocation and quarantine close upstream peers, preserve workspace, forbid restart and report confirmed stop correctly")

	t.Run("broker-configuration-scope-and-private-file", func(t *testing.T) {
		opts := RunOptions{Name: prefix + "-broker", ProjectDir: t.TempDir(), Image: os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE"), Runtime: RuntimeKata, ContainerUser: strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()), NoInternet: true, Detach: true, ExecCmd: "exec sleep infinity"}
		worker, err := m.RunContext(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := m.RemoveContext(context.Background(), worker, true); err != nil {
				t.Error(err)
			}
		})
		script := `import os,urllib.request,urllib.error
assert not os.path.exists('/run/cxd-broker.json')
for route,method,path,expected in [
 ('service','GET','/v1/read/item',200),('service','POST','/v1/read/item',403),('service','GET','/private',403),
 ('service','GET','/v1/exact',200),('service','GET','/v1/exact/child',403),('service','GET','/v1/exact-more',403),
 ('service','GET','/v1/%65xact',403),('service','GET','/v1/exact%00',403),
 ('service-write','POST','/v1/write',200),('service-write','GET','/v1/write',403),('service-write','POST','/v1/write/child',403)]:
 req=urllib.request.Request(os.environ['CODEX_AUTH_PROXY_URL']+'/broker/'+route+path,method=method,headers={'Authorization':'Bearer '+os.environ['CODEX_TOKEN']})
 try:
  with urllib.request.urlopen(req,timeout=10) as r: status=r.status; body=r.read(); assert b'fixture-broker-secret' not in body
 except urllib.error.HTTPError as e: status=e.code; e.close()
 assert status==expected,(status,expected)
print('BROKER_SCOPE_PASS')`
		var out bytes.Buffer
		code, err := m.Exec(ctx, worker, []string{"python3", "-c", script}, nil, &out, &out)
		if err != nil || code != 0 || !strings.Contains(out.String(), "BROKER_SCOPE_PASS") {
			t.Fatalf("broker scope: %s %d %v", &out, code, err)
		}
		t.Run("candidate-agent-clis", func(t *testing.T) { checkCandidateAgentCLIs(t, ctx, m, worker) })
		opts.Name += "-forbidden"
		opts.ProjectDir = brokerDirectory
		if forbidden, err := m.RunContext(ctx, opts); err == nil {
			_ = m.RemoveContext(context.Background(), forbidden, true)
			t.Fatal("broker secrets shared into VM")
		} else if !strings.Contains(err.Error(), "protected proxy") {
			t.Fatalf("share denied for unrelated reason: %v", err)
		}
		t.Log("PASS: configured broker enforces prefix/exact paths and explicit GET/POST, rejects encoded/control paths and credential-file sharing")
	})

	t.Run("execution-usage-persistence", func(t *testing.T) {
		limits := &identity.UsagePolicy{MaxRequests: 2, MaxConcurrent: 1, MaxBytes: 1 << 20, MaxTokens: 20}
		options := RunOptions{Name: prefix + "-usage", ProjectDir: t.TempDir(), Image: os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE"), Runtime: RuntimeKata,
			ContainerUser: strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()), NoInternet: true, Detach: true, ExecCmd: "exec sleep infinity", TokenUsagePolicy: limits}
		worker, err := m.RunContext(ctx, options)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := m.RemoveContext(context.Background(), worker, true); err != nil {
				t.Error(err)
			}
		})
		inspect, err := node.ContainerInspect(ctx, worker)
		if err != nil {
			t.Fatal(err)
		}
		token, _ := envValue(inspect.Config.Env, "CODEX_TOKEN")
		endpoint, _ := envValue(inspect.Config.Env, "OPENAI_BASE_URL")
		if token == "" || endpoint == "" {
			t.Fatal("missing proxy placeholder/endpoint")
		}
		probe := func(expected []int) {
			t.Helper()
			input, _ := json.Marshal(map[string]any{"url": endpoint, "token": token, "expected": expected})
			script := `import sys,json,time,urllib.request,urllib.error
v=json.load(sys.stdin)
for expected in v['expected']:
 for attempt in range(50):
  r=urllib.request.Request(v['url']+'/responses',data=b'{"input":"usage-budget"}',headers={'Authorization':'Bearer '+v['token'],'Content-Type':'application/json'})
  try:
   with urllib.request.urlopen(r,timeout=10) as response:
    status=response.status; response.read()
  except urllib.error.HTTPError as e: status=e.code; e.close()
  if status==429 and expected==200: time.sleep(.02); continue
  break
 assert status==expected,(status,expected)
print('usage limits PASS')`
			var output bytes.Buffer
			code, err := m.Exec(ctx, worker, []string{"python3", "-c", script}, bytes.NewReader(input), &output, &output)
			if err != nil || code != 0 {
				t.Fatalf("usage request: exit=%d err=%v %s", code, err, &output)
			}
		}
		probe([]int{200, 200, 429})
		stats, err := proxy.UsageContext(ctx, options.Name)
		if err != nil || stats.Requests != 2 || stats.Tokens != 10 || stats.Active != 0 || stats.Indeterminate {
			t.Fatalf("usage snapshot: %+v %v", stats, err)
		}
		// A stop/start of both the actual VM and proxy must preserve the budget.
		if err := m.StopContext(ctx, worker, 5); err != nil {
			t.Fatal(err)
		}
		timeout := 5
		if err := node.ContainerStop(ctx, authName, container.StopOptions{Timeout: &timeout}); err != nil {
			t.Fatal(err)
		}
		if err := node.ContainerStart(ctx, authName, container.StartOptions{}); err != nil {
			t.Fatal(err)
		}
		for attempt := 0; attempt < 100; attempt++ {
			_, err = authproxy.NewRemoteProxy(adminURL, "http://"+authName+":18080", "network-acceptance-admin-fixture-32-bytes")
			if err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := m.StartContext(ctx, worker); err != nil {
			t.Fatal(err)
		}
		probe([]int{429})
		stats, err = proxy.UsageContext(ctx, options.Name)
		if err != nil || stats.Requests != 2 || stats.Tokens != 10 {
			t.Fatalf("proxy/worker restart reset budget: %+v %v", stats, err)
		}
		bad := options
		bad.Name += "-forbidden-share"
		bad.ProjectDir = usageDirectory
		bad.TokenUsagePolicy = nil
		if forbidden, err := m.RunContext(ctx, bad); err == nil {
			_ = m.RemoveContext(context.Background(), forbidden, true)
			t.Fatal("worker received writable usage authority")
		} else if !strings.Contains(err.Error(), "usage state") {
			t.Fatalf("share denied for unrelated reason: %v", err)
		}
		unsafeProxy, err := node.ContainerCreate(ctx, &container.Config{Image: fixtureImage, Labels: map[string]string{"io.codex-dock.proxy-layout": "usage-negative-fixture"}},
			&container.HostConfig{Runtime: "runc", NetworkMode: container.NetworkMode(egress.ID), Mounts: []mount.Mount{{Type: mount.TypeBind, Source: options.ProjectDir, Target: "/run/cxd-usage"}}}, nil, nil, prefix+"-unsafe-ledger")
		if err == nil {
			_ = node.ContainerRemove(context.Background(), unsafeProxy.ID, container.RemoveOptions{Force: true})
			t.Fatal("new ledger overlapped a live worker share")
		} else if !strings.Contains(err.Error(), "usage state") {
			t.Fatalf("ledger denied for unrelated reason: %v", err)
		}
		t.Log("PASS: real Kata request/token usage, denied excess request, proxy+worker restart persistence and private-ledger share denial")
	})
}
