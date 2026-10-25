package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/networkpolicy"
	"github.com/pacificbelt30/codex-dock/internal/policyevents"
	"github.com/pacificbelt30/codex-dock/internal/proxysettings"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/strictjson"
	"github.com/pacificbelt30/codex-dock/internal/workeregress"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"os"
)

func configuredPolicy() proxysettings.Settings {
	defaultDeny := true
	if viper.IsSet("proxy.forward_default_deny") {
		defaultDeny = viper.GetBool("proxy.forward_default_deny")
	}
	return proxysettings.Settings{DefaultDeny: defaultDeny, UpstreamProxy: viper.GetString("proxy.upstream_proxy"), CAFile: viper.GetString("proxy.ca_bundle"), AllowDomains: viper.GetStringSlice("proxy.forward_allow_domains"), DenyDomains: viper.GetStringSlice("proxy.forward_deny_domains"), AllowCIDRs: viper.GetStringSlice("proxy.forward_allow_cidrs"), DenyCIDRs: viper.GetStringSlice("proxy.forward_deny_cidrs"), AllowPorts: viper.GetIntSlice("proxy.forward_allow_ports"), DenyPorts: viper.GetIntSlice("proxy.forward_deny_ports")}
}

func newPolicyCommand() *cobra.Command {
	policy := &cobra.Command{Use: "policy", Short: "Inspect, propose and apply egress policies or quarantine a selected worker"}
	var worker, preset string
	var domains []string
	var asJSON, resolve bool
	policy.PersistentFlags().StringVar(&worker, "worker", "", "Inspect a managed worker's actual selected proxy")
	policy.PersistentFlags().StringVar(&preset, "preset", "", "Preview an opt-in preset for new workers")
	policy.PersistentFlags().StringArrayVar(&domains, "allow-domain", nil, "Narrow the host allowlist for this preview")
	policy.PersistentFlags().BoolVar(&asJSON, "json", false, "Print structured JSON")
	get := func(cmd *cobra.Command) (workeregress.Snapshot, error) {
		if worker != "" {
			if preset != "" || len(domains) > 0 {
				return workeregress.Snapshot{}, fmt.Errorf("worker inspection cannot override its policy")
			}
			return workeregress.Inspect(cmd.Context(), engine.Socket(), worker)
		}
		rules, offline, err := workeregress.ResolvePreset(configuredPolicy().Rules(), preset, domains)
		if err == nil {
			_, err = networkpolicy.Compile(rules)
		}
		return workeregress.Snapshot{Source: "configuration for NEW workers; not a live update", PolicyID: workeregress.ID(rules), GeneralEgress: !offline, Rules: rules}, err
	}
	for _, name := range []string{"show", "check"} {
		name := name
		policy.AddCommand(&cobra.Command{Use: name, Short: "Validate and display the selected policy", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := get(cmd)
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(s)
			}
			if s.UpdatePending {
				return fmt.Errorf("policy update pending; new connections are blocked and completion is unconfirmed")
			}
			if s.Quarantined {
				if s.QuarantinePending {
					_, err = fmt.Fprintln(cmd.OutOrStdout(), "QUARANTINE PENDING: stop not confirmed; network access may continue. Retry policy quarantine for this worker.")
					return err
				}
				_, err = fmt.Fprintln(cmd.OutOrStdout(), "QUARANTINED: all execution and network access stopped; restart forbidden. Create a new worker to apply a new policy.")
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Source: %s\nPolicy: %s\nGeneral egress: %t\nPrivate destinations blocked: %t\nDefault deny without domain/CIDR grants: %t\nDomains: bare names include subdomains; =name is exact.\nAllow domains: %v\nDeny domains: %v\nAllow ports: %v\nDeny ports: %v\nAllow IP/CIDRs: %v\nDeny IP/CIDRs: %v\nFixed AI routes use their own live-token authorization.\n", s.Source, s.PolicyID, s.GeneralEgress, !s.GeneralEgress || s.Rules.BlockPrivate, s.Rules.DefaultDeny, s.Rules.AllowDomains, s.Rules.DenyDomains, s.Rules.AllowPorts, s.Rules.DenyPorts, s.Rules.AllowCIDRs, s.Rules.DenyCIDRs)
			return err
		}})
	}
	explain := &cobra.Command{Use: "explain HOST[:PORT]", Short: "Explain a destination without opening an upstream connection", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		s, err := get(cmd)
		if err != nil {
			return err
		}
		if s.Quarantined {
			if s.QuarantinePending {
				return fmt.Errorf("quarantine stop not confirmed; retry policy quarantine for this worker")
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(networkpolicy.Decision{Code: "worker_quarantined", Complete: true})
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "DENY worker_quarantined (all execution and network access stopped)")
			return err
		}
		if !s.GeneralEgress {
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"allowed": false, "code": "general_egress_disabled", "complete": true})
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "DENY general_egress_disabled (fixed AI routes are independent)")
			return err
		}
		d, err := networkpolicy.Explain(cmd.Context(), s.Rules, args[0], resolve, nil)
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(d)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "allowed=%t code=%s stage=%s complete=%t addresses_checked=%d\n", d.Allowed, d.Code, d.Stage, d.Complete, d.AddressesChecked)
		return err
	}}
	explain.Flags().BoolVar(&resolve, "resolve", false, "Resolve and validate every DNS answer; never connect")
	policy.AddCommand(explain, &cobra.Command{Use: "presets", Short: "List optional policy presets", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(workeregress.Presets())
		}
		for _, p := range workeregress.Presets() {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", p.Name, p.Description); err != nil {
				return err
			}
		}
		return nil
	}})
	var follow bool
	events := &cobra.Command{Use: "events", Short: "Read retained denial events for --worker (shared proxy history)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if worker == "" {
			return fmt.Errorf("--worker is required")
		}
		return workeregress.Events(cmd.Context(), engine.Socket(), worker, follow, func(e policyevents.Event) error {
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(e)
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s source=%s destination=%s reason=%s policy=%s\n", e.Time.Format("15:04:05"), e.Source, e.Destination, e.Code, e.PolicyID)
			return err
		})
	}}
	events.Flags().BoolVar(&follow, "follow", false, "Follow denial events until canceled")
	policy.AddCommand(events)
	policy.AddCommand(&cobra.Command{Use: "quarantine", Aliases: []string{"revoke"}, Short: "Stop ALL work/network access for --worker and forbid restart; keep workspace/logs", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if worker == "" || preset != "" || len(domains) > 0 {
			return fmt.Errorf("quarantine requires --worker and cannot use --preset or --allow-domain")
		}
		mgr, err := sandbox.NewManager(managementConfig())
		if err != nil {
			return err
		}
		defer func() { _ = mgr.Close() }()
		if err = mgr.QuarantineContext(cmd.Context(), worker); err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"worker": worker, "quarantined": true, "restart_allowed": false})
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "Worker %s quarantined: all work and network access stopped; workspace/logs retained. Create a new worker to apply a new policy.\n", worker)
		return err
	}})
	var policyFile, planID string
	for _, action := range []string{"plan", "apply"} {
		action := action
		c := &cobra.Command{Use: action, Args: cobra.NoArgs, Short: "Review or apply a shared proxy policy change; old connections are closed", RunE: func(cmd *cobra.Command, _ []string) error {
			if worker == "" || policyFile == "" || preset != "" || len(domains) > 0 {
				return fmt.Errorf("--worker and --file are required; presets/overrides cannot be combined")
			}
			f, err := os.Open(policyFile)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			var proposal workeregress.Proposal
			if err := strictjson.Decode(f, 64<<10, &proposal); err != nil {
				return fmt.Errorf("invalid policy proposal: %w", err)
			}
			if action == "plan" {
				p, err := workeregress.PlanChange(cmd.Context(), engine.Socket(), worker, proposal.Rules)
				if err != nil {
					return err
				}
				if err := proposal.ValidateBase(p.Before); err != nil {
					return err
				}
				return json.NewEncoder(cmd.OutOrStdout()).Encode(p)
			}
			state, err := workeregress.ApplyChange(cmd.Context(), engine.Socket(), worker, proposal.Rules, planID)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(state)
		}}
		c.Flags().StringVar(&policyFile, "file", "", "Reviewed proposal JSON file")
		if action == "apply" {
			c.Flags().StringVar(&planID, "plan-id", "", "Reviewed change plan ID")
		}
		policy.AddCommand(c)
	}
	var proposalOutput string
	propose := &cobra.Command{Use: "propose [HOST:PORT]", Args: cobra.MaximumNArgs(1), Short: "Draft an exact-host grant; omit destination to use the latest retained denial", RunE: func(cmd *cobra.Command, args []string) error {
		if worker == "" || proposalOutput == "" || preset != "" || len(domains) > 0 {
			return fmt.Errorf("--worker and --output are required")
		}
		current, err := workeregress.Inspect(cmd.Context(), engine.Socket(), worker)
		if err != nil {
			return err
		}
		destination := ""
		if len(args) == 1 {
			destination = args[0]
		} else {
			err = workeregress.Events(cmd.Context(), engine.Socket(), worker, false, func(e policyevents.Event) error {
				if e.Destination != "" && e.PolicyID == current.PolicyID {
					destination = e.Destination
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		if destination == "" {
			return fmt.Errorf("no suitable retained denial; specify HOST:PORT")
		}
		p, err := workeregress.Propose(cmd.Context(), current, destination)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(proposalOutput, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		if err = json.NewEncoder(f).Encode(p); err != nil {
			_ = os.Remove(proposalOutput)
			return err
		}
		return f.Sync()
	}}
	propose.Flags().StringVar(&proposalOutput, "output", "", "New proposal JSON file; never automatically approved")
	policy.AddCommand(propose)
	return policy
}
func init() { rootCmd.AddCommand(newPolicyCommand()) }
