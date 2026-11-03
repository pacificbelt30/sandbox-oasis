package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/storageinfo"
	"github.com/rivo/tview"
)

func lifecycleSummary(s sandbox.LifecycleStatus, now time.Time) string {
	var text strings.Builder
	deadline := func(label string, base time.Time, d time.Duration) {
		if d == 0 {
			fmt.Fprintf(&text, "%s: disabled\n", label)
			return
		}
		if base.IsZero() {
			fmt.Fprintf(&text, "%s: timestamp unavailable\n", label)
			return
		}
		at := base.Add(d)
		remaining := at.Sub(now).Round(time.Second)
		if remaining < 0 {
			remaining = 0
		}
		fmt.Fprintf(&text, "%s: %s · remaining %s\n", label, at.Format(time.RFC3339), remaining)
	}
	deadline("Maximum lifetime (from creation)", s.CreatedAt, s.Policy.MaxLifetime)
	if s.Running && s.ActiveExecutions > 0 && s.Policy.IdleTimeout > 0 {
		fmt.Fprintf(&text, "Idle stop: protected by %d active execution(s)/activity leases\n", s.ActiveExecutions)
	} else {
		deadline("Idle stop (management activity)", s.LastActivity, s.Policy.IdleTimeout)
	}
	if s.Running {
		fmt.Fprintf(&text, "Retention after exit: %s (starts when stopped)\n", s.Policy.Retention)
	} else {
		deadline("Remove after exit", s.FinishedAt, s.Policy.Retention)
	}
	fmt.Fprintf(&text, "Protect running primary process from idle: %t\n", s.Policy.ProtectMain)
	return text.String()
}
func quotaSummary(s storageinfo.Usage) string {
	if s.ReadOnly {
		return "Rootfs: read-only\n"
	}
	if !s.Available {
		return "Rootfs usage: unavailable · " + s.Error + "\n"
	}
	return fmt.Sprintf("Rootfs: %.1f / %.1f MiB · %d / %d inodes\nObserved by host: %s\nShared host folders are outside this rootfs quota.\n", float64(s.UsedBytes)/(1<<20), float64(s.LimitBytes)/(1<<20), s.UsedInodes, s.LimitInodes, s.ObservedAt.Format(time.RFC3339))
}

func (d *sandboxDesktop) limits(worker sandbox.Worker) {
	if d.backend.Lifecycle == nil || d.backend.Usage == nil || d.backend.Reap == nil || d.backend.ApplyDue == nil {
		d.message("Lifetime and quota diagnostics are unavailable.")
		return
	}
	view := tview.NewTextView().SetDynamicColors(false).SetScrollable(true).SetText("Loading node lifecycle and quota…")
	view.SetBorder(true).SetTitle(" Lifetime / storage · " + tview.Escape(worker.Name) + " ")
	watching, _ := d.supervisor.State(worker.ID)
	watchLabel := "Supervise this VM…"
	if watching {
		watchLabel = "Stop TUI supervision"
	}
	form := tview.NewForm().AddButton("Refresh", func() { d.limits(worker) }).AddButton(watchLabel, func() {
		if watching {
			d.supervisor.Watch(worker.ID, false)
			d.limits(worker)
			return
		}
		d.confirmation("Supervise lifecycle for "+worker.Name+" while this TUI is open?\nExpired maximum/idle limits STOP the VM. Expired retention REMOVES it and managed worktrees. Independent copies remain. An already expired policy may act immediately.\nNo other VM is selected. TUI exit ends this supervision; use oaiso reap --watch for persistent enforcement.", func() { d.supervisor.Watch(worker.ID, true); d.limits(worker) }, func() { d.limits(worker) })
	}).AddButton("Review due action…", func() { d.reviewDueAction(worker) }).AddButton("Back", d.back)
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(form, 3, 0, false).AddItem(tview.NewTextView().SetText("Tab switches panes · ↑↓ / PgUp / PgDn scroll · Esc back\nValues captured on refresh. TUI supervision ends on exit."), 2, 0, false)
	body.SetInputCapture(d.pageFormNavigation(view, form, d.back))
	d.pages.AddAndSwitchToPage("limits", body, true)
	d.app.SetFocus(view)
	d.work(func(ctx context.Context) (func(), error) {
		s, lifeErr := d.backend.Lifecycle(ctx, worker.ID)
		usage, usageErr := d.backend.Usage(ctx, worker.ID)
		var text strings.Builder
		if lifeErr != nil {
			fmt.Fprintf(&text, "Lifecycle unavailable: %v\n", lifeErr)
		} else {
			text.WriteString(lifecycleSummary(s, time.Now()))
		}
		text.WriteString("\n")
		if d.backend.ManagedInfo != nil {
			info, err := d.backend.ManagedInfo(ctx, worker.ID)
			if err != nil {
				fmt.Fprintf(&text, "Managed workspace unavailable: %v\n", err)
			} else if info != nil {
				text.WriteString(managedSummary(*info))
				if worker.Status == "running" && d.backend.ManagedUsage != nil {
					observed, e := d.backend.ManagedUsage(ctx, worker.ID)
					if e != nil {
						fmt.Fprintf(&text, "Live usage unavailable: %v\n", e)
					} else {
						text.WriteString(observed + "\n")
					}
				} else {
					text.WriteString("Live usage unavailable while stopped; fixed reservation remains.\n")
				}
				text.WriteString("\n")
			}
		}
		if usageErr != nil {
			fmt.Fprintf(&text, "Rootfs usage unavailable: %v\n", usageErr)
		} else {
			text.WriteString(quotaSummary(usage))
		}
		watch, lastErr := d.supervisor.State(worker.ID)
		fmt.Fprintf(&text, "\nSupervised by this TUI: %t\n", watch)
		if lastErr != "" {
			fmt.Fprintf(&text, "Last supervision error: %s\n", lastErr)
		}
		text.WriteString("\nIdle is management execution/activity, not guest CPU or network activity. Running servers need protect-main or a suitable timeout.\nAn external oaiso reap --watch may also enforce saved policies independently.\nNo policy is applied to other workers by this page.")
		return func() { view.SetText(text.String()) }, nil
	})
}
func (d *sandboxDesktop) reviewDueAction(worker sandbox.Worker) {
	d.work(func(ctx context.Context) (func(), error) {
		actions, err := d.backend.Reap(ctx, time.Now(), true, []string{worker.ID})
		if err != nil {
			return nil, err
		}
		if len(actions) == 0 {
			return func() { d.message("No lifecycle action is due for this VM.") }, nil
		}
		text := fmt.Sprintf("Apply due lifecycle action to %s?\n", worker.Name)
		for _, a := range actions {
			text += a.Action + ": " + a.Reason + "\n"
		}
		return func() {
			d.confirmation(text+"Removal also removes managed worktrees. Independent copies remain. The node rechecks current state before applying the policy.", func() {
				d.work(func(ctx context.Context) (func(), error) {
					applied, err := d.backend.ApplyDue(ctx, time.Now(), actions)
					return func() {
						for _, a := range applied {
							if a.Action == "remove" {
								d.supervisor.Watch(a.Worker, false)
							}
						}
						d.changed()
						d.message(fmt.Sprintf("Applied %d lifecycle action(s) to this VM.", len(applied)))
					}, err
				})
			}, func() { d.limits(worker) })
		}, nil
	})
}
