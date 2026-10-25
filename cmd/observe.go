package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/launchtiming"
	"github.com/pacificbelt30/codex-dock/internal/observability"
	"github.com/pacificbelt30/codex-dock/internal/policyevents"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/workerapp"
	"github.com/pacificbelt30/codex-dock/pkg/cxd"
	"github.com/spf13/cobra"
)

var runObserveDir string

// Only RunContext is timed: foreground console/wait and later inference are
// outside this wrapper. Recording failure must not change worker ownership.
type diagnosticRunner struct {
	runner    workerapp.Runner
	directory string
}

func (r diagnosticRunner) RunContext(ctx context.Context, opts sandbox.RunOptions) (string, error) {
	if r.directory == "" {
		return r.runner.RunContext(ctx, opts)
	}
	ctx, timer := launchtiming.Begin(ctx)
	id, err := r.runner.RunContext(ctx, opts)
	total, phases := timer.Finish()
	dir, pathErr := filepath.Abs(r.directory)
	if pathErr != nil || (observability.Journal{Directory: dir}).Append(observability.Launch(id, total, phases, err != nil)) != nil {
		_, _ = fmt.Fprintln(os.Stderr, "warning: launch diagnostics could not be saved; worker result is unchanged")
	}
	return id, err
}

func init() { rootCmd.AddCommand(newObserveCommand()) }

func newObserveCommand() *cobra.Command {
	root := &cobra.Command{Use: "observe", Short: "Save and export bounded, secret-free local diagnostics"}
	var dir, worker string
	var samples int
	var interval time.Duration
	var maxBytes int64
	root.PersistentFlags().StringVar(&dir, "directory", "", "Private diagnostic directory (required)")
	root.PersistentFlags().Int64Var(&maxBytes, "max-bytes", observability.DefaultMaxBytes, "Maximum bytes per retained file (current + previous, 1 KiB–8 MiB each)")
	journal := func() (observability.Journal, error) {
		if dir == "" {
			return observability.Journal{}, fmt.Errorf("--directory is required")
		}
		absolute, err := filepath.Abs(dir)
		return observability.Journal{Directory: absolute, MaxBytes: maxBytes}, err
	}
	capture := &cobra.Command{Use: "capture", Short: "Collect status and optional verified memory/retained proxy denials; no inference", Args: cobra.NoArgs}
	capture.Flags().StringVar(&worker, "worker", "", "Optional worker to inspect (shared proxy events are not per-worker attribution)")
	capture.Flags().IntVar(&samples, "samples", 1, "Finite number of samples (1–10000)")
	capture.Flags().DurationVar(&interval, "interval", 5*time.Second, "Interval between samples (at least 1s)")
	capture.RunE = func(cmd *cobra.Command, _ []string) error {
		if samples < 1 || samples > 10000 || interval < time.Second {
			return fmt.Errorf("invalid sample count or interval")
		}
		j, err := journal()
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		partial := false
		for i := 0; i < samples; i++ {
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			report := collectStatusReport(ctx, false, false)
			err = j.Append(observability.Status(report))
			partial = partial || report.Err() != nil
			if err == nil && worker != "" {
				var records []observability.Record
				records, partial = collectWorkerObservations(ctx, worker, seen, partial)
				for _, record := range records {
					if err = j.Append(record); err != nil {
						break
					}
				}
			}
			cancel()
			if err != nil {
				return err
			}
			if i+1 < samples {
				timer := time.NewTimer(interval)
				select {
				case <-cmd.Context().Done():
					timer.Stop()
					return cmd.Context().Err()
				case <-timer.C:
				}
			}
		}
		if partial {
			return fmt.Errorf("diagnostics saved with failed or unavailable checks; inspect export and oaiso status")
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), "Diagnostics saved (no connection check or inference performed).")
		return err
	}
	export := &cobra.Command{Use: "export", Short: "Export validated retained records as JSONL", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		j, err := journal()
		if err != nil {
			return err
		}
		records, err := j.Read()
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		for _, record := range records {
			if err := encoder.Encode(record); err != nil {
				return err
			}
		}
		return nil
	}}
	root.AddCommand(capture, export)
	return root
}

func collectWorkerObservations(ctx context.Context, worker string, seen map[string]bool, partial bool) ([]observability.Record, bool) {
	client, err := cxd.Open(ctx, cxd.Options{NodeSocket: engine.Socket()})
	if err != nil {
		return []observability.Record{observability.Resources(worker, nil), observability.Unavailable(worker)}, true
	}
	defer func() { _ = client.Close() }()
	var memory *observability.Memory
	usage, err := client.ResourceUsage(ctx, worker)
	if err == nil {
		memory = &observability.Memory{Current: usage.HostMemoryCurrentBytes, GuestLimit: usage.Profile.GuestMemoryBytes, HostLimit: usage.Profile.HostMemoryBytes, OOMKills: usage.OOMKills}
		if usage.PeakAvailable {
			memory.Peak = &usage.HostMemoryPeakBytes
		}
	} else {
		partial = true
	}
	records := []observability.Record{observability.Resources(worker, memory)}
	err = client.PolicyEvents(ctx, worker, false, func(event policyevents.Event) error {
		key := fmt.Sprintf("%d/%s", event.Sequence, event.Time.UTC().Format(time.RFC3339Nano))
		if seen[key] {
			return nil
		}
		// The existing proxy console is bounded to 1000 lines. Drop the oldest
		// deduplication window rather than grow with a long capture session.
		if len(seen) >= 2048 {
			clear(seen)
		}
		seen[key] = true
		records = append(records, observability.Denial(worker, event))
		return nil
	})
	if err != nil {
		records = append(records, observability.Unavailable(worker))
		partial = true
	}
	return records, partial
}
