package sandbox

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	"github.com/pacificbelt30/codex-dock/internal/authproxy"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
)

//go:embed testdata/network-revocation.py
var networkRevocationFixture string

// Real production handlers/dialer over Kata networking, with synthetic upstreams.
// The observer proves peer-side closure, rather than inferring it from an API ACK.
func TestKataNetworkRevocation(t *testing.T) {
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
	start := func(name string, args []string, auth bool) (string, string) {
		t.Helper()
		ports := nat.PortSet{observerPort: {}}
		bindings := nat.PortMap{observerPort: {{HostIP: "127.0.0.1"}}}
		if auth {
			ports[adminPort] = struct{}{}
			bindings[adminPort] = []nat.PortBinding{{HostIP: "127.0.0.1"}}
		}
		created, err := node.ContainerCreate(ctx, &container.Config{Image: fixtureImage, Cmd: args,
			Env:    []string{"CXD_KATA_NETWORK_FIXTURE=1", "OPENAI_API_KEY=sk-network-fixture-invalid", "ANTHROPIC_API_KEY=sk-ant-network-fixture-invalid"},
			Labels: map[string]string{"io.codex-dock.proxy-layout": "network-acceptance"}, ExposedPorts: ports},
			&container.HostConfig{Runtime: "runc", NetworkMode: container.NetworkMode(egress.ID), PortBindings: bindings}, nil, nil, name)
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
	authObserver, adminURL := start(authName, []string{"proxy", "serve", "--role", "auth", "--listen", "0.0.0.0:18080", "--admin-listen", "egress:18081", "--admin-secret", "network-acceptance-admin"}, true)
	egressObserver, _ := start(httpName, []string{"proxy", "serve", "--role", "egress", "--listen", "0.0.0.0:18082", "--block-private", "--forward-default-deny", "--forward-allow-cidr", "0.0.0.0/0", "--forward-allow-cidr", "::/0", "--forward-allow-port", "80", "--forward-allow-port", "443"}, false)
	var proxy *authproxy.RemoteProxy
	for attempt := 0; attempt < 100; attempt++ {
		proxy, err = authproxy.NewRemoteProxy(adminURL, "http://"+authName+":18080", "network-acceptance-admin")
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	project, state := t.TempDir(), t.TempDir()
	if err := os.Chmod(state, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "network.py"), []byte(networkRevocationFixture), 0600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{cli: node, proxy: proxy, proxyAdminURL: adminURL, httpProxyURL: "http://" + httpName + ":18082", stateDir: state}
	t.Cleanup(func() { _ = m.Close() })
	name := prefix + "-worker"
	id, err := m.RunContext(ctx, RunOptions{Name: name, ProjectDir: project, Image: os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE"), Runtime: RuntimeKata,
		ContainerUser: strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid()), Detach: true, ExecCmd: "exec python3 -u /workspace/network.py"})
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
}
