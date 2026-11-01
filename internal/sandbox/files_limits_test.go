package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

func TestFileOperationsBoundHostOutputAndCancelGuest(t *testing.T) {
	for _, op := range []string{"read", "write", "list"} {
		for _, stderr := range []bool{false, true} {
			t.Run(op+map[bool]string{false: "-stdout", true: "-stderr"}[stderr], func(t *testing.T) {
				var isolated, killed atomic.Bool
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					p := strings.TrimPrefix(r.URL.Path, "/v1.51")
					switch p {
					case "/containers/json":
						_ = json.NewEncoder(w).Encode([]container.Summary{{ID: "worker-id", Names: []string{"/worker"}}})
					case "/containers/worker-id/json":
						_ = json.NewEncoder(w).Encode(container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: "worker-id", State: &container.State{Running: true}}, Config: &container.Config{Labels: map[string]string{labelManaged: "true", labelIsolation: RuntimeRunc}}})
					case "/containers/worker-id/exec":
						var opts container.ExecOptions
						_ = json.NewDecoder(r.Body).Decode(&opts)
						isolated.Store(len(opts.Cmd) > 1 && opts.Cmd[1] == "-I")
						_ = json.NewEncoder(w).Encode(map[string]string{"Id": "exec-id"})
					case "/exec/exec-id/start":
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
						if stderr {
							stream = stdcopy.Stderr
						}
						out := stdcopy.NewStdWriter(conn, stream)
						chunk := bytes.Repeat([]byte{'x'}, 32<<10)
						for i := 0; i < 2050; i++ {
							if _, err := out.Write(chunk); err != nil {
								return
							}
						}
					case "/exec/exec-id/json":
						_ = json.NewEncoder(w).Encode(container.ExecInspect{Running: !killed.Load()})
					case "/exec/exec-id/kill":
						killed.Store(true)
						w.WriteHeader(204)
					default:
						t.Errorf("unexpected path %s", p)
						w.WriteHeader(404)
					}
				}))
				defer server.Close()
				transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
				}}
				defer transport.CloseIdleConnections()
				cli, err := client.NewClientWithOpts(client.WithHost("tcp://"+strings.TrimPrefix(server.URL, "http://")), client.WithVersion("1.51"), client.WithHTTPClient(&http.Client{Transport: transport}), client.WithDialContext(transport.DialContext))
				if err != nil {
					t.Fatal(err)
				}
				defer cli.Close()
				m := &Manager{cli: cli}
				if err := m.fileOperation(context.Background(), "worker", op, "file", nil, io.Discard); err == nil || !strings.Contains(err.Error(), "host limit") {
					t.Fatalf("unbounded output accepted: %v", err)
				}
				if !isolated.Load() || !killed.Load() {
					t.Fatalf("isolation=%v cancellation=%v", isolated.Load(), killed.Load())
				}
			})
		}
	}
}
