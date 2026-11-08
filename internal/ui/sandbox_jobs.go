package ui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/rivo/tview"
)

type desktopJobs interface {
	StartJob(context.Context, string, sandbox.ExecOptions) (sandbox.Job, error)
	ListJobs(context.Context, string) ([]sandbox.Job, error)
	JobLogs(context.Context, string, string, int64) (sandbox.JobOutput, error)
	KillJob(context.Context, string, string) error
}

func (d *sandboxDesktop) jobs(worker sandbox.Worker) {
	if d.backend.Jobs == nil {
		d.message("Background jobs are unavailable.")
		return
	}
	list := tview.NewList().ShowSecondaryText(true)
	list.SetBorder(true).SetTitle(" Background jobs · " + tview.Escape(worker.Name) + " ")
	form := tview.NewForm().AddButton("Refresh", func() { d.jobs(worker) }).AddButton("New job…", func() { d.newJob(worker) }).AddButton("Back", d.back)
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(list, 0, 1, true).AddItem(form, 3, 0, false).AddItem(tview.NewTextView().SetText("Enter views output · Tab switches panes · Esc back\nJobs continue after TUI exit. Refresh to reload retained jobs."), 2, 0, false)
	body.SetInputCapture(d.pageFormNavigation(list, form, d.back))
	d.pages.AddAndSwitchToPage("jobs", body, true)
	d.app.SetFocus(list)
	d.read("Listing background jobs", func(ctx context.Context) (func(), error) {
		jobs, err := d.backend.Jobs.ListJobs(ctx, worker.ID)
		sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
		return func() {
			list.Clear()
			for _, job := range jobs {
				state := fmt.Sprintf("Exited · code %d", job.ExitCode)
				if job.Running {
					state = "Running · exit code pending"
				}
				list.AddItem(tview.Escape(job.ID), state, 0, func() { d.jobOutput(worker, job) })
			}
			if len(jobs) == 0 {
				list.AddItem("No retained jobs", "Use New job to start a background command.", 0, nil)
			}
		}, err
	})
}

func (d *sandboxDesktop) newJob(worker sandbox.Worker) {
	if worker.Status != "running" {
		d.message("Start this sandbox with R before starting a job.")
		return
	}
	back := func() { d.jobs(worker) }
	var form *tview.Form
	form = commandForm(worker, func(command string) {
		d.confirmation(tview.Escape("Start background job in "+worker.Name+"?\nWorking directory: /workspace\nCommand:\n"+command+"\n\nClosing the TUI does not stop the job. Use the job output page to stop it."), func() {
			d.work(func(ctx context.Context) (func(), error) {
				job, err := d.backend.Jobs.StartJob(ctx, worker.ID, sandbox.ExecOptions{Argv: []string{"/bin/sh", "-lc", command}, WorkingDir: "/workspace"})
				return func() { d.jobOutput(worker, job) }, err
			})
		}, func() { d.pages.SwitchToPage("new-job"); d.app.SetFocus(form) })
	}, back)
	form.SetTitle(" New background job · Esc cancels ")
	d.pages.AddAndSwitchToPage("new-job", centered(form, 76, 9), true)
	d.app.SetFocus(form)
}

func cleanJobText(text string) string {
	var out strings.Builder
	for _, r := range strings.ToValidUTF8(strings.ReplaceAll(text, "\r\n", "\n"), "�") {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			out.WriteString(strconv.QuoteRuneToASCII(r))
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func (d *sandboxDesktop) jobOutput(worker sandbox.Worker, job sandbox.Job) {
	if d.jobLogCancel != nil {
		d.jobLogCancel()
	}
	ctx, cancel := context.WithCancel(d.ctx)
	d.jobLogCancel = cancel
	back := func() { cancel(); d.pages.RemovePage("job-logs"); d.jobs(worker) }
	view := tview.NewTextView().SetDynamicColors(false).SetScrollable(true)
	view.SetBorder(true).SetTitle(" Job output · " + tview.Escape(job.ID) + " ")
	state := tview.NewTextView().SetDynamicColors(false).SetText("Reading retained output…")
	form := tview.NewForm().AddButton("Stop job…", func() {
		d.confirmation(tview.Escape("Stop only job "+job.ID+" in "+worker.Name+"?\nThe VM and other jobs remain."), func() {
			d.work(func(c context.Context) (func(), error) {
				err := d.backend.Jobs.KillJob(c, worker.ID, job.ID)
				return func() { d.jobOutput(worker, job) }, err
			})
		}, func() { d.pages.SwitchToPage("job-logs"); d.app.SetFocus(view) })
	}).AddButton("Back", back)
	follow := true
	body := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(view, 0, 1, true).AddItem(state, 2, 0, false).AddItem(form, 3, 0, false).AddItem(tview.NewTextView().SetText("Space pauses/resumes following · Tab switches panes · Esc returns without stopping job"), 1, 0, false)
	navigation := d.pageFormNavigation(view, form, back)
	body.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Rune() == ' ' && view.HasFocus() {
			follow = !follow
			return nil
		}
		return navigation(e)
	})
	d.pages.AddAndSwitchToPage("job-logs", body, true)
	d.app.SetFocus(view)
	go func() {
		var offset int64
		var text string
		truncated := false
		for {
			c, stop := context.WithTimeout(ctx, 10*time.Second)
			output, err := d.backend.Jobs.JobLogs(c, worker.ID, job.ID, offset)
			stop()
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				if output.Next < offset {
					err = fmt.Errorf("job log offset moved backwards")
				} else {
					offset = output.Next
					truncated = truncated || output.Truncated
					text += output.Output
					if len(text) > 256<<10 {
						text = text[len(text)-(256<<10):]
						for len(text) > 0 && !utf8.RuneStart(text[0]) {
							text = text[1:]
						}
					}
				}
			}
			rendered := cleanJobText(text)
			if truncated {
				rendered = "[Node output retention limit reached]\n" + rendered
			}
			d.app.QueueUpdateDraw(func() {
				if ctx.Err() != nil {
					return
				}
				if err != nil {
					state.SetText("Output unavailable: " + err.Error() + " · retrying")
					return
				}
				if output.Running {
					state.SetText("Running · exit code pending")
				} else {
					state.SetText("Exited · final status pending")
				}
				row, column := view.GetScrollOffset()
				view.SetText(rendered)
				if follow {
					view.ScrollToEnd()
				} else {
					view.ScrollTo(row, column)
				}
			})
			// Read the authoritative final exit code after draining retained output.
			if err == nil && !output.Running && len(output.Output) == 0 {
				c, stop := context.WithTimeout(ctx, 10*time.Second)
				jobs, e := d.backend.Jobs.ListJobs(c, worker.ID)
				stop()
				if ctx.Err() != nil {
					return
				}
				final := "Exited · final code unavailable"
				if e != nil {
					final += " · " + e.Error()
				} else {
					for _, j := range jobs {
						if j.ID == job.ID {
							if j.Running {
								final = "Running · exit status changed; reopen output"
							} else {
								final = fmt.Sprintf("Exited · code %d", j.ExitCode)
							}
							break
						}
					}
				}
				d.app.QueueUpdateDraw(func() {
					if ctx.Err() == nil {
						state.SetText(final)
					}
				})
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}()
}
