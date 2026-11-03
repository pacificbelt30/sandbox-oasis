package sandbox

import (
	"context"
	"encoding/json"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestReapTouchesOnlyExplicitExpiredPolicies(t *testing.T) {
	now := time.Now().UTC()
	encoded, _ := json.Marshal(workerpolicy.Lifecycle{MaxLifetime: time.Hour})
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1.51")
		switch path {
		case "/containers/json":
			_ = json.NewEncoder(w).Encode([]container.Summary{{ID: "expired", Names: []string{"/expired"}}, {ID: "legacy", Names: []string{"/legacy"}}})
		case "/containers/expired/json", "/containers/legacy/json":
			labels := map[string]string{labelManaged: "true"}
			if strings.Contains(path, "expired") {
				labels[lifecycleLabel] = string(encoded)
			}
			_ = json.NewEncoder(w).Encode(container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: strings.Split(path, "/")[2], Created: now.Add(-2 * time.Hour).Format(time.RFC3339Nano), State: &container.State{Running: true, Status: "running"}}, Config: &container.Config{Labels: labels}})
		default:
			calls++
			t.Errorf("unexpected mutation %s", path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.51"), client.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	m := &Manager{cli: cli}
	actions, err := m.Reap(context.Background(), now, true)
	if err != nil || len(actions) != 1 || actions[0].Worker != "expired" || actions[0].Reason != "max_lifetime" || calls != 0 {
		t.Fatalf("%+v %v calls=%d", actions, err, calls)
	}
}

func TestSelectedReapDoesNotInspectOrCollectUnselectedWorkers(t *testing.T) {
	now := time.Now().UTC()
	encoded, _ := json.Marshal(workerpolicy.Lifecycle{MaxLifetime: time.Minute})
	inspected := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1.51")
		switch path {
		case "/containers/json":
			_ = json.NewEncoder(w).Encode([]container.Summary{{ID: "selected", Names: []string{"/selected"}}, {ID: "other", Names: []string{"/other"}}})
		case "/containers/selected/json":
			inspected++
			_ = json.NewEncoder(w).Encode(container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: "selected", Created: now.Add(-time.Hour).Format(time.RFC3339Nano), State: &container.State{Running: true, Status: "running"}}, Config: &container.Config{Labels: map[string]string{labelManaged: "true", lifecycleLabel: string(encoded)}}})
		default:
			t.Errorf("selection escaped: %s", path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	cli, err := client.NewClientWithOpts(client.WithHost(server.URL), client.WithVersion("1.51"), client.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	m := &Manager{cli: cli}
	for _, ids := range [][]string{nil, {}, {"not-selected"}} {
		actions, err := m.ReapWorkers(t.Context(), now, true, ids)
		if err != nil || len(actions) != 0 || inspected != 0 {
			t.Fatal("empty or unknown selection expanded", ids, actions, err, inspected)
		}
	}
	actions, err := m.ReapWorkers(t.Context(), now, true, []string{"selected"})
	if err != nil || len(actions) != 1 || actions[0].Worker != "selected" || inspected != 1 {
		t.Fatal(actions, err, inspected)
	}
	changed := []ReapAction{{Worker: "selected", Action: "remove", Reason: "retention"}}
	if applied, err := m.ApplyReapActions(t.Context(), now, changed); err == nil || len(applied) != 0 {
		t.Fatal("a different action was applied without approval", applied, err)
	}
}
