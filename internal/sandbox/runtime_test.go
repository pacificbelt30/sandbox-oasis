package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/containerd/errdefs"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/system"
	"github.com/docker/docker/client"
	"github.com/pacificbelt30/codex-dock/internal/kataguard"
)

func TestRuntimeSelectionFailsClosed(t *testing.T) {
	mode, registration, err := ResolveRuntime("", "")
	if err != nil || mode != RuntimeKata || registration != DefaultKataRuntime {
		t.Fatalf("default: %s %s %v", mode, registration, err)
	}
	for _, bad := range []RunOptions{{Runtime: "auto"}, {KataRuntime: "runc"}, {KataRuntime: "../kata"}, {KataRuntime: "kata\n"}} {
		if _, _, err := ResolveRuntime(bad.Runtime, bad.KataRuntime); err == nil {
			t.Fatalf("accepted invalid selection: %#v", bad)
		}
	}
	info := system.Info{OSType: "linux", Runtimes: map[string]system.RuntimeWithStatus{"runc": {}}}
	if err := validateRuntime(info, RuntimeKata, DefaultKataRuntime); err == nil {
		t.Fatal("missing Kata accepted")
	}
	info.Runtimes["kata"] = system.RuntimeWithStatus{Runtime: system.Runtime{Path: "/usr/bin/runc"}}
	if err := validateRuntime(info, RuntimeKata, "kata"); err == nil {
		t.Fatal("runc disguised as Kata accepted")
	}
	info.Runtimes["kata"] = system.RuntimeWithStatus{Runtime: system.Runtime{Type: "/opt/kata/bin/containerd-shim-kata-v2"}}
	if err := validateRuntime(info, RuntimeKata, "kata"); err != nil {
		t.Fatal(err)
	}
	info.OSType = "windows"
	if err := validateRuntime(info, RuntimeKata, "kata"); err == nil {
		t.Fatal("non-Linux daemon accepted")
	}
}

func TestSmallKataProfileSetsAndVerifiesWorkerMemory(t *testing.T) {
	f, m := newEngineFixture(t)
	id, err := m.Run(RunOptions{Name: "small-worker", ProjectDir: t.TempDir(), Runtime: RuntimeKata, ResourceProfile: "small", Detach: true})
	if err != nil {
		t.Fatal(err)
	}
	if f.worker.Config.Labels[labelPrefix+"resource-profile"] != "small" || f.worker.HostConfig.Memory != kataguard.SmallGuestMemory || f.worker.HostConfig.MemorySwap != kataguard.SmallGuestMemory {
		t.Fatalf("small VM size was not applied: %+v", f.worker.HostConfig.Resources)
	}
	if err := m.validateKataWorker(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	f.worker.HostConfig.Memory = kataguard.GuestMemory
	if err := m.validateKataWorker(context.Background(), id); err == nil {
		t.Fatal("changed VM memory passed validation")
	}
}

func TestCreateOnlyDefersGuestExecutionUntilStart(t *testing.T) {
	f, m := newEngineFixture(t)
	id, err := m.RunContext(context.Background(), RunOptions{Name: "prepared", ProjectDir: t.TempDir(), Runtime: RuntimeKata, Detach: true, CreateOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if f.starts != 0 {
		t.Fatal("create started user code")
	}
	if err := m.StartContext(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if f.starts != 1 {
		t.Fatalf("start count %d", f.starts)
	}
}

// engineFixture exercises the actual Docker SDK requests and manager lifecycle,
// including cleanup. It makes no assertions about a real VM having booted.
type engineFixture struct {
	t                                                  *testing.T
	front                                              *dockernetwork.Inspect
	worker                                             *container.InspectResponse
	starts, removed, networksRemoved                   int
	missingKata, startError, wrongRuntime, removeError bool
	networkRemoveError                                 bool
	removeReplyLost                                    bool
	removeNotFound                                     int
}

func newEngineFixture(t *testing.T) (*engineFixture, *Manager) {
	t.Helper()
	f := &engineFixture{t: t}
	s := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(s.Close)
	c, err := client.NewClientWithOpts(client.WithHost(s.URL), client.WithVersion("1.51"), client.WithHTTPClient(s.Client()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	m := &Manager{cli: c, stateDir: t.TempDir(), guard: &fakeKataNode{}}
	if err := os.Chmod(m.stateDir, 0700); err != nil {
		t.Fatal(err)
	}
	m.execProbe = func(ctx context.Context, id string, args []string) ([]byte, error) {
		if args[1] == "release" {
			return nil, nil
		}
		uid, gid := "1000", "1000"
		parts := strings.Split(f.worker.Config.User, ":")
		if _, err := strconv.Atoi(parts[0]); err == nil {
			uid = parts[0]
		}
		if len(parts) > 1 {
			gid = parts[1]
		}
		status := "Uid:\t" + strings.Repeat(uid+" ", 4) + "\nGid:\t" + strings.Repeat(gid+" ", 4) + "\nNoNewPrivs:\t1\nSeccomp:\t2\n"
		for _, key := range []string{"CapInh", "CapPrm", "CapEff", "CapBnd", "CapAmb"} {
			status += key + ":\t0000000000000000\n"
		}
		profile, err := m.resourceProfile(ctx, f.worker.Config.Labels[labelPrefix+"resource-profile"])
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"boot_id": "fixture-guest-kernel", "status": status, "cmdline": guestGuard + "\x00wait\x00" + f.worker.Config.Labels[labelAllocation] + "\x00", "pids": strconv.FormatInt(profile.PIDLimit, 10), "memory": strconv.FormatInt(profile.GuestMemoryBytes, 10), "cpu": profile.CPUQuota(), "nofile": fmt.Sprintf("%d %d", profile.FileDescriptorLimit, profile.FileDescriptorLimit), "init_nofile": fmt.Sprintf("%d %d", profile.FileDescriptorLimit, profile.FileDescriptorLimit)})
	}
	return f, m
}

type fakeKataNode struct{ profiles []kataguard.ResourceProfile }

func (f *fakeKataNode) Profiles(context.Context) ([]kataguard.ResourceProfile, error) {
	return append(kataguard.ResourceProfiles(), f.profiles...), nil
}

func (*fakeKataNode) Validate(context.Context, string) error { return nil }
func (*fakeKataNode) Reserve(_ context.Context, id string) (string, error) {
	return "/codex-dock/cdx-" + id, nil
}
func (f *fakeKataNode) ReserveProfile(ctx context.Context, id, profile string) (string, error) {
	profiles, _ := f.Profiles(ctx)
	for _, p := range profiles {
		if p.Name == profile {
			return f.Reserve(ctx, id)
		}
	}
	return "", fmt.Errorf("invalid profile")
}
func (*fakeKataNode) Verify(context.Context, string, string) error  { return nil }
func (*fakeKataNode) Release(context.Context, string, string) error { return nil }
func (*fakeKataNode) ProtectedPaths() []string                      { return nil }
func (*fakeKataNode) Launcher() string                              { return "/trusted/worker-guard" }
func (*fakeKataNode) Parent(id string) (string, error)              { return "/codex-dock/cdx-" + id, nil }
func (*fakeKataNode) Stamp() string                                 { return "fixture-profile" }

func (f *engineFixture) serve(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	if strings.HasPrefix(p, "/v1.51/") {
		p = strings.TrimPrefix(p, "/v1.51")
	}
	w.Header().Set("Content-Type", "application/json")
	respond := func(v any) {
		if err := json.NewEncoder(w).Encode(v); err != nil {
			f.t.Error(err)
		}
	}
	notFound := func() { w.WriteHeader(404); respond(map[string]string{"message": "not found"}) }
	switch {
	case p == "/info":
		runtimes := map[string]system.RuntimeWithStatus{"runc": {}}
		if !f.missingKata {
			runtimes[DefaultKataRuntime] = system.RuntimeWithStatus{Runtime: system.Runtime{Type: DefaultKataRuntime}}
		}
		respond(system.Info{DriverStatus: [][2]string{{"cxd.worker-policy", "1"}, {"cxd.exec-options", "1"}, {"cxd.jobs", "1"}}, OSType: "linux", Runtimes: runtimes, CgroupDriver: "cgroupfs", CgroupVersion: "2"})
	case strings.HasPrefix(p, "/images/"):
		respond(map[string]any{"Config": map[string]any{"Entrypoint": []string{"/entrypoint.sh"}}})
	case p == "/networks/create":
		var req dockernetwork.CreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			f.t.Error(err)
		}
		f.front = &dockernetwork.Inspect{ID: "front-id", Name: req.Name, Driver: req.Driver, Internal: req.Internal,
			Options: req.Options, Labels: req.Labels, Containers: map[string]dockernetwork.EndpointResource{}}
		w.WriteHeader(201)
		respond(map[string]string{"Id": f.front.ID})
	case strings.HasPrefix(p, "/networks/") && strings.HasSuffix(p, "/connect"):
		var req struct{ Container string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			f.t.Error(err)
		}
		ip := "172.25.0.2/16"
		if req.Container == "egress" {
			ip = "172.25.0.3/16"
		}
		f.front.Containers[req.Container+"-id"] = dockernetwork.EndpointResource{Name: req.Container, IPv4Address: ip}
		w.WriteHeader(200)
	case strings.HasPrefix(p, "/networks/") && strings.HasSuffix(p, "/disconnect"):
		var req struct{ Container string }
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			f.t.Error(err)
		}
		delete(f.front.Containers, req.Container)
		w.WriteHeader(200)
	case strings.HasPrefix(p, "/networks/") && r.Method == http.MethodDelete:
		if f.networkRemoveError {
			w.WriteHeader(500)
			respond(map[string]string{"message": "fixture network failure"})
			return
		}
		f.front = nil
		f.networksRemoved++
		w.WriteHeader(204)
	case strings.HasPrefix(p, "/networks/"):
		if f.front == nil {
			notFound()
		} else {
			respond(f.front)
		}
	case p == "/containers/create":
		var req struct {
			container.Config
			HostConfig *container.HostConfig
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			f.t.Error(err)
		}
		id := strings.Repeat("a", 64)
		f.worker = &container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: id, Name: "/" + r.URL.Query().Get("name"), HostConfig: req.HostConfig,
			State: &container.State{Status: "created"}}, Config: &req.Config,
			NetworkSettings: &container.NetworkSettings{Networks: map[string]*dockernetwork.EndpointSettings{}}}
		if f.front != nil {
			f.worker.NetworkSettings.Networks[f.front.Name] = &dockernetwork.EndpointSettings{}
			f.front.Containers[id] = dockernetwork.EndpointResource{Name: r.URL.Query().Get("name"), IPv4Address: "172.25.0.4/16"}
		}
		w.WriteHeader(201)
		respond(map[string]string{"Id": id})
	case strings.HasPrefix(p, "/containers/") && strings.HasSuffix(p, "/json"):
		name := strings.TrimSuffix(strings.TrimPrefix(p, "/containers/"), "/json")
		if name == "auth" || name == "egress" {
			respond(container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: name + "-id", State: &container.State{Running: true}}})
		} else if f.worker != nil {
			respond(f.worker)
		} else {
			notFound()
		}
	case p == "/containers/json":
		respond([]container.Summary{})
	case strings.HasSuffix(p, "/start"):
		f.starts++
		if f.startError {
			w.WriteHeader(500)
			respond(map[string]string{"message": "KVM unavailable"})
			return
		}
		f.worker.State.Running = true
		if f.front != nil {
			f.worker.NetworkSettings.Networks[f.front.Name].NetworkID = f.front.ID
		}
		if f.wrongRuntime {
			f.worker.HostConfig.Runtime = "runc"
		}
		w.WriteHeader(204)
	case strings.HasSuffix(p, "/stop"):
		f.worker.State.Running = false
		w.WriteHeader(204)
	case strings.HasPrefix(p, "/containers/") && r.Method == http.MethodDelete:
		if f.worker == nil {
			notFound()
			return
		}
		if f.removeNotFound > 0 {
			f.removeNotFound--
			notFound()
			return
		}
		if f.removeError {
			w.WriteHeader(500)
			respond(map[string]string{"message": "cannot reap VM"})
			return
		}
		f.removed++
		if f.front != nil {
			delete(f.front.Containers, f.worker.ID)
		}
		f.worker = nil
		if f.removeReplyLost {
			w.WriteHeader(500)
			respond(map[string]string{"message": "fixture removal response lost"})
			return
		}
		w.WriteHeader(204)
	default:
		f.t.Errorf("unexpected Docker API request: %s %s", r.Method, p)
		w.WriteHeader(500)
	}
}

type runtimeProxy struct {
	fakeProxy
	revoked  []string
	restored []string
}

func (p *runtimeProxy) RevokeToken(name string) { p.revoked = append(p.revoked, name) }

func (p *runtimeProxy) RestoreToken(name, token string, ttl int) error {
	p.restored = append(p.restored, name)
	return nil
}

func TestKataRunKeepsAgentAndProxyFeatures(t *testing.T) {
	for _, tc := range []struct {
		agent             Agent
		noInternet, shell bool
	}{
		{AgentCodex, false, false}, {AgentClaude, false, false}, {AgentNone, false, false}, {AgentNone, true, false}, {AgentNone, false, true},
	} {
		t.Run(fmt.Sprintf("%s/noInternet=%t/shell=%t", tc.agent, tc.noInternet, tc.shell), func(t *testing.T) {
			f, m := newEngineFixture(t)
			p := &runtimeProxy{fakeProxy: fakeProxy{endpoint: "http://auth:18080", anthropic: true, oauth: true}}
			m.proxy = p
			m.httpProxyURL = "http://egress:18082"
			opts := RunOptions{Name: "worker", Image: "custom-template:v1", ProjectDir: t.TempDir(), Detach: true, Runtime: RuntimeKata,
				Agent: tc.agent, NoInternet: tc.noInternet, ShellMode: tc.shell, ShellType: ShellZsh, ContainerUser: "1234:5678",
				ApprovalMode: ApprovalModeFullAuto, ReadOnly: true, Packages: []string{"npm:example"}, Model: "test-model", Task: "test task"}
			id, err := m.Run(opts)
			if err != nil {
				t.Fatal(err)
			}
			w := f.worker
			if id != w.ID || w.HostConfig.Runtime != DefaultKataRuntime || w.Config.User != opts.ContainerUser || w.Config.Image != opts.Image {
				t.Fatal("worker configuration was lost")
			}
			if !w.HostConfig.Mounts[0].ReadOnly || w.Config.Tty {
				t.Fatal("read-only/detach options lost")
			}
			if tc.shell && !slices.Equal([]string(w.Config.Cmd), []string{"/bin/zsh"}) {
				t.Fatal("debug shell lost")
			}
			if !slices.Contains(w.HostConfig.ExtraHosts, "auth:172.25.0.2") {
				t.Fatal("auth hostname was not pinned")
			}
			if got := slices.Contains(w.HostConfig.ExtraHosts, "egress:172.25.0.3"); got == tc.noInternet {
				t.Fatal("egress isolation incorrect")
			}
			if _, ok := f.front.Containers["egress-id"]; ok == tc.noInternet {
				t.Fatal("no-internet connected wrong proxy")
			}
			if v, _ := envValue(w.Config.Env, "CODEX_INSTALL_SCRIPT"); v == "" {
				t.Fatal("package installation lost")
			}
			if v, _ := envValue(w.Config.Env, "CODEX_TASK"); v != opts.Task {
				t.Fatal("task lost")
			}
			if len(p.revoked) != 0 {
				t.Fatal("successful worker token revoked")
			}
			if err := m.Remove(id, true); err != nil {
				t.Fatal(err)
			}
			if f.networksRemoved != 1 || f.removed != 1 {
				t.Fatal("worker/network leaked")
			}
		})
	}
}

func TestKataMissingRuntimeHasNoSideEffects(t *testing.T) {
	f, m := newEngineFixture(t)
	f.missingKata = true
	p := &runtimeProxy{fakeProxy: fakeProxy{endpoint: "http://auth:18080"}}
	m.proxy = p
	if _, err := m.Run(RunOptions{Name: "worker", ProjectDir: t.TempDir(), Detach: true}); err == nil {
		t.Fatal("missing runtime accepted")
	}
	if f.front != nil || f.worker != nil || len(p.issued) != 0 || len(p.revoked) != 0 {
		t.Fatal("failed preflight had side effects")
	}
}

func TestKataFailedStartCleansOnlyOwnedResources(t *testing.T) {
	for _, wrongRuntime := range []bool{false, true} {
		t.Run(fmt.Sprint(wrongRuntime), func(t *testing.T) {
			f, m := newEngineFixture(t)
			f.startError = !wrongRuntime
			f.wrongRuntime = wrongRuntime
			p := &runtimeProxy{fakeProxy: fakeProxy{endpoint: "http://auth:18080"}}
			m.proxy = p
			if _, err := m.Run(RunOptions{Name: "worker", ProjectDir: t.TempDir(), Detach: true}); err == nil {
				t.Fatal("failed/incorrect runtime accepted")
			}
			if f.removed != 1 || f.networksRemoved != 1 || !slices.Equal(p.revoked, []string{"worker"}) {
				t.Fatal("failed launch resources/token leaked")
			}
		})
	}
}

func TestKataCleanupFailureIsReportedAndNetworkRetained(t *testing.T) {
	f, m := newEngineFixture(t)
	f.startError = true
	f.removeError = true
	_, err := m.Run(RunOptions{Name: "worker", ProjectDir: t.TempDir(), Detach: true})
	if err == nil || !strings.Contains(err.Error(), "cannot reap VM") {
		t.Fatalf("cleanup failure lost: %v", err)
	}
	if f.front == nil || f.networksRemoved != 0 {
		t.Fatal("network detached while VM might still exist")
	}
}

func TestKataRestartRejectsChangedProxyAddress(t *testing.T) {
	f, m := newEngineFixture(t)
	m.proxy = &runtimeProxy{fakeProxy: fakeProxy{endpoint: "http://auth:18080"}}
	id, err := m.Run(RunOptions{Name: "worker", ProjectDir: t.TempDir(), Detach: true})
	if err != nil {
		t.Fatal(err)
	}
	f.worker.State.Running = false
	f.front.Containers["auth-id"] = dockernetwork.EndpointResource{Name: "auth", IPv4Address: "172.25.0.99/16"}
	if err := m.Start(id); err == nil || !strings.Contains(err.Error(), "addresses changed") {
		t.Fatalf("changed proxy accepted: %v", err)
	}
	if f.starts != 1 {
		t.Fatal("restart dispatched despite invalid network")
	}
}

func TestKataStopRestartRestoresOnlyPlaceholder(t *testing.T) {
	f, m := newEngineFixture(t)
	p := &runtimeProxy{fakeProxy: fakeProxy{endpoint: "http://auth:18080"}}
	m.proxy = p
	id, err := m.Run(RunOptions{Name: "worker", ProjectDir: t.TempDir(), Detach: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(id, 5); err != nil {
		t.Fatal(err)
	}
	if f.worker.State.Running || !slices.Equal(p.revoked, []string{"worker"}) {
		t.Fatal("stop did not stop worker and revoke its token")
	}
	if err := m.Start(id); err != nil {
		t.Fatal(err)
	}
	if !f.worker.State.Running || !slices.Equal(p.restored, []string{"worker", "worker"}) {
		t.Fatal("restart did not restore the worker token")
	}
	if len(p.issued) != 0 {
		t.Fatal("restart issued an unusable new environment token")
	}
}

func TestExplicitRuncDoesNotInheritDaemonDefault(t *testing.T) {
	f, m := newEngineFixture(t)
	if _, err := m.Run(RunOptions{Runtime: RuntimeRunc, Name: "worker", ProjectDir: t.TempDir(), Detach: true}); err != nil {
		t.Fatal(err)
	}
	if f.worker.HostConfig.Runtime != "runc" {
		t.Fatal("explicit compatibility runtime was not set")
	}
}

func TestKataRejectsUnexpectedNetworkPeer(t *testing.T) {
	f, m := newEngineFixture(t)
	id, err := m.Run(RunOptions{Name: "worker", ProjectDir: t.TempDir(), Detach: true})
	if err != nil {
		t.Fatal(err)
	}
	f.front.Containers["other-worker"] = dockernetwork.EndpointResource{Name: "other"}
	if err := m.validateKataWorker(context.Background(), id); err == nil {
		t.Fatal("unexpected peer accepted")
	}
	if err := m.Remove(id, true); err == nil {
		t.Fatal("unexpected peer silently disconnected during cleanup")
	}
	if f.front == nil || f.networksRemoved != 0 {
		t.Fatal("another worker's network removed")
	}
}

func TestRemovalConfirmsContainerAbsenceAfterNestedNotFound(t *testing.T) {
	for _, failures := range []int{1, 3} {
		t.Run(fmt.Sprint(failures), func(t *testing.T) {
			f, m := newEngineFixture(t)
			id, err := m.Run(RunOptions{Name: "delete-retry", ProjectDir: t.TempDir(), Detach: true})
			if err != nil {
				t.Fatal(err)
			}
			f.removeNotFound = failures
			err = m.Remove(id, true)
			if failures == 1 {
				if err != nil || f.worker != nil || f.front != nil {
					t.Fatalf("transient deletion not recovered: %v", err)
				}
			} else {
				if err == nil || f.worker == nil || f.front == nil || f.networksRemoved != 0 {
					t.Fatalf("unconfirmed removal discarded ownership: %v", err)
				}
				if errdefs.IsNotFound(err) {
					t.Fatal("retained worker reported as absent")
				}
				if err := m.Remove(id, true); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestOperatorProfileAppliesAndAttestsCPUAndPIDLimits(t *testing.T) {
	f, m := newEngineFixture(t)
	p := kataguard.ResourceProfile{Name: "batch", CPUCores: 2, GuestMemoryBytes: kataguard.CompactGuestMemory, HostMemoryBytes: kataguard.CompactHostMemory, PIDLimit: 256, FileDescriptorLimit: 512}
	m.guard = &fakeKataNode{profiles: []kataguard.ResourceProfile{p}}
	id, err := m.Run(RunOptions{Name: "custom-budget", ProjectDir: t.TempDir(), ResourceProfile: p.Name, Detach: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := m.Remove(id, true); err != nil {
			t.Error(err)
		}
	}()
	h := f.worker.HostConfig
	if h.NanoCPUs != 2_000_000_000 || h.PidsLimit == nil || *h.PidsLimit != 256 || h.Memory != p.GuestMemoryBytes {
		t.Fatal("custom resource budget was not applied")
	}
	if err := m.validateKataWorker(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if len(h.Ulimits) != 1 || h.Ulimits[0].Soft != 512 || h.Ulimits[0].Hard != 512 {
		t.Fatal("nofile not applied")
	}
	h.Ulimits[0].Hard = 513
	if err := m.validateKataWorker(context.Background(), id); err == nil {
		t.Fatal("changed nofile accepted")
	}
	h.Ulimits[0].Hard = 512
	h.NanoCPUs = 1_000_000_000
	if err := m.validateKataWorker(context.Background(), id); err == nil {
		t.Fatal("changed CPU budget passed attestation")
	}
	h.NanoCPUs = 2_000_000_000
	*h.PidsLimit = 512
	if err := m.validateKataWorker(context.Background(), id); err == nil {
		t.Fatal("changed PID budget passed attestation")
	}
}
