package ui

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/rivo/tview"
)

type pickerPlanRecorder struct{ paths chan []string }

func (r pickerPlanRecorder) PlanArtifacts(_ context.Context, id string, options sandbox.ArtifactOptions) (sandbox.ArtifactPlan, error) {
	r.paths <- options.Paths
	return sandbox.ArtifactPlan{Worker: id, ID: strings.Repeat("a", 64)}, nil
}
func (pickerPlanRecorder) ExportArtifacts(context.Context, string, sandbox.ArtifactOptions, io.Writer) error {
	return fmt.Errorf("unexpected export before review approval")
}

func TestArtifactPickerReviewPreservesWhitespaceInSelectedNames(t *testing.T) {
	name := " result 日本語.txt "
	d, update, wait := runBrowser(t, &browserFiles{listings: map[string][]sandboxux.FileEntry{".": {{Name: name, Kind: "file"}}}})
	plans := make(chan []string, 1)
	update(func() {
		d.backend.Artifacts = pickerPlanRecorder{plans}
		d.exportArtifacts(sandbox.Worker{ID: "worker"})
		_, form := d.pages.GetFrontPage()
		form.(*tview.Form).GetButton(1).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	})
	wait()
	update(func() {
		_, page := d.pages.GetFrontPage()
		body := page.(*tview.Flex)
		body.GetItem(0).(*tview.Table).InputHandler()(tcell.NewEventKey(tcell.KeyRune, ' ', 0), func(p tview.Primitive) { d.app.SetFocus(p) })
		body.GetItem(2).(*tview.Form).GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
		_, form := d.pages.GetFrontPage()
		form.(*tview.Form).GetButton(1).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	})
	wait()
	update(func() {
		_, page := d.pages.GetFrontPage()
		body := page.(*tview.Flex)
		if body.GetItem(0).(*tview.Table).GetCell(1, 0).Text != tview.Escape("[x]") {
			t.Error("reopening the picker lost the exact selected name")
		}
		body.GetItem(2).(*tview.Form).GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
		_, form := d.pages.GetFrontPage()
		form.(*tview.Form).GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	})
	wait()
	if got := <-plans; !slices.Equal(got, []string{name}) {
		t.Fatal("review trimmed the selected filename", got)
	}
}

func TestArtifactPickerSelectsAcrossDirectoriesAndExcludesUnsafeEntries(t *testing.T) {
	guest := &browserFiles{listings: map[string][]sandboxux.FileEntry{
		".":   {{Name: ".env", Kind: "file"}, {Name: ".ssh", Kind: "directory"}, {Name: "link", Kind: "symlink"}, {Name: "dir", Kind: "directory"}, {Name: "root.txt", Kind: "file"}},
		"dir": {{Name: "日本語.txt", Kind: "file"}, {Name: "private.key", Kind: "file"}},
	}}
	d, update, wait := runBrowser(t, guest)
	var selected []string
	update(func() {
		d.chooseArtifacts(sandbox.Worker{ID: "worker"}, "", func(paths []string) { selected = paths }, func() {})
	})
	wait()
	update(func() {
		_, page := d.pages.GetFrontPage()
		table := page.(*tview.Flex).GetItem(0).(*tview.Table)
		if table.GetRowCount() != 3 || table.GetCell(1, 1).Text != "dir" || table.GetCell(2, 1).Text != "root.txt" {
			t.Error("picker offered excluded paths", table.GetRowCount())
		}
		table.Select(2, 0)
		table.InputHandler()(tcell.NewEventKey(tcell.KeyRune, ' ', 0), func(p tview.Primitive) { d.app.SetFocus(p) })
		table.Select(1, 0)
		table.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(p tview.Primitive) { d.app.SetFocus(p) })
	})
	wait()
	update(func() {
		_, page := d.pages.GetFrontPage()
		body := page.(*tview.Flex)
		table := body.GetItem(0).(*tview.Table)
		if table.GetRowCount() != 2 {
			t.Error("nested credential file was offered")
		}
		table.InputHandler()(tcell.NewEventKey(tcell.KeyRune, ' ', 0), func(p tview.Primitive) { d.app.SetFocus(p) })
		body.GetItem(2).(*tview.Form).GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	})
	if !slices.Equal(selected, []string{"dir/日本語.txt", "root.txt"}) {
		t.Fatal("selection lost or included another file", selected)
	}
}

func TestArtifactPickerEscapeKeepsOriginalSelection(t *testing.T) {
	d, update, wait := runBrowser(t, &browserFiles{listings: map[string][]sandboxux.FileEntry{".": {{Name: "new.txt", Kind: "file"}}}})
	accepted, cancelled := false, false
	update(func() {
		d.chooseArtifacts(sandbox.Worker{ID: "worker"}, "original.txt", func([]string) { accepted = true }, func() { cancelled = true })
	})
	wait()
	update(func() {
		_, page := d.pages.GetFrontPage()
		body := page.(*tview.Flex)
		table := body.GetItem(0).(*tview.Table)
		table.InputHandler()(tcell.NewEventKey(tcell.KeyRune, ' ', 0), func(p tview.Primitive) { d.app.SetFocus(p) })
		body.InputHandler()(tcell.NewEventKey(tcell.KeyEscape, 0, 0), func(p tview.Primitive) { d.app.SetFocus(p) })
	})
	if accepted || !cancelled {
		t.Fatal("Escape applied the picker draft")
	}
}
