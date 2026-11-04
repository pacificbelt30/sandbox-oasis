package engine

import (
	"maps"
	"time"

	"github.com/docker/docker/api/types/container"
)

// Summary timestamps are observations, not persisted project-supplied labels.
// Created keeps its Docker-compatible meaning; consumers opt into task times.
const WorkerStartedAtLabel = "codex-dock.observed-started-at"
const WorkerFinishedAtLabel = "codex-dock.observed-finished-at"

func workerSummary(v container.InspectResponse) container.Summary {
	created, _ := time.Parse(time.RFC3339Nano, v.Created)
	labels := maps.Clone(v.Config.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	labels[WorkerStartedAtLabel] = v.State.StartedAt
	labels[WorkerFinishedAtLabel] = v.State.FinishedAt
	return container.Summary{ID: v.ID, Names: []string{v.Name}, Image: v.Config.Image, Labels: labels, State: v.State.Status, Status: v.State.Status, Created: created.Unix()}
}
