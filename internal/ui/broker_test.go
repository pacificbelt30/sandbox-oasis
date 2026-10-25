package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/pacificbelt30/codex-dock/internal/brokersettings"
	"github.com/rivo/tview"
)

func TestBrokerFormNeverLoadsSavedSecretAndPreservesItOnSave(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	path := filepath.Join(dir, "routes.json")
	d, _ := brokersettings.Template("bearer-readonly", "service", "https://api.example.com", "/v1")
	d.ExactPaths = []string{"/single"}
	_, rev, _ := brokersettings.Load(path)
	if err := brokersettings.Save(path, rev, d, "fixture-private-secret"); err != nil {
		t.Fatal(err)
	}
	page := brokerSettingsPage(path, func() {}).(*tview.Flex)
	form := page.GetItem(0).(*tview.Form)
	form.GetFormItem(1).(*tview.InputField).SetText("service")
	form.GetButton(1).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	for i := 1; i < form.GetFormItemCount(); i++ {
		if field, ok := form.GetFormItem(i).(*tview.InputField); ok && field.GetText() == "fixture-private-secret" {
			t.Fatal("saved secret loaded into form")
		}
	}
	if form.GetFormItem(7).(*tview.InputField).GetText() != "/single" {
		t.Fatal("exact scope lost on load")
	}
	form.GetFormItem(3).(*tview.InputField).SetText("/v2")
	form.GetButton(3).InputHandler()(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	routes, _, err := brokersettings.Load(path)
	if err != nil || len(routes) != 1 || routes[0].Paths[0] != "/v2" || len(routes[0].ExactPaths) != 1 || routes[0].ExactPaths[0] != "/single" || !routes[0].SecretSet {
		t.Fatal("form failed to save metadata while preserving secret", err)
	}
}
