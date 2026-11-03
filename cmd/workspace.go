package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/pacificbelt30/codex-dock/internal/managedworkspace"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/pacificbelt30/codex-dock/internal/workspacecopy"
	"github.com/spf13/cobra"
)

func init() {
	api := engine.WorkspaceClient{}
	root := &cobra.Command{Use: "workspace", Short: "Manage bounded node-owned workspace disks; VM removal retains their data"}
	list := &cobra.Command{Use: "list", Args: cobra.NoArgs, Short: "List workspaces, snapshots, reservations and VM leases", RunE: func(cmd *cobra.Command, _ []string) error {
		out, err := api.List(cmd.Context())
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
	}}
	inspect := &cobra.Command{Use: "inspect ID", Args: cobra.ExactArgs(1), Short: "Inspect a full workspace ID", RunE: func(cmd *cobra.Command, args []string) error {
		out, err := api.Inspect(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
	}}
	remove := &cobra.Command{Use: "rm ID", Args: cobra.ExactArgs(1), Short: "Permanently delete an unleased workspace; recovery-point references are protected", RunE: func(cmd *cobra.Command, args []string) error { return api.Remove(cmd.Context(), args[0]) }}
	var bytes int64
	plan := &cobra.Command{Use: "plan DIRECTORY", Args: cobra.ExactArgs(1), Short: "Review current regular files and excluded paths without creating resources", RunE: func(cmd *cobra.Command, args []string) error {
		o := managedworkspace.Options{Name: "review", Bytes: bytes, Inodes: 16384, UID: 1001, GID: 1001}
		if err := o.Validate(); err != nil {
			return err
		}
		out, err := workspacecopy.Prepare(cmd.Context(), args[0], o.SeedLimit())
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
	}}
	plan.Flags().Int64Var(&bytes, "bytes", managedworkspace.DefaultBytes, "Workspace capacity; multiple of 64 MiB, at most 4 GiB")
	var spec sandboxux.LaunchSpec
	var expected, owner string
	create := &cobra.Command{Use: "create DIRECTORY", Args: cobra.ExactArgs(1), Short: "Import reviewed current files into a new bounded workspace (never mounts the original)", RunE: func(cmd *cobra.Command, args []string) error {
		spec.Project, spec.Workspace, spec.Network = args[0], "managed", "offline"
		launcher := &sandboxux.CLILauncher{UserMode: owner}
		prepared, err := launcher.Prepare(cmd.Context(), spec)
		if err != nil {
			return err
		}
		if prepared.CopyPlan.Manifest.ID != expected {
			return fmt.Errorf("copy plan changed; run workspace plan and review the files again")
		}
		// Reuse the same reviewed streaming contract as the TUI without launching
		// a VM. Failed imports are deliberately retained for catalog inspection.
		info, err := sandboxux.ImportWorkspace(cmd.Context(), api, prepared.WorkspaceOwner, *prepared.CopyPlan)
		if err != nil {
			return fmt.Errorf("workspace %s retained; inspect workspace list: %w", info.ID, err)
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
	}}
	create.Flags().StringVar(&spec.Name, "name", "workspace", "Workspace display name")
	create.Flags().Int64Var(&spec.WorkspaceBytes, "bytes", managedworkspace.DefaultBytes, "Fixed workspace capacity in bytes")
	create.Flags().Uint64Var(&spec.WorkspaceInodes, "inodes", 16384, "Inode ceiling; power of two, 1024–65536")
	create.Flags().StringVar(&expected, "plan-id", "", "Reviewed manifest ID from workspace plan")
	create.Flags().StringVar(&owner, "user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()), "Non-root VM owner: current, codex, dir or numeric UID:GID")
	_ = create.MarkFlagRequired("plan-id")
	var cloneName string
	clone := &cobra.Command{Use: "clone SNAPSHOT_ID", Args: cobra.ExactArgs(1), Short: "Copy a saved workspace snapshot into a new available workspace", RunE: func(cmd *cobra.Command, args []string) error {
		out, err := api.Clone(cmd.Context(), args[0], cloneName)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
	}}
	clone.Flags().StringVar(&cloneName, "name", "restored-workspace", "New workspace display name")
	root.AddCommand(list, inspect, remove, plan, create, clone)
	rootCmd.AddCommand(root)

	points := &cobra.Command{Use: "recovery-point", Short: "Save filtered stopped-VM rootfs and optionally a managed workspace; restore as new resources"}
	var options managedworkspace.SaveOptions
	save := &cobra.Command{Use: "create WORKER", Args: cobra.ExactArgs(1), Short: "Save a stopped VM; homes, RAM and host bind mounts are excluded", RunE: func(cmd *cobra.Command, args []string) error {
		options.Worker = args[0]
		out, err := api.SavePoint(cmd.Context(), options)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
	}}
	save.Flags().StringVar(&options.Name, "name", "recovery", "Recovery-point display name")
	save.Flags().BoolVar(&options.IncludeWorkspace, "workspace", false, "Also snapshot the stopped VM's managed workspace disk")
	save.Flags().StringArrayVar(&options.Checkpoint.Exclude, "exclude", nil, "Additional absolute guest rootfs path to exclude")
	pointList := &cobra.Command{Use: "list", Args: cobra.NoArgs, Short: "List ready and incomplete recovery points", RunE: func(cmd *cobra.Command, _ []string) error {
		out, err := api.Points(cmd.Context())
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
	}}
	pointRemove := &cobra.Command{Use: "rm ID", Args: cobra.ExactArgs(1), Short: "Delete an unused point and its owned rootfs/workspace components", RunE: func(cmd *cobra.Command, args []string) error { return api.RemovePoint(cmd.Context(), args[0]) }}
	points.AddCommand(save, pointList, pointRemove)
	rootCmd.AddCommand(points)
}
