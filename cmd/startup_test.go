package cmd

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pacificbelt30/codex-dock/internal/diagnostics"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
)

func TestStartupFailurePreservesCauseAndSelectedPaths(t *testing.T) {
	oldConfig, oldPolicy, oldOptions := cfgFile, agentPolicyFile, runOpts
	t.Cleanup(func() { cfgFile, agentPolicyFile, runOpts = oldConfig, oldPolicy, oldOptions })
	cfgFile = "/tmp/config with spaces.toml"
	agentPolicyFile = "/tmp/policy's config.toml"
	runOpts = sandbox.RunOptions{Image: "example/custom:dev"}
	cause := &exec.ExitError{}
	for _, stage := range []string{"agent policy", "node", "runtime", "authentication", "auth proxy", "image", "worker launch"} {
		err := startupFailure(stage, cause)
		var original *exec.ExitError
		var startup *diagnostics.StartupError
		if !errors.Is(err, cause) || !errors.As(err, &original) || !errors.As(err, &startup) {
			t.Fatal("lost cause", stage, err)
		}
		if !strings.Contains(err.Error(), "Next: oaiso --config '/tmp/config with spaces.toml'") {
			t.Fatal("hint lost selected config", err)
		}
		if wrapped := startupFailure("node", err); wrapped != err {
			t.Fatal("nested recovery hints")
		}
	}
	policy := startupFailure("agent policy", cause).Error()
	if !strings.Contains(policy, "--agent-policy-file '/tmp/policy'\"'\"'s config.toml'") {
		t.Fatal("policy path not shell quoted", policy)
	}
	image := startupFailure("image", cause).Error()
	if !strings.Contains(image, "--dockerfile /path/to/Dockerfile") || strings.Contains(image, "--template plain") {
		t.Fatal("custom image recovery selected the wrong build source", image)
	}
	if startupFailure("configuration", cause) != cause || startupFailure("runtime", nil) != nil {
		t.Fatal("unrelated error changed")
	}
}

func TestStartupImageRecoveryKeepsProjectSourceAndRuntime(t *testing.T) {
	old := runOpts
	t.Cleanup(func() { runOpts = old })
	runOpts.Image, runOpts.Runtime, runOpts.KataRuntime = "codex-dock:latest", "kata", "io.containerd.custom.v2"
	project := &ProjectConfig{Root: "/tmp/project with spaces"}
	project.Build.Tag, project.Build.Dockerfile = runOpts.Image, "Dockerfile.project"
	err := startupFailure("image", errors.New("fixture build failed"), project)
	for _, want := range []string{"--kata-runtime 'io.containerd.custom.v2'", "cd '/tmp/project with spaces'", "--dockerfile '/tmp/project with spaces/Dockerfile.project'"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatal("recovery lost selected source or runtime", err)
		}
	}
	if strings.Contains(err.Error(), "--template 'plain'") {
		t.Fatal("project image replaced with plain", err)
	}
	if buildCmd.Flags().Lookup("dockerfile") == nil || buildCmd.Flags().Lookup("tag") == nil {
		t.Fatal("recovery uses unsupported build flags")
	}
}

func TestRunPolicyFailureProvidesRecoveryWithoutNodeAccess(t *testing.T) {
	oldFile := agentPolicyFile
	t.Cleanup(func() { agentPolicyFile = oldFile })
	agentPolicyFile = filepath.Join(t.TempDir(), "missing-parent", "config.toml")
	// A nonexistent policy must stop before attempting to connect to the node.
	oldName := agentPolicyName
	t.Cleanup(func() { agentPolicyName = oldName })
	agentPolicyName = "does-not-exist"
	err := runWorker(runCmd, nil)
	var startup *diagnostics.StartupError
	if !errors.As(err, &startup) || startup.Stage != "agent policy" || !strings.Contains(err.Error(), "agent-policy configure") {
		t.Fatal("policy failure lacked targeted recovery", err)
	}
}
