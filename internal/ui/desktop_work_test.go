package ui

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/rivo/tview"
)

func TestDesktopReadCancelDiscardsLateResultAndAllowsNextRead(t *testing.T) {
	d, update, wait := runBrowser(t, &browserFiles{listings: map[string][]sandboxux.FileEntry{".": {}}})
	started, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var stale, next atomic.Int32
	update(func() {
		d.read("Delayed read", func(context.Context) (func(), error) {
			close(started)
			<-release // Simulate a backend responding after cancellation.
			close(returned)
			return func() { stale.Add(1) }, nil
		})
	})
	<-started
	update(func() {
		_, working := d.pages.GetFrontPage()
		working.InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(p tview.Primitive) { d.app.SetFocus(p) })
		if d.busy {
			t.Error("Escape did not release the read page")
		}
		d.read("Next read", func(context.Context) (func(), error) { return func() { next.Add(1) }, nil })
	})
	wait()
	close(release)
	<-returned
	update(func() {})
	if stale.Load() != 0 || next.Load() != 1 {
		t.Fatal("late read changed the new page", stale.Load(), next.Load())
	}
}

func TestDesktopReadTimeoutOffersRetryAndRejectsLateSuccess(t *testing.T) {
	d, update, wait := runBrowser(t, &browserFiles{listings: map[string][]sandboxux.FileEntry{".": {}}})
	release := make(chan struct{})
	var calls, applied atomic.Int32
	update(func() {
		// Retry includes an event-loop redraw. A tiny deadline can expire a
		// correct retry while other race-instrumented packages are running.
		d.startWork("Slow read", time.Second, true, func(context.Context) (func(), error) {
			if calls.Add(1) == 1 {
				<-release
			}
			return func() { applied.Add(1) }, nil
		})
	})
	wait()
	update(func() {
		name, modal := d.pages.GetFrontPage()
		if name != "message" {
			t.Error("timeout did not return a retry page", name)
			return
		}
		focus := func(p tview.Primitive) { d.app.SetFocus(p) }
		modal.InputHandler()(tcell.NewEventKey(tcell.KeyTab, 0, 0), focus)
		modal.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), focus)
	})
	wait()
	close(release)
	update(func() {})
	if calls.Load() != 2 || applied.Load() != 1 {
		t.Fatal("timeout retry applied the old result", calls.Load(), applied.Load())
	}
}

func TestDesktopMutationEscapeDoesNotHideCompletion(t *testing.T) {
	d, update, wait := runBrowser(t, &browserFiles{listings: map[string][]sandboxux.FileEntry{".": {}}})
	release := make(chan struct{})
	var applied atomic.Int32
	update(func() {
		d.work(func(context.Context) (func(), error) { <-release; return func() { applied.Add(1) }, nil })
		_, page := d.pages.GetFrontPage()
		page.InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(p tview.Primitive) { d.app.SetFocus(p) })
		if !d.busy {
			t.Error("mutation was treated as a cancelled read")
		}
	})
	close(release)
	wait()
	if applied.Load() != 1 {
		t.Fatal("mutation completion was lost")
	}
}
