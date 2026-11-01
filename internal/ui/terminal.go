package ui

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// tcell reports LF as Ctrl-J. tview v0.42's bracketed-paste collector only
// retains KeyEnter (CR). Normalize LF and CRLF during paste without changing
// normal Ctrl-J shortcuts or doubling Windows clipboard line endings.
type pasteScreen struct {
	tcell.Screen
	pasting    bool
	previousCR bool
	initErr    error
}

func (s *pasteScreen) Init() error { s.initErr = s.Screen.Init(); return s.initErr }
func (s *pasteScreen) PollEvent() tcell.Event {
	for {
		event := s.Screen.PollEvent()
		switch e := event.(type) {
		case *tcell.EventPaste:
			s.pasting = e.Start()
			s.previousCR = false
		case *tcell.EventKey:
			if s.pasting {
				if e.Key() == tcell.KeyCtrlJ {
					cr := s.previousCR
					s.previousCR = false
					if cr {
						continue
					}
					return tcell.NewEventKey(tcell.KeyEnter, 0, 0)
				}
				s.previousCR = e.Key() == tcell.KeyEnter
			}
		}
		return event
	}
}
func runTerminal(app *tview.Application) error {
	screen, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	wrapped := &pasteScreen{Screen: screen}
	app.SetScreen(wrapped).EnableMouse(true).EnablePaste(true)
	if wrapped.initErr != nil {
		return wrapped.initErr
	}
	return app.Run()
}
