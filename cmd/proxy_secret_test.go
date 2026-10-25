package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/authproxy"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestProxyAdminSecretFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin")
	secret := strings.Repeat("fixture-secret-", 3)
	if err := os.WriteFile(path, []byte(secret+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := resolveProxyAdminSecret("", path); err != nil || got != secret {
		t.Fatal("could not read private secret")
	}
	if _, err := resolveProxyAdminSecret("raw", path); err == nil {
		t.Fatal("ambiguous admin authority accepted")
	}
	alias := path + "-alias"
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveProxyAdminSecret("", alias); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveProxyAdminSecret("", path); err == nil {
		t.Fatal("public secret file accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	a := proxyRunArgs{role: authproxy.RoleAuth, image: "fixture:latest", name: "proxy", adminSecret: secret, adminSecretFile: path}
	c, h, err := proxyConfiguration(a)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(c.Cmd, " ")+strings.Join(c.Env, " "), secret) || !containsSequence(c.Cmd, "--admin-secret-file", "/run/cxd-admin-secret") {
		t.Fatal("admin secret leaked to process configuration")
	}
	if len(h.Mounts) != 1 || !h.Mounts[0].ReadOnly || h.Mounts[0].Target != "/run/cxd-admin-secret" {
		t.Fatal("admin secret file mount is not private and readonly")
	}
}

func TestRunAdminSecretFileConfigPrecedence(t *testing.T) {
	viper.Reset()
	oldRaw, oldFile := runProxyAdminSecret, runProxyAdminSecretFile
	t.Cleanup(func() { runProxyAdminSecret, runProxyAdminSecretFile = oldRaw, oldFile; viper.Reset() })
	viper.Set("proxy.admin_secret_file", "/config/admin")
	viper.Set("proxy.admin_secret", "legacy")
	c := &cobra.Command{}
	c.Flags().StringVar(&runProxyAdminSecret, "proxy-admin-secret", "", "")
	c.Flags().StringVar(&runProxyAdminSecretFile, "proxy-admin-secret-file", "", "")
	applyRunConfigDefaults(c)
	if runProxyAdminSecretFile != "/config/admin" {
		t.Fatal("configured secret file ignored")
	}
	if _, err := effectiveRunProxySecret(); err == nil {
		t.Fatal("ambiguous configured authority accepted")
	}
	if err := c.Flags().Set("proxy-admin-secret", "explicit"); err != nil {
		t.Fatal(err)
	}
	applyRunConfigDefaults(c)
	if runProxyAdminSecret != "explicit" || runProxyAdminSecretFile != "" {
		t.Fatal("explicit raw flag did not override config")
	}
}

func TestManagedSecretDoesNotEscapeDefaultLocalAdmin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	secret, path, err := prepareManagedProxyAdminSecret()
	if err != nil || len(secret) < 32 {
		t.Fatal(err)
	}
	if got, err := automaticProxyAdminSecret("http://127.0.0.1:18081", "", ""); err != nil || got != secret {
		t.Fatal("local secret unavailable", err)
	}
	for _, endpoint := range []string{"http://remote.example:18081", "https://remote.example", "http://127.0.0.1:18099"} {
		got, err := automaticProxyAdminSecret(endpoint, "", "")
		if err != nil || got != "" {
			t.Fatal("implicit secret sent outside default admin", err)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("unsafe generated secret", err)
	}
}
