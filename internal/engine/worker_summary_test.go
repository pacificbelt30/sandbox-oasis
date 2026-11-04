package engine

import (
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

func TestWorkerSummaryKeepsCreationSeparateFromRestart(t *testing.T) {
	created := time.Unix(1700000000, 0).UTC()
	start := created.Add(24 * time.Hour)
	v := container.InspectResponse{ContainerJSONBase: &container.ContainerJSONBase{ID: "worker", Name: "/worker", Created: created.Format(time.RFC3339Nano), State: &container.State{Status: "running", StartedAt: start.Format(time.RFC3339Nano)}}, Config: &container.Config{Image: "fixture", Labels: map[string]string{WorkerStartedAtLabel: "project-supplied-invalid-time"}}}
	summary := workerSummary(v)
	if summary.Created != created.Unix() || summary.Labels[WorkerStartedAtLabel] != start.Format(time.RFC3339Nano) {
		t.Fatal(summary)
	}
	if v.Config.Labels[WorkerStartedAtLabel] != "project-supplied-invalid-time" {
		t.Fatal("inspection/configuration labels mutated")
	}
}
