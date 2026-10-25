package workeregress

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/pacificbelt30/codex-dock/internal/egresscontrol"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/networkpolicy"
	"io"
	"slices"
	"time"
)

type ChangePlan struct {
	ID                string              `json:"id"`
	Proxy             string              `json:"proxy"`
	Before            egresscontrol.State `json:"before"`
	Proposed          networkpolicy.Rules `json:"proposed"`
	Workers           []string            `json:"workers"`
	ConnectionsClosed string              `json:"connections_closed"`
}

// Control uses the existing trusted node exec channel, never a network admin
// listener. The helper talks only to the proxy's owner-private Unix socket.
func Control(ctx context.Context, socket, proxy string, update *egresscontrol.Update) (egresscontrol.State, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cli, err := engine.NewClientAt(socket)
	if err != nil {
		return egresscontrol.State{}, err
	}
	defer func() { _ = cli.Close() }()
	inspected, err := cli.ContainerInspect(ctx, proxy)
	if err != nil {
		return egresscontrol.State{}, err
	}
	if inspected.Config == nil || inspected.HostConfig == nil || inspected.Config.Labels[proxyPolicyLabel] == "" || inspected.HostConfig.Runtime != "runc" {
		return egresscontrol.State{}, fmt.Errorf("not a managed policy proxy")
	}
	if _, err = ParseProxyRules(inspected.Config.Cmd); err != nil {
		return egresscontrol.State{}, err
	}
	enabled := false
	for i, v := range inspected.Config.Cmd {
		if v == "--policy-control-dir" && i+1 < len(inspected.Config.Cmd) && inspected.Config.Cmd[i+1] == egresscontrol.Directory {
			enabled = true
		}
	}
	if !enabled {
		return egresscontrol.State{}, fmt.Errorf("proxy requires recreation to enable live policy control")
	}
	argv := []string{"/codex-dock", "proxy", "policy-control"}
	var input []byte
	if update != nil {
		argv = append(argv, "--apply")
		input, err = json.Marshal(update)
		if err != nil || len(input) > 64<<10 {
			return egresscontrol.State{}, fmt.Errorf("invalid policy update")
		}
	}
	created, err := cli.ContainerExecCreate(ctx, inspected.ID, container.ExecOptions{Cmd: argv, AttachStdin: update != nil, AttachStdout: true, AttachStderr: true, WorkingDir: "/"})
	if err != nil {
		return egresscontrol.State{}, err
	}
	stream, err := cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return egresscontrol.State{}, err
	}
	defer stream.Close()
	stop := context.AfterFunc(ctx, stream.Close)
	defer stop()
	if update != nil {
		go func() { _, _ = stream.Conn.Write(append(input, '\n')); _ = stream.CloseWrite() }()
	}
	var out limitedControl
	if _, err = stdcopy.StdCopy(&out, io.Discard, stream.Reader); err != nil {
		return egresscontrol.State{}, fmt.Errorf("policy response unavailable; inspect before retrying")
	}
	var state egresscontrol.State
	if json.Unmarshal(out.Bytes(), &state) != nil || state.PolicyID != ID(state.Rules) || state.Generation == 0 {
		return state, fmt.Errorf("invalid policy response; inspect before retrying")
	}
	status, err := cli.ContainerExecInspect(ctx, created.ID)
	if err != nil || status.Running || status.ExitCode != 0 || update != nil && !state.Ready {
		return state, fmt.Errorf("policy update not confirmed; inspect before retrying")
	}
	return state, nil
}

type limitedControl struct{ bytes.Buffer }

func (w *limitedControl) Write(p []byte) (int, error) {
	if len(p)+w.Len() > 64<<10 {
		return 0, fmt.Errorf("policy response too large")
	}
	return w.Buffer.Write(p)
}
func PlanChange(ctx context.Context, socket, worker string, rules networkpolicy.Rules) (ChangePlan, error) {
	if !rules.BlockPrivate {
		return ChangePlan{}, fmt.Errorf("private-address blocking is mandatory")
	}
	if _, err := networkpolicy.Compile(rules); err != nil {
		return ChangePlan{}, err
	}
	selected, err := Inspect(ctx, socket, worker)
	if err != nil {
		return ChangePlan{}, err
	}
	if !selected.GeneralEgress {
		return ChangePlan{}, fmt.Errorf("worker has no general-egress proxy")
	}
	state, err := Control(ctx, socket, selected.Proxy, nil)
	if err != nil {
		return ChangePlan{}, err
	}
	cli, err := engine.NewClientAt(socket)
	if err != nil {
		return ChangePlan{}, err
	}
	defer func() { _ = cli.Close() }()
	all, err := cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return ChangePlan{}, err
	}
	plan := ChangePlan{Proxy: selected.Proxy, Before: state, Proposed: rules, Workers: []string{}, ConnectionsClosed: "all HTTP and CONNECT connections on this shared proxy"}
	for _, v := range all {
		if v.Labels[managedLabel] != "true" {
			continue
		}
		s, err := Inspect(ctx, socket, v.ID)
		if err != nil {
			return ChangePlan{}, fmt.Errorf("cannot determine affected workers: %w", err)
		}
		if s.Proxy == plan.Proxy {
			plan.Workers = append(plan.Workers, v.ID)
		}
	}
	slices.Sort(plan.Workers)
	data, _ := json.Marshal(plan)
	sum := sha256.Sum256(data)
	plan.ID = hex.EncodeToString(sum[:])
	return plan, nil
}
func ApplyChange(ctx context.Context, socket, worker string, rules networkpolicy.Rules, expected string) (egresscontrol.State, error) {
	p, err := PlanChange(ctx, socket, worker, rules)
	if err != nil {
		return egresscontrol.State{}, err
	}
	if expected == "" || p.ID != expected {
		return egresscontrol.State{}, fmt.Errorf("policy plan changed; review again")
	}
	return Control(ctx, socket, p.Proxy, &egresscontrol.Update{ExpectedGeneration: p.Before.Generation, Rules: rules})
}

// Rules are declarative; a textual diff cannot prove whether DNS/CIDR overlap
// widens access. Callers must review the complete before/proposed rule sets.
func HasLiveControl(args []string) bool {
	for i, arg := range args {
		if arg == "--policy-control-dir" && i+1 < len(args) && args[i+1] == egresscontrol.Directory {
			return true
		}
	}
	return false
}
