package authproxy

import (
	"regexp"
	"testing"

	dockerdefaults "github.com/pacificbelt30/codex-dock/docker"
)

func pinnedAgentVersion(t *testing.T, arg string) string {
	t.Helper()
	match := regexp.MustCompile(`(?m)^ARG ` + arg + `=([0-9]+\.[0-9]+\.[0-9]+)$`).FindSubmatch(dockerdefaults.Dockerfile)
	if len(match) != 2 {
		t.Fatal("missing exact embedded CLI pin")
	}
	return string(match[1])
}
