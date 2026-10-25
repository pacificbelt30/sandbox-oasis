package cmd

import (
	"github.com/pacificbelt30/codex-dock/internal/netquota"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"testing"
)

func TestProxyNetworkResourceFlagsOverrideConfigAndPinLaunch(t *testing.T) {
	viper.Reset()
	t.Cleanup(func() { viper.Reset() })
	viper.Set("proxy.max_connections", 100)
	viper.Set("proxy.max_connections_per_source", 4)
	viper.Set("proxy.network_bytes_per_second", 65536)
	c := &cobra.Command{Use: "proxy"}
	bindNetworkResourceFlags(c)
	if err := c.ParseFlags([]string{"--max-connections", "50"}); err != nil {
		t.Fatal(err)
	}
	resources, err := proxyNetworkResourceLimits(c)
	if err != nil {
		t.Fatal(err)
	}
	want := netquota.Limits{MaxConnections: 50, MaxConnectionsPerSource: 4, BytesPerSecond: 65536}
	if resources != want {
		t.Fatalf("limits: %+v", resources)
	}
	cfg, _, err := proxyConfiguration(proxyRunArgs{name: "limited", role: "egress", networkResources: want})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Labels[netquota.Label] != want.ID() {
		t.Fatal("network resource identity not pinned")
	}
	for _, pair := range [][2]string{{"--max-connections", "50"}, {"--max-connections-per-source", "4"}, {"--network-bytes-per-second", "65536"}} {
		found := false
		for i := 0; i+1 < len(cfg.Cmd); i++ {
			if cfg.Cmd[i] == pair[0] && cfg.Cmd[i+1] == pair[1] {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing setting %v", pair)
		}
	}
}

func TestMalformedNetworkResourceConfigCannotBecomeUnlimited(t *testing.T) {
	defer func() { viper.Reset() }()
	for _, key := range []string{"proxy.max_connections", "proxy.max_connections_per_source", "proxy.network_bytes_per_second"} {
		for _, bad := range []any{"invalid", true, "18446744073709551616", -1, 1.5} {
			viper.Reset()
			viper.Set(key, bad)
			command := &cobra.Command{Use: "proxy"}
			bindNetworkResourceFlags(command)
			if _, err := proxyNetworkResourceLimits(command); err == nil {
				t.Fatalf("bad %s=%v became unlimited", key, bad)
			}
		}
	}
}
