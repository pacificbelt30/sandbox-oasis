package ui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/taskboard"
	"github.com/rivo/tview"
)

func TestReviewSelectionWholeFileAndHunks(t *testing.T) {
	s := taskboard.Selection{}
	whole := reviewRow{path: "file"}
	a := reviewRow{path: "file", hunk: "a"}
	all := []string{"a", "b"}
	toggleReviewSelection(s, whole, all)
	toggleReviewSelection(s, a, all)
	if !reflect.DeepEqual(s["file"], []string{"b"}) {
		t.Fatal(s)
	}
	toggleReviewSelection(s, a, all)
	if len(s["file"]) != 2 {
		t.Fatal(s)
	}
	toggleReviewSelection(s, whole, all)
	if len(s) != 0 {
		t.Fatal(s)
	}
}
func TestTaskBoardEscPasteAndSmallScreen(t *testing.T) {
	app := tview.NewApplication()
	back := false
	s := &taskboard.Service{Store: taskboard.Store{Root: filepath.Join(t.TempDir(), "private")}}
	b := newTaskBoardPage(app, s, func() { back = true })
	app.SetRoot(b.pages, true).SetFocus(b.table)
	b.table.GetInputCapture()(tcell.NewEventKey(tcell.KeyRune, 'n', 0))
	_, page := b.pages.GetFrontPage()
	form := page.(*tview.Form)
	field := form.GetFormItem(0).(*tview.InputField)
	field.PasteHandler()("日本語 [red]task", func(tview.Primitive) {})
	if field.GetText() != "日本語 [red]task" {
		t.Fatal(field.GetText())
	}
	instructions := form.GetFormItem(3).(*tview.TextArea)
	instructions.PasteHandler()("first\n日本語 [blue]second", func(tview.Primitive) {})
	if instructions.GetText() != "first\n日本語 [blue]second" {
		t.Fatal(instructions.GetText())
	}
	form.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if back {
		t.Fatal("Esc left board rather than cancelling new task")
	}
	if list, e := s.Store.List(); e != nil || len(list) != 0 {
		t.Fatal("cancel saved task", list, e)
	}
	screen := tcell.NewSimulationScreen("")
	if e := screen.Init(); e != nil {
		t.Fatal(e)
	}
	defer screen.Fini()
	for _, size := range [][2]int{{120, 40}, {80, 24}, {60, 22}} {
		screen.SetSize(size[0], size[1])
		b.pages.SetRect(0, 0, size[0], size[1])
		b.pages.Draw(screen)
		screen.Show()
		cells, _, _ := screen.GetContents()
		var visible strings.Builder
		for _, c := range cells {
			visible.Write(c.Bytes)
		}
		if !strings.Contains(visible.String(), "Tasks") || !strings.Contains(visible.String(), "Q/Esc") {
			t.Fatal("board invisible", size)
		}
	}
	b.confirm("Create branch?", func() { t.Fatal("Esc approved operation") })
	_, modal := b.pages.GetFrontPage()
	modal.(*tview.Modal).InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(tview.Primitive) {})
	b.table.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if !back {
		t.Fatal("Esc did not leave board")
	}
}

func TestTaskBoardPTYHelper(t *testing.T) {
	root := os.Getenv("OAISO_TASK_BOARD_HELPER")
	if root == "" {
		t.Skip("isolated terminal helper")
	}
	if err := RunTaskBoard(&taskboard.Service{Store: taskboard.Store{Root: filepath.Join(root, "private")}}); err != nil {
		t.Fatal(err)
	}
}
func TestTaskBoardThroughPTY(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux PTY")
	}
	python, e := exec.LookPath("python3")
	if e != nil {
		t.Fatal(e)
	}
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, python, "testdata/task-board-probe.py", binary, t.TempDir())
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("task board PTY: %v\n%s", e, b)
	}
	t.Log(string(b))
}

type taskPasteEvents struct {
	tcell.Screen
	events []tcell.Event
}

func (s *taskPasteEvents) PollEvent() tcell.Event {
	if len(s.events) == 0 {
		return nil
	}
	e := s.events[0]
	s.events = s.events[1:]
	return e
}
func TestTaskPasteLFAndCRLFWithoutChangingCtrlJShortcut(t *testing.T) {
	for _, newline := range [][]tcell.Key{{tcell.KeyCtrlJ}, {tcell.KeyEnter, tcell.KeyCtrlJ}, {tcell.KeyEnter}} {
		events := []tcell.Event{tcell.NewEventPaste(true), tcell.NewEventKey(tcell.KeyRune, 'a', 0)}
		for _, key := range newline {
			events = append(events, tcell.NewEventKey(key, 0, 0))
		}
		events = append(events, tcell.NewEventKey(tcell.KeyRune, 'b', 0), tcell.NewEventPaste(false), tcell.NewEventKey(tcell.KeyCtrlJ, 0, 0))
		screen := &pasteScreen{Screen: &taskPasteEvents{events: events}}
		var keys []tcell.Key
		for {
			e := screen.PollEvent()
			if e == nil {
				break
			}
			if k, ok := e.(*tcell.EventKey); ok {
				keys = append(keys, k.Key())
			}
		}
		if !reflect.DeepEqual(keys, []tcell.Key{tcell.KeyRune, tcell.KeyEnter, tcell.KeyRune, tcell.KeyCtrlJ}) {
			t.Fatalf("newline %v became %v", newline, keys)
		}
	}
}
