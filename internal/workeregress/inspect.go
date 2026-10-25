package workeregress

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/networkpolicy"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"
)

type Snapshot struct {
	Source            string              `json:"source"`
	Worker            string              `json:"worker,omitempty"`
	Proxy             string              `json:"proxy,omitempty"`
	PolicyID          string              `json:"policy_id,omitempty"`
	Generation        uint64              `json:"generation,omitempty"`
	UpdatePending     bool                `json:"update_pending,omitempty"`
	GeneralEgress     bool                `json:"general_egress"`
	Quarantined       bool                `json:"quarantined,omitempty"`
	QuarantinePending bool                `json:"quarantine_pending,omitempty"`
	Rules             networkpolicy.Rules `json:"rules"`
}

// Inspect reads the launch policy and live generation of the selected proxy. It
// does not confuse a newly edited host config with a running proxy's policy.
func Inspect(ctx context.Context, socket, worker string) (Snapshot, error) {
	cli, err := engine.NewClientAt(socket)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = cli.Close() }()
	v, err := cli.ContainerInspect(ctx, worker)
	if err != nil {
		return Snapshot{}, err
	}
	if v.Config == nil || v.Config.Labels[managedLabel] != "true" {
		return Snapshot{}, fmt.Errorf("worker is not managed by Oaiso")
	}
	result := Snapshot{Source: "running worker configuration", Worker: v.ID, Quarantined: v.Config.Labels[workerpolicy.QuarantinedLabel] == "true"}
	if result.Quarantined {
		result.QuarantinePending = v.State == nil || v.State.Running || v.State.Paused || v.State.Status != "created" && v.State.Status != "exited"
		if !result.QuarantinePending {
			return result, nil
		}
	}
	endpoint := ""
	for _, env := range v.Config.Env {
		if strings.HasPrefix(env, "HTTP_PROXY=") {
			endpoint = strings.TrimPrefix(env, "HTTP_PROXY=")
			break
		}
	}
	if endpoint == "" {
		return result, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.User != nil || u.Hostname() == "" {
		return result, fmt.Errorf("worker uses an unsupported egress endpoint")
	}
	reference, err := proxyReference(v, u.Hostname())
	if err != nil {
		return result, err
	}
	p, err := cli.ContainerInspect(ctx, reference)
	if err != nil {
		return result, err
	}
	if p.Config == nil || p.Config.Labels[proxyPolicyLabel] == "" {
		return result, fmt.Errorf("proxy does not advertise an immutable Oaiso egress policy")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !proxySharesAddress(v, p, ip) {
		return result, fmt.Errorf("selected proxy address drifted from the worker network")
	}
	rules, err := ParseProxyRules(p.Config.Cmd)
	if err != nil {
		return result, err
	}
	if ID(rules) != p.Config.Labels[proxyPolicyLabel] {
		return result, fmt.Errorf("proxy policy identity does not match its launch configuration")
	}
	result.Proxy, result.PolicyID, result.Rules, result.GeneralEgress = p.ID, ID(rules), rules, true
	if HasLiveControl(p.Config.Cmd) {
		state, err := Control(ctx, socket, p.ID, nil)
		if err != nil {
			return result, err
		}
		result.Source = "selected proxy live policy generation"
		result.PolicyID, result.Rules, result.Generation, result.UpdatePending = state.PolicyID, state.Rules, state.Generation, !state.Ready
	}
	return result, nil
}

func ParseProxyRules(args []string) (networkpolicy.Rules, error) {
	var rules networkpolicy.Rules
	role := ""
	for i := 0; i < len(args); i++ {
		key, value, inline := strings.Cut(args[i], "=")
		if key == "--forward-default-deny" {
			rules.DefaultDeny = true
			if inline {
				parsed, err := strconv.ParseBool(value)
				if err != nil {
					return rules, fmt.Errorf("invalid default-deny policy flag")
				}
				rules.DefaultDeny = parsed
			}
			continue
		}
		if key == "--block-private" {
			if !inline {
				rules.BlockPrivate = true
			} else {
				parsed, err := strconv.ParseBool(value)
				if err != nil {
					return rules, fmt.Errorf("invalid private-address policy flag")
				}
				rules.BlockPrivate = parsed
			}
			continue
		}
		known := key == "--role" || strings.HasPrefix(key, "--forward-")
		if !known {
			continue
		}
		if !inline {
			i++
			if i >= len(args) {
				return rules, fmt.Errorf("incomplete proxy policy arguments")
			}
			value = args[i]
		}
		switch key {
		case "--role":
			role = value
		case "--forward-allow-domain":
			rules.AllowDomains = append(rules.AllowDomains, value)
		case "--forward-deny-domain":
			rules.DenyDomains = append(rules.DenyDomains, value)
		case "--forward-allow-cidr":
			rules.AllowCIDRs = append(rules.AllowCIDRs, value)
		case "--forward-deny-cidr":
			rules.DenyCIDRs = append(rules.DenyCIDRs, value)
		case "--forward-allow-port", "--forward-deny-port":
			for _, part := range strings.Split(value, ",") {
				n, err := strconv.Atoi(part)
				if err != nil {
					return rules, err
				}
				if key == "--forward-allow-port" {
					rules.AllowPorts = append(rules.AllowPorts, n)
				} else {
					rules.DenyPorts = append(rules.DenyPorts, n)
				}
			}
		default:
			return rules, fmt.Errorf("unsupported proxy policy flag %s", key)
		}
	}
	if role != "egress" {
		return rules, fmt.Errorf("proxy is not a credential-free egress proxy")
	}
	_, err := networkpolicy.Compile(rules)
	return rules, err
}

// Kata URLs contain pinned IPs. Resolve them using the worker's immutable
// launch labels and host mapping rather than treating an IP as a container ID.
func proxyReference(worker container.InspectResponse, host string) (string, error) {
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return host, nil
	}
	if worker.Config == nil || worker.HostConfig == nil {
		return "", fmt.Errorf("worker has no pinned proxy mapping")
	}
	var names []string
	if err = json.Unmarshal([]byte(worker.Config.Labels["codex-dock.proxy-hosts"]), &names); err != nil {
		return "", fmt.Errorf("invalid pinned proxy identities")
	}
	allowed := map[string]bool{}
	for _, name := range names {
		allowed[name] = true
	}
	found := ""
	for _, entry := range worker.HostConfig.ExtraHosts {
		name, address, ok := strings.Cut(entry, ":")
		if !ok || !allowed[name] {
			continue
		}
		candidate, err := netip.ParseAddr(strings.Trim(address, "[]"))
		if err != nil || candidate.Unmap() != ip.Unmap() {
			continue
		}
		if found != "" && found != name {
			return "", fmt.Errorf("ambiguous pinned proxy identity")
		}
		found = name
	}
	if found == "" {
		return "", fmt.Errorf("worker proxy IP is not mapped to an advertised proxy identity")
	}
	return found, nil
}
func proxySharesAddress(worker, proxy container.InspectResponse, ip netip.Addr) bool {
	if worker.NetworkSettings == nil || proxy.NetworkSettings == nil {
		return false
	}
	for name, front := range worker.NetworkSettings.Networks {
		if front == nil || front.NetworkID == "" {
			continue
		}
		other := proxy.NetworkSettings.Networks[name]
		if other == nil || other.NetworkID != front.NetworkID {
			continue
		}
		for _, address := range []string{other.IPAddress, other.GlobalIPv6Address} {
			candidate, err := netip.ParseAddr(address)
			if err == nil && candidate.Unmap() == ip.Unmap() {
				return true
			}
		}
	}
	return false
}
