package ui

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/storageinfo"
)

type latencyDashboard struct {
	workers int
	delay   time.Duration
}

func TestDashboardRefreshCoalescesBurstsAndCancelsInflightRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan int, 4)
	release := make(chan struct{})
	var calls atomic.Int32
	load := func(ctx context.Context) ([]sandbox.Worker, error) {
		n := int(calls.Add(1))
		started <- n
		if n == 1 {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		} else {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return []sandbox.Worker{{ID: "first"}}, nil
	}
	apply := make(chan []sandbox.Worker, 4)
	request, done := startDashboardRefresh(ctx, time.Hour, load, func(ws []sandbox.Worker) { apply <- ws }, func(err error) { t.Errorf("unexpected refresh error %v", err) })
	request()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	for range 100 {
		request()
	}
	if calls.Load() != 1 {
		t.Fatal("parallel refreshes", calls.Load())
	}
	close(release)
	select {
	case <-apply:
	case <-time.After(time.Second):
		t.Fatal("completed refresh not applied")
	}
	select {
	case n := <-started:
		if n != 2 {
			t.Fatal(n)
		}
	case <-time.After(time.Second):
		t.Fatal("latest request was lost")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("node read survived dashboard exit")
	}
	if len(apply) != 0 || calls.Load() != 2 {
		t.Fatal("stale/cancelled read was applied", calls.Load())
	}
}

type gatedDashboard struct {
	active, maximum atomic.Int32
	started         chan struct{}
	release         chan struct{}
}

func (s *gatedDashboard) ListContext(context.Context, bool) ([]sandbox.Worker, error) {
	return make([]sandbox.Worker, 8), nil
}
func (s *gatedDashboard) RootFSUsage(ctx context.Context, _ string) (storageinfo.Usage, error) {
	n := s.active.Add(1)
	defer s.active.Add(-1)
	for old := s.maximum.Load(); n > old; old = s.maximum.Load() {
		if s.maximum.CompareAndSwap(old, n) {
			break
		}
	}
	s.started <- struct{}{}
	select {
	case <-s.release:
		return storageinfo.Usage{Available: true}, nil
	case <-ctx.Done():
		return storageinfo.Usage{Available: true, ReadOnly: true, LimitBytes: 4 << 30}, errors.New("cancelled quota")
	}
}
func TestDashboardQuotaFanoutIsBoundedAndRetainsUnavailableRows(t *testing.T) {
	s := &gatedDashboard{started: make(chan struct{}, 8), release: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan []sandbox.Worker, 1)
	go func() {
		ws, err := loadDashboard(ctx, s)
		if err != nil {
			t.Error(err)
		}
		done <- ws
	}()
	for range 4 {
		select {
		case <-s.started:
		case <-time.After(time.Second):
			t.Fatal("quota reads remain serialized")
		}
	}
	if s.maximum.Load() != 4 {
		t.Fatal("unbounded quota work", s.maximum.Load())
	}
	cancel()
	select {
	case ws := <-done:
		if len(ws) != 8 {
			t.Fatal("lost workers", ws)
		}
		for _, w := range ws {
			if w.RootFS == nil || w.RootFS.Available || w.RootFS.ReadOnly || w.RootFS.LimitBytes != 0 || w.RootFS.Error == "" {
				t.Fatal("cancelled measurement presented as available", w)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("quota cancellation did not complete")
	}
}

func (s latencyDashboard) ListContext(context.Context, bool) ([]sandbox.Worker, error) {
	workers := make([]sandbox.Worker, s.workers)
	for i := range workers {
		workers[i] = sandbox.Worker{ID: fmt.Sprint(i), Name: fmt.Sprint(i)}
	}
	return workers, nil
}
func (s latencyDashboard) RootFSUsage(ctx context.Context, _ string) (storageinfo.Usage, error) {
	timer := time.NewTimer(s.delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return storageinfo.Usage{Available: true, LimitBytes: 4 << 30}, nil
	case <-ctx.Done():
		return storageinfo.Usage{}, ctx.Err()
	}
}
func BenchmarkDashboardQuotaCollection(b *testing.B) {
	for _, count := range []int{4, 20} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			source := latencyDashboard{workers: count, delay: time.Millisecond}
			b.ReportAllocs()
			for b.Loop() {
				workers, err := loadDashboard(b.Context(), source)
				if err != nil || len(workers) != count {
					b.Fatal(err)
				}
			}
		})
	}
}
