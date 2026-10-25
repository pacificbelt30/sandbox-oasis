package authproxy

import (
	"errors"
	"log"
	"path/filepath"

	"github.com/pacificbelt30/codex-dock/internal/auditlog"
)

func (p *Proxy) auditBegin(action, subject string) (func(error) error, error) {
	dir := p.cfg.AuditDirectory
	if dir == "" && p.cfg.UsageStateDir != "" {
		dir = filepath.Join(p.cfg.UsageStateDir, "audit")
	}
	if dir == "" {
		return func(error) error { return nil }, nil
	}
	return (auditlog.Journal{Directory: dir}).Begin(action, subject)
}
func (p *Proxy) auditRestrictive(action, subject string) func(error) {
	finish, err := p.auditBegin(action, subject)
	if err != nil {
		log.Print("audit unavailable during proxy revocation")
		return func(error) {}
	}
	return func(outcome error) {
		if finish(outcome) != nil {
			log.Print("audit result unavailable during proxy revocation")
		}
	}
}
func auditResult(finish func(error) error, result *error) {
	if err := finish(*result); err != nil {
		*result = errors.New("audit result unavailable; operation outcome unknown")
	}
}
