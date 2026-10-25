package workeregress

import (
	"context"
	"github.com/pacificbelt30/codex-dock/internal/egresscontrol"
	"github.com/pacificbelt30/codex-dock/internal/networkpolicy"
	"testing"
)

func TestProposalPreservesDeniesPortsAndExactMatching(t *testing.T) {
	s := Snapshot{GeneralEgress: true, Rules: networkpolicy.Rules{DefaultDeny: true, BlockPrivate: true, AllowPorts: []int{443}, DenyDomains: []string{"blocked.example"}}}
	p, err := Propose(context.Background(), s, "allowed.example:443")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Rules.AllowDomains) != 1 || p.Rules.AllowDomains[0] != "=allowed.example" || len(s.Rules.AllowDomains) != 0 {
		t.Fatal("proposal broadened or changed current policy")
	}
	for _, host := range []string{"blocked.example:443", "allowed.example:80", "127.0.0.1:443", "169.254.169.254:80"} {
		if _, err := Propose(context.Background(), s, host); err == nil {
			t.Fatal("unsafe proposal", host)
		}
	}
}

func TestProposalCanFinishItsPendingIntentWithoutAcceptingOtherStaleGrants(t *testing.T) {
	before := networkpolicy.Rules{DefaultDeny: true, BlockPrivate: true}
	desired := networkpolicy.Rules{DefaultDeny: true, BlockPrivate: true, AllowDomains: []string{"=example.com"}}
	proposal := Proposal{BasedOnPolicy: ID(before), Rules: desired}
	for _, tc := range []struct {
		state egresscontrol.State
		ok    bool
	}{
		{egresscontrol.State{PolicyID: ID(before), Ready: true}, true},
		{egresscontrol.State{PolicyID: ID(desired), Ready: false}, true},
		{egresscontrol.State{PolicyID: ID(desired), Ready: true}, false},
		{egresscontrol.State{PolicyID: ID(networkpolicy.Rules{BlockPrivate: true, DefaultDeny: true, AllowDomains: []string{"=other.example"}}), Ready: false}, false},
	} {
		if err := proposal.ValidateBase(tc.state); (err == nil) != tc.ok {
			t.Fatalf("pending=%t policy=%s error=%v", !tc.state.Ready, tc.state.PolicyID, err)
		}
	}
}
