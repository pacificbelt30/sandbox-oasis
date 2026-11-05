package cmd

import (
	"context"
	"sort"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/kataguard"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/template"
	"github.com/spf13/cobra"
)

const completionTimeout = 500 * time.Millisecond
const vmCompletionTimeout = 2 * time.Second

func registerCompletion(cmd *cobra.Command, flag string, fn cobra.CompletionFunc) {
	if err := cmd.RegisterFlagCompletionFunc(flag, fn); err != nil {
		panic(err)
	}
}

func completeTemplates(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	items, err := template.List()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]cobra.Completion, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

func completeImageTags(_ *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	items, err := template.List()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	tags := make([]cobra.Completion, 0, len(items))
	for _, item := range items {
		tags = append(tags, item.Tag(""))
	}
	return tags, cobra.ShellCompDirectiveNoFileComp
}

func completeWorkers(accept func(sandbox.Worker) bool) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		mgr, err := sandbox.NewManager(managementConfig())
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		defer func() { _ = mgr.Close() }()
		ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
		defer cancel()
		workers, err := mgr.ListContext(ctx, true)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return workerCompletionNames(workers, args, accept), cobra.ShellCompDirectiveNoFileComp
	}
}

func workerCompletionNames(workers []sandbox.Worker, args []string, accept func(sandbox.Worker) bool) []cobra.Completion {
	used := make(map[string]bool, len(args))
	for _, arg := range args {
		used[arg] = true
	}
	names := make([]cobra.Completion, 0, len(workers))
	for _, worker := range workers {
		if worker.Name != "" && accept(worker) && !used[worker.Name] {
			names = append(names, worker.Name)
		}
	}
	sort.Strings(names)
	return names
}

func completeVMs(action string) cobra.CompletionFunc {
	return func(cmd *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if action == "inspect" && len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		mgr, err := sandbox.NewManager(sandbox.ManagerConfig{})
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		defer func() { _ = mgr.Close() }()
		ctx, cancel := context.WithTimeout(cmd.Context(), vmCompletionTimeout)
		defer cancel()
		inventory, err := mgr.VMInventory(ctx)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return vmCompletionNames(inventory, args, action), cobra.ShellCompDirectiveNoFileComp
	}
}

func vmCompletionNames(inventory kataguard.Inventory, args []string, action string) []cobra.Completion {
	if action != "inspect" && !inventory.EngineAvailable {
		return nil
	}
	used := make(map[string]bool, len(args))
	for _, arg := range args {
		used[arg] = true
	}
	names := make([]cobra.Completion, 0, len(inventory.VMs))
	for _, vm := range inventory.VMs {
		if !vmSelectable(vm, action) {
			continue
		}
		value := vm.Name
		resolved, err := kataguard.ResolveVM(inventory, value)
		if err != nil || resolved.ID != vm.ID {
			value = vm.ID
		}
		if value == "" {
			value = vm.Allocation
		}
		resolved, err = kataguard.ResolveVM(inventory, value)
		if err == nil && resolved.ID == vm.ID && !used[value] {
			names = append(names, value)
			used[value] = true
		}
	}
	sort.Strings(names)
	return names
}

func vmSelectable(vm kataguard.VM, action string) bool {
	switch action {
	case "start":
		return vm.ContainerPresent && vm.EngineState != "running" && vm.EngineState != "paused" && !vm.Populated
	case "stop":
		return vm.Orphan || vm.ContainerPresent && (vm.EngineState == "running" || vm.EngineState == "paused" || vm.Populated)
	case "inspect", "rm":
		return true
	default:
		return false
	}
}

func init() {
	defaultCmd.ValidArgsFunction = func(_ *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return []cobra.Completion{"shell", "codex", "claude"}, cobra.ShellCompDirectiveNoFileComp
	}
	startCmd.ValidArgsFunction = completeWorkers(func(w sandbox.Worker) bool { return w.Status != "running" })
	stopCmd.ValidArgsFunction = completeWorkers(func(w sandbox.Worker) bool { return w.Status == "running" })
	rmCmd.ValidArgsFunction = completeWorkers(func(sandbox.Worker) bool { return true })
	logsCmd.ValidArgsFunction = completeWorkers(func(sandbox.Worker) bool { return true })
	connectCmd.ValidArgsFunction = completeWorkerArgument(func(w sandbox.Worker) bool { return connectStart || w.Status == "running" })
	execCmd.ValidArgsFunction = completeExecArgument
}

func completeResourceProfiles(cmd *cobra.Command, _ []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	profiles := kataguard.ResourceProfiles()
	if mgr, err := sandbox.NewManager(sandbox.ManagerConfig{}); err == nil {
		defer func() { _ = mgr.Close() }()
		ctx, cancel := context.WithTimeout(cmd.Context(), completionTimeout)
		defer cancel()
		if current, err := mgr.ResourceProfiles(ctx); err == nil {
			profiles = current
		}
	}
	values := make([]cobra.Completion, 0, len(profiles))
	for _, p := range profiles {
		values = append(values, p.Name)
	}
	return values, cobra.ShellCompDirectiveNoFileComp
}
