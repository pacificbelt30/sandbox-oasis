package ui

import (
	"context"
	"errors"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

const desktopReadTimeout = 30 * time.Second

type desktopResult struct {
	next func()
	err  error
}

func (d *sandboxDesktop) work(fn func(context.Context) (func(), error)) {
	d.startWork("Working…", 10*time.Minute, false, fn)
}

func (d *sandboxDesktop) read(title string, fn func(context.Context) (func(), error)) {
	d.startWork(title, desktopReadTimeout, true, fn)
}

// Only reads can return to their previous page before the backend finishes.
// Mutations retain their completion handling so a late success is not hidden.
func (d *sandboxDesktop) startWork(title string, timeout time.Duration, cancellable bool, fn func(context.Context) (func(), error)) {
	if d.busy || d.ctx.Err() != nil {
		return
	}
	d.busy = true
	d.workGeneration++
	generation := d.workGeneration
	previous, primitive := d.pages.GetFrontPage()
	ctx, cancel := context.WithTimeout(d.ctx, timeout)
	restore := func() {
		d.busy = false
		d.pages.RemovePage("working")
		d.pages.SwitchToPage(previous)
		if primitive != nil {
			d.app.SetFocus(primitive)
		}
	}
	text := tview.NewTextView().SetDynamicColors(false).SetText(title)
	text.SetBorder(true).SetTitle(" Sandbox operation ")
	var working tview.Primitive = text
	if cancellable {
		stop := func() {
			if d.workGeneration == generation && d.busy {
				d.workGeneration++
				cancel()
				restore()
				if primitive == nil {
					d.back()
				}
			}
		}
		buttons := tview.NewForm().AddButton("Cancel", stop).SetCancelFunc(stop)
		body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(text, 0, 1, false).AddItem(buttons, 3, 0, true).AddItem(tview.NewTextView().SetText("Esc cancels this read · Timeout returns to this page with Retry"), 1, 0, false)
		body.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
			if e.Key() == tcell.KeyEscape {
				stop()
				return nil
			}
			return e
		})
		working = body
	}
	d.pages.AddAndSwitchToPage("working", working, true)
	d.app.SetFocus(working)
	result := make(chan desktopResult, 1)
	go func() {
		next, err := fn(ctx)
		result <- desktopResult{next: next, err: err}
	}()
	go func() {
		defer cancel()
		var outcome desktopResult
		if cancellable {
			select {
			case outcome = <-result:
			case <-ctx.Done():
				outcome.err = ctx.Err()
			}
		} else {
			outcome = <-result
		}
		if d.ctx.Err() != nil || (cancellable && errors.Is(ctx.Err(), context.Canceled)) {
			return
		}
		d.app.QueueUpdateDraw(func() {
			if d.workGeneration != generation || d.ctx.Err() != nil {
				return
			}
			restore()
			if cancellable && ctx.Err() != nil {
				outcome.err = ctx.Err()
			}
			if outcome.err != nil {
				if cancellable {
					d.readFailure(outcome.err, func() { d.startWork(title, timeout, true, fn) })
				} else {
					d.message(outcome.err.Error())
				}
			} else if outcome.next != nil {
				outcome.next()
			}
		})
	}()
}

func (d *sandboxDesktop) readFailure(err error, retry func()) {
	previous, primitive := d.pages.GetFrontPage()
	modal := tview.NewModal().SetText(tview.Escape(err.Error())).AddButtons([]string{"Back", "Retry"})
	modal.SetDoneFunc(func(index int, _ string) {
		d.pages.RemovePage("message")
		d.pages.SwitchToPage(previous)
		if primitive != nil {
			d.app.SetFocus(primitive)
		}
		if index == 1 {
			retry()
		} else if primitive == nil {
			d.back()
		}
	})
	d.pages.AddAndSwitchToPage("message", modal, true)
	d.app.SetFocus(modal)
}
