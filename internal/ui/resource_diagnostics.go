package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/workerstatus"
)

func resourceSummaryForStatus(status string, s workerstatus.Diagnostics) string {
	switch status {
	case "created":
		return "VM not started. Resource and exit-code observations are not yet available.\n"
	case "paused":
		return "VM paused. No completion exit code is available; resume before collecting live resource observations.\n"
	default:
		return resourceSummary(s)
	}
}

func resourceSummary(s workerstatus.Diagnostics) string {
	var text strings.Builder
	r := s.Resources
	text.WriteString("Host resource observations (VMM and its host processes; not guest-only RSS):\n")
	if r.ObservedAt.IsZero() {
		text.WriteString("No retained host sample.\n")
	} else {
		fmt.Fprintf(&text, "Observed: %s\n", r.ObservedAt.Format(time.RFC3339))
	}
	if r.CPUUsec == nil {
		text.WriteString("CPU time: unavailable\n")
	} else {
		fmt.Fprintf(&text, "CPU time: %.3f seconds total\n", float64(*r.CPUUsec)/1e6)
	}
	if r.MemoryBytes == nil {
		text.WriteString("Host memory: unavailable\n")
	} else {
		fmt.Fprintf(&text, "Host memory: %.1f MiB", float64(*r.MemoryBytes)/(1<<20))
		if r.MemoryLimit != nil && *r.MemoryLimit > 0 {
			fmt.Fprintf(&text, " / %.1f MiB", float64(*r.MemoryLimit)/(1<<20))
			if float64(*r.MemoryBytes)/float64(*r.MemoryLimit) >= .9 {
				text.WriteString(" · WARNING: at least 90% of host limit")
			}
		} else {
			text.WriteString(" · host limit unavailable or unlimited")
		}
		text.WriteString("\n")
	}
	if r.OOMKills != nil {
		fmt.Fprintf(&text, "Host cgroup OOM kill events: %d\n", *r.OOMKills)
	} else {
		text.WriteString("Host OOM event count: unavailable\n")
	}
	if r.Error != "" {
		text.WriteString("Observation incomplete: " + r.Error + "\n")
	}
	if !s.Running {
		fmt.Fprintf(&text, "Exit code: %d\n", s.ExitCode)
		reasons := map[string]string{"max_lifetime": "maximum lifetime reached", "idle_timeout": "management idle timeout reached", "stop_requested": "stop requested by operator or node"}
		reason := reasons[s.StopReason]
		if reason == "" {
			reason = "unknown; exit code alone does not establish OOM or disk-full"
		}
		text.WriteString("Recorded stop request: " + reason + "\nLast retained resource sample may precede exit.\n")
	}
	text.WriteString("Guest OOM and filesystem errors may occur without a host OOM event. A quota warning does not prove the exit cause.\n")
	return text.String()
}

func cpuUtilization(previous, current workerstatus.Resources) string {
	if previous.CPUUsec == nil || current.CPUUsec == nil || *current.CPUUsec < *previous.CPUUsec || !current.ObservedAt.After(previous.ObservedAt) {
		return "CPU utilization: refresh again to collect two comparable host samples.\n"
	}
	seconds := current.ObservedAt.Sub(previous.ObservedAt).Seconds()
	return fmt.Sprintf("Average host CPU over %.1fs: %.1f%% (100%% = one host core)\n", seconds, float64(*current.CPUUsec-*previous.CPUUsec)/seconds/10000)
}
