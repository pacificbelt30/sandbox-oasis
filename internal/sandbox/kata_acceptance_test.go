package sandbox

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"
	"github.com/pacificbelt30/codex-dock/internal/agentpolicy"
	"github.com/pacificbelt30/codex-dock/internal/authproxy"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/kataguard"
	"os/exec"
)

//go:embed testdata/codex-tool-fixture.py
var codexToolFixture string

// This suite must run ON the Linux/KVM execution host, using prebuilt images.
// It creates only uniquely named test resources and uses synthetic credentials.
// An explicitly requested run fails (rather than skips) if prerequisites fail.
func TestKataAcceptance(t *testing.T) {
	if os.Getenv("CODEX_DOCK_KATA_E2E") != "1" {
		t.Skip("set CODEX_DOCK_KATA_E2E=1 on a configured Linux/KVM host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c, err := engine.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if _, err := c.Ping(ctx); err != nil {
		t.Fatalf("Kata acceptance requires the containerd node: %v", err)
	}
	workerImage := os.Getenv("CODEX_DOCK_KATA_TEST_IMAGE")
	if workerImage == "" {
		workerImage = "codex-dock:latest"
	}
	proxyImage := os.Getenv("CODEX_DOCK_KATA_PROXY_IMAGE")
	if proxyImage == "" {
		proxyImage = "codex-dock-proxy:latest"
	}
	for _, image := range []string{workerImage, proxyImage} {
		if _, err := c.ImageInspect(ctx, image); err != nil {
			t.Fatalf("preload test image %s: %v", image, err)
		}
	}
	prefix := "dock-kata-test-" + randomSuffix()
	buildDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte("FROM "+workerImage+"\nUSER 0\nRUN mkdir -p /opt/codex-dock-custom && printf custom-image-ok > /opt/codex-dock-custom/sentinel && chmod 0644 /opt/codex-dock-custom/sentinel\nUSER 1001\n"), 0644); err != nil {
		t.Fatal(err)
	}
	customImage := prefix + "-custom:verification"
	var buildLog bytes.Buffer
	if err := engine.Build(ctx, customImage, filepath.Join(buildDir, "Dockerfile"), buildDir, &buildLog); err != nil {
		t.Fatalf("standalone BuildKit custom image: %v\n%s", err, buildLog.String())
	}
	workerImage = customImage
	t.Log("verified: standalone BuildKit custom Dockerfile RUN, OCI export and containerd import")
	report, err := engine.CheckImage(ctx, workerImage, "codex")
	if err != nil || report.Err() != nil {
		t.Fatalf("custom image static preflight: %v %v", err, report.Err())
	}
	if err = os.WriteFile(filepath.Join(buildDir, "Dockerfile"), []byte("FROM "+workerImage+"\nUSER 0\nRUN rm /usr/bin/python3\nUSER 1001\n"), 0644); err != nil {
		t.Fatal(err)
	}
	badImage := prefix + "-missing-python:verification"
	if err = engine.Build(ctx, badImage, filepath.Join(buildDir, "Dockerfile"), buildDir, &buildLog); err != nil {
		t.Fatalf("negative image fixture: %v", err)
	}
	badReport, err := engine.CheckImage(ctx, badImage, "codex")
	if err != nil || badReport.Err() == nil {
		t.Fatalf("missing Python not caught before worker creation: %v %+v", err, badReport)
	}
	t.Log("verified: read-only image compatibility checks accept the custom image and reject missing Python without a worker")

	egress, err := c.NetworkCreate(ctx, prefix+"-egress", dockernetwork.CreateOptions{Driver: "bridge"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.NetworkRemove(context.Background(), egress.ID); err != nil {
			t.Error("test egress cleanup:", err)
		}
	})
	start := func(name string, config *container.Config, hc *container.HostConfig) string {
		if config.Labels == nil {
			config.Labels = map[string]string{}
		}
		config.Labels["io.codex-dock.proxy-layout"] = "acceptance"
		t.Helper()
		r, err := c.ContainerCreate(ctx, config, hc, nil, nil, name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := c.ContainerRemove(context.Background(), r.ID, container.RemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
				t.Error("fixture cleanup:", err)
			}
		})
		if err := c.ContainerStart(ctx, r.ID, container.StartOptions{}); err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	authName, httpName := prefix+"-auth", prefix+"-http"
	adminPort := nat.Port("18081/tcp")
	authID := start(authName, &container.Config{Image: proxyImage,
		Env:          []string{"OPENAI_API_KEY=sk-fixture-never-valid", "ANTHROPIC_API_KEY=sk-ant-fixture-never-valid"},
		Cmd:          []string{"proxy", "serve", "--role", "auth", "--listen", "0.0.0.0:18080", "--admin-listen", "egress:18081", "--admin-secret", "fixture-admin", "--block-private"},
		ExposedPorts: nat.PortSet{adminPort: {}}}, &container.HostConfig{Runtime: "runc", NetworkMode: container.NetworkMode(egress.ID),
		PortBindings: nat.PortMap{adminPort: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: ""}}}})
	start(httpName, &container.Config{Image: proxyImage, Cmd: []string{"proxy", "serve", "--role", "egress", "--listen", "0.0.0.0:18082", "--block-private"}},
		&container.HostConfig{Runtime: "runc", NetworkMode: container.NetworkMode(egress.ID)})
	authInfo, err := c.ContainerInspect(ctx, authID)
	if err != nil {
		t.Fatal(err)
	}
	bindings := authInfo.NetworkSettings.Ports[adminPort]
	if len(bindings) != 1 {
		t.Fatal("fixture admin port was not published to loopback")
	}
	adminURL := "http://127.0.0.1:" + bindings[0].HostPort
	containerURL := "http://" + authName + ":18080"
	var proxy *authproxy.RemoteProxy
	for i := 0; i < 50; i++ {
		proxy, err = authproxy.NewRemoteProxy(adminURL, containerURL, "fixture-admin")
		if err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal("fixture auth proxy did not start:", err)
	}
	m := &Manager{cli: c, proxy: proxy, httpProxyURL: "http://" + httpName + ":18082", proxyAdminURL: adminURL}
	runtimeName := os.Getenv("CODEX_DOCK_KATA_RUNTIME")
	if err := m.CheckRuntime(ctx, RunOptions{KataRuntime: runtimeName}); err != nil {
		t.Fatal(err)
	}
	t.Run("host-agent-policy", func(t *testing.T) {
		file := agentpolicy.Empty()
		file.Profiles["kata-policy"] = agentpolicy.Profile{
			Extends: "approve-for-me",
			Codex: agentpolicy.CodexSettings{
				WebSearch:          agentpolicy.String("disabled"),
				HistoryPersistence: agentpolicy.String("none"),
				HistoryMaxBytes:    agentpolicy.Int64(0),
			},
		}
		policy, err := agentpolicy.Resolve(file, "kata-policy")
		if err != nil {
			t.Fatal(err)
		}
		id, err := m.Run(RunOptions{
			Name: prefix + "-agent-policy", Image: workerImage, KataRuntime: runtimeName,
			ProjectDir: t.TempDir(), ContainerUser: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
			NoInternet: true, AgentPolicy: policy,
			ExecCmd: `grep -Fx 'approvals_reviewer = "auto_review"' "$HOME/.codex/config.toml" && grep -Fx 'web_search = "disabled"' "$HOME/.codex/config.toml" && grep -Fx 'history.persistence = "none"' "$HOME/.codex/config.toml" && grep -Fx 'history.max_bytes = 0' "$HOME/.codex/config.toml" && printf 'AGENT_POLICY_OK\n'`,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := m.Remove(id, true); err != nil {
				t.Error(err)
			}
		}()
		select {
		case err := <-m.Wait(id):
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("agent-policy worker did not exit")
		}
		info, err := c.ContainerInspect(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if info.Config == nil || info.Config.Labels[labelPrefix+"agent-policy"] != "kata-policy" {
			t.Fatalf("agent-policy label missing: %#v", info.Config)
		}
		logs, err := c.ContainerLogs(ctx, id, container.LogsOptions{ShowStdout: true, ShowStderr: true, Tail: "all"})
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(logs)
		_ = logs.Close()
		if readErr != nil || !bytes.Contains(data, []byte("AGENT_POLICY_OK")) {
			t.Fatalf("agent-policy configuration was not applied: %q %v", data, readErr)
		}
	})
	t.Run("primary-terminal-exit-and-restart", func(t *testing.T) {
		id, err := m.Run(RunOptions{Name: prefix + "-terminal", Image: workerImage, KataRuntime: runtimeName,
			ProjectDir: t.TempDir(), ContainerUser: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
			NoInternet: true, ExecCmd: "printf 'PRIMARY_TERMINAL_OK\\n'"})
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := m.Remove(id, true); err != nil {
				t.Error(err)
			}
		}()
		for generation := 1; generation <= 2; generation++ {
			if generation == 2 {
				if err := m.StartContext(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case err := <-m.Wait(id):
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("primary terminal failed to report exit")
			}
			logs, err := c.ContainerLogs(ctx, id, container.LogsOptions{ShowStdout: true, ShowStderr: true, Tail: "all"})
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(logs)
			_ = logs.Close()
			if err != nil || strings.Count(string(data), "\nPRIMARY_TERMINAL_OK\r\n") != generation {
				t.Fatalf("terminal history after generation %d: %q %v", generation, data, err)
			}
			if attached, err := c.ContainerAttach(ctx, id, container.AttachOptions{Stdin: true, Stream: true}); err == nil {
				attached.Close()
				t.Fatal("finished task accepted stdin-only attach")
			}
		}
	})

	// Exercise a shim Create/Start failure before any image code can run.
	guard, err := m.kataNode()
	if err != nil {
		t.Fatal(err)
	}
	failedAllocation, err := randomID(16)
	if err != nil {
		t.Fatal(err)
	}
	failedParent, err := guard.Reserve(ctx, failedAllocation)
	if err != nil {
		t.Fatal(err)
	}
	failedName := prefix + "-failed-start"
	failedNet, err := c.NetworkCreate(ctx, "dock-net-w-"+failedName, dockernetwork.CreateOptions{Driver: "bridge", Internal: true, Options: map[string]string{"com.docker.network.bridge.gateway_mode_ipv4": "isolated"}, Labels: map[string]string{"codex-dock.managed": "true", "codex-dock.isolation": "kata"}})
	if err != nil {
		t.Fatal(err)
	}
	failedHost := buildHostConfig(nil, "dock-net-w-"+failedName)
	failedHost.Runtime = DefaultKataRuntime
	failedHost.CgroupParent = failedParent
	failedHost.Memory = kataguard.GuestMemory
	failedHost.MemorySwap = kataguard.GuestMemory
	failedHost.NanoCPUs = 1_000_000_000
	failedHost.DNS = []string{"127.0.0.1"}
	failed, err := c.ContainerCreate(ctx, &container.Config{Image: workerImage, User: "1000:1000", Entrypoint: []string{"/nonexistent-codex-dock-test-entrypoint"}, Labels: map[string]string{labelManaged: "true", labelIsolation: RuntimeKata, labelRuntime: DefaultKataRuntime, labelAllocation: failedAllocation, labelProfile: guard.Stamp()}}, failedHost, nil, nil, failedName)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.ContainerRemove(context.Background(), failed.ID, container.RemoveOptions{Force: true})
		_ = guard.Release(context.Background(), failedAllocation, failed.ID)
		_ = c.NetworkRemove(context.Background(), failedNet.ID)
	})
	if err = c.ContainerStart(ctx, failed.ID, container.StartOptions{}); err == nil {
		t.Fatal("invalid entrypoint unexpectedly started")
	}
	if err = c.ContainerRemove(ctx, failed.ID, container.RemoveOptions{Force: true}); err != nil {
		t.Fatal("failed-start container cleanup:", err)
	}
	if err = guard.Release(ctx, failedAllocation, failed.ID); err != nil {
		t.Fatal("failed-start envelope cleanup:", err)
	}
	if err = c.NetworkRemove(ctx, failedNet.ID); err != nil {
		t.Fatal("failed-start network cleanup:", err)
	}
	t.Log("verified: failed Dragonball task creation is contained, removed and releases its allocation")

	hostBoot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	hostBootID := strings.TrimSpace(string(hostBoot))

	repo, wt := gitFixture(t)
	pointerBefore, err := os.ReadFile(filepath.Join(wt, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "sentinel"), []byte("fixture"), 0644); err != nil {
		t.Fatal(err)
	}
	opts := RunOptions{Name: prefix + "-worker", Image: workerImage, KataRuntime: runtimeName, ProjectDir: repo, WorktreePath: wt, UseWorktree: true,
		Detach: true, TokenTTL: 900, ContainerUser: fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), ExecCmd: `exec python3 -u -c 'import time
while True:
 print("LOG_TICK", flush=True)
 time.sleep(0.2)'`,
		Mounts: []MountSpec{{Source: data, Target: "/fixtures", ReadOnly: true}}}
	w1, err := m.Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	cleanupWorker := func(id string) {
		t.Cleanup(func() {
			if _, err := c.ContainerInspect(context.Background(), id); errdefs.IsNotFound(err) {
				return
			}
			if err := m.Remove(id, true); err != nil {
				t.Error("worker cleanup:", err)
			}
		})
	}
	cleanupWorker(w1)
	ready := func(id, process string) {
		t.Helper()
		for i := 0; i < 80; i++ {
			v, err := c.ContainerInspect(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if v.State != nil && !v.State.Running {
				logs, e := c.ContainerLogs(ctx, id, container.LogsOptions{ShowStdout: true, ShowStderr: true, Tail: "30"})
				var details []byte
				if e == nil {
					details, _ = io.ReadAll(io.LimitReader(logs, 8192))
					_ = logs.Close()
				}
				t.Fatalf("worker exited during auth setup (exit %d): %s", v.State.ExitCode, details)
			}
			if out, err := kataExecResult(ctx, c, id, false, []string{"sh", "-c", `test -s "$HOME/.codex/config.toml" && test "$(cat /proc/1/comm)" = "$1" && echo ready`, "readiness", process}); err == nil && strings.TrimSpace(out) == "ready" {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("worker auth setup timed out")
	}
	ready(w1, "python3")
	inventory, err := m.VMInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	live, err := kataguard.ResolveVM(inventory, w1)
	if err != nil || !inventory.EngineAvailable || live.State != "running" || !live.HostVerified || !live.Tracked || len(live.Processes) != 2 {
		t.Fatalf("VM inventory did not attest the running worker: %+v %v", live, err)
	}
	// The actual public CLI must use the same node inventory as the manager.
	if binary := os.Getenv("CODEX_DOCK_KATA_TEST_BINARY"); binary != "" {
		command := exec.CommandContext(ctx, binary, "vm", "ps", "--all", "--json")
		data, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("vm ps CLI: %v %s", err, data)
		}
		var snapshot kataguard.Inventory
		if err := json.Unmarshal(data, &snapshot); err != nil {
			t.Fatal(err)
		}
		if row, err := kataguard.ResolveVM(snapshot, w1); err != nil || row.State != "running" {
			t.Fatalf("CLI VM list mismatch: %+v %v", row, err)
		}
		connect := exec.CommandContext(ctx, binary, "connect", "--shell", "sh", w1)
		connect.Stdin = strings.NewReader("printf 'CONNECT_EXEC_OK\\n'\nexit\n")
		connected, err := connect.CombinedOutput()
		if err != nil || !bytes.Contains(connected, []byte("CONNECT_EXEC_OK")) {
			t.Fatalf("connect CLI did not complete a separate PTY session: %v %q", err, connected)
		}
	}
	t.Log("verified: VM inventory and public vm ps CLI attest host PIDs and resource envelope")
	guestBootID := strings.TrimSpace(kataExec(t, ctx, c, w1, false, []string{"cat", "/proc/sys/kernel/random/boot_id"}))
	if guestBootID == "" || guestBootID == hostBootID {
		t.Fatal("worker shares the host kernel boot ID; VM boundary not established")
	}
	t.Log("verified: guest kernel boot ID differs from execution host")
	if requestDir := os.Getenv("CODEX_DOCK_NODE_RESTART_REQUEST_DIR"); requestDir != "" {
		if err := os.Remove(filepath.Join(requestDir, "complete")); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		readLogs := func() string {
			rc, err := c.ContainerLogs(ctx, w1, container.LogsOptions{ShowStdout: true, ShowStderr: true, Tail: "all"})
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				t.Fatal(err)
			}
			return string(data)
		}
		before := strings.Count(readLogs(), "LOG_TICK")
		if err := os.WriteFile(filepath.Join(requestDir, "request"), []byte("restart\n"), 0600); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 600; i++ {
			if _, err := os.Stat(filepath.Join(requestDir, "complete")); err == nil {
				break
			}
			if i == 599 {
				t.Fatal("node restart harness timed out")
			}
			time.Sleep(100 * time.Millisecond)
		}
		if after := strings.TrimSpace(kataExec(t, ctx, c, w1, false, []string{"cat", "/proc/sys/kernel/random/boot_id"})); after != guestBootID {
			t.Fatal("node restart recreated the guest")
		}
		time.Sleep(time.Second)
		if strings.Count(readLogs(), "LOG_TICK") <= before {
			t.Fatal("continuous console output did not survive node restart")
		}
		t.Log("verified: node restart preserves VM boot identity, continuous logs, proxy admin binding and management")
	}

	// Inspect the actual worker and its inherited restrictions. The Go shim's
	// cmdToKataProcess does not forward NoNewPrivileges for host-admin Docker
	// exec; the product launches user code through the restricted entrypoint,
	// never through ContainerExecCreate. Admin exec still has seccomp/capdrop.
	kataExec(t, ctx, c, w1, false, []string{"sh", "-c", `grep -Eq '^Seccomp:[[:space:]]+2$' /proc/1/status && grep -Eq '^NoNewPrivs:[[:space:]]+1$' /proc/1/status && grep -Eq '^CapEff:[[:space:]]+0+$' /proc/1/status && grep -Eq '^Seccomp:[[:space:]]+2$' /proc/self/status && grep -Eq '^CapEff:[[:space:]]+0+$' /proc/self/status`})
	kataExec(t, ctx, c, w1, false, []string{"sh", "-c", `test "$(cat /sys/fs/cgroup/pids.max)" = 512`})
	t.Log("verified: guest seccomp, no-new-privileges and PID limit")
	t.Run("CodexToolExecution", func(t *testing.T) {
		t.Log(kataExec(t, ctx, c, w1, false, []string{"python3", "-c", codexToolFixture}))
	})
	kataExec(t, ctx, c, w1, false, []string{"sh", "-c", `test "$(cat /opt/codex-dock-custom/sentinel)" = custom-image-ok && codex --version && claude --version && git status --porcelain && echo kata > /workspace/kata-created && test "$(cat /fixtures/sentinel)" = fixture && ! touch /fixtures/forbidden`})
	if _, err := os.Stat(filepath.Join(wt, "kata-created")); err != nil {
		t.Fatal("workspace writes not visible on host:", err)
	}
	pointerAfter, err := os.ReadFile(filepath.Join(wt, ".git"))
	if err != nil || !bytes.Equal(pointerBefore, pointerAfter) {
		t.Fatal("host Git pointer changed")
	}
	kataExec(t, ctx, c, w1, false, []string{"sh", "-c", `git add kata-created && git -c user.name=Fixture -c user.email=fixture@example.invalid -c commit.gpgsign=false commit -m kata-acceptance`})
	kataExec(t, ctx, c, w1, true, []string{"bash", "-ic", "test -t 1 && stty size"})
	w1info, err := c.ContainerInspect(ctx, w1)
	if err != nil {
		t.Fatal(err)
	}
	authEndpoint, _ := envValue(w1info.Config.Env, "CODEX_AUTH_PROXY_URL")
	httpEndpoint, _ := envValue(w1info.Config.Env, "HTTP_PROXY")
	if authEndpoint == "" || httpEndpoint == "" {
		t.Fatal("worker has no pinned proxy endpoints")
	}
	kataExec(t, ctx, c, w1, false, []string{"curl", "-fsS", "--noproxy", "*", authEndpoint + "/health"})
	kataExec(t, ctx, c, w1, false, []string{"curl", "-fsS", "--noproxy", "*", httpEndpoint + "/health"})
	kataExec(t, ctx, c, w1, false, []string{"sh", "-c", `test "$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 -x "$HTTP_PROXY" http://10.255.255.1/)" = 403`})
	deny := func(id, url string) {
		t.Helper()
		kataExec(t, ctx, c, id, false, []string{"sh", "-c", `! curl -fsS --noproxy '*' --connect-timeout 2 --max-time 3 "$1"`, "probe", url})
	}
	deny(w1, "http://"+net.JoinHostPort(proxyEndpointHost(authEndpoint), "18081")+"/admin/mode")

	// A live host service has a positive control from the egress network.
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "canary") }), ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	n, err := c.NetworkInspect(ctx, egress.ID, dockernetwork.InspectOptions{})
	if err != nil || len(n.IPAM.Config) == 0 {
		t.Fatal("missing egress gateway")
	}
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	hostCanary := "http://" + net.JoinHostPort(n.IPAM.Config[0].Gateway, port)
	probeID := start(prefix+"-probe", &container.Config{Image: workerImage, Entrypoint: []string{"sleep"}, Cmd: []string{"900"}},
		&container.HostConfig{Runtime: "runc", NetworkMode: container.NetworkMode(egress.ID)})
	kataExec(t, ctx, c, probeID, false, []string{"curl", "-fsS", "--noproxy", "*", "--max-time", "3", hostCanary})
	deny(w1, hostCanary)

	opts.Name = prefix + "-readonly"
	opts.WorktreePath = ""
	opts.UseWorktree = false
	opts.ReadOnly = true
	opts.NoInternet = true
	opts.ExecCmd = "python3 -m http.server 8089 --bind 0.0.0.0"
	opts.Mounts = nil
	w2, err := m.Run(opts)
	if err != nil {
		t.Fatal(err)
	}
	cleanupWorker(w2)
	third := opts
	third.Name = prefix + "-over-capacity"
	if id, err := m.Run(third); err == nil {
		_ = m.Remove(id, true)
		t.Fatal("node admitted a third VM beyond its configured limit of two")
	} else if !strings.Contains(err.Error(), "limit") {
		t.Fatalf("unexpected admission error: %v", err)
	}
	t.Log("verified: node concurrency ceiling rejects third worker")
	ready(w2, "python3")
	kataExec(t, ctx, c, w2, false, []string{"sh", "-c", `! touch /workspace/forbidden && test -z "${HTTP_PROXY:-}"`})
	w2info, err := c.ContainerInspect(ctx, w2)
	if err != nil {
		t.Fatal(err)
	}
	w2AuthEndpoint, _ := envValue(w2info.Config.Env, "CODEX_AUTH_PROXY_URL")
	if w2AuthEndpoint == "" {
		t.Fatal("second worker has no pinned auth endpoint")
	}
	kataExec(t, ctx, c, w2, false, []string{"curl", "-fsS", "--noproxy", "*", w2AuthEndpoint + "/health"})
	for i := 0; i < 50; i++ {
		if _, err := kataExecResult(ctx, c, w2, false, []string{"curl", "-fsS", "--noproxy", "*", "http://127.0.0.1:8089/"}); err == nil {
			break
		}
		if i == 49 {
			t.Fatal("second worker listener not ready")
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, ep := range w2info.NetworkSettings.Networks {
		deny(w1, "http://"+ep.IPAddress+":8089/")
	}
	w1info, err = c.ContainerInspect(ctx, w1)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range w1info.HostConfig.ExtraHosts {
		if strings.HasPrefix(host, httpName+":") {
			deny(w2, "http://"+strings.TrimPrefix(host, httpName+":")+":18082/health")
		}
	}
	secondBoot := strings.TrimSpace(kataExec(t, ctx, c, w2, false, []string{"cat", "/proc/sys/kernel/random/boot_id"}))
	if secondBoot == hostBootID || secondBoot == guestBootID {
		t.Fatal("parallel workers do not have separate guest kernels")
	}
	if err := m.VMStop(ctx, prefix+"-worker", 5, false); err != nil {
		t.Fatal(err)
	}
	stopped, err := m.VMInspect(ctx, w1)
	if err != nil || stopped.Populated || stopped.State != "exited" {
		t.Fatalf("stopped VM inventory: %+v %v", stopped, err)
	}
	if requestDir := os.Getenv("CODEX_DOCK_NODE_RESTART_REQUEST_DIR"); requestDir != "" {
		if err := os.Remove(filepath.Join(requestDir, "complete")); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(requestDir, "request"), []byte("namespace "+w1+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 600; i++ {
			if _, err := os.Stat(filepath.Join(requestDir, "complete")); err == nil {
				break
			}
			if i == 599 {
				t.Fatal("namespace recovery harness timed out")
			}
			time.Sleep(100 * time.Millisecond)
		}
		// /run/netns belongs to root. Observe through the trusted fault
		// supervisor rather than weakening permissions for the test operator.
		evidence, err := os.ReadFile(filepath.Join(requestDir, "complete"))
		if err != nil {
			t.Fatal(err)
		}
		if err := validateNamespaceReplacement(evidence, w1); err != nil {
			t.Fatal(err)
		}
		t.Log("verified: stopped worker namespace loss is reconstructed with a new inode from durable endpoint ownership")
	}
	if err := m.VMStart(ctx, prefix+"-worker"); err != nil {
		t.Fatal("retained worker restart:", err)
	}
	ready(w1, "python3")
	if err := m.VMRemove(ctx, w1, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("removed detached worker retained its worktree")
	}
	// Simulate a worker deleted outside this product. Its durable host record
	// must remain visible and recoverable without destroying retained workers.
	timeout := 5
	if err := c.ContainerStop(ctx, w2, container.StopOptions{Timeout: &timeout}); err != nil {
		t.Fatal(err)
	}
	if removed, err := m.VMPrune(ctx); err != nil || len(removed) != 0 {
		t.Fatalf("prune touched a retained worker: %v %v", removed, err)
	}
	if err := c.ContainerRemove(ctx, w2, container.RemoveOptions{}); err != nil {
		t.Fatal(err)
	}
	orphan, err := m.VMInspect(ctx, prefix+"-readonly")
	if err != nil || orphan.State != "orphan-empty" || !orphan.Orphan || !orphan.Tracked {
		t.Fatalf("lost orphan VM identity: %+v %v", orphan, err)
	}
	if err := m.VMRemove(ctx, orphan.Allocation, false); err != nil {
		t.Fatal("orphan envelope recovery:", err)
	}
	if _, err := m.VMInspect(ctx, orphan.Allocation); err == nil {
		t.Fatal("recovered orphan remains in VM inventory")
	}
	if err := m.revokeWorkerTokenChecked(w2info); err != nil {
		t.Fatal(err)
	}
	if err := m.network.RemoveKataWorkerNetwork(ctx, w2info.Config.Labels[labelPrefix+"network-id"], m.proxyContainerNames(true)); err != nil {
		t.Fatal(err)
	}
	t.Log("verified: vm stop/start/rm, stopped-worker prune protection, orphan discovery and exact allocation recovery")
	t.Log("PASS: guest kernel isolation, real proxy/token setup, CLI binaries, workspace/Git/extra mounts, PTY, parallel network separation, host/admin denial, no-internet and stop/restart/removal. External AI inference and interactive keyboard shortcuts are separate manual checks.")
}

func validateNamespaceReplacement(data []byte, cid string) error {
	type namespaceIdentity struct {
		Device uint64 `json:"device"`
		Inode  uint64 `json:"inode"`
	}
	var proof struct {
		Operation   string            `json:"operation"`
		ContainerID string            `json:"container_id"`
		Removed     bool              `json:"removed"`
		Before      namespaceIdentity `json:"before"`
		After       namespaceIdentity `json:"after"`
	}
	if err := json.Unmarshal(data, &proof); err != nil {
		return fmt.Errorf("namespace supervisor must return replacement evidence: %w", err)
	}
	if proof.Operation != "namespace" || proof.ContainerID != cid || !proof.Removed || proof.Before.Device == 0 || proof.Before.Inode == 0 || proof.After.Device == 0 || proof.After.Inode == 0 || proof.Before == proof.After {
		return fmt.Errorf("namespace supervisor did not attest removal and a different inode for the exact stopped worker")
	}
	return nil
}

func TestNamespaceFaultHarnessCannotReportANoopAsRecovery(t *testing.T) {
	valid := `{"operation":"namespace","container_id":"owned","removed":true,"before":{"device":5,"inode":77},"after":{"device":5,"inode":88}}`
	if err := validateNamespaceReplacement([]byte(valid), "owned"); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"ready\n", `{}`, strings.Replace(valid, `"inode":77`, `"inode":88`, 1), strings.Replace(valid, `"removed":true`, `"removed":false`, 1), strings.Replace(valid, `"container_id":"owned"`, `"container_id":"other"`, 1)} {
		if err := validateNamespaceReplacement([]byte(data), "owned"); err == nil {
			t.Fatal("incomplete or unrelated namespace fault evidence was accepted")
		}
	}
}

func kataExec(t *testing.T, ctx context.Context, c *client.Client, id string, tty bool, args []string) string {
	t.Helper()
	out, err := kataExecResult(ctx, c, id, tty, args)
	if err != nil {
		t.Fatalf("acceptance command %v: %v: %s", args, err, out)
	}
	return out
}

func kataExecResult(ctx context.Context, c *client.Client, id string, tty bool, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	r, err := c.ContainerExecCreate(ctx, id, container.ExecOptions{Cmd: args, AttachStdout: true, AttachStderr: true, Tty: tty})
	if err != nil {
		return "", err
	}
	stream, err := c.ContainerExecAttach(ctx, r.ID, container.ExecAttachOptions{Tty: tty})
	if err != nil {
		return "", err
	}
	defer stream.Close()
	stop := context.AfterFunc(ctx, stream.Close)
	defer stop()
	var out bytes.Buffer
	if tty {
		_, err = io.Copy(&out, stream.Reader)
	} else {
		_, err = stdcopy.StdCopy(&out, &out, stream.Reader)
	}
	if err != nil {
		return out.String(), err
	}
	state, err := c.ContainerExecInspect(ctx, r.ID)
	if err != nil {
		return out.String(), err
	}
	if state.ExitCode != 0 {
		return out.String(), fmt.Errorf("exit %d", state.ExitCode)
	}
	return out.String(), nil
}
