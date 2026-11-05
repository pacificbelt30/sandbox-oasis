package sandboxux

import (
	"strings"
	"testing"
)

func TestArtifactSelectionPathsKeepBackendExclusions(t *testing.T) {
	for _, name := range []string{".env", "sub/.ENV.local", ".ssh/config", "AUTH.json", "sub/credentials.json", "sub/private.KEY", "sub/.git/config", "link/../file", "/file", "sub\n/file", strings.Repeat("x", 95)} {
		if ArtifactPath(name) == nil {
			t.Errorf("offered excluded artifact %q", name)
		}
	}
	for _, name := range []string{"result.txt", "sub/日本語.txt", "config.toml", strings.Repeat("x", 94)} {
		if err := ArtifactPath(name); err != nil {
			t.Errorf("refused regular artifact %q: %v", name, err)
		}
	}
}
