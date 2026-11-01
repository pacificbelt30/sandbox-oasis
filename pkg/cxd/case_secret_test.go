package cxd

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	dn "github.com/docker/docker/api/types/network"
	dockerimage "github.com/moby/docker-image-spec/specs-go/v1"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pacificbelt30/codex-dock/internal/buildinfo"
	"github.com/pacificbelt30/codex-dock/internal/network"
	"github.com/pacificbelt30/codex-dock/internal/proxyimage"
)

func TestCaseSecretPrivateFileLifecycle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	dir := t.TempDir()
	socket := filepath.Join(dir, "node")
	store := filepath.Join(dir, "store")
	storeListener, err := net.Listen("unix", store)
	if err != nil {
		t.Fatal(err)
	}
	defer storeListener.Close()
	if err := os.Chmod(store, 0600); err != nil {
		t.Fatal(err)
	}
	id := "sha256:" + strings.Repeat("a", 64)
	var config *container.Config
	var host *container.HostConfig
	authority := proxyimage.Authority{Name: "docker.io/library/codex-dock-proxy:latest", Digest: id, Build: buildinfo.Version}
	secretFile := filepath.Join(t.TempDir(), "node-case-secret")
	s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/v1.51")
		switch {
		case p == "/authority/images":
			_ = json.NewEncoder(w).Encode(authority)
		case p == "/authority/cases" && r.Method == http.MethodPost:
			var req struct{ Secret string }
			_ = json.NewDecoder(r.Body).Decode(&req)
			if err := os.WriteFile(secretFile, []byte(req.Secret), 0600); err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"Path": secretFile})
		case p == "/authority/cases" && r.Method == http.MethodDelete:
			if err := os.Remove(secretFile); err != nil {
				t.Error(err)
			}
			w.WriteHeader(204)
		case strings.HasPrefix(p, "/images/"):
			_ = json.NewEncoder(w).Encode(image.InspectResponse{ID: id, Config: &dockerimage.DockerOCIImageConfig{ImageConfig: v1.ImageConfig{Labels: map[string]string{"io.codex-dock.proxy-protocol": "external-store-v1", "io.codex-dock.proxy-build": buildinfo.Version}}}})
		case strings.HasPrefix(p, "/networks/"):
			_ = json.NewEncoder(w).Encode(dn.Inspect{Driver: "bridge", Labels: map[string]string{"codex-dock.managed": "true"}})
		case p == "/containers/create":
			var req struct {
				container.Config
				HostConfig container.HostConfig
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			config, host = &req.Config, &req.HostConfig
			w.WriteHeader(500)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "fixture create failure"})
		case strings.HasPrefix(p, "/containers/") && strings.HasSuffix(p, "/json"):
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "missing"})
		default:
			t.Errorf("unexpected path %s", p)
			w.WriteHeader(404)
		}
	}))
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	s.Listener = listener
	s.Start()
	defer s.Close()
	if err := proxyimage.Record(context.Background(), socket, "codex-dock-proxy:latest", id); err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("fixture-secret-", 4)
	opts := CaseProxyOptions{NodeSocket: socket, Name: "case-proxy", CaseID: "test-case", StoreSocket: store, AdminPort: 18091, AdminSecret: secret}
	if _, err := EnsureCaseProxy(context.Background(), opts); err == nil {
		t.Fatal("fixture failure lost")
	}
	if config == nil || host == nil {
		t.Fatal("proxy did not reach create boundary")
	}
	data, _ := json.Marshal(config)
	if strings.Contains(string(data), secret) || !strings.Contains(strings.Join(config.Cmd, " "), "--admin-secret-file") {
		t.Fatal("admin secret exposed in OCI config")
	}
	var path string
	for _, m := range host.Mounts {
		if m.Target == "/run/cxd-admin-secret" && m.ReadOnly {
			path = m.Source
		}
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != secret {
		t.Fatalf("authority unavailable after uncertain creation: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("authority permissions")
	}
	if config.Labels["codex-dock.image-id"] != id || host.NetworkMode != container.NetworkMode(network.EgressNetworkName) {
		t.Fatal("proxy image/network contract changed")
	}
	if err := RemoveCaseProxy(context.Background(), socket, opts.Name, opts.CaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("removed proxy left bearer authority")
	}
}

func TestCaseAuthorityRejectsLossyJSONStrings(t *testing.T) {
	for _, field := range []string{"secret", "case"} {
		t.Run(field, func(t *testing.T) {
			opts := CaseProxyOptions{Name: "case-proxy", CaseID: "test-case", AdminPort: 18091, AdminSecret: strings.Repeat("a", 32)}
			if field == "secret" {
				opts.AdminSecret += "\xff"
			} else {
				opts.CaseID += "\xff"
			}
			if _, err := EnsureCaseProxy(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "case proxy requires") {
				t.Fatalf("lossy JSON input reached node/filesystem boundary: %v", err)
			}
		})
	}
}
