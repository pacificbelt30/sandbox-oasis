package sandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

func TestHeartbeatRequiresConfirmedSameWorkerRenewal(t *testing.T) {
	for _, scenario := range []string{"success", "wrong-worker", "missing-deadline", "node-error"} {
		t.Run(scenario, func(t *testing.T) {
			id := strings.Repeat("a", 64)
			until := time.Now().UTC().Add(time.Minute)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/json"):
					_ = json.NewEncoder(w).Encode(container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: id}})
				case r.URL.Path == "/containers/"+id+"/activity" && r.Method == http.MethodPost && r.URL.Query().Get("ttl") == "60":
					if scenario == "node-error" {
						w.WriteHeader(500)
						return
					}
					worker, expiry := id, until
					if scenario == "wrong-worker" {
						worker = "another"
					}
					if scenario == "missing-deadline" {
						expiry = time.Time{}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"worker": worker, "active_until": expiry})
				default:
					t.Errorf("unexpected activity request: %s %s", r.Method, r.URL)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL)
			httpClient := server.Client()
			httpClient.Transport = quarantineTransport{base: httpClient.Transport, host: u.Host}
			cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.51"), client.WithHTTPClient(httpClient))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			m := &Manager{cli: cli}
			got, err := m.Heartbeat(context.Background(), "worker", time.Minute)
			if (err == nil) != (scenario == "success") || err == nil && !got.Equal(until) {
				t.Fatalf("unconfirmed renewal accepted: %s %v", got, err)
			}
			if _, err := m.Heartbeat(context.Background(), "worker", time.Hour); err == nil {
				t.Fatal("unbounded lease accepted")
			}
		})
	}
}
