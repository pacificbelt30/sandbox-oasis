package ui

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/brokersettings"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/instanceux"
	"github.com/pacificbelt30/codex-dock/internal/policyevents"
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
	app := tview.NewApplication()

	// Include a network manager so deleting a worker also removes its per-worker
	// Internal network (otherwise the networks accumulate).
	netMgr, err := network.NewManager()
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}
	defer func() { _ = netMgr.Close() }()
	cfg.Network = netMgr
	mgr, err := sandbox.NewManager(cfg)
	if err != nil {
		return fmt.Errorf("connecting to Docker: %w", err)
	}

	defer func() { _ = mgr.Close() }()
	done := make(chan struct{})
	defer close(done)

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

	refreshData := func() {
		ws, err := mgr.List(true)
		if err != nil {
			reportError(err)
			return
		}
		events := tracker.Observe(ws, time.Now())
		app.QueueUpdateDraw(func() {
			for _, event := range events {
				_, _ = fmt.Fprintln(activityView, tview.Escape(instanceux.FormatEvent(event)))
			}
			model.Replace(ws)
			workers = append(workers[:0], model.Visible...)
			renderDashboard(views, model, time.Now())
		})
	}

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
			reportError(fmt.Errorf("worker %s is %s; start it with R before connecting", worker.Name, worker.Status))
			return
		}
		app.Suspend(func() {
			_, err := instanceux.Connect(context.Background(), mgr, worker.ID, instanceux.ConnectOptions{
				Shell: "auto", WorkingDir: "/workspace", Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "oaiso connect: %v\n", err)
			}
		})
		refreshData()
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

	views.filter.SetChangedFunc(func(text string) {
		if err := model.SetQuery(text); err != nil {
			footer.SetText("[red]" + tview.Escape(err.Error()) + "[-]")
			return
		}
		workers = append(workers[:0], model.Visible...)
		renderDashboard(views, model, time.Now())
	})
	views.filter.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEscape {
			views.filter.SetText(model.Query.Raw)
		}
		app.SetFocus(table)
	})

	table.SetSelectionChangedFunc(func(row, _ int) {
		index := row - 1
		if index >= 0 && index < len(workers) {
			model.SelectIndex(index)
			renderDetail(views.detail, workers[index], time.Now())
		}
	})

	// Key handling
	table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		row, _ := table.GetSelection()
		idx := row - 1 // header offset

		switch event.Rune() {
		case '/':
			app.SetFocus(views.filter)
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

	// Polling ticker
	go func() {
		refreshData()
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				refreshData()
			}
		}
	}()

	return app.SetRoot(pages, true).EnableMouse(true).Run()
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
