package kataguard

import (
	"context"
	"encoding/json"
	"github.com/pacificbelt30/codex-dock/internal/buildinfo"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestCombinedVerificationKeepsAttestationAndLegacyFallback(t *testing.T) {
	originalVersion := buildinfo.Version
	buildinfo.Version = "fixture-combined"
	t.Cleanup(func() { buildinfo.Version = originalVersion })
	for _, modern := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy", true: "combined"}[modern], func(t *testing.T) {
			const endpoint = "unix:///run/fixture.sock"
			info := NodeInfo{BuildVersion: buildinfo.Version, ProtocolVersion: 2, Profile: Profile, Endpoint: endpoint, Stamp: strings.Repeat("a", 64), Launcher: "/opt/fixture/guard", Root: "/sys/fs/cgroup/fixture", SharedFSCache: "none"}
			if modern {
				info.Capabilities = []string{"verify-runtime-v1"}
			}
			var actions []string
			failure := false
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req nodeRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				actions = append(actions, req.Action)
				if req.Action == "verify-runtime" && (req.Runtime != "io.containerd.kata.v2" || req.Allocation != "allocation" || req.Worker != "worker") {
					t.Error("attestation identity lost")
				}
				if failure {
					w.WriteHeader(409)
					_ = json.NewEncoder(w).Encode(nodeReply{Error: "changed asset"})
					return
				}
				_ = json.NewEncoder(w).Encode(nodeReply{Info: info})
			}))
			defer s.Close()
			c := &Client{client: s.Client(), endpoint: endpoint, info: info}
			c.client.Transport = &fixtureNodeTransport{target: s.URL, next: s.Client().Transport}
			for range 2 {
				if err := c.VerifyRuntime(context.Background(), "io.containerd.kata.v2", "allocation", "worker"); err != nil {
					t.Fatal(err)
				}
			}
			expected := []string{"validate", "verify", "validate", "verify"}
			if modern {
				expected = []string{"verify-runtime", "verify-runtime"}
			}
			if !reflect.DeepEqual(actions, expected) {
				t.Fatalf("requests=%v", actions)
			}
			if modern {
				info.Stamp = strings.Repeat("b", 64)
				if err := c.VerifyRuntime(context.Background(), "io.containerd.kata.v2", "allocation", "worker"); err == nil {
					t.Fatal("changed manifest accepted")
				}
				info.Stamp = strings.Repeat("a", 64)
				info.BuildVersion = "different-build"
				if err := c.VerifyRuntime(context.Background(), "io.containerd.kata.v2", "allocation", "worker"); err == nil {
					t.Fatal("different node version accepted")
				}
				info.BuildVersion = buildinfo.Version
				info.Endpoint = "unix:///run/another.sock"
				if err := c.VerifyRuntime(context.Background(), "io.containerd.kata.v2", "allocation", "worker"); err == nil {
					t.Fatal("different daemon endpoint accepted")
				}
				info.Endpoint = endpoint
				info.SharedFSCache = "always"
				if err := c.VerifyRuntime(context.Background(), "io.containerd.kata.v2", "allocation", "worker"); err == nil {
					t.Fatal("incoherent shared filesystem accepted")
				}
			}
			failure = true
			before := len(actions)
			if err := c.VerifyRuntime(context.Background(), "io.containerd.kata.v2", "allocation", "worker"); err == nil {
				t.Fatal("failed validation accepted")
			}
			if len(actions) != before+1 {
				t.Fatal("failed combined/legacy validation retried via another path")
			}
		})
	}
}

func TestFreshClientCombinedVerification(t *testing.T) {
	for _, mode := range []string{"current", "legacy", "validation-failure", "transport-reply-failure"} {
		t.Run(mode, func(t *testing.T) {
			const endpoint = "unix:///run/fixture.sock"
			var actions []string
			info := NodeInfo{BuildVersion: buildinfo.Version, ProtocolVersion: 2, Profile: Profile, Endpoint: endpoint, Stamp: strings.Repeat("a", 64), Launcher: "/opt/fixture/guard", Root: "/sys/fs/cgroup/fixture", SharedFSCache: "none", Capabilities: []string{"verify-runtime-v1"}}
			if mode == "legacy" {
				info.Capabilities = nil
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req nodeRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				actions = append(actions, req.Action)
				switch {
				case req.Action == "verify-runtime" && mode == "legacy":
					w.WriteHeader(409)
					_ = json.NewEncoder(w).Encode(nodeReply{Error: "unknown node action"})
				case mode == "validation-failure":
					w.WriteHeader(409)
					_ = json.NewEncoder(w).Encode(nodeReply{Error: "changed asset"})
				case mode == "transport-reply-failure":
					w.WriteHeader(500)
					_, _ = w.Write([]byte("invalid response"))
				default:
					_ = json.NewEncoder(w).Encode(nodeReply{Info: info})
				}
			}))
			defer server.Close()
			c := &Client{client: server.Client(), endpoint: endpoint}
			c.client.Transport = &fixtureNodeTransport{target: server.URL, next: server.Client().Transport}
			err := c.VerifyRuntime(context.Background(), "io.containerd.kata.v2", "allocation", "worker")
			expected := []string{"verify-runtime"}
			if mode == "legacy" {
				expected = append(expected, "validate", "verify")
			}
			if !reflect.DeepEqual(actions, expected) {
				t.Fatalf("actions=%v", actions)
			}
			failure := strings.Contains(mode, "failure")
			if (err != nil) != failure || (!failure && c.Stamp() != info.Stamp) || (failure && c.Stamp() != "") {
				t.Fatalf("fresh attestation: stamp=%q error=%v", c.Stamp(), err)
			}
		})
	}
}
