package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/rivo/tview"
)

type browserFiles struct {
	mu       sync.Mutex
	listings map[string][]sandboxux.FileEntry
	reads    []string
	readLog  string
	lists    int
}

func (f *browserFiles) ListFileEntries(_ context.Context, _, dir string, out io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lists++
	entries, ok := f.listings[dir]
	if !ok {
		return fmt.Errorf("directory no longer exists")
	}
	return json.NewEncoder(out).Encode(entries)
}
func (f *browserFiles) ReadFile(_ context.Context, _, name string, out io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, name)
	if f.readLog != "" {
		data, err := json.Marshal(f.reads)
		if err != nil {
			return err
		}
		if err := os.WriteFile(f.readLog, data, 0600); err != nil {
			return err
		}
	}
	_, err := io.WriteString(out, "literal file contents")
	return err
}
func (*browserFiles) CreateFile(context.Context, string, string, io.Reader) error { return nil }

func runBrowser(t *testing.T, guest *browserFiles) (*sandboxDesktop, func(func()), func()) {
	t.Helper()
	d, app, _ := testDesktop(t)
	d.backend.Files = sandboxux.Files{Guest: guest}
	screen := tcell.NewSimulationScreen("")
	app.SetScreen(screen)
	screen.SetSize(100, 30)
	ready := make(chan struct{})
	var once sync.Once
	app.SetAfterDrawFunc(func(tcell.Screen) { once.Do(func() { close(ready) }) })
	done := make(chan error, 1)
	go func() { done <- app.Run() }()
	t.Cleanup(func() {
		app.Stop()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("browser event loop did not stop")
		}
	})
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("browser event loop did not start")
	}
	update := func(fn func()) { app.QueueUpdateDraw(fn) }
	wait := func() {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			idle := false
			update(func() { idle = !d.busy })
			if idle {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("browser operation did not finish")
			}
			time.Sleep(time.Millisecond)
		}
	}
	update(func() { d.files(sandbox.Worker{ID: "worker", Name: "sandbox", Status: "running"}) })
	wait()
	return d, update, wait
}

func browserKey(d *sandboxDesktop, key tcell.Key, r rune) {
	d.fileTable.InputHandler()(tcell.NewEventKey(key, r, 0), func(p tview.Primitive) { d.app.SetFocus(p) })
}

func TestFileBrowserFailedNavigationKeepsPathsAndRowsTogether(t *testing.T) {
	guest := &browserFiles{listings: map[string][]sandboxux.FileEntry{".": {{Name: "gone", Kind: "directory"}, {Name: "root.txt", Kind: "file"}}}}
	d, update, wait := runBrowser(t, guest)
	update(func() { d.fileTable.Select(1, 0); browserKey(d, tcell.KeyEnter, 0) })
	wait()
	update(func() {
		if name, _ := d.pages.GetFrontPage(); name != "message" {
			t.Error("failed directory read did not display an error", name)
		}
		if d.directory != "." {
			t.Error("failed read changed directory while retaining old rows:", d.directory)
		}
		_, modal := d.pages.GetFrontPage()
		modal.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(p tview.Primitive) { d.app.SetFocus(p) })
		d.fileTable.Select(2, 0)
		browserKey(d, tcell.KeyEnter, 0)
	})
	wait()
	guest.mu.Lock()
	defer guest.mu.Unlock()
	if len(guest.reads) != 1 || guest.reads[0] != "root.txt" {
		t.Fatal("opening an unchanged row targeted another path", guest.reads)
	}
}

func TestFileBrowserRefreshKeepsSelectedNameWhenRowsMove(t *testing.T) {
	guest := &browserFiles{listings: map[string][]sandboxux.FileEntry{".": {{Name: "b.txt", Kind: "file"}, {Name: "日本語.txt", Kind: "file"}}}}
	d, update, wait := runBrowser(t, guest)
	update(func() { d.fileTable.Select(2, 0) })
	guest.mu.Lock()
	guest.listings["."] = append(guest.listings["."], sandboxux.FileEntry{Name: "a.txt", Kind: "file"})
	guest.mu.Unlock()
	update(func() { browserKey(d, tcell.KeyRune, 'g') })
	wait()
	update(func() {
		row, _ := d.fileTable.GetSelection()
		if row < 1 || row > len(d.entries) || d.entries[row-1].Name != "日本語.txt" {
			t.Errorf("refresh selected a different file: row %d, entries %+v", row, d.entries)
		}
	})
}

func TestFileBrowserParentSelectsTheDirectoryJustLeft(t *testing.T) {
	guest := &browserFiles{listings: map[string][]sandboxux.FileEntry{".": {{Name: "a", Kind: "directory"}, {Name: "z", Kind: "directory"}}, "z": {{Name: "child.txt", Kind: "file"}}}}
	d, update, wait := runBrowser(t, guest)
	update(func() { d.fileTable.Select(2, 0); browserKey(d, tcell.KeyEnter, 0) })
	wait()
	update(func() { browserKey(d, tcell.KeyBackspace2, 0) })
	wait()
	update(func() {
		row, _ := d.fileTable.GetSelection()
		if d.directory != "." || row < 1 || row > len(d.entries) || d.entries[row-1].Name != "z" {
			t.Errorf("returning to parent lost the directory selection: %s row %d", d.directory, row)
		}
	})
}

func TestFileBrowserCanRetryInitialFailureAndBrowseEmptyDirectory(t *testing.T) {
	guest := &browserFiles{listings: map[string][]sandboxux.FileEntry{}}
	d, update, wait := runBrowser(t, guest)
	update(func() {
		_, modal := d.pages.GetFrontPage()
		modal.InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, 0), func(p tview.Primitive) { d.app.SetFocus(p) })
	})
	guest.mu.Lock()
	guest.listings["."] = []sandboxux.FileEntry{{Name: "empty", Kind: "directory"}}
	guest.listings["empty"] = []sandboxux.FileEntry{}
	guest.mu.Unlock()
	update(func() { browserKey(d, tcell.KeyRune, 'g') })
	wait()
	update(func() { browserKey(d, tcell.KeyEnter, 0) })
	wait()
	update(func() {
		if d.directory != "empty" || len(d.entries) != 0 || d.fileTable.GetRowCount() != 1 {
			t.Error("empty directory retained stale rows", d.directory, d.entries)
		}
		browserKey(d, tcell.KeyEnter, 0)
		browserKey(d, tcell.KeyRune, 'd')
		browserKey(d, tcell.KeyBackspace2, 0)
	})
	wait()
	update(func() {
		if d.directory != "." || len(d.entries) != 1 {
			t.Error("could not leave empty directory", d.directory, d.entries)
		}
	})
	guest.mu.Lock()
	defer guest.mu.Unlock()
	if len(guest.reads) != 0 {
		t.Fatal("empty-directory actions read a stale file", guest.reads)
	}
}

func TestFileBrowserPTYHelper(t *testing.T) {
	root := os.Getenv("OAISO_FILES_PTY_HELPER")
	if root == "" {
		t.Skip("isolated terminal helper")
	}
	app := tview.NewApplication()
	guest := &browserFiles{listings: map[string][]sandboxux.FileEntry{".": {{Name: "gone", Kind: "directory"}, {Name: "z", Kind: "directory"}, {Name: "root.txt", Kind: "file"}}, "z": {{Name: "child.txt", Kind: "file"}}}, readLog: filepath.Join(root, "reads.json")}
	d := newSandboxDesktop(app, t.Context(), desktopBackend{Files: sandboxux.Files{Guest: guest}}, app.Stop, func() {})
	defer d.close()
	app.SetAfterDrawFunc(func(tcell.Screen) {
		guest.mu.Lock()
		calls := guest.lists
		guest.mu.Unlock()
		page, _ := d.pages.GetFrontPage()
		data, err := json.Marshal(map[string]any{"lists": calls, "busy": d.busy, "page": page})
		if err == nil {
			_ = os.WriteFile(filepath.Join(root, "state.json"), data, 0600)
		}
	})
	app.SetRoot(d.pages, true)
	d.files(sandbox.Worker{ID: "worker", Name: "browser-fixture", Status: "running"})
	if err := runTerminal(app); err != nil {
		t.Fatal(err)
	}
}

func TestFileBrowserThroughPTY(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux PTY")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", "testdata/file-browser-probe.py", binary, t.TempDir())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("file browser PTY: %v\n%s", err, output)
	}
	t.Log(string(output))
}
