package ui

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/kataguard"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/pacificbelt30/codex-dock/internal/storageinfo"
	"github.com/rivo/tview"
)

type desktopTestFiles struct{}

func (desktopTestFiles) ListFileEntries(_ context.Context, _, _ string, w io.Writer) error {
	_, e := io.WriteString(w, `[{"name":"日本語 [red].txt","kind":"file","size":4},{"name":"link","kind":"symlink","size":4}]`)
	return e
}
func (desktopTestFiles) ReadFile(_ context.Context, _, _ string, w io.Writer) error {
	_, e := io.WriteString(w, "[red]literal 日本語")
	return e
}
func (desktopTestFiles) CreateFile(_ context.Context, _, _ string, _ io.Reader) error { return nil }
func testDesktop(t *testing.T) (*sandboxDesktop, *tview.Application, *bool) {
	t.Helper()
	app := tview.NewApplication()
	back := false
	backend := desktopBackend{Files: sandboxux.Files{Guest: desktopTestFiles{}}, Profiles: func(context.Context) ([]kataguard.ResourceProfile, error) { return kataguard.ResourceProfiles(), nil }, Storage: func(context.Context) (storageinfo.Status, error) {
		return storageinfo.Status{Enforced: true, WorkerLimitBytes: 4 << 30, WorkerLimitInodes: 1000}, nil
	}}
	d := newSandboxDesktop(app, t.Context(), backend, func() { back = true }, func() {})
	app.SetRoot(d.pages, true)
	t.Cleanup(d.close)
	return d, app, &back
}
func TestSandboxCreationReviewAndCancelKeepAgentDisabled(t *testing.T) {
	d, _, back := testDesktop(t)
	d.createForm(kataguard.ResourceProfiles(), storageinfo.Status{WorkerLimitBytes: 4 << 30}, nil)
	_, page := d.pages.GetFrontPage()
	form := page.(*tview.Flex).GetItem(0).(*tview.Form)
	form.GetFormItem(0).(*tview.InputField).SetText("sandbox-name")
	form.GetFormItem(1).(*tview.InputField).SetText(t.TempDir())
	form.GetFormItem(3).(*tview.TextArea).SetText("echo 日本語\nprintf '[red]literal'", true)
	form.GetButton(0).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(tview.Primitive) {})
	if name, _ := d.pages.GetFrontPage(); name != "confirm" {
		t.Fatal("creation skipped review", name)
	}
	_, modal := d.pages.GetFrontPage()
	modal.(*tview.Flex).GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if name, _ := d.pages.GetFrontPage(); name != "create" || *back {
		t.Fatal("cancel did not return to creation form")
	}
	form.GetInputCapture()(tcell.NewEventKey(tcell.KeyEscape, 0, 0))
	if !*back {
		t.Fatal("Esc did not leave creation")
	}
}
func TestSandboxCreationSmallScreenAndLiteralText(t *testing.T) {
	d, _, _ := testDesktop(t)
	d.createForm(kataguard.ResourceProfiles(), storageinfo.Status{}, nil)
	screen := tcell.NewSimulationScreen("")
	if e := screen.Init(); e != nil {
		t.Fatal(e)
	}
	defer screen.Fini()
	for _, size := range [][2]int{{120, 40}, {80, 24}, {60, 22}} {
		screen.SetSize(size[0], size[1])
		d.pages.SetRect(0, 0, size[0], size[1])
		d.pages.Draw(screen)
		screen.Show()
		cells, _, _ := screen.GetContents()
		var text strings.Builder
		for _, c := range cells {
			text.Write(c.Bytes)
		}
		if !strings.Contains(text.String(), "New sandbox") {
			t.Fatal("creation page invisible", size)
		}
		_, page := d.pages.GetFrontPage()
		form := page.(*tview.Flex).GetItem(0).(*tview.Form)
		form.SetFocus(form.GetFormItemCount())
		d.pages.Draw(screen)
		screen.Show()
		cells, _, _ = screen.GetContents()
		text.Reset()
		for _, c := range cells {
			text.Write(c.Bytes)
		}
		if !strings.Contains(text.String(), "Review") {
			t.Fatal("creation review button cannot be reached on a small screen", size)
		}
	}
}
func TestPreviewsPruneExpiredURLsAndCloseOnExit(t *testing.T) {
	d, _, _ := testDesktop(t)
	active, expired := make(chan struct{}), make(chan struct{})
	close(expired)
	closed := false
	d.previews = []desktopPreview{{URL: "expired-private-url", Done: expired, Close: func() error { return nil }}, {URL: "active-private-url", Done: active, Close: func() error { closed = true; close(active); return nil }}}
	d.prunePreviews()
	if len(d.previews) != 1 || d.previews[0].URL != "active-private-url" {
		t.Fatal("expired URL retained")
	}
	d.close()
	d.previews = nil
	if !closed {
		t.Fatal("TUI exit did not close preview")
	}
}

type recordingDesktopLauncher struct{ path string }

func (l recordingDesktopLauncher) Launch(_ context.Context, s sandboxux.LaunchSpec) (sandboxux.LaunchResult, error) {
	b, e := json.Marshal(s)
	if e != nil {
		return sandboxux.LaunchResult{}, e
	}
	if e = os.WriteFile(l.path, b, 0600); e != nil {
		return sandboxux.LaunchResult{}, e
	}
	return sandboxux.LaunchResult{ID: "created-worker-id", Name: s.Name}, nil
}
func TestSandboxDesktopPTYHelper(t *testing.T) {
	root := os.Getenv("OAISO_SANDBOX_DESKTOP_HELPER")
	if root == "" {
		t.Skip("isolated terminal helper")
	}
	app := tview.NewApplication()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d := newSandboxDesktop(app, ctx, desktopBackend{Launcher: recordingDesktopLauncher{filepath.Join(root, "launched.json")}, Profiles: func(context.Context) ([]kataguard.ResourceProfile, error) { return kataguard.ResourceProfiles(), nil }, Storage: func(context.Context) (storageinfo.Status, error) {
		return storageinfo.Status{Enforced: true, WorkerLimitBytes: 4 << 30}, nil
	}}, app.Stop, func() {})
	d.environments = &sandboxux.EnvironmentStore{Path: filepath.Join(root, "private", "environments.json")}
	defer d.close()
	d.createForm(kataguard.ResourceProfiles(), storageinfo.Status{Enforced: true, WorkerLimitBytes: 4 << 30}, nil)
	app.SetRoot(d.pages, true)
	if err := runTerminal(app); err != nil {
		t.Fatal(err)
	}
}
func TestSandboxDesktopThroughPTY(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, python, "testdata/sandbox-create-probe.py", binary, t.TempDir())
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("sandbox create PTY: %v\n%s", e, b)
	}
	t.Log(string(b))
}
