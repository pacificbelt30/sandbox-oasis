package sandbox

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/pacificbelt30/codex-dock/internal/engine"
)

func TestWorkerListUsesObservedTaskTimesAndRejectsMalformedRows(t *testing.T) {
	created := time.Unix(1700000000, 0)
	start := created.Add(24 * time.Hour)
	row := container.Summary{ID: "worker", Names: []string{"/worker"}, State: "running", Created: created.Unix(), Labels: map[string]string{engine.WorkerStartedAtLabel: start.Format(time.RFC3339Nano)}}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if !strings.HasSuffix(r.URL.Path, "/containers/json") {
			t.Errorf("unexpected per-worker inspect %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]container.Summary{row})
	}))
	defer server.Close()
	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.51"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	m := &Manager{cli: cli}
	before, err := m.ListContext(t.Context(), true)
	if err != nil || !before[0].StartedAt.Equal(start) || requests != 1 {
		t.Fatal(before, err, requests)
	}
	row.Labels[engine.WorkerStartedAtLabel] = start.Add(time.Hour).Format(time.RFC3339Nano)
	after, err := m.ListContext(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	if before[0].StartedAt.Equal(*after[0].StartedAt) {
		t.Fatal("restart timestamp did not change")
	}
	row.Labels[engine.WorkerStartedAtLabel] = "0001-01-01T00:00:00Z"
	never, err := m.ListContext(t.Context(), true)
	if err != nil || never[0].StartedAt != nil {
		t.Fatal("never-started worker given a fabricated uptime", never, err)
	}
	row.Labels = nil
	legacy, err := m.ListContext(t.Context(), true)
	if err != nil || !legacy[0].StartedAt.Equal(created) {
		t.Fatal("legacy node compatibility", legacy, err)
	}
	row.Names = nil
	if _, err = m.ListContext(t.Context(), true); err == nil {
		t.Fatal("malformed node row accepted")
	}
}
