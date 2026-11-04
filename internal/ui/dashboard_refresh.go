package ui

import (
	"context"
	"sync"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/storageinfo"
)

type dashboardSource interface {
	ListContext(context.Context, bool) ([]sandbox.Worker, error)
	RootFSUsage(context.Context, string) (storageinfo.Usage, error)
}

func loadDashboard(ctx context.Context, source dashboardSource) ([]sandbox.Worker, error) {
	workers, err := source.ListContext(ctx, true)
	if err != nil {
		return nil, err
	}
	jobs := make(chan int)
	var group sync.WaitGroup
	for range min(4, len(workers)) {
		group.Go(func() {
			for i := range jobs {
				usage, err := source.RootFSUsage(ctx, workers[i].ID)
				if err != nil {
					usage = storageinfo.Usage{Error: "Storage measurement unavailable; run oaiso runtime-check"}
				}
				workers[i].RootFS = &usage
			}
		})
	}
	for i := range workers {
		jobs <- i
	}
	close(jobs)
	group.Wait()
	return workers, nil
}

// One reader owns refresh ordering. A single buffered request preserves the
// latest user-triggered refresh while merging bursts with the periodic poll.
func startDashboardRefresh(ctx context.Context, interval time.Duration, load func(context.Context) ([]sandbox.Worker, error), apply func([]sandbox.Worker), failed func(error)) (request func(), done <-chan struct{}) {
	requests := make(chan struct{}, 1)
	finished := make(chan struct{})
	request = func() {
		if ctx.Err() == nil {
			select {
			case requests <- struct{}{}:
			default:
			}
		}
	}
	go func() {
		defer close(finished)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-requests:
			case <-ticker.C:
			}
			if ctx.Err() != nil {
				return
			}
			probe, cancel := context.WithTimeout(ctx, 3*time.Second)
			workers, err := load(probe)
			cancel()
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				failed(err)
			} else {
				apply(workers)
			}
		}
	}()
	return request, finished
}
