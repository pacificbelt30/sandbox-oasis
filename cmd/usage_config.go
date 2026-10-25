package cmd

import "path/filepath"

// Legacy unprotected admin configurations keep their previous unlimited mode.
// New limits need protected administration; independent login already sets it up.
func managedUsageDirectory(home, name, adminSecret string) string {
	if adminSecret == "" {
		return ""
	}
	return filepath.Join(home, ".local", "state", "codex-dock", "usage", caseDigest(name))
}
