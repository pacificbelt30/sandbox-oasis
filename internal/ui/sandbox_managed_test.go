package ui

import (
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
)

func TestManagedRestoreWithoutLauncherExplainsUnavailableConnection(t *testing.T) {
	d, _, _ := testDesktop(t)
	d.createPrepared(sandboxux.LaunchSpec{Workspace: "existing"})
	if name, _ := d.pages.GetFrontPage(); name != "message" || d.busy {
		t.Fatal("legacy connection started a launch with no launcher", name, d.busy)
	}
}
