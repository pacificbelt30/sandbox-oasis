package cmd

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyUnprotectedProxyDoesNotImplicitlyEnableUsageLimits(t *testing.T) {
	home := t.TempDir()
	if path := managedUsageDirectory(home, "proxy", ""); path != "" {
		t.Fatal("legacy proxy unexpectedly requires usage storage/admin authority")
	}
	path := managedUsageDirectory(home, "proxy", "dummy-secret")
	if !filepath.IsAbs(path) || !strings.HasPrefix(path, home+string(filepath.Separator)) || strings.Contains(path, "dummy-secret") {
		t.Fatal("unsafe managed usage path")
	}
	if path == managedUsageDirectory(home, "other-proxy", "dummy-secret") {
		t.Fatal("proxies share one ledger by default")
	}
}
