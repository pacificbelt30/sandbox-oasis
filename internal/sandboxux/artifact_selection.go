package sandboxux

import (
	"fmt"
	"strings"
)

// Match the artifact backend's path exclusions before offering a file in the UI.
// The backend still validates contents and paths again when planning/exporting.
func ArtifactPath(name string) error {
	if err := GuestPath(name, false); err != nil {
		return err
	}
	if len(name) > 94 {
		return fmt.Errorf("artifact paths must fit within 94 UTF-8 bytes")
	}
	for _, part := range strings.Split(name, "/") {
		n := strings.ToLower(part)
		switch n {
		case ".git", ".ssh", ".aws", ".azure", ".config", ".codex", ".claude", "auth.json", "credentials.json", ".npmrc", ".netrc", ".pypirc":
			return fmt.Errorf("credential path excluded")
		}
		if strings.HasPrefix(n, ".env") || strings.HasSuffix(n, ".pem") || strings.HasSuffix(n, ".key") || strings.HasSuffix(n, ".p12") || strings.HasSuffix(n, ".pfx") {
			return fmt.Errorf("credential path excluded")
		}
	}
	return nil
}
