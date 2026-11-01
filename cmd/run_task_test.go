package cmd

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/spf13/cobra"
)

func TestHeadlessTaskUsesAgentBatchModeWithoutApprovalOverride(t *testing.T) {
	oldOpts, oldAgent, oldFile, oldHeadless, oldApproval := runOpts, agentFlag, runTaskFile, runHeadless, approvalModeFlag
	t.Cleanup(func() {
		runOpts, agentFlag, runTaskFile, runHeadless, approvalModeFlag = oldOpts, oldAgent, oldFile, oldHeadless, oldApproval
	})
	for _, agent := range []string{"codex", "claude", "shell"} {
		t.Run(agent, func(t *testing.T) {
			runOpts = sandbox.RunOptions{Task: "do the work"}
			agentFlag = agent
			runHeadless = true
			runTaskFile = ""
			approvalModeFlag = "auto-edit"
			cmd := &cobra.Command{}
			cmd.Flags().String("task", "", "")
			if e := applyHeadlessTask(cmd); e != nil {
				t.Fatal(e)
			}
			if approvalModeFlag != "auto-edit" {
				t.Fatal("approval changed")
			}
			switch agent {
			case "codex":
				if !reflect.DeepEqual(runOpts.AgentArgs, []string{"exec", "--json", "--", "do the work"}) {
					t.Fatal(runOpts.AgentArgs)
				}
			case "claude":
				if !reflect.DeepEqual(runOpts.AgentArgs, []string{"--print", "--", "do the work"}) {
					t.Fatal(runOpts.AgentArgs)
				}
			case "shell":
				if runOpts.ExecCmd != "do the work" {
					t.Fatal(runOpts)
				}
			}
		})
	}

	for _, agent := range []string{"codex", "claude"} {
		runOpts = sandbox.RunOptions{Task: "--dangerously-bypass-approvals-and-sandbox"}
		agentFlag = agent
		runHeadless = true
		runTaskFile = ""
		cmd := &cobra.Command{}
		cmd.Flags().String("task", "", "")
		if e := applyHeadlessTask(cmd); e != nil {
			t.Fatal(e)
		}
		if codexOptionPresent(runOpts.AgentArgs, "--dangerously-bypass-approvals-and-sandbox") {
			t.Fatal("task text became an agent flag")
		}
		if n := len(runOpts.AgentArgs); n < 2 || runOpts.AgentArgs[n-2] != "--" {
			t.Fatal("prompt lacks option terminator")
		}
	}
	runOpts = sandbox.RunOptions{}
	runHeadless = true
	agentFlag = "codex"
	runTaskFile = filepath.Join(t.TempDir(), "prompt")
	if e := os.WriteFile(runTaskFile, []byte("private prompt"), 0644); e != nil {
		t.Fatal(e)
	}
	cmd := &cobra.Command{}
	cmd.Flags().String("task", "", "")
	if e := applyHeadlessTask(cmd); e == nil {
		t.Fatal("public task file accepted")
	}
	if e := os.Chmod(runTaskFile, 0600); e != nil {
		t.Fatal(e)
	}
	if e := applyHeadlessTask(cmd); e != nil {
		t.Fatal(e)
	}
}
