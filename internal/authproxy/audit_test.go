package authproxy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/auditlog"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

func TestAuditFailureCannotGrantButCanRevoke(t *testing.T) {
	p := newTestProxy(t)
	p.cfg.AuditDirectory = filepath.Join(t.TempDir(), "audit")
	token, e := p.IssueTokenWithPolicy("worker", 60, identity.Identity{}, &identity.ModelPolicy{AllowedModels: []string{"allowed"}})
	if e != nil {
		t.Fatal(e)
	}
	report, e := (auditlog.Journal{Directory: p.cfg.AuditDirectory}).Read()
	if e != nil || len(report.Records) != 2 || report.Records[1].Action != "permission_change" {
		t.Fatalf("%+v %v", report, e)
	}
	_ = os.WriteFile(filepath.Join(p.cfg.AuditDirectory, "head"), []byte("corrupt"), 0600)
	if _, e = p.IssueToken("other", 60); e == nil {
		t.Fatal("audit failure granted token")
	}
	p.RevokeToken("worker")
	r := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	if _, _, ok := p.workerRequest(r, false); ok {
		t.Fatal("audit failure prevented revocation")
	}
	if e = p.RestoreToken("worker", token, 60); e == nil {
		t.Fatal("audit failure restored token")
	}
}
