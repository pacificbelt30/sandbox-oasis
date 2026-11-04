package ui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/brokersettings"
	"github.com/pacificbelt30/codex-dock/internal/diagnostics"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/instanceux"
	"github.com/pacificbelt30/codex-dock/internal/policyevents"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/pacificbelt30/codex-dock/internal/taskboard"
	"github.com/pacificbelt30/codex-dock/internal/workeregress"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/network"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/rivo/tview"
)

const pollInterval = 2 * time.Second

// Run starts the dock-ui TUI.
func Run(cfg sandbox.ManagerConfig) error {
	return RunWithOptions(cfg, "")
}

func RunWithOptions(cfg sandbox.ManagerConfig, configPath string) error {
	return RunWithPolicyOptions(cfg, configPath, "", "")
}

func RunWithPolicyOptions(cfg sandbox.ManagerConfig, configPath, policyPath, policyOverride string) error {
	return RunWithTaskOptions(cfg, configPath, policyPath, policyOverride, nil)
}

func RunWithTaskOptions(cfg sandbox.ManagerConfig, configPath, policyPath, policyOverride string, tasks *taskboard.Service) error {
	return RunWithSandboxOptions(cfg, configPath, policyPath, policyOverride, tasks, nil)
}

func RunWithSandboxOptions(cfg sandbox.ManagerConfig, configPath, policyPath, policyOverride string, tasks *taskboard.Service, launcher sandboxux.Launcher) error {
	app := tview.NewApplication()

	// Include a network manager so deleting a worker also removes its per-worker
	// Internal network (otherwise the networks accumulate).
	netMgr, err := network.NewManager()
	if err != nil {
		return &diagnostics.StartupError{Stage: "node", Cause: err, Next: []string{"oaiso runtime-check", "systemctl status codex-dock-node"}}
	}
	defer func() { _ = netMgr.Close() }()
	cfg.Network = netMgr
	mgr, err := sandbox.NewManager(cfg)
	if err != nil {
		return &diagnostics.StartupError{Stage: "node", Cause: err, Next: []string{"oaiso runtime-check", "systemctl status codex-dock-node"}}
	}

	defer func() { _ = mgr.Close() }()
	dashboardCtx, cancelDashboard := context.WithCancel(context.Background())
	defer cancelDashboard()

	views := newDashboardViews()
	table, footer := views.table, views.footer
	reportError := func(err error) {
		if err != nil {
			app.QueueUpdateDraw(func() { footer.SetText("[red]" + tview.Escape(err.Error()) + "[-]") })
		}
	}

	// Log view panel
	logView := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	logView.SetMaxLines(2000)
	logView.SetTitle(" Logs ").SetBorder(true)
	activityView := tview.NewTextView().SetDynamicColors(true).SetScrollable(true).SetWrap(false)
	activityView.SetTitle(" Instance activity ").SetBorder(true)
	activityView.SetText("[gray]Changes observed while this dashboard is open appear here.[-]")
	tracker := instanceux.NewTracker(1000)

	mainFlex := dashboardBody(views)

	pages := tview.NewPages().
		AddPage("main", mainFlex, true, true).
		AddPage("logs", tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(logView, 0, 1, true).
			AddItem(tview.NewTextView().SetText("[Q] Back"), 1, 0, false),
			true, false)
	pages.AddPage("activity", tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(activityView, 0, 1, true).
		AddItem(tview.NewTextView().SetText("[Q/Esc] Back  [C] Clear"), 1, 0, false), true, false)

	if configPath != "" {
		pages.AddPage("proxy", proxySettingsPage(configPath, func() { pages.SwitchToPage("main") }), true, false)
	}

	if brokerPath, err := brokersettings.DefaultPath(); err == nil {
		pages.AddPage("broker", brokerSettingsPage(brokerPath, func() { pages.SwitchToPage("main") }), true, false)
	}

	model := instanceux.NewModel()
	var workers []sandbox.Worker

	var desktop *sandboxDesktop
	refreshData, _ := startDashboardRefresh(dashboardCtx, pollInterval, func(ctx context.Context) ([]sandbox.Worker, error) { return loadDashboard(ctx, mgr) }, func(ws []sandbox.Worker) {
		app.QueueUpdateDraw(func() {
			events := tracker.Observe(ws, time.Now())
			for _, event := range events {
				_, _ = fmt.Fprintln(activityView, tview.Escape(instanceux.FormatEvent(event)))
			}
			model.Replace(ws)
			workers = append(workers[:0], model.Visible...)
			renderDashboard(views, model, time.Now())
			if desktop != nil {
				desktop.refreshPreviews()
			}
		})
	}, reportError)

	// showLogs fetches real container logs and displays them in the log view (F-UI-03).
	showLogs := func(w sandbox.Worker) {
		logView.Clear()
		// Switch to log page immediately so the user sees activity.
		pages.SwitchToPage("logs")
		go func() {
			lw := &tviewWriter{view: logView, app: app}
			opts := sandbox.LogOptions{
				Name:   w.ID,
				Tail:   200,
				Output: lw,
			}
			if err := mgr.Logs(opts); err != nil {
				_, _ = fmt.Fprintf(lw, "[red]Error fetching logs: %v[-]", err)
			}
		}()
	}

	showMain := func() {
		pages.SwitchToPage("main")
		app.SetFocus(table)
		footer.SetText(dashboardHelp(false))
	}
	desktopCtx, cancelDesktop := context.WithCancel(context.Background())
	desktop = newSandboxDesktop(app, desktopCtx, managerDesktop(cfg, mgr, launcher), showMain, func() { go refreshData() })
	defer func() { cancelDesktop(); desktop.close() }()
	openDesktop := func(action func()) {
		pages.AddAndSwitchToPage("sandbox", desktop.pages, true)
		action()
		app.SetFocus(desktop.pages)
	}

	openPolicies := func() {
		page := newAgentPolicyPage(policyPath, policyOverride, app.SetFocus, func() {
			pages.RemovePage("agent-policies")
			showMain()
		})
		pages.AddAndSwitchToPage("agent-policies", page.pages, true)
		app.SetFocus(page.pages)
	}

	showHelp := func() {
		pages.AddAndSwitchToPage("help", helpModal(func() { pages.RemovePage("help"); showMain() }), true)
	}

	confirm := func(question string, action func()) {
		pages.AddAndSwitchToPage("confirm", confirmationModal(question, func() {
			pages.RemovePage("confirm")
			showMain()
			action()
		}, func() { pages.RemovePage("confirm"); showMain() }), true)
	}

	connectWorker := func(worker sandbox.Worker) {
		if worker.Status != "running" {
			footer.SetText(tview.Escape(fmt.Sprintf("worker %s is %s; start it with R before connecting", worker.Name, worker.Status)))
			return
		}
		var connectErr error
		app.Suspend(func() {
			_, connectErr = instanceux.Connect(context.Background(), mgr, worker.ID, instanceux.ConnectOptions{
				Shell: "auto", WorkingDir: "/workspace", Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
			})
		})
		if connectErr != nil {
			footer.SetText("[red]" + tview.Escape(connectErr.Error()) + "[-]")
		}
		// QueueUpdateDraw waits for the event loop. This callback already runs
		// on that loop, so refresh must execute outside it after Suspend.
		go refreshData()
	}

	runCommand := func(worker sandbox.Worker) {
		form := commandForm(worker, func(command string) {
			pages.RemovePage("command")
			logView.Clear()
			pages.SwitchToPage("logs")
			go func() {
				writer := &tviewWriter{view: logView, app: app}
				code, err := mgr.ExecWithOptions(context.Background(), worker.ID, sandbox.ExecOptions{
					Argv: []string{"/bin/sh", "-lc", command}, Stdout: writer, Stderr: writer, WorkingDir: "/workspace",
				})
				if err != nil {
					_, _ = fmt.Fprintf(writer, "\n[red]Exec error: %v[-]\n", err)
					return
				}
				_, _ = fmt.Fprintf(writer, "\n[yellow]Exit code: %d[-]\n", code)
			}()
		}, func() { pages.RemovePage("command"); showMain() })
		pages.AddAndSwitchToPage("command", centered(form, 76, 9), true)
		app.SetFocus(form)
	}

	filterBefore := ""
	beginFilter := func() { filterBefore = model.Query.Raw; app.SetFocus(views.filter) }
	views.filter.SetChangedFunc(func(text string) {
		if err := model.SetQuery(text); err != nil {
			footer.SetText("[red]" + tview.Escape(err.Error()) + "[-]")
			return
		}
		workers = append(workers[:0], model.Visible...)
		renderDashboard(views, model, time.Now())
		footer.SetText(dashboardHelp(false))
	})
	views.filter.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEscape {
			views.filter.SetText(filterBefore)
		}
		if key == tcell.KeyEnter && views.filter.GetText() != model.Query.Raw {
			return
		}
		footer.SetText(dashboardHelp(false))
		app.SetFocus(table)
	})

	table.SetSelectionChangedFunc(func(row, _ int) {
		index := row - 1
		if index >= 0 && index < len(workers) {
			model.SelectIndex(index)
			renderSelectedDetail(views, workers[index], time.Now())
		}
	})

	showActions := func() {
		var selected *sandbox.Worker
		if worker, ok := model.Selected(); ok {
			selected = &worker
		}
		closeMenu := func() { pages.RemovePage("actions"); showMain() }
		menu := dashboardActionMenu(selected, tasks != nil, func() string { return model.SelectedID }, func(event *tcell.EventKey) {
			closeMenu()
			table.InputHandler()(event, func(p tview.Primitive) { app.SetFocus(p) })
		}, func() { closeMenu(); footer.SetText("Selection changed. Choose an instance and open Actions again.") }, closeMenu)
		pages.AddAndSwitchToPage("actions", dashboardMenuPage(menu), true)
		app.SetFocus(menu)
	}
	views.actions.SetSelectedFunc(showActions)
	views.actions.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyF2 {
			showActions()
			return nil
		}
		if event.Key() == tcell.KeyTab || event.Key() == tcell.KeyBacktab || event.Key() == tcell.KeyEscape {
			app.SetFocus(table)
			return nil
		}
		return event
	})
	app.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		width, _ := screen.Size()
		if resizeDashboard(views, width) {
			renderDashboard(views, model, time.Now())
		}
		return false
	})
	// Key handling
	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		row, _ := table.GetSelection()
		idx := row - 1 // header offset
		switch event.Key() {
		case tcell.KeyF2:
			showActions()
			return nil
		case tcell.KeyTab, tcell.KeyBacktab:
			app.SetFocus(views.actions)
			return nil
		case tcell.KeyEscape:
			views.filter.SetText("")
			footer.SetText(dashboardHelp(false))
			return nil
		}
		if event.Key() == tcell.KeyRune && event.Rune() == ' ' {
			showActions()
			return nil
		}

		switch event.Rune() {
		case 'n', 'N':
			openDesktop(desktop.create)
			return nil
		case 'm', 'M':
			openDesktop(desktop.managedWorkspaces)
			return nil
		case 'z', 'Z':
			openDesktop(desktop.recoveryPoints)
			return nil
		case 'k', 'K':
			if idx >= 0 && idx < len(workers) {
				worker := workers[idx]
				openDesktop(func() { desktop.saveRecoveryPoint(worker) })
			}
			return nil
		case 'i', 'I':
			if idx >= 0 && idx < len(workers) {
				page := dashboardDetailPage(workers[idx], func() { pages.RemovePage("details"); showMain() })
				pages.AddAndSwitchToPage("details", page, true)
				app.SetFocus(page)
			}
			return nil
		case 'w', 'W':
			if idx >= 0 && idx < len(workers) {
				worker := workers[idx]
				if worker.Status != "running" {
					footer.SetText("Start this sandbox with R before inspecting guest changes or exporting files.")
					return nil
				}
				openDesktop(func() { desktop.changes(worker) })
			}
			return nil
		case 'l', 'L':
			if idx >= 0 && idx < len(workers) {
				worker := workers[idx]
				openDesktop(func() { desktop.limits(worker) })
			}
			return nil
		case 'f', 'F':
			if idx >= 0 && idx < len(workers) {
				worker := workers[idx]
				if worker.Status != "running" {
					footer.SetText("Start this sandbox with R before opening files or previews.")
					return nil
				}
				openDesktop(func() { desktop.files(worker) })
			}
			return nil
		case 'u', 'U':
			if idx >= 0 && idx < len(workers) {
				worker := workers[idx]
				if worker.Status != "running" {
					footer.SetText("Start this sandbox with R before opening files or previews.")
					return nil
				}
				openDesktop(func() { desktop.preview(worker) })
			}
			return nil
		case 't', 'T':
			if tasks != nil {
				page := newTaskBoardPage(app, tasks, showMain)
				page.start(dashboardCtx.Done())
				pages.AddAndSwitchToPage("tasks", page.pages, true)
				app.SetFocus(page.table)
			}
			return nil
		case 'g', 'G':
			openPolicies()
			return nil
		case '/':
			beginFilter()
			return nil
		case '?', 'h', 'H':
			showHelp()
			return nil
		case 'o', 'O':
			model.CycleSort()
			workers = append(workers[:0], model.Visible...)
			renderDashboard(views, model, time.Now())
			return nil
		case 'v', 'V':
			pages.SwitchToPage("activity")
			app.SetFocus(activityView)
			return nil
		case 'c', 'C':
			if idx >= 0 && idx < len(workers) {
				connectWorker(workers[idx])
			}
			return nil
		case 'x', 'X':
			if idx >= 0 && idx < len(workers) {
				runCommand(workers[idx])
			}
			return nil
		case 'e', 'E':
			if idx >= 0 && idx < len(workers) {
				logView.Clear()
				pages.SwitchToPage("logs")
				selected := workers[idx]
				go func() {
					lw := &tviewWriter{view: logView, app: app}
					err := workeregress.Events(context.Background(), engine.Socket(), selected.ID, false, func(e policyevents.Event) error {
						_, err := fmt.Fprintf(lw, "%s source=%s destination=%s reason=%s\n", e.Time.Format("15:04:05"), e.Source, e.Destination, e.Code)
						return err
					})
					if err != nil {
						_, _ = fmt.Fprintln(lw, err)
					}
				}()
			}
		case 'p', 'P':
			if configPath != "" {
				pages.SwitchToPage("proxy")
			}
		case 'b', 'B':
			pages.SwitchToPage("broker")
		case 'q', 'Q':
			app.Stop()

		case 's', 'S':
			if idx >= 0 && idx < len(workers) {
				go func(w sandbox.Worker) {
					reportError(mgr.Stop(w.ID, 10))
					refreshData()
				}(workers[idx])
			}

		case 'd', 'D':
			if idx >= 0 && idx < len(workers) {
				selected := workers[idx]
				confirm(fmt.Sprintf("Delete %s and its managed network?", selected.Name), func() {
					go func(w sandbox.Worker) { reportError(mgr.Remove(w.ID, true)); refreshData() }(selected)
				})
			}

		case 'r', 'R':
			// F-UI-02: Restart a stopped/exited container via Docker start.
			if idx >= 0 && idx < len(workers) {
				go func(w sandbox.Worker) {
					reportError(mgr.Start(w.ID))
					refreshData()
				}(workers[idx])
			}

		case 'a', 'A':
			selected := append([]sandbox.Worker(nil), workers...)
			confirm("Stop every running worker shown in this dashboard?", func() {
				go func() {
					for _, w := range selected {
						if w.Status == "running" {
							reportError(mgr.Stop(w.ID, 10))
						}
					}
					refreshData()
				}()
			})
		}

		switch event.Key() {
		case tcell.KeyEnter:
			if idx >= 0 && idx < len(workers) {
				showLogs(workers[idx])
			}
		}

		return event
	})

	// Log page key handling
	logView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 'q' || event.Rune() == 'Q' || event.Key() == tcell.KeyEscape {
			showMain()
			return nil
		}
		return event
	})
	activityView.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Rune() == 'c' || event.Rune() == 'C' {
			tracker.Clear()
			activityView.SetText("[gray]Activity cleared.[-]")
			return nil
		}
		if event.Rune() == 'q' || event.Rune() == 'Q' || event.Key() == tcell.KeyEscape {
			showMain()
			return nil
		}
		return event
	})

	refreshData()

	app.SetRoot(pages, true)
	return runTerminal(app)
}

func statusIcon(status string) string {
	switch status {
	case "running":
		return "✅ "
	case "stopped", "exited":
		return "⏹ "
	case "error":
		return "❌ "
	default:
		return "  "
	}
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func truncate(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n-3]) + "..."
}

// tviewWriter is an io.Writer that appends text to a tview TextView.
type tviewWriter struct {
	view     *tview.TextView
	app      *tview.Application
	retained int
}

func (w *tviewWriter) Write(p []byte) (int, error) {
	text := tview.Escape(string(p))
	reset := w.retained+len(text) > 4<<20
	if reset {
		w.retained = 0
	}
	w.retained += len(text)
	w.app.QueueUpdateDraw(func() {
		if reset {
			w.view.Clear()
		}
		_, _ = w.view.Write([]byte(text))
		w.view.ScrollToEnd()
	})
	return len(p), nil
}
