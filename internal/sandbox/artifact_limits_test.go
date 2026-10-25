package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestArtifactHostLimitsUntrustedGuestOutput(t *testing.T) {
	var isolated atomic.Bool
	var stderrOnly atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1.51")
		switch {
		case path == "/containers/json":
			_ = json.NewEncoder(w).Encode([]container.Summary{{ID: "worker-id", Names: []string{"/worker"}}})
		case strings.HasPrefix(path, "/containers/") && strings.HasSuffix(path, "/json"):
			_ = json.NewEncoder(w).Encode(container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: "worker-id", HostConfig: &container.HostConfig{Runtime: "runc"}, State: &container.State{Running: true}}, Config: &container.Config{Labels: map[string]string{labelManaged: "true", labelIsolation: RuntimeRunc}}})
		case strings.HasSuffix(path, "/exec"):
			var opts container.ExecOptions
			_ = json.NewDecoder(r.Body).Decode(&opts)
			isolated.Store(len(opts.Cmd) > 1 && opts.Cmd[1] == "-I")
			stderrOnly.Store(len(opts.Cmd) > 0 && strings.Contains(opts.Cmd[len(opts.Cmd)-1], `"stderr"`))
			_ = json.NewEncoder(w).Encode(map[string]string{"Id": "exec-id"})
		case path == "/exec/exec-id/start":
			_, _ = io.Copy(io.Discard, r.Body)
			conn, rw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			_, _ = rw.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\n\r\n")
			_ = rw.Flush()
			stream := stdcopy.Stdout
			if stderrOnly.Load() {
				stream = stdcopy.Stderr
			}
			stdout := stdcopy.NewStdWriter(conn, stream)
			for i := 0; i < 65; i++ {
				if _, err := stdout.Write(bytes.Repeat([]byte{'x'}, 32<<10)); err != nil {
					break
				}
			}
		case path == "/exec/exec-id/json":
			_ = json.NewEncoder(w).Encode(container.ExecInspect{Running: false})
		default:
			t.Errorf("unexpected request %s", path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	cli, err := client.NewClientWithOpts(client.WithHost("tcp://"+strings.TrimPrefix(server.URL, "http://")), client.WithVersion("1.51"), client.WithHTTPClient(&http.Client{Transport: &http.Transport{}}), client.WithDialContext((&net.Dialer{}).DialContext))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	manager := &Manager{cli: cli}
	if _, err = manager.PlanArtifacts(context.Background(), "worker", ArtifactOptions{Paths: []string{"file"}}); err == nil {
		t.Fatal("untrusted plan exceeded host buffer bound")
	}
	var out bytes.Buffer
	if err = manager.ExportArtifacts(context.Background(), "worker", ArtifactOptions{Paths: []string{"file"}, MaxBytes: 1, ExpectedPlan: strings.Repeat("a", 64)}, &out); err == nil || out.Len() > 1<<20+1 {
		t.Fatal("untrusted archive exceeded host output bound", out.Len(), err)
	}
	if _, err = manager.PlanArtifacts(context.Background(), "worker", ArtifactOptions{Paths: []string{"stderr"}}); err == nil {
		t.Fatal("untrusted stderr exceeded host bound")
	}
	if !isolated.Load() {
		t.Fatal("workspace Python modules could replace the collector")
	}
}
