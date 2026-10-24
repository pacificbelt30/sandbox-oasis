package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
)

func TestPolicyQuarantineRejectsMissingWorkerOrPolicyOverrides(t *testing.T) {
	for _, args := range [][]string{{"quarantine"}, {"revoke"}, {"quarantine", "--worker", "target", "--preset", "offline"}, {"quarantine", "--worker", "target", "--allow-domain", "example.com"}} {
		command := newPolicyCommand()
		command.SetOut(io.Discard)
		command.SetErr(io.Discard)
		command.SetArgs(args)
		if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "quarantine requires --worker") {
			t.Fatalf("invalid quarantine arguments accepted: %v %v", args, err)
		}
	}
}

func TestPolicyCommandsNeverReportUnconfirmedStopAsBlocked(t *testing.T) {
	for _, state := range []*container.State{nil, {Running: true, Status: "running"}, {Paused: true, Status: "paused"}, {Status: "restarting"}} {
		name := "unknown"
		if state != nil {
			name = state.Status
		}
		t.Run(name, func(t *testing.T) {
			// Keep the socket short enough for Unix-domain limits on macOS.
			dir, err := os.MkdirTemp("", "policy-cli-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			socket := filepath.Join(dir, "node.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/containers/worker/json") {
					t.Errorf("unexpected runtime request: %s", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				_ = json.NewEncoder(w).Encode(container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: "worker", State: state}, Config: &container.Config{Labels: map[string]string{"codex-dock.managed": "true", "codex-dock.quarantined": "true"}}})
			}))
			_ = server.Listener.Close()
			server.Listener = listener
			server.Start()
			defer server.Close()
			t.Setenv("CODEX_DOCK_KATA_NODE_SOCKET", socket)
			for _, action := range []string{"show", "check"} {
				var output bytes.Buffer
				command := newPolicyCommand()
				command.SetOut(&output)
				command.SetArgs([]string{action, "--worker", "worker"})
				if err := command.Execute(); err != nil || !strings.Contains(output.String(), "QUARANTINE PENDING") || strings.Contains(output.String(), "all execution and network access stopped") {
					t.Fatalf("%s misreported stop: %s %v", action, &output, err)
				}
			}
			var output bytes.Buffer
			command := newPolicyCommand()
			command.SetOut(&output)
			command.SetErr(io.Discard)
			command.SetArgs([]string{"explain", "example.com:443", "--worker", "worker", "--json"})
			if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "stop not confirmed") || strings.Contains(output.String(), "worker_quarantined") {
				t.Fatalf("explain claimed completed denial: %s %v", &output, err)
			}
			output.Reset()
			command = newPolicyCommand()
			command.SetOut(&output)
			command.SetArgs([]string{"show", "--worker", "worker", "--json"})
			if err := command.Execute(); err != nil {
				t.Fatal(err)
			}
			var status struct {
				Pending bool `json:"quarantine_pending"`
			}
			if err := json.Unmarshal(output.Bytes(), &status); err != nil || !status.Pending {
				t.Fatalf("JSON omitted pending stop: %s %v", &output, err)
			}
		})
	}
}
