//go:build linux

package engine

import (
	"errors"
	"time"

	cd "github.com/containerd/containerd/v2/client"
)

// taskExit publishes one runtime Wait result to lifecycle callers. The result
// is published before taking Backend.mu, which a stopping caller can hold.
type taskExit struct {
	ready    chan struct{}
	status   cd.ExitStatus
	received bool
}

func newTaskExit() *taskExit { return &taskExit{ready: make(chan struct{})} }

func (e *taskExit) capture(wait <-chan cd.ExitStatus) {
	e.status, e.received = <-wait
	if !e.received {
		e.status = *cd.NewExitStatus(255, time.Time{}, errors.New("task exit channel closed without a result"))
	}
	close(e.ready)
}
