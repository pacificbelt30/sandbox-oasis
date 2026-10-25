package cmd

import (
	"fmt"
	"net/url"

	"github.com/pacificbelt30/codex-dock/internal/authproxy"
	"github.com/pacificbelt30/codex-dock/internal/network"
	"github.com/pacificbelt30/codex-dock/internal/workeregress"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// workerEgressSpec creates an immutable policy-specific egress proxy. A worker
// network connects to exactly this instance rather than the unrestricted one.
func workerEgressSpec(cmd *cobra.Command, domains []string) (proxyRunArgs, error) {
	applyForwardPolicyDefaults(cmd)
	host := forwardPolicyRules()
	if len(domains) == 0 && !host.DefaultDeny {
		return proxyRunArgs{}, fmt.Errorf("empty worker egress allowlist requires default-deny mode")
	}
	rules, _, err := workeregress.ResolvePreset(host, "", domains)
	if err != nil {
		return proxyRunArgs{}, err
	}
	resources, err := proxyNetworkResourceLimits(cmd)
	if err != nil {
		return proxyRunArgs{}, err
	}
	spec := proxyRunArgs{networkResources: resources, upstreamProxy: proxyUpstreamURL, caFile: proxyCAFile, role: authproxy.RoleEgress, networkName: proxyRunNetwork, image: viper.GetString("proxy_image"), listenAddr: "0.0.0.0:18082", blockPrivate: true, forwardPolicy: rules}
	spec.name = workeregress.Name(rules)
	if id := spec.networkResources.ID(); id != "" {
		spec.name += "-" + id[:16]
	}
	return spec, nil
}

func ensureWorkerEgressProxy(cmd *cobra.Command, domains []string) (string, error) {
	ctx := cmd.Context()
	spec, err := workerEgressSpec(cmd, domains)
	if err != nil {
		return "", err
	}
	if err := ensureBridgeNetwork(ctx, spec.networkName, network.ProxyBridgeName); err != nil {
		return "", err
	}
	if err := ensureProxyImage(ctx, spec.image); err != nil {
		return "", err
	}
	if err := ensureProxyContainer(ctx, spec); err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "http", Host: fmt.Sprintf("%s:18082", spec.name)}).String(), nil
}
