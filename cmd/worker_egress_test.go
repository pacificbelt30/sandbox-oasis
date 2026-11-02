package cmd

import (
	"strings"
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/authproxy"
	"github.com/pacificbelt30/codex-dock/internal/networkpolicy"
	"github.com/spf13/viper"
)

func TestWorkerEgressProxyUsesPolicySpecificIdentity(t *testing.T) {
	oldRules, oldAllow := proxyForwardRules, proxyForwardAllow
	defer func() { proxyForwardRules, proxyForwardAllow = oldRules, oldAllow }()
	proxyForwardRules.AllowDomains = nil
	proxyForwardAllow = []string{"example.com"}
	viper.Set("proxy.forward_allow_domains", []string{"example.com"})
	defer viper.Set("proxy.forward_allow_domains", nil)
	viper.Set("proxy_image", "proxy:test")
	defer viper.Set("proxy_image", nil)
	one, err := workerEgressSpec(runCmd, []string{"api.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := workerEgressSpec(runCmd, []string{"other.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if one.name == two.name || !strings.HasPrefix(one.name, "codex-http-policy-") {
		t.Fatal("workers with different rules share a proxy")
	}
	if one.mountCreds || one.adminPort != 0 {
		t.Fatal("policy proxy gained credentials or admin")
	}
	if _, err := workerEgressSpec(runCmd, []string{"outside.example.org"}); err == nil {
		t.Fatal("worker widened host egress policy")
	}
	if _, err := workerEgressSpec(runCmd, []string{"bad domain"}); err == nil {
		t.Fatal("invalid domain accepted")
	}
}

func TestNoAIEgressRetainsHostPolicyWithoutCredentials(t *testing.T) {
	oldRules, oldAllow := proxyForwardRules, proxyForwardAllow
	oldDeny := viper.Get("proxy.forward_default_deny")
	viper.Set("proxy.forward_default_deny", false)
	t.Cleanup(func() { viper.Set("proxy.forward_default_deny", oldDeny) })
	t.Cleanup(func() { proxyForwardRules, proxyForwardAllow = oldRules, oldAllow })
	proxyForwardRules = networkpolicy.Rules{DenyDomains: []string{"blocked.example"}}
	proxyForwardAllow = nil
	if _, err := workerEgressSpec(runCmd, nil); err == nil {
		t.Fatal("ordinary worker unexpectedly accepted an empty grant")
	}
	spec, err := workerEgressSpecForMode(runCmd, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if spec.role != authproxy.RoleEgress || spec.mountCreds || spec.adminPort != 0 || !spec.blockPrivate || len(spec.forwardPolicy.DenyDomains) != 1 || spec.forwardPolicy.DefaultDeny {
		t.Fatalf("no-AI egress changed host policy or gained credentials: %+v", spec)
	}
}
