package workerpolicy

import (
	"fmt"
	"strconv"
	"strings"
)

const AgentStateLabel = "codex-dock.agent-state-size"
const AgentStateTarget = "/var/lib/oaiso-agent-state"
const AgentStateDefault = int64(512 << 20)

// AgentStateBytes is a bounded, immutable-at-creation disk capacity. "off"
// preserves workloads that intentionally manage their own SQLite storage.
func AgentStateBytes(value string) (int64, error) {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" || v == "auto" {
		return AgentStateDefault, nil
	}
	if v == "off" {
		return 0, nil
	}
	unit := int64(1 << 20)
	switch {
	case strings.HasSuffix(v, "mib"):
		v = strings.TrimSuffix(v, "mib")
	case strings.HasSuffix(v, "gib"):
		v = strings.TrimSuffix(v, "gib")
		unit = 1 << 30
	default:
		return 0, fmt.Errorf("agent state size must be auto, off, or an integer MiB/GiB capacity (64MiB–8GiB)")
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 || n > (8<<30)/unit || n*unit < 64<<20 {
		return 0, fmt.Errorf("agent state size must be between 64MiB and 8GiB")
	}
	return n * unit, nil
}
