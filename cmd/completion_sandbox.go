package cmd

import (
	"context"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/instanceux"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/spf13/cobra"
)

// Guest completion includes runtime attestation and a guarded guest exec.
// Keep it bounded without reusing the shorter inventory-only deadline.
const guestCompletionTimeout = 5 * time.Second

func completeWorkerArgument(accept func(sandbox.Worker) bool) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return completeWorkers(accept)(cmd, args, prefix)
	}
}

func guestPathCandidates(entries []sandboxux.FileEntry, prefix string, directories, artifacts bool) []string {
	dir, base := path.Split(prefix)
	var values []string
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name, base) || entry.Kind != "file" && entry.Kind != "directory" || directories && entry.Kind != "directory" {
			continue
		}
		value := dir + entry.Name
		if sandboxux.GuestPath(value, false) != nil || artifacts && sandboxux.ArtifactPath(value) != nil {
			continue
		}
		if entry.Kind == "directory" {
			value += "/"
		}
		values = append(values, value)
	}
	slices.Sort(values)
	return values
}

func completeGuestPath(op string, artifactFlag bool) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
		directive := cobra.ShellCompDirectiveNoFileComp
		if !artifactFlag && len(args) == 0 {
			return completeWorkerArgument(func(w sandbox.Worker) bool { return w.Status == "running" })(cmd, args, prefix)
		}
		if len(args) != 1 || strings.HasPrefix(prefix, "/") {
			return nil, directive
		}
		dir, _ := path.Split(prefix)
		dir = strings.TrimSuffix(dir, "/")
		if dir == "" {
			dir = "."
		}
		if sandboxux.GuestPath(dir, true) != nil {
			return nil, directive
		}
		mgr, err := sandbox.NewManager(managementConfig())
		if err != nil {
			return nil, directive
		}
		defer func() { _ = mgr.Close() }()
		ctx, cancel := context.WithTimeout(cmd.Context(), guestCompletionTimeout)
		defer cancel()
		entries, err := (sandboxux.Files{Guest: mgr}).List(ctx, args[0], dir)
		if err != nil {
			return nil, directive
		}
		return guestPathCandidates(entries, prefix, op == "list" || op == "put", artifactFlag), directive | cobra.ShellCompDirectiveNoSpace
	}
}

func completeExecArgument(cmd *cobra.Command, args []string, prefix string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) == 0 {
		return completeWorkerArgument(func(w sandbox.Worker) bool { return w.Status == "running" })(cmd, args, prefix)
	}
	if len(args) != 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	mgr, err := sandbox.NewManager(managementConfig())
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer func() { _ = mgr.Close() }()
	ctx, cancel := context.WithTimeout(cmd.Context(), guestCompletionTimeout)
	defer cancel()
	names, err := instanceux.CommandNamesMatching(ctx, mgr, args[0], prefix)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
