package ui

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/kataguard"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/pacificbelt30/codex-dock/internal/storageinfo"
	"github.com/rivo/tview"
)

type desktopPreview struct {
	URL    string
	Done   <-chan struct{}
	Close  func() error
	Worker string
	Port   int
}
type desktopBackend struct {
	Launcher sandboxux.Launcher
	Files    sandboxux.Files
	Profiles func(context.Context) ([]kataguard.ResourceProfile, error)
	Storage  func(context.Context) (storageinfo.Status, error)
	Preview  func(context.Context, string, int, time.Duration) (desktopPreview, error)
}
type sandboxDesktop struct {
	uploadMu       sync.Mutex
	closed         bool
	previewRefresh func()
	app            *tview.Application
	pages          *tview.Pages
	ctx            context.Context
	backend        desktopBackend
	back, changed  func()
	busy           bool
	previews       []desktopPreview
	uploads        map[*sandboxux.Upload]bool
	worker         sandbox.Worker
	directory      string
	entries        []sandboxux.FileEntry
	fileTable      *tview.Table
	fileStatus     *tview.TextView
}

func managerDesktop(cfg sandbox.ManagerConfig, m *sandbox.Manager, launcher sandboxux.Launcher) desktopBackend {
	socket := cfg.Socket
	if socket == "" {
		socket = engine.Socket()
	}
	return desktopBackend{Launcher: launcher, Files: sandboxux.Files{Guest: m}, Profiles: m.ResourceProfiles, Storage: func(ctx context.Context) (storageinfo.Status, error) { return engine.StorageStatus(ctx, socket) }, Preview: func(ctx context.Context, id string, port int, lifetime time.Duration) (desktopPreview, error) {
		p, e := m.Preview(ctx, id, port, lifetime)
		if e != nil {
			return desktopPreview{}, e
		}
		return desktopPreview{URL: p.URL, Done: p.Done(), Close: p.Close, Worker: id, Port: port}, nil
	}}
}
func newSandboxDesktop(app *tview.Application, ctx context.Context, backend desktopBackend, back, changed func()) *sandboxDesktop {
	d := &sandboxDesktop{app: app, pages: tview.NewPages(), ctx: ctx, backend: backend, changed: changed, uploads: map[*sandboxux.Upload]bool{}}
	d.back = func() {
		if !d.busy {
			back()
		}
	}
	return d
}
func (d *sandboxDesktop) close() {
	for _, p := range d.previews {
		_ = p.Close()
	}
	d.uploadMu.Lock()
	d.closed = true
	var uploads []*sandboxux.Upload
	for u := range d.uploads {
		uploads = append(uploads, u)
	}
	d.uploads = map[*sandboxux.Upload]bool{}
	d.uploadMu.Unlock()
	for _, u := range uploads {
		_ = u.Close()
	}
}
func (d *sandboxDesktop) trackUpload(u *sandboxux.Upload) error {
	d.uploadMu.Lock()
	defer d.uploadMu.Unlock()
	if d.closed {
		_ = u.Close()
		return context.Canceled
	}
	d.uploads[u] = true
	return nil
}
func (d *sandboxDesktop) releaseUpload(u *sandboxux.Upload) {
	d.uploadMu.Lock()
	delete(d.uploads, u)
	d.uploadMu.Unlock()
	_ = u.Close()
}
func (d *sandboxDesktop) message(text string) {
	previous, primitive := d.pages.GetFrontPage()
	modal := tview.NewModal().SetText(tview.Escape(text)).AddButtons([]string{"Close"}).SetDoneFunc(func(_ int, _ string) {
		d.pages.RemovePage("message")
		d.pages.SwitchToPage(previous)
		if primitive != nil {
			d.app.SetFocus(primitive)
		} else {
			d.back()
		}
	})
	d.pages.AddAndSwitchToPage("message", modal, true)
	d.app.SetFocus(modal)
}
func (d *sandboxDesktop) work(fn func(context.Context) (func(), error)) {
	if d.busy {
		return
	}
	d.busy = true
	previous, primitive := d.pages.GetFrontPage()
	working := tview.NewTextView().SetText("Working…").SetDynamicColors(false)
	working.SetBorder(true).SetTitle(" Sandbox operation ")
	d.pages.AddAndSwitchToPage("working", working, true)
	d.app.SetFocus(working)
	go func() {
		ctx, cancel := context.WithTimeout(d.ctx, 10*time.Minute)
		defer cancel()
		next, err := fn(ctx)
		if d.ctx.Err() != nil {
			return
		}
		d.app.QueueUpdateDraw(func() {
			d.busy = false
			d.pages.RemovePage("working")
			d.pages.SwitchToPage(previous)
			if primitive != nil {
				d.app.SetFocus(primitive)
			}
			if err != nil {
				d.message(err.Error())
			} else if next != nil {
				next()
			}
		})
	}()
}
func (d *sandboxDesktop) confirmation(text string, yes, no func()) {
	previous, primitive := d.pages.GetFrontPage()
	restore := func() {
		d.pages.RemovePage("confirm")
		d.pages.SwitchToPage(previous)
		if primitive != nil {
			d.app.SetFocus(primitive)
		}
	}
	modal := confirmationModal(text, func() { restore(); yes() }, func() { restore(); no() })
	d.pages.AddAndSwitchToPage("confirm", modal, true)
	d.app.SetFocus(modal)
}
func (d *sandboxDesktop) cancelForm(form *tview.Form, back func()) {
	form.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			if !d.busy {
				back()
			}
			return nil
		}
		return e
	})
}
func readMode(index int) *bool {
	if index == 0 {
		return nil
	}
	v := index == 1
	return &v
}
func (d *sandboxDesktop) create() {
	if d.backend.Launcher == nil {
		d.message("Sandbox creation is unavailable for this dashboard connection.")
		return
	}
	loading := tview.NewTextView().SetText("Loading node profiles and storage…")
	loading.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			d.back()
			return nil
		}
		return e
	})
	d.pages.AddAndSwitchToPage("loading", loading, true)
	d.work(func(ctx context.Context) (func(), error) {
		profiles, err := d.backend.Profiles(ctx)
		if err != nil {
			return nil, err
		}
		storage, storageErr := d.backend.Storage(ctx)
		return func() { d.createForm(profiles, storage, storageErr) }, nil
	})
}
func (d *sandboxDesktop) createForm(profiles []kataguard.ResourceProfile, storage storageinfo.Status, storageErr error) {
	project, _ := os.Getwd()
	spec := sandboxux.LaunchSpec{Project: project, Network: "offline"}
	domains, mounts := "", ""
	form := tview.NewForm()
	form.AddInputField("Name (optional)", "", 42, nil, func(v string) { spec.Name = v }).AddInputField("Project /workspace", project, 56, nil, func(v string) { spec.Project = v }).AddInputField("Image (blank: defaults)", "", 48, nil, func(v string) { spec.Image = v }).AddTextArea("Command (blank: keep alive)", "", 56, 3, 32768, func(v string) { spec.Command = v })
	labels := []string{"Host/project default"}
	for _, p := range profiles {
		labels = append(labels, fmt.Sprintf("%s · %d CPU / %.0f MiB guest / %.0f MiB host", p.Name, p.CPUCores, float64(p.GuestMemoryBytes)/(1<<20), float64(p.HostMemoryBytes)/(1<<20)))
	}
	form.AddDropDown("CPU / memory", labels, 0, func(_ string, i int) {
		spec.Profile = ""
		if i > 0 {
			spec.Profile = profiles[i-1].Name
		}
	})
	form.AddDropDown("Project access", []string{"Host/project default", "Read-only", "Read-write"}, 0, func(_ string, i int) { spec.ReadOnly = readMode(i) }).AddDropDown("Image rootfs", []string{"Host/project default", "Read-only", "Writable with node quota"}, 0, func(_ string, i int) { spec.ReadOnlyRoot = readMode(i) })
	form.AddDropDown("Internet access", []string{"Offline", "Host policy", "Dependency registries", "Explicit domains"}, 0, func(_ string, i int) { spec.Network = []string{"offline", "host", "dependencies", "allowlist"}[i] }).AddInputField("Allowed domains (comma)", "", 50, nil, func(v string) { domains = v }).AddCheckbox("Enable AI services (optional)", false, func(v bool) { spec.AI = v }).AddTextArea("Extra mounts (one/line)", "", 56, 2, 8192, func(v string) { mounts = v })
	storageText := fmt.Sprintf("Node rootfs ceiling: %.1f GiB / %d inodes. Shared host folders use host storage.", float64(storage.WorkerLimitBytes)/(1<<30), storage.WorkerLimitInodes)
	if storageErr != nil {
		storageText = "Storage diagnostics: " + storageErr.Error() + ". The launch gate validates the selected rootfs mode."
	}
	form.AddTextView("Storage", storageText, 56, 3, false, false)
	form.AddButton("Review", func() {
		spec.Domains = nil
		for _, v := range strings.Split(domains, ",") {
			if v = strings.TrimSpace(v); v != "" {
				spec.Domains = append(spec.Domains, v)
			}
		}
		spec.Mounts = nil
		for _, v := range strings.Split(mounts, "\n") {
			if v = strings.TrimSpace(v); v != "" {
				spec.Mounts = append(spec.Mounts, v)
			}
		}
		prepared, _, err := sandboxux.PrepareLaunch(spec)
		if err != nil {
			d.message(err.Error())
			return
		}
		spec = prepared
		d.confirmation(launchReview(spec, storageText), func() {
			d.pages.SwitchToPage("create")
			d.work(func(ctx context.Context) (func(), error) {
				result, err := d.backend.Launcher.Launch(ctx, spec)
				return func() {
					d.changed()
					d.message("Created sandbox " + result.Name + "\n" + result.ID + "\nReturn to the instance list to connect or inspect logs.")
				}, err
			})
		}, func() { d.pages.SwitchToPage("create"); d.app.SetFocus(form) })
	}).AddButton("Cancel", d.back)
	form.SetBorder(true).SetTitle(" New sandbox · Esc cancels ")
	d.cancelForm(form, d.back)
	hint := tview.NewTextView().SetDynamicColors(false).SetText("Tab / Shift-Tab: move   Enter: select   Esc: cancel\nSpace: toggle options. Review settings before launch.\nProject: host folder → /workspace. Blank command keeps VM alive.")
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(form, 0, 1, true).AddItem(hint, 4, 0, false)
	d.pages.AddAndSwitchToPage("create", body, true)
	d.app.SetFocus(form)
}
func launchReview(s sandboxux.LaunchSpec, storage string) string {
	mode := func(v *bool) string {
		if v == nil {
			return "configured default"
		}
		if *v {
			return "read-only"
		}
		return "writable"
	}
	command := s.Command
	if command == "" {
		command = "persistent sandbox; use C on the instance list to open a shell"
	}
	profile := s.Profile
	if profile == "" {
		profile = "configured default"
	}
	image := s.Image
	if image == "" {
		image = "configured default"
	}
	return fmt.Sprintf("Create %s?\nProject: %s → /workspace (%s)\nImage: %s | profile: %s\nRootfs: %s\nEgress: %s %s | AI routes: %t\nCommand: %s\nExtra mounts: %s\n%s\nHost/project defaults still apply to unspecified options. No host settings are changed.", s.Name, s.Project, mode(s.ReadOnly), image, profile, mode(s.ReadOnlyRoot), s.Network, strings.Join(s.Domains, ","), s.AI, command, strings.Join(s.Mounts, ", "), storage)
}
func (d *sandboxDesktop) files(worker sandbox.Worker) {
	d.worker = worker
	d.directory = "."
	d.fileTable = tview.NewTable().SetSelectable(true, false).SetFixed(1, 0)
	d.fileStatus = tview.NewTextView().SetDynamicColors(false)
	d.fileTable.SetBorder(true).SetTitle(" Workspace files · " + tview.Escape(worker.Name) + " ")
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(d.fileTable, 0, 1, true).AddItem(d.fileStatus, 2, 0, false).AddItem(tview.NewTextView().SetText("Enter open  Backspace parent  G refresh  D download  U upload new file  Esc back"), 2, 0, false)
	d.pages.AddAndSwitchToPage("files", body, true)
	d.app.SetFocus(d.fileTable)
	d.fileTable.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if d.busy {
			return nil
		}
		row, _ := d.fileTable.GetSelection()
		var entry sandboxux.FileEntry
		if row > 0 && row <= len(d.entries) {
			entry = d.entries[row-1]
		}
		switch {
		case e.Key() == tcell.KeyEscape:
			d.back()
			return nil
		case e.Key() == tcell.KeyBackspace || e.Key() == tcell.KeyBackspace2:
			d.directory = path.Dir(d.directory)
			d.loadFiles()
			return nil
		case e.Rune() == 'g' || e.Rune() == 'G':
			d.loadFiles()
			return nil
		case e.Rune() == 'u' || e.Rune() == 'U':
			d.uploadForm()
			return nil
		case e.Rune() == 'd' || e.Rune() == 'D':
			if entry.Kind == "file" {
				d.downloadForm(path.Join(d.directory, entry.Name))
			}
			return nil
		case e.Key() == tcell.KeyEnter:
			if entry.Kind == "directory" {
				d.directory = path.Join(d.directory, entry.Name)
				d.loadFiles()
			} else if entry.Kind == "file" {
				d.viewFile(path.Join(d.directory, entry.Name))
			} else if entry.Name != "" {
				d.message("Only regular files and real directories can be opened.")
			}
			return nil
		}
		return e
	})
	d.loadFiles()
}
func (d *sandboxDesktop) loadFiles() {
	id, dir := d.worker.ID, d.directory
	d.fileStatus.SetText("Loading /workspace/" + dir)
	d.work(func(ctx context.Context) (func(), error) {
		entries, err := d.backend.Files.List(ctx, id, dir)
		return func() {
			d.entries = entries
			d.fileTable.Clear()
			for c, h := range []string{"Name", "Type", "Bytes"} {
				d.fileTable.SetCell(0, c, tview.NewTableCell(h).SetSelectable(false).SetTextColor(tcell.ColorYellow))
			}
			for i, e := range entries {
				for c, text := range []string{e.Name, e.Kind, strconv.FormatInt(e.Size, 10)} {
					d.fileTable.SetCell(i+1, c, tview.NewTableCell(tview.Escape(text)))
				}
			}
			if len(entries) > 0 {
				d.fileTable.Select(1, 0)
			}
			d.fileStatus.SetText(fmt.Sprintf("/workspace/%s · %d entries · symlinks cannot be opened", dir, len(entries)))
		}, err
	})
}
func (d *sandboxDesktop) viewFile(name string) {
	id := d.worker.ID
	d.work(func(ctx context.Context) (func(), error) {
		text, err := d.backend.Files.Text(ctx, id, name)
		return func() {
			if text.Binary {
				d.message("This file is binary or is not UTF-8. Use Download to retrieve it.")
				return
			}
			view := tview.NewTextView().SetDynamicColors(false).SetWrap(false).SetScrollable(true).SetText(text.Content)
			title := " " + name + " · Esc back "
			if text.Truncated {
				title += "· first 256 KiB only "
			}
			view.SetBorder(true).SetTitle(tview.Escape(title))
			view.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
				if e.Key() == tcell.KeyEscape {
					d.pages.SwitchToPage("files")
					d.app.SetFocus(d.fileTable)
					return nil
				}
				return e
			})
			d.pages.AddAndSwitchToPage("text", view, true)
			d.app.SetFocus(view)
		}, err
	})
}
func (d *sandboxDesktop) downloadForm(guest string) {
	local, _ := os.Getwd()
	local = filepath.Join(local, path.Base(guest))
	form := tview.NewForm().AddInputField("New host file", local, 64, nil, func(v string) { local = v })
	back := func() { d.pages.SwitchToPage("files"); d.app.SetFocus(d.fileTable) }
	form.AddButton("Review", func() {
		destination := local
		d.confirmation("Download /workspace/"+guest+"\nto new host file "+destination+"?\nExisting files and symlink parents are refused.", func() {
			d.work(func(ctx context.Context) (func(), error) {
				err := d.backend.Files.Download(ctx, d.worker.ID, guest, destination)
				return func() { back(); d.fileStatus.SetText("Downloaded to " + destination) }, err
			})
		}, func() { d.pages.SwitchToPage("download"); d.app.SetFocus(form) })
	}).AddButton("Cancel", back)
	form.SetBorder(true).SetTitle(" Download · Esc cancels ")
	d.cancelForm(form, back)
	d.pages.AddAndSwitchToPage("download", form, true)
	d.app.SetFocus(form)
}
func (d *sandboxDesktop) uploadForm() {
	local, guest := "", ""
	form := tview.NewForm().AddInputField("Host source file", local, 64, nil, func(v string) { local = v }).AddInputField("New workspace path", guest, 64, nil, func(v string) { guest = v })
	back := func() { d.pages.SwitchToPage("files"); d.app.SetFocus(d.fileTable) }
	form.AddButton("Review", func() {
		if err := sandboxux.GuestPath(guest, false); err != nil {
			d.message(err.Error())
			return
		}
		destination := path.Join(d.directory, guest)
		if err := sandboxux.GuestPath(destination, false); err != nil {
			d.message(err.Error())
			return
		}
		source := local
		d.work(func(context.Context) (func(), error) {
			u, err := sandboxux.StageUpload(source)
			if err == nil {
				err = d.trackUpload(u)
			}
			return func() {
				closeUpload := func() { d.releaseUpload(u) }
				d.confirmation(fmt.Sprintf("Upload %s\nto new /workspace/%s?\n%d bytes\nSHA-256: %s\nExisting guest files are refused.", source, destination, u.Size, u.SHA256), func() {
					d.work(func(ctx context.Context) (func(), error) {
						defer closeUpload()
						err := d.backend.Files.Upload(ctx, d.worker.ID, destination, u)
						return func() { back(); d.loadFiles() }, err
					})
				}, func() { closeUpload(); d.pages.SwitchToPage("upload"); d.app.SetFocus(form) })
			}, err
		})
	}).AddButton("Cancel", back)
	form.SetBorder(true).SetTitle(" Upload new file · Esc cancels ")
	d.cancelForm(form, back)
	d.pages.AddAndSwitchToPage("upload", form, true)
	d.app.SetFocus(form)
}
func (d *sandboxDesktop) preview(worker sandbox.Worker) {
	port, lifetime := "3000", "30m"
	form := tview.NewForm().AddInputField("Guest loopback port", port, 8, nil, func(v string) { port = v }).AddDropDown("Lifetime", []string{"5m", "15m", "30m", "1h"}, 2, func(v string, _ int) { lifetime = v })
	list := tview.NewList().ShowSecondaryText(true)
	list.SetBorder(true).SetTitle(" Active localhost previews ")
	render := func() {
		selectedURL := ""
		if list.GetItemCount() > 0 {
			_, selectedURL = list.GetItemText(list.GetCurrentItem())
		}
		d.prunePreviews()
		list.Clear()
		for i, p := range d.previews {
			if p.Worker != worker.ID {
				continue
			}
			select {
			case <-p.Done:
				continue
			default:
			}
			index := i
			list.AddItem(fmt.Sprintf("Port %d · select to stop", p.Port), p.URL, 0, func() {
				d.confirmation("Stop this preview and close its connections?", func() {
					d.work(func(context.Context) (func(), error) {
						err := d.previews[index].Close()
						return func() { d.preview(worker) }, err
					})
				}, func() { d.preview(worker) })
			})
			if p.URL == selectedURL {
				list.SetCurrentItem(list.GetItemCount() - 1)
			}
		}
	}
	d.previewRefresh = render
	render()
	form.AddButton("Start", func() {
		number, err := strconv.Atoi(port)
		duration, e := time.ParseDuration(lifetime)
		if err != nil || e != nil || number < 1 || number > 65535 {
			d.message("Choose a port from 1 to 65535.")
			return
		}
		active := 0
		for _, p := range d.previews {
			select {
			case <-p.Done:
			default:
				active++
				if p.Worker == worker.ID && p.Port == number {
					d.message("This port already has an active preview.")
					return
				}
			}
		}
		if active >= 4 {
			d.message("Stop an existing preview before starting another (maximum 4).")
			return
		}
		d.work(func(context.Context) (func(), error) {
			p, err := d.backend.Preview(d.ctx, worker.ID, number, duration)
			return func() { d.previews = append(d.previews, p); d.preview(worker) }, err
		})
	}).AddButton("Stop all for this worker", func() {
		d.confirmation("Stop every preview for "+worker.Name+"?", func() {
			d.work(func(context.Context) (func(), error) {
				for _, p := range d.previews {
					if p.Worker == worker.ID {
						if err := p.Close(); err != nil {
							return nil, err
						}
					}
				}
				return func() { d.preview(worker) }, nil
			})
		}, func() { d.preview(worker) })
	}).AddButton("Refresh", func() { d.preview(worker) }).AddButton("Back", d.back)
	form.SetBorder(true).SetTitle(" Web preview · " + tview.Escape(worker.Name) + " · Esc back ")
	d.cancelForm(form, d.back)
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(form, 13, 0, true).AddItem(list, 0, 1, false).AddItem(tview.NewTextView().SetText("Tab moves through fields, buttons and URLs. Ctrl-L also focuses URLs; Enter stops a selected preview. URLs are private and work in a browser on this host. TUI exit closes previews."), 3, 0, false)
	form.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			if !d.busy {
				d.back()
			}
			return nil
		}
		field, button := form.GetFocusedItemIndex()
		if (e.Key() == tcell.KeyTab && button == form.GetButtonCount()-1) || (e.Key() == tcell.KeyBacktab && field == 0) {
			d.app.SetFocus(list)
			return nil
		}
		if e.Key() == tcell.KeyCtrlL {
			d.app.SetFocus(list)
			return nil
		}
		return e
	})
	list.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			d.back()
			return nil
		}
		if e.Key() == tcell.KeyTab || e.Key() == tcell.KeyBacktab {
			focus := 0
			if e.Key() == tcell.KeyBacktab {
				focus = form.GetFormItemCount() + form.GetButtonCount() - 1
			}
			form.SetFocus(focus)
			d.app.SetFocus(form)
			return nil
		}
		return e
	})
	d.pages.AddAndSwitchToPage("preview", body, true)
	d.app.SetFocus(form)
}

func (d *sandboxDesktop) refreshPreviews() {
	if page, _ := d.pages.GetFrontPage(); page == "preview" && !d.busy && d.previewRefresh != nil {
		d.previewRefresh()
	}
}

func (d *sandboxDesktop) prunePreviews() {
	kept := d.previews[:0]
	for _, p := range d.previews {
		select {
		case <-p.Done:
		default:
			kept = append(kept, p)
		}
	}
	clear(d.previews[len(kept):])
	d.previews = kept
}
