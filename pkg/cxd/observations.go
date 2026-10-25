package cxd

import (
	"path/filepath"

	"github.com/pacificbelt30/codex-dock/internal/launchtiming"
	"github.com/pacificbelt30/codex-dock/internal/observability"
)

// Observation is a fixed, redacted diagnostic schema, not arbitrary log text.
type Observation = observability.Record

// SaveLaunchObservation saves only hashed identity, result and measured phases.
// Recording is separate from Launch so a storage failure never changes its result.
func SaveLaunchObservation(directory, worker string, timings LaunchTimings, failed bool) error {
	path, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	phases := make([]launchtiming.Phase, 0, len(timings.Phases))
	for _, p := range timings.Phases {
		phases = append(phases, launchtiming.Phase{Name: p.Name, Duration: p.Duration})
	}
	return (observability.Journal{Directory: path}).Append(observability.Launch(worker, timings.Duration, phases, failed))
}

// ReadObservations validates both bounded retained files before returning data.
func ReadObservations(directory string) ([]Observation, error) {
	path, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	return (observability.Journal{Directory: path}).Read()
}
