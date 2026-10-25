// Package launchtiming records one operation's sequential host-side phases.
// It is opt-in, local to the caller, and never stores request or credential data.
package launchtiming

import (
	"context"
	"time"
)

type key struct{}

type Phase struct {
	Name     string
	Duration time.Duration
}

// Recorder belongs to one synchronous operation. Do not share its context between
// concurrent operations. Background cleanup is included by the operation owner.
type Recorder struct {
	started time.Time
	last    time.Time
	name    string
	phases  []Phase
}

func Begin(ctx context.Context) (context.Context, *Recorder) {
	now := time.Now()
	r := &Recorder{started: now, last: now, name: "request_validation"}
	return context.WithValue(ctx, key{}, r), r
}

// Next is a no-op unless the caller explicitly requested operation timings.
func Next(ctx context.Context, name string) {
	if r, ok := ctx.Value(key{}).(*Recorder); ok {
		r.next(name, time.Now())
	}
}

func (r *Recorder) next(name string, now time.Time) {
	r.phases = append(r.phases, Phase{Name: r.name, Duration: now.Sub(r.last)})
	r.name, r.last = name, now
}

func (r *Recorder) Finish() (time.Duration, []Phase) {
	now := time.Now()
	r.next("", now)
	return now.Sub(r.started), r.phases
}
