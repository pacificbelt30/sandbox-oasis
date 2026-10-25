package brokersettings

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/authproxy"
)

func TestSettingsPreserveHiddenSecretsAndRejectLostUpdates(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	path := filepath.Join(dir, "routes.json")
	d, err := Template("bearer-readonly", "service", "https://api.example.com", "/v1/read")
	if err != nil {
		t.Fatal(err)
	}
	_, rev, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Save(path, rev, d, "fixture-private-secret"); err != nil {
		t.Fatal(err)
	}
	routes, rev, err := Load(path)
	if err != nil || len(routes) != 1 || !routes[0].SecretSet {
		t.Fatal(routes, err)
	}
	data, _ := json.Marshal(routes)
	if bytes.Contains(data, []byte("fixture-private-secret")) {
		t.Fatal("secret rendered")
	}
	d.Paths = nil
	d.ExactPaths = []string{"/v2/read"}
	d.Methods = []string{"POST"}
	if err := Save(path, rev, d, ""); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, rev, d, "lost-replacement"); err == nil {
		t.Fatal("stale save accepted")
	}
	loaded, err := authproxy.LoadCredentialRoutes(path)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := loaded[0].Secret(context.Background())
	if err != nil || secret != "fixture-private-secret" || len(loaded[0].Paths) != 0 || len(loaded[0].ExactPaths) != 1 || loaded[0].ExactPaths[0] != "/v2/read" || len(loaded[0].Methods) != 1 || loaded[0].Methods[0] != "POST" {
		t.Fatal("saved routes lost compatibility or secret")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("public credential file")
	}
}

func TestSettingsRejectUnsafeFilesAndNeverEchoInvalidSecretValues(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	path := filepath.Join(dir, "routes.json")
	for _, data := range []string{`[{"Secret":"fixture-private-secret","Unknown":"fixture-private-secret"}]`, `[] {"secret":"fixture-private-secret"}`} {
		_ = os.WriteFile(path, []byte(data), 0600)
		if _, _, err := Load(path); err == nil || strings.Contains(err.Error(), "fixture-private-secret") {
			t.Fatal("unsafe parse result", err)
		}
	}
	_ = os.Remove(path)
	target := filepath.Join(t.TempDir(), "secret")
	_ = os.WriteFile(target, []byte("[]"), 0600)
	_ = os.Symlink(target, path)
	if _, _, err := Load(path); err == nil {
		t.Fatal("symlink accepted")
	}
	if _, err := authproxy.LoadCredentialRoutes(path); err == nil {
		t.Fatal("proxy followed symlink")
	}
	for _, origin := range []string{"http://api.example.com", "https://key:secret@api.example.com", "https://api.example.com?secret=key"} {
		if _, err := Template("bearer-readonly", "service", origin, "/v1"); err == nil {
			t.Fatal("unsafe template origin accepted")
		}
	}
}

func TestLegacyPrefixSaveOmitsOptionalExactPaths(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	file := filepath.Join(dir, "routes.json")
	d, err := Template("bearer-readonly", "legacy", "https://api.example.com", "/v1")
	if err != nil {
		t.Fatal(err)
	}
	_, rev, err := Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = Save(file, rev, d, "fixture-secret"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	// An older strict decoder must still accept a route that uses old features.
	var legacy []struct {
		Name, Upstream, Header, Prefix, Provider, Secret string
		Paths, Methods                                   []string
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&legacy); err != nil || len(legacy) != 1 || legacy[0].Paths[0] != "/v1" {
		t.Fatalf("legacy schema no longer readable: %v", err)
	}
}
