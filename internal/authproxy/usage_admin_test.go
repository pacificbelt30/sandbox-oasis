package authproxy

import (
	"net/http"
	"strings"
	"testing"

	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

func TestUsageAdminRemainsOffWorkerListenerAndMetadataIsNotInference(t *testing.T) {
	p := meteredProxy(t)
	if err := p.PrepareUsage("worker", identity.Identity{}, &identity.UsagePolicy{MaxRequests: 2}); err != nil {
		t.Fatal(err)
	}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/admin/usage?container=worker", "/admin/usage/prepare"} {
		resp, err := http.Get(p.Endpoint() + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatalf("worker reached usage admin: %s %d", path, resp.StatusCode)
		}
	}
	req, _ := http.NewRequest("GET", "http://"+p.adminAddr+"/admin/usage?container=worker", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatal("usage admin accepted no authority")
	}
	req.Header.Set("X-Proxy-Admin-Secret", strings.Repeat("s", 32))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal("authorized usage reporting failed")
	}
	for _, route := range []struct{ method, path string }{
		{"GET", "/v1/models"}, {"GET", "/chatgpt/codex/models"}, {"POST", "/anthropic/v1/messages/count_tokens"},
	} {
		r, _ := http.NewRequest(route.method, "http://proxy"+route.path, nil)
		if !usageMetadataRoute(r) || tokenUsageRoute(r) {
			t.Fatal("model metadata was treated as inference")
		}
	}
}
