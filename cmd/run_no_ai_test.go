package cmd

import (
	"strings"
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
	"github.com/spf13/cobra"
)

func TestNoAIModeKeepsRuntimeRestrictionsAndDisablesCredentialPolicies(t *testing.T) {
	old := runOpts
	t.Cleanup(func() { runOpts = old })
	runOpts = sandbox.RunOptions{Agent: sandbox.AgentNone, NoInternet: true, ReadOnly: true, ResourceProfile: "compact", TokenUsagePolicy: &identity.UsagePolicy{MaxRequests: 1}}
	c := &cobra.Command{}
	c.Flags().Bool("no-ai", true, "")
	c.Flags().String("agent-state-size", "", "")
	if e := applyNoAIRun(c); e != nil {
		t.Fatal(e)
	}
	if runOpts.TokenUsagePolicy != nil || runOpts.AgentStateSize != "off" || !runOpts.NoInternet || !runOpts.ReadOnly || runOpts.ResourceProfile != "compact" {
		t.Fatal("no-AI mode changed non-AI restrictions", runOpts)
	}
	runOpts.Agent = sandbox.AgentCodex
	if e := applyNoAIRun(c); e == nil {
		t.Fatal("agent accepted without AI access")
	}
	runOpts.Agent = sandbox.AgentNone
	c.Flags().Int("max-requests", 0, "")
	if e := c.Flags().Set("max-requests", "1"); e != nil {
		t.Fatal(e)
	}
	if e := applyNoAIRun(c); e == nil {
		t.Fatal("explicit AI budget silently ignored")
	}
}
func TestTaskFromStdinPreservesMultilineAndBoundsInput(t *testing.T) {
	opts, file, headless, agent := runOpts, runTaskFile, runHeadless, agentFlag
	t.Cleanup(func() { runOpts, runTaskFile, runHeadless, agentFlag = opts, file, headless, agent })
	runOpts = sandbox.RunOptions{}
	runTaskFile = "-"
	runHeadless = true
	agentFlag = "shell"
	cmd := &cobra.Command{}
	cmd.Flags().String("task", "", "")
	cmd.SetIn(strings.NewReader("printf '日本語\\n'\ntrue"))
	if e := applyHeadlessTask(cmd); e != nil {
		t.Fatal(e)
	}
	if runOpts.ExecCmd != "printf '日本語\\n'\ntrue" {
		t.Fatal(runOpts.ExecCmd)
	}
	runOpts = sandbox.RunOptions{}
	cmd.SetIn(strings.NewReader(strings.Repeat("x", 32769)))
	if e := applyHeadlessTask(cmd); e == nil {
		t.Fatal("oversized stdin accepted")
	}
}
