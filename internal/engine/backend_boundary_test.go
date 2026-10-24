//go:build linux

package engine

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNodeRejectsDaemonPluginEndpointsBeforeRPC(t *testing.T) {
	// A nil client makes any attempted runtime access fail the test. Unsupported
	// daemon APIs must be rejected before decoding input or touching containerd.
	backend := &Backend{}
	for _, prefix := range []string{"", "/v1.51"} {
		for _, endpoint := range []struct{ method, path string }{
			{http.MethodPost, "/plugins/pull?remote=example.invalid/plugin"},
			{http.MethodPost, "/plugins/create"},
			{http.MethodPost, "/plugins/example/enable"},
			{http.MethodGet, "/plugins/privileges?remote=example.invalid/plugin"},
			{http.MethodPost, "/auth"},
			{http.MethodGet, "/unknown"},
		} {
			t.Run(prefix+endpoint.path, func(t *testing.T) {
				request := httptest.NewRequest(endpoint.method, prefix+endpoint.path, strings.NewReader("{invalid"))
				response := httptest.NewRecorder()
				backend.ServeHTTP(response, request)
				if response.Code != http.StatusNotFound {
					t.Fatalf("unsupported API returned %d, want 404", response.Code)
				}
			})
		}
	}
}
