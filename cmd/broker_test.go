package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func TestBrokerCommandsKeepSecretOutOfOutputAndAllowScopeOnlyUpdate(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	file := filepath.Join(dir, "routes.json")
	run := func(input string, args ...string) string {
		t.Helper()
		cmd := newBrokerCommand()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetIn(strings.NewReader(input))
		cmd.SetArgs(append([]string{"--file", file}, args...))
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out.String(), "fixture-private-secret") {
			t.Fatal("secret printed")
		}
		return out.String()
	}
	run("fixture-private-secret\n", "set", "service", "--origin", "https://api.example.com", "--path", "/v1", "--secret-stdin")
	run("", "set", "service", "--origin", "https://api.example.com", "--path", "/v2")
	output := run("", "check")
	if !strings.Contains(output, `"secret_set":true`) || !strings.Contains(output, "/v2") {
		t.Fatal("scope update failed", output)
	}
}

func TestBrokerHostConfigurationHonorsExplicitFlag(t *testing.T) {
	old := proxyBrokerFile
	t.Cleanup(func() { proxyBrokerFile = old; viper.Reset() })
	viper.Set("proxy.credential_routes", "/private/configured.json")
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().StringVar(&proxyBrokerFile, "credential-routes", "", "")
	applyForwardPolicyDefaults(cmd)
	if proxyBrokerFile != "/private/configured.json" {
		t.Fatal("host configuration ignored")
	}
	if err := cmd.Flags().Set("credential-routes", "/private/explicit.json"); err != nil {
		t.Fatal(err)
	}
	applyForwardPolicyDefaults(cmd)
	if proxyBrokerFile != "/private/explicit.json" {
		t.Fatal("explicit flag overridden")
	}
}

func TestBrokerCLIExactPathAndExplicitMethodRoundTrip(t *testing.T) {
	dir := t.TempDir()
	_ = os.Chmod(dir, 0700)
	file := filepath.Join(dir, "routes.json")
	run := func(input string, args ...string) (string, error) {
		cmd := newBrokerCommand()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetIn(strings.NewReader(input))
		cmd.SetArgs(append([]string{"--file", file}, args...))
		err := cmd.Execute()
		return out.String(), err
	}
	out, err := run("fixture-secret\n", "set", "write", "--origin", "https://api.example.com", "--exact-path", "/v1/write", "--method", "POST", "--secret-stdin")
	if err != nil || strings.Contains(out, "fixture-secret") {
		t.Fatal("exact path configuration failed", err)
	}
	out, err = run("", "check")
	if err != nil || !strings.Contains(out, `"exact_paths":["/v1/write"]`) || !strings.Contains(out, `"methods":["POST"]`) || strings.Contains(out, "fixture-secret") {
		t.Fatalf("wrong scopes: %s %v", out, err)
	}
	// Updating the scope does not require re-entering or displaying the secret.
	if _, err = run("", "set", "write", "--origin", "https://api.example.com", "--exact-path", "/v2/write", "--method", "POST"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"set", "bad", "--origin", "https://api.example.com", "--exact-path", "/v1/write", "--method", "CONNECT"},
		{"set", "bad", "--origin", "https://api.example.com", "--exact-path", "/v1/write", "--method", ""},
		{"set", "bad", "--origin", "https://api.example.com"},
	} {
		if _, err = run("", args...); err == nil {
			t.Fatalf("invalid scope accepted: %v", args)
		}
	}
}
