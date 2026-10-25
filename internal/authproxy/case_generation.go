package authproxy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pacificbelt30/codex-dock/pkg/cxd/credentialstore"
)

// checkCaseGeneration prevents a retained proxy from issuing or restoring a
// token after the host product replaced or disconnected its credential case.
func (p *Proxy) checkCaseGeneration() error {
	if p.cfg.CredentialStore == nil {
		return nil
	}
	p.refreshMu.Lock()
	defer p.refreshMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	current, err := p.cfg.CredentialStore.Load(ctx, p.credentialScope())
	if err != nil {
		if errors.Is(err, credentialstore.ErrNotFound) {
			p.revokeCaseTokens()
		}
		return fmt.Errorf("checking credential case: %w", err)
	}
	if current.Version != p.storeVersion || current.ConnectionVersion != p.caseConnectionVersion {
		p.revokeCaseTokens()
		return credentialstore.ErrConflict
	}
	return nil
}

func (p *Proxy) credentialScope() string {
	if p.cfg.ProfileName != "" {
		return p.cfg.ProfileName
	}
	return p.cfg.CaseID
}

func (p *Proxy) revokeCaseTokens() {
	finish := p.auditRestrictive("revoke", p.credentialScope())
	defer finish(nil)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.caseRevoked = true
	for name := range p.tokens {
		p.removeTokenLocked(name)
	}
}
