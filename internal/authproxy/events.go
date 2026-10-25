package authproxy

import (
	"errors"
	"net/http"

	"github.com/pacificbelt30/codex-dock/internal/networkpolicy"
	"github.com/pacificbelt30/codex-dock/internal/policyevents"
)

// PolicyEvents returns a bounded snapshot. A sequence is local to this process.
func (p *Proxy) PolicyEvents(after uint64) []policyevents.Event { return p.events.Since(after) }

func (p *Proxy) recordDenial(w http.ResponseWriter, r *http.Request, err error) {
	code := networkpolicy.Reason(err)
	w.Header().Set("X-Cxd-Policy-Reason", code)
	event := policyevents.Event{PolicyID: p.cfg.PolicyID, Source: r.RemoteAddr, Code: code}
	if epoch, ok := r.Context().Value(egressPolicyKey{}).(*egressEpoch); ok {
		event.PolicyID = epoch.state.PolicyID
	}
	var denied *networkpolicy.Denial
	if errors.As(err, &denied) {
		event.Destination = denied.Destination.String()
	}
	// Never infer a run identity from untrusted headers on the shared egress proxy.
	p.events.Add(event)
	p.auditDenial(code)
}
