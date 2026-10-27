package cmd

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pacificbelt30/codex-dock/internal/diagnostics"
	"github.com/pacificbelt30/codex-dock/internal/template"
)

func startupFailure(stage string, cause error, projects ...*ProjectConfig) error {
	if cause == nil {
		return nil
	}
	var already *diagnostics.StartupError
	if errors.As(cause, &already) {
		return cause
	}
	command := "oaiso"
	if cfgFile != "" {
		path := cfgFile
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
		command += " --config " + shellArgument(path)
	}
	runtimeCheck := command + " runtime-check"
	if runOpts.Runtime != "" {
		runtimeCheck += " --runtime " + shellArgument(runOpts.Runtime)
	}
	if runOpts.KataRuntime != "" {
		runtimeCheck += " --kata-runtime " + shellArgument(runOpts.KataRuntime)
	}
	var next []string
	switch stage {
	case "agent policy":
		if store, err := agentPolicyStore(); err == nil {
			command += " --agent-policy-file " + shellArgument(store.Path)
		}
		next = []string{command + " agent-policy validate", command + " agent-policy configure"}
	case "node", "runtime":
		next = []string{runtimeCheck, "systemctl status codex-dock-node codex-dock-containerd", "For a new host, review the installation plan with: " + command + " setup --help"}
	case "authentication", "auth proxy":
		if profile := selectedAuthProfile(); profile != "" {
			command += " --auth-profile " + shellArgument(profile)
		}
		if authProfileDir != "" {
			command += " --auth-profile-dir " + shellArgument(authProfileDir)
		}
		next = []string{command + " status", command + " login --help", command + " init --help (choose the required agent; save work before replacing proxies)"}
	case "image":
		build := command + " build --tag " + shellArgument(runOpts.Image)
		var project *ProjectConfig
		if len(projects) > 0 {
			project = projects[0]
		}
		if project != nil && project.Build.Tag == runOpts.Image && project.Build.Dockerfile != "" {
			file, directory, _ := projectDockerfile(project)
			build = "(cd " + shellArgument(directory) + " && " + build + " --template '' --dockerfile " + shellArgument(file) + ")"
		} else if project != nil && project.Build.Tag == runOpts.Image && project.Build.Template != "" {
			build += " --template " + shellArgument(project.Build.Template)
		} else if name := template.MatchTag(runOpts.Image); name != "" {
			build += " --template " + shellArgument(name)
		} else {
			build += " --template '' --dockerfile /path/to/Dockerfile"
		}
		next = []string{runtimeCheck, "Review the selected image and build source; build explicitly with: " + build}
	case "worker launch":
		next = []string{runtimeCheck, command + " ps -a", command + " vm ps -a (inspect retained resources before retrying)"}
	default:
		return cause
	}
	return &diagnostics.StartupError{Stage: stage, Cause: cause, Next: next}
}

func shellArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func startupImageProgress(image, reason string) string {
	return fmt.Sprintf("Preparing image %s: %s; building the selected source before starting the worker...", image, reason)
}
