package cxd

import (
	"context"
	"github.com/pacificbelt30/codex-dock/internal/launchtiming"
)

// ExecTimings reports fixed phase names and elapsed time only, never argv,
// paths, output, identity, credentials or error strings. It is opt-in.
type ExecTimings = LaunchTimings

func (c *Client) ExecWithTimings(ctx context.Context, worker string, request ExecRequest) (int64, ExecTimings, error) {
	ctx, recorder := launchtiming.Begin(ctx)
	code, err := c.Exec(ctx, worker, request)
	total, phases := recorder.Finish()
	out := ExecTimings{Duration: total, Phases: make([]LaunchPhase, 0, len(phases))}
	for _, p := range phases {
		out.Phases = append(out.Phases, LaunchPhase{Name: p.Name, Duration: p.Duration})
	}
	return code, out, err
}
