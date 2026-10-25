package ui

import (
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/brokersettings"
	"github.com/rivo/tview"
)

func RunBrokerSettings(path string) error {
	app := tview.NewApplication()
	return app.SetRoot(brokerSettingsPage(path, app.Stop), true).EnableMouse(true).Run()
}

func brokerSettingsPage(path string, back func()) tview.Primitive {
	status := tview.NewTextView().SetDynamicColors(false)
	form := tview.NewForm()
	name := tview.NewInputField().SetLabel("Route name: ").SetFieldWidth(50)
	origin := tview.NewInputField().SetLabel("HTTPS origin: ").SetFieldWidth(60)
	paths := tview.NewInputField().SetLabel("Paths (comma separated): ").SetFieldWidth(60)
	methods := tview.NewInputField().SetLabel("Methods: ").SetText("GET, HEAD").SetFieldWidth(50)
	header := tview.NewInputField().SetLabel("Credential header: ").SetText("Authorization").SetFieldWidth(50)
	secret := tview.NewInputField().SetLabel("New secret (blank keeps saved): ").SetMaskCharacter('*').SetFieldWidth(50)
	template := tview.NewDropDown().SetLabel("Template: ").SetOptions([]string{"bearer-readonly", "api-key-readonly"}, nil).SetCurrentOption(0)
	prefix, provider := "Bearer ", ""
	var revision string
	load := func() {
		routes, rev, err := brokersettings.Load(path)
		if err != nil {
			status.SetText(err.Error())
			return
		}
		revision = rev
		secret.SetText("")
		for _, r := range routes {
			if r.Name == name.GetText() {
				origin.SetText(r.Upstream)
				paths.SetText(strings.Join(r.Paths, ", "))
				methods.SetText(strings.Join(r.Methods, ", "))
				header.SetText(r.Header)
				prefix, provider = r.Prefix, r.Provider
				status.SetText("Loaded scope; saved secret is never displayed. Blank replacement preserves it.")
				return
			}
		}
		status.SetText("No route selected. Enter a name and scope. Validate/save never makes network requests.")
	}
	collect := func() brokersettings.Descriptor {
		return brokersettings.Descriptor{Name: name.GetText(), Upstream: origin.GetText(), Paths: splitBrokerFields(paths.GetText()), Methods: splitBrokerFields(methods.GetText()), Header: header.GetText(), Prefix: prefix, Provider: provider}
	}
	for _, item := range []tview.FormItem{template, name, origin, paths, methods, header, secret} {
		form.AddFormItem(item)
	}
	form.AddButton("Apply template", func() {
		_, selected := template.GetCurrentOption()
		d, err := brokersettings.Template(selected, name.GetText(), origin.GetText(), strings.TrimSpace(paths.GetText()))
		if err != nil {
			status.SetText(err.Error())
			return
		}
		header.SetText(d.Header)
		methods.SetText(strings.Join(d.Methods, ", "))
		prefix, provider = d.Prefix, d.Provider
		status.SetText("Template applied to form only. Validate and save explicitly.")
	})
	form.AddButton("Load route", load)
	form.AddButton("Validate", func() {
		if err := brokersettings.Validate(collect()); err != nil {
			status.SetText(err.Error())
			return
		}
		status.SetText("Scope valid. DNS, connectivity and authentication have not been tested.")
	})
	form.AddButton("Save", func() {
		err := brokersettings.Save(path, revision, collect(), secret.GetText())
		secret.SetText("")
		if err != nil {
			status.SetText(err.Error())
			return
		}
		load()
		status.SetText("Saved. Drain and recreate auth proxies explicitly to apply. Workers receive no secret file.")
	})
	form.AddButton("Back", back)
	form.SetBorder(true).SetTitle(" Credential broker settings ")
	load()
	return tview.NewFlex().SetDirection(tview.FlexRow).AddItem(form, 0, 3, true).AddItem(status, 0, 1, false)
}

func splitBrokerFields(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out
}
