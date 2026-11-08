package ui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/kataguard"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/pacificbelt30/codex-dock/internal/storageinfo"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
	"github.com/rivo/tview"
)

func (d *sandboxDesktop) reviewPage(name, title, text string, approve, cancel func()) {
	view := tview.NewTextView().SetDynamicColors(false).SetScrollable(true).SetText(text)
	view.SetBorder(true).SetTitle(title)
	buttons := tview.NewForm().AddButton("Cancel", cancel).AddButton("Confirm", approve).SetButtonsAlign(tview.AlignCenter)
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(buttons, 3, 0, false).AddItem(tview.NewTextView().SetText("↑↓ / PgUp / PgDn scroll · Tab switches to buttons · Esc cancels"), 1, 0, false)
	body.SetInputCapture(d.pageFormNavigation(view, buttons, cancel))
	d.pages.AddAndSwitchToPage(name, body, true)
	d.app.SetFocus(view)
}

func (d *sandboxDesktop) pageFormNavigation(view tview.Primitive, form *tview.Form, back func()) func(*tcell.EventKey) *tcell.EventKey {
	return func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == tcell.KeyEscape {
			back()
			return nil
		}
		if e.Key() != tcell.KeyTab && e.Key() != tcell.KeyBacktab {
			return e
		}
		if view.HasFocus() {
			i := 0
			if e.Key() == tcell.KeyBacktab {
				i = form.GetFormItemCount() + form.GetButtonCount() - 1
			}
			form.SetFocus(i)
			d.app.SetFocus(form)
			return nil
		}
		item, button := form.GetFocusedItemIndex()
		if (e.Key() == tcell.KeyTab && button == form.GetButtonCount()-1) || (e.Key() == tcell.KeyBacktab && (item == 0 || (form.GetFormItemCount() == 0 && button == 0))) {
			d.app.SetFocus(view)
			return nil
		}
		return e
	}
}

func (d *sandboxDesktop) saveEnvironment(spec sandboxux.LaunchSpec, original *tview.Form) {
	if d.environments == nil {
		d.message("Environment storage is unavailable.")
		return
	}
	snapshot, err := d.environments.Load()
	if err != nil {
		d.message(err.Error())
		return
	}
	name := ""
	back := func() { d.pages.SwitchToPage("create"); d.app.SetFocus(original) }
	form := tview.NewForm().AddInputField("Environment name", "", 42, nil, func(v string) { name = v })
	form.AddTextView("Saved settings", "Includes the command, image, resource profile, workspace mode, access, egress and lifetime. No AI credentials, AI enablement, extra mounts, project/copy paths or worker names. Saved commands may contain sensitive text: review before saving.", 56, 5, false, false)
	form.AddButton("Review save", func() {
		name = strings.TrimSpace(name)
		environment := sandboxux.EnvironmentFrom(spec)
		overwrite := "new environment"
		if _, exists := snapshot.File.Environments[name]; exists {
			overwrite = "REPLACE existing environment"
		}
		d.reviewPage("environment-review", " Save environment ", fmt.Sprintf("%s: %s\nPrivate file: %s\n\n%s", overwrite, name, d.environments.Path, environmentSummary(environment)), func() {
			snapshot.File.Environments[name] = environment
			if e := d.environments.Save(snapshot.Revision, snapshot.File); e != nil {
				d.message(e.Error() + "; draft retained, reopen Save environment to reload")
				return
			}
			back()
			d.message("Saved environment " + name + ". Loading it never launches a VM.")
		}, func() { d.pages.SwitchToPage("save-environment"); d.app.SetFocus(form) })
	}).AddButton("Cancel", back)
	form.SetBorder(true).SetTitle(" Save environment · Esc cancels ")
	d.cancelForm(form, back)
	d.pages.AddAndSwitchToPage("save-environment", form, true)
	d.app.SetFocus(form)
}

func (d *sandboxDesktop) loadEnvironment(spec sandboxux.LaunchSpec, profiles []kataguard.ResourceProfile, storage storageinfo.Status, storageErr error, original *tview.Form) {
	if d.environments == nil {
		d.message("Environment storage is unavailable.")
		return
	}
	snapshot, err := d.environments.Load()
	if err != nil {
		d.message(err.Error())
		return
	}
	if len(snapshot.File.Environments) == 0 {
		d.message("No saved environments. Fill in settings and choose Save environment.")
		return
	}
	back := func() { d.pages.SwitchToPage("create"); d.app.SetFocus(original) }
	list := tview.NewList().ShowSecondaryText(true)
	list.SetBorder(true).SetTitle(" Saved environments · Enter review · D delete · Esc back ")
	for _, name := range snapshot.Names() {
		e := snapshot.File.Environments[name]
		list.AddItem(name, fmt.Sprintf("image=%s · network=%s · workspace=%s", e.Image, e.Network, e.Workspace), 0, func() {
			d.reviewPage("environment-review", " Load environment ", "Load "+name+"?\n\n"+environmentSummary(e)+"\n\nAI remains disabled. Extra mounts and copy destination are cleared. Check the command and choose a project/copy destination, then review launch separately.", func() { d.createFormSpec(profiles, storage, storageErr, e.Apply(spec)) }, func() { d.pages.SwitchToPage("load-environment"); d.app.SetFocus(list) })
		})
	}
	list.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyEscape {
			back()
			return nil
		}
		if event.Rune() == 'd' || event.Rune() == 'D' {
			name, _ := list.GetItemText(list.GetCurrentItem())
			d.confirmation("Delete saved environment "+name+"? Existing VMs and their files remain unchanged.", func() {
				delete(snapshot.File.Environments, name)
				if err := d.environments.Save(snapshot.Revision, snapshot.File); err != nil {
					d.message(err.Error())
					return
				}
				back()
				d.message("Deleted environment " + name)
			}, func() { d.pages.SwitchToPage("load-environment"); d.app.SetFocus(list) })
			return nil
		}
		return event
	})
	d.pages.AddAndSwitchToPage("load-environment", list, true)
	d.app.SetFocus(list)
}

func environmentSummary(e sandboxux.Environment) string {
	value := func(v string) string {
		if v == "" {
			return "configured default"
		}
		return v
	}
	access := func(v *bool) string {
		if v == nil {
			return "configured default"
		}
		if *v {
			return "read-only"
		}
		return "writable"
	}
	command := e.Command
	if command == "" {
		command = "keep VM alive; connect to a shell after creation"
	}
	workspace := e.Workspace
	switch workspace {
	case "", "shared":
		workspace = "share selected host project"
	case "copy":
		workspace = "independent committed Git copy; choose a new destination"
	case "managed":
		workspace = "managed current files; secrets/cache/links excluded"
	default:
		workspace = "existing managed workspace; choose an unleased ID"
	}
	lifetime := "host/project defaults"
	if e.LifecycleSet || e.Lifecycle != (workerpolicy.Lifecycle{}) {
		lifetime = fmt.Sprintf("maximum %s · idle %s · retention %s · protect main %t", e.Lifecycle.MaxLifetime, e.Lifecycle.IdleTimeout, e.Lifecycle.Retention, e.Lifecycle.ProtectMain)
	}
	if e.Workspace == "managed" || e.Workspace == "existing" {
		workspace += fmt.Sprintf(" · %d MiB / %d inodes (zero uses defaults)", e.WorkspaceBytes>>20, e.WorkspaceInodes)
	}
	return fmt.Sprintf("Image: %s\nResource profile: %s\nWorkspace: %s\nProject access: %s\nRootfs: %s\nInternet: %s\nAllowed domains: %s\nLifetime: %s\n\nStartup command:\n%s", value(e.Image), value(e.Profile), workspace, access(e.ReadOnly), access(e.ReadOnlyRoot), e.Network, strings.Join(e.Domains, ", "), lifetime, command)
}
