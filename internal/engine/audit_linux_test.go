//go:build linux

package engine

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrokenAuditRefusesCreateBeforeBackendAccess(t *testing.T) {
	state := t.TempDir()
	dir := filepath.Join(state, "audit")
	if e := os.Mkdir(dir, 0755); e != nil {
		t.Fatal(e)
	}
	b := &Backend{opts: Options{State: state}}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1.51/containers/create?name=worker", strings.NewReader(`{}`))
	b.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("%d", w.Code)
	}
}
func TestAuditRouteClassification(t *testing.T) {
	for _, item := range []struct{ method, path, action string }{{"POST", "/containers/create", "create"}, {"POST", "/containers/id/start", "start"}, {"POST", "/containers/id/stop", "stop"}, {"POST", "/containers/id/quarantine", "stop"}, {"DELETE", "/containers/id", "delete"}, {"POST", "/checkpoints", "checkpoint"}, {"DELETE", "/checkpoints/id", "delete"}, {"GET", "/containers/json", ""}, {"POST", "/containers/id/attach", ""}} {
		r := httptest.NewRequest(item.method, item.path, nil)
		got, _ := nodeAuditAction(r)
		if got != item.action {
			t.Errorf("%s: %s", item.path, got)
		}
	}
}
