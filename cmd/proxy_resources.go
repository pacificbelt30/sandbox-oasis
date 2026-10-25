package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/netquota"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func bindNetworkResourceFlags(command *cobra.Command) {
	f := command.Flags()
	f.Int("max-connections", 0, "Maximum worker-facing TCP connections per proxy (0 unlimited; rejects new connections)")
	f.Int("max-connections-per-source", 0, "Maximum TCP connections per socket source IP (0 unlimited)")
	f.Int64("network-bytes-per-second", 0, "Combined TCP bytes/second per source IP; throttles both directions with a 32 KiB burst (0 unlimited)")
}

func proxyNetworkResourceLimits(command *cobra.Command) (netquota.Limits, error) {
	var limits netquota.Limits
	values := make([]int64, 0, 3)
	for _, setting := range []struct{ flag, key string }{
		{"max-connections", "proxy.max_connections"},
		{"max-connections-per-source", "proxy.max_connections_per_source"},
		{"network-bytes-per-second", "proxy.network_bytes_per_second"},
	} {
		var raw any = 0
		if viper.IsSet(setting.key) {
			raw = viper.Get(setting.key)
		}
		if command != nil {
			if flag := command.Flags().Lookup(setting.flag); flag != nil && flag.Changed {
				raw = flag.Value.String()
			}
		}
		value, err := strconv.ParseInt(strings.TrimSpace(fmt.Sprint(raw)), 10, 64)
		if err != nil {
			return limits, fmt.Errorf("%s must be an integer", setting.key)
		}
		values = append(values, value)
	}
	// Check before converting to int, including on a 32-bit CLI build.
	if values[0] < 0 || values[0] > 65536 || values[1] < 0 || values[1] > 65536 {
		return limits, fmt.Errorf("network connection limits must be between 0 and 65536")
	}
	limits = netquota.Limits{MaxConnections: int(values[0]), MaxConnectionsPerSource: int(values[1]), BytesPerSecond: values[2]}
	return limits, limits.Validate()
}
