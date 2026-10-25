package authproxy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestOAuthAuditFailureRevokesAndDoesNotPublishRotatedCredential(t *testing.T) {
	p := newTestProxy(t)
	p.cfg.AuditDirectory = filepath.Join(t.TempDir(), "audit")
	p.cfg.OAuthStateDir = filepath.Join(t.TempDir(), "state")
	p.oauthCreds = &OAuthCredentials{AccessToken: "fixture-old-access", RefreshToken: "fixture-old-refresh"}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fixture-new-access","refresh_token":"fixture-new-refresh","expires_in":3600}`))
	}))
	defer upstream.Close()
	p.oauthTokenURL = upstream.URL
	p.httpClient = upstream.Client()
	token, err := p.IssueToken("worker", 60)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(p.cfg.AuditDirectory, "head"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/oauth/token?cdx="+token, strings.NewReader(`{"grant_type":"refresh_token","client_id":"fixture"}`))
	response := httptest.NewRecorder()
	p.handleOAuthTokenRefresh(response, request)
	if response.Code < 500 || p.oauthCreds.AccessToken != "fixture-old-access" || strings.Contains(response.Body.String(), "fixture-new-access") {
		t.Fatal("failed audit published refreshed credentials")
	}
	request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if _, _, ok := p.workerRequest(request, false); ok {
		t.Fatal("failed refresh audit retained worker authority")
	}
}
