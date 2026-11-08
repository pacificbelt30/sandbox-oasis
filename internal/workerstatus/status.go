// Package workerstatus describes host observations separately from guest limits.
package workerstatus

import "time"

const BaselineHeadLabel = "codex-dock.observed-workspace-baseline"
const BaselineStateLabel = "codex-dock.observed-workspace-baseline-state"

type Resources struct {
	ObservedAt  time.Time `json:"observed_at"`
	CPUUsec     *uint64   `json:"cpu_usec,omitempty"`
	MemoryBytes *uint64   `json:"memory_bytes,omitempty"`
	MemoryLimit *uint64   `json:"memory_limit,omitempty"`
	OOMKills    *uint64   `json:"oom_kills,omitempty"`
	Error       string    `json:"error,omitempty"`
}

type Diagnostics struct {
	Running    bool      `json:"running"`
	ExitCode   int       `json:"exit_code"`
	StopReason string    `json:"stop_reason"`
	Resources  Resources `json:"resources"`
}
