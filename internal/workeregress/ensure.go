package workeregress

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	dn "github.com/docker/docker/api/types/network"
	"github.com/pacificbelt30/codex-dock/internal/buildinfo"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/netquota"
	"github.com/pacificbelt30/codex-dock/internal/network"
	"github.com/pacificbelt30/codex-dock/internal/networkpolicy"
)

const (
	managedLabel       = "codex-dock.managed"
	proxyLayoutLabel   = "io.codex-dock.proxy-layout"
	proxyLayoutVersion = "split-v3-oauth-state"
	proxyPolicyLabel   = "io.codex-dock.forward-policy"
	imageProtocolLabel = "io.codex-dock.proxy-protocol"
	imageProtocol      = "external-store-v1"
	imageBuildLabel    = "io.codex-dock.proxy-build"
	proxyPort          = 18082
)

// Ensure creates or resumes an immutable, credential-free policy proxy. It
// never replaces a mismatched proxy because retained workers pin its address.
func Ensure(ctx context.Context, socket, image string, rules networkpolicy.Rules) (string, error) {
	return EnsureWithLimits(ctx, socket, image, rules, netquota.Limits{})
}

func EnsureWithLimits(ctx context.Context, socket, image string, rules networkpolicy.Rules, limits netquota.Limits) (string, error) {
	if err := limits.Validate(); err != nil {
		return "", err
	}
	if !rules.BlockPrivate || !Configured(rules) {
		return "", fmt.Errorf("worker egress requires an explicit policy and private address blocking")
	}
	if _, err := networkpolicy.Compile(rules); err != nil {
		return "", err
	}
	if image == "" {
		image = "codex-dock-proxy:latest"
	}
	cli, err := engine.NewClientAt(socket)
	if err != nil {
		return "", err
	}
	defer func() { _ = cli.Close() }()
	imageInfo, err := cli.ImageInspect(ctx, image)
	if err != nil {
		return "", fmt.Errorf("inspecting egress proxy image: %w", err)
	}
	if imageInfo.Config == nil || imageInfo.Config.Labels[imageProtocolLabel] != imageProtocol || imageInfo.Config.Labels[imageBuildLabel] != buildinfo.Version {
		return "", fmt.Errorf("egress proxy image %q is incompatible with the Go client; rebuild it before launching workers", image)
	}
	if existing, err := cli.NetworkInspect(ctx, network.EgressNetworkName, dn.InspectOptions{}); err == nil {
		if existing.Driver != "bridge" || existing.Labels[managedLabel] != "true" {
			return "", fmt.Errorf("egress network %q is not managed by Oaiso", network.EgressNetworkName)
		}
	} else if errdefs.IsNotFound(err) {
		_, err = cli.NetworkCreate(ctx, network.EgressNetworkName, dn.CreateOptions{Driver: "bridge", Options: map[string]string{"com.docker.network.bridge.name": network.EgressBridgeName}, Labels: map[string]string{managedLabel: "true"}})
		if err != nil {
			return "", fmt.Errorf("creating egress network: %w", err)
		}
	} else {
		return "", fmt.Errorf("inspecting egress network: %w", err)
	}
	name := Name(rules)
	if id := limits.ID(); id != "" {
		name += "-" + id[:16]
	}
	if existing, err := cli.ContainerInspect(ctx, name); err == nil {
		if existing.Config == nil || existing.State == nil || existing.Config.Image != image || existing.Config.Labels[proxyLayoutLabel] != proxyLayoutVersion || existing.Config.Labels[proxyPolicyLabel] != ID(rules) || existing.Config.Labels[netquota.Label] != limits.ID() {
			return "", fmt.Errorf("egress proxy %q has a different image, layout or policy; drain its workers before replacement", name)
		}
		if !existing.State.Running {
			if err := cli.ContainerStart(ctx, name, container.StartOptions{}); err != nil {
				return "", err
			}
		}
	} else if errdefs.IsNotFound(err) {
		args := []string{"proxy", "serve", "--listen", "0.0.0.0:" + strconv.Itoa(proxyPort), "--role", "egress", "--block-private"}
		if rules.DefaultDeny {
			args = append(args, "--forward-default-deny")
		} else {
			args = append(args, "--forward-default-deny=false")
		}
		for _, pair := range []struct {
			flag   string
			values []string
		}{{"--forward-allow-domain", rules.AllowDomains}, {"--forward-deny-domain", rules.DenyDomains}, {"--forward-allow-cidr", rules.AllowCIDRs}, {"--forward-deny-cidr", rules.DenyCIDRs}} {
			for _, value := range pair.values {
				args = append(args, pair.flag, value)
			}
		}
		for _, pair := range []struct {
			flag   string
			values []int
		}{{"--forward-allow-port", rules.AllowPorts}, {"--forward-deny-port", rules.DenyPorts}} {
			for _, value := range pair.values {
				args = append(args, pair.flag, strconv.Itoa(value))
			}
		}
		args = append(args, limits.Args()...)
		cfg := &container.Config{Image: image, User: "0:0", Cmd: args, Labels: map[string]string{proxyLayoutLabel: proxyLayoutVersion, proxyPolicyLabel: ID(rules)}}
		if id := limits.ID(); id != "" {
			cfg.Labels[netquota.Label] = id
		}
		host := &container.HostConfig{Runtime: "runc", CapDrop: []string{"ALL"}, SecurityOpt: []string{"no-new-privileges:true"}, NetworkMode: container.NetworkMode(network.EgressNetworkName)}
		created, err := cli.ContainerCreate(ctx, cfg, host, nil, nil, name)
		if err != nil {
			return "", err
		}
		if err := cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
			return "", err
		}
	} else {
		return "", fmt.Errorf("inspecting egress proxy: %w", err)
	}
	return (&url.URL{Scheme: "http", Host: name + ":" + strconv.Itoa(proxyPort)}).String(), nil
}
