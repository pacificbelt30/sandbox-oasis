package ui

import (
	"strings"
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/storageinfo"
	"github.com/pacificbelt30/codex-dock/internal/workerstatus"
)

func TestResourceDiagnosticsDoesNotGuessExitCause(t *testing.T) {
	text := resourceSummary(workerstatus.Diagnostics{ExitCode: 137})
	if !strings.Contains(text, "CPU time: unavailable") || !strings.Contains(text, "unknown; exit code alone") || strings.Contains(text, "OOM kill events: 0") {
		t.Fatal(text)
	}
	used, limit, oom := uint64(490), uint64(500), uint64(1)
	text = resourceSummary(workerstatus.Diagnostics{Running: true, Resources: workerstatus.Resources{MemoryBytes: &used, MemoryLimit: &limit, OOMKills: &oom}})
	if !strings.Contains(text, "at least 90%") || !strings.Contains(text, "OOM kill events: 1") {
		t.Fatal(text)
	}
	text = quotaSummary(storageinfo.Usage{Available: true, UsedBytes: 95, LimitBytes: 100, UsedInodes: 10, LimitInodes: 10})
	if !strings.Contains(text, "rootfs bytes at least 90%") || !strings.Contains(text, "rootfs inodes at least 90%") {
		t.Fatal(text)
	}
}

func TestResourceDiagnosticsDoesNotReportCompletionForCreatedOrPausedVM(t *testing.T) {
	for _, status := range []string{"created", "paused"} {
		text := resourceSummaryForStatus(status, workerstatus.Diagnostics{ExitCode: 0})
		if strings.Contains(text, "Exit code: 0") || !strings.Contains(text, "not") && !strings.Contains(text, "No completion") {
			t.Fatal("unfinished VM reported a successful exit", status, text)
		}
	}
	if text := resourceSummaryForStatus("exited", workerstatus.Diagnostics{ExitCode: 0}); !strings.Contains(text, "Exit code: 0") {
		t.Fatal("successful completion was hidden", text)
	}
}
