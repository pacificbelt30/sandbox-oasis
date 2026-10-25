// Package netquota bounds worker-facing TCP connections and per-source bandwidth.
package netquota

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
)

const Label = "io.codex-dock.network-resources"

// Limits applies independently to one proxy's data listener. Source means the
// socket peer IP, never an HTTP forwarding header or a claimed execution ID.
// Zero leaves that dimension unlimited. BytesPerSecond combines both directions.
type Limits struct {
	MaxConnections          int   `json:"max_connections,omitempty"`
	MaxConnectionsPerSource int   `json:"max_connections_per_source,omitempty"`
	BytesPerSecond          int64 `json:"bytes_per_second,omitempty"`
}

func (l Limits) Validate() error {
	if l.MaxConnections < 0 || l.MaxConnections > 65536 || l.MaxConnectionsPerSource < 0 || l.MaxConnectionsPerSource > 65536 {
		return fmt.Errorf("network connection limits must be between 0 and 65536")
	}
	if l.MaxConnections != 0 && l.MaxConnectionsPerSource > l.MaxConnections {
		return fmt.Errorf("per-source connection limit exceeds proxy connection limit")
	}
	if l.BytesPerSecond != 0 && (l.BytesPerSecond < 1024 || l.BytesPerSecond > 1<<40) {
		return fmt.Errorf("network bytes per second must be 0 or between 1024 and 1099511627776")
	}
	return nil
}

// ID keeps existing unlimited proxies compatible while making changed limits
// require explicit replacement, rather than silently adopting stale settings.
func (l Limits) ID() string {
	if l == (Limits{}) {
		return ""
	}
	data, _ := json.Marshal(l)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (l Limits) Args() []string {
	if l == (Limits{}) {
		return nil
	}
	return []string{"--max-connections", strconv.Itoa(l.MaxConnections), "--max-connections-per-source", strconv.Itoa(l.MaxConnectionsPerSource), "--network-bytes-per-second", strconv.FormatInt(l.BytesPerSecond, 10)}
}
