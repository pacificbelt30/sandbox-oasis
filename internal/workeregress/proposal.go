package workeregress

import (
	"context"
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/egresscontrol"
	"github.com/pacificbelt30/codex-dock/internal/networkpolicy"
	"net/netip"
	"slices"
)

type Proposal struct {
	BasedOnPolicy string              `json:"based_on_policy"`
	Destination   string              `json:"destination"`
	Rules         networkpolicy.Rules `json:"rules"`
	Notice        string              `json:"notice"`
}

// Propose adds one exact hostname. It never removes a deny, broadens the global
// port set, grants an IP range or applies anything. DNS is rechecked at dial.
func Propose(ctx context.Context, current Snapshot, destination string) (Proposal, error) {
	if !current.GeneralEgress || current.UpdatePending {
		return Proposal{}, fmt.Errorf("general egress is disabled or policy update is pending")
	}
	d, err := networkpolicy.ParseDestination(destination, "443")
	if err != nil {
		return Proposal{}, err
	}
	if _, err := netip.ParseAddr(d.Host); err == nil {
		return Proposal{}, fmt.Errorf("IP grants require manual policy review")
	}
	before, err := networkpolicy.Explain(ctx, current.Rules, d.String(), false, nil)
	if err != nil {
		return Proposal{}, err
	}
	if before.Allowed {
		return Proposal{}, fmt.Errorf("destination name and port already permitted; inspect DNS/address denials instead")
	}
	rules := current.Rules
	if len(rules.AllowPorts) > 0 && !slices.Contains(rules.AllowPorts, int(d.Port)) {
		return Proposal{}, fmt.Errorf("port not granted; changing shared port grants requires manual review")
	}
	rules.AllowDomains = append(append([]string(nil), rules.AllowDomains...), "="+d.Host)
	decision, err := networkpolicy.Explain(ctx, rules, d.String(), false, nil)
	if err != nil {
		return Proposal{}, err
	}
	if !decision.Allowed {
		return Proposal{}, fmt.Errorf("proposal still denied (%s); existing denies are preserved", decision.Code)
	}
	return Proposal{BasedOnPolicy: current.PolicyID, Destination: d.String(), Rules: rules, Notice: "pending review; no change applied; DNS and private-address checks remain mandatory"}, nil
}

// ValidateBase accepts the original proposal when finishing its own pending
// intent. Other stale proposals must be drafted against the observed policy.
func (p Proposal) ValidateBase(state egresscontrol.State) error {
	if p.BasedOnPolicy == "" || p.BasedOnPolicy == state.PolicyID || !state.Ready && ID(p.Rules) == state.PolicyID {
		return nil
	}
	return fmt.Errorf("proposal is based on an older policy; create a new proposal")
}
