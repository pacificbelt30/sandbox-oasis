package sandbox

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

func TestJobLogsAcceptsBoundedJSONEscapingOfControlOutput(t *testing.T) {
	payload := strings.Repeat("\x01", 1<<20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch strings.TrimPrefix(r.URL.Path, "/v1.51") {
		case "/containers/worker/json":
			_ = json.NewEncoder(w).Encode(container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: "worker", State: &container.State{Running: true}}, Config: &container.Config{Labels: map[string]string{labelManaged: "true"}}})
		case "/exec/job/json":
			_ = json.NewEncoder(w).Encode(container.ExecInspect{ContainerID: "worker", ExecID: "job"})
		case "/exec/job/logs":
			_ = json.NewEncoder(w).Encode(JobOutput{Output: payload, Next: int64(len(payload))})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	cli, err := client.NewClientWithOpts(client.WithHost("tcp://"+strings.TrimPrefix(server.URL, "http://")), client.WithVersion("1.51"), client.WithHTTPClient(&http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}}}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cli.Close() }()
	m := &Manager{cli: cli}
	out, err := m.JobLogs(context.Background(), "worker", "job", 0)
	if err != nil || out.Output != payload || out.Next != int64(len(payload)) {
		t.Fatal("bounded escaped log chunk failed", len(out.Output), err)
	}
}
