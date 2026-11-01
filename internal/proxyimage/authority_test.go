package proxyimage

import (
	"context"
	"encoding/json"
	"github.com/pacificbelt30/codex-dock/internal/buildinfo"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProxyAuthorityCannotBeInferredFromProjectTags(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(t.TempDir(), "node")
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var a Authority
			_ = json.NewDecoder(r.Body).Decode(&a)
			if err := Save(root, a); err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			a.Name, _ = Normalize(a.Name)
			_ = json.NewEncoder(w).Encode(a)
			return
		}
		a, err := Load(root, r.URL.Query().Get("name"))
		if os.IsNotExist(err) {
			w.WriteHeader(404)
			return
		}
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(a)
	}))
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server.Listener = listener
	server.Start()
	defer server.Close()
	ctx := context.Background()
	for _, name := range []string{"codex-dock-proxy", "codex-dock-proxy:latest", "docker.io/library/codex-dock-proxy:latest", "index.docker.io/library/codex-dock-proxy:other", "oaiso-proxy-build:any", "oaiso-login:recipe"} {
		if CheckWorkerTag(ctx, socket, name, false) == nil {
			t.Fatalf("reserved alias accepted: %s", name)
		}
	}
	id := "sha256:" + strings.Repeat("a", 64)
	// Same-UID guest writes in HOME cannot create root node authority.
	forged := filepath.Join(home, ".config", "codex-dock", "proxy-authority")
	if err := Save(forged, Authority{"custom/proxy", id, buildinfo.Version}); err != nil {
		t.Fatal(err)
	}
	if Verify(ctx, socket, "custom/proxy", id) == nil {
		t.Fatal("guest-written HOME authority trusted")
	}
	if err := Record(ctx, socket, "custom/proxy", id); err != nil {
		t.Fatal(err)
	}
	if err := Verify(ctx, socket, "docker.io/custom/proxy:latest", id); err != nil {
		t.Fatal(err)
	}
	if Verify(ctx, socket, "custom/proxy", "sha256:"+strings.Repeat("b", 64)) == nil {
		t.Fatal("changed digest trusted")
	}
	if CheckWorkerTag(ctx, socket, "custom/proxy", false) == nil {
		t.Fatal("worker can overwrite authorized custom proxy")
	}
	if err := CheckWorkerTag(ctx, socket, "worker-image:latest", false); err != nil {
		t.Fatal(err)
	}
	if err := CheckWorkerTag(ctx, socket, "oaiso-login:recipe", true); err != nil {
		t.Fatal(err)
	}
}
