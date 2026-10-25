package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/diskcheckpoint"
	"github.com/pacificbelt30/codex-dock/internal/engine"
	"github.com/spf13/cobra"
)

func init() {
	root := &cobra.Command{Use: "checkpoint", Short: "Save stopped Kata disk state and restore it as a new worker"}
	var options diskcheckpoint.Options
	create := &cobra.Command{Use: "create WORKER", Args: cobra.ExactArgs(1), Short: "Save filtered rootfs; stop the worker first (mounts, homes and RAM excluded)", RunE: func(cmd *cobra.Command, args []string) error {
		var info diskcheckpoint.Info
		if err := engine.Checkpoints(cmd.Context(), engine.Socket(), http.MethodPost, "", args[0], options, &info); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
	}}
	create.Flags().Int64Var(&options.MaxBytes, "max-bytes", diskcheckpoint.DefaultMaxBytes, "Maximum uncompressed rootfs archive bytes (1 MiB–4 GiB)")
	create.Flags().StringArrayVar(&options.Exclude, "exclude", nil, "Additional absolute guest path to exclude, including descendants (repeatable)")
	list := &cobra.Command{Use: "list", Args: cobra.NoArgs, Short: "List local checkpoints and logical archive capacity", RunE: func(cmd *cobra.Command, _ []string) error {
		var out diskcheckpoint.Catalog
		if err := engine.Checkpoints(cmd.Context(), engine.Socket(), http.MethodGet, "", "", diskcheckpoint.Options{}, &out); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
	}}
	inspect := &cobra.Command{Use: "inspect ID", Args: cobra.ExactArgs(1), Short: "Inspect checkpoint compatibility and exclusions", RunE: func(cmd *cobra.Command, args []string) error {
		var out diskcheckpoint.Info
		if err := engine.Checkpoints(cmd.Context(), engine.Socket(), http.MethodGet, args[0], "", diskcheckpoint.Options{}, &out); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
	}}
	remove := &cobra.Command{Use: "rm ID", Args: cobra.ExactArgs(1), Short: "Remove an unused checkpoint reference (no force or shared-content pruning)", RunE: func(cmd *cobra.Command, args []string) error {
		var out any
		return engine.Checkpoints(cmd.Context(), engine.Socket(), http.MethodDelete, args[0], "", diskcheckpoint.Options{}, &out)
	}}
	// Restoration shares all existing run flags and their precedence, rather than
	// duplicating authentication, mounts, networking or foreground cleanup.
	var retention diskcheckpoint.RetentionOptions
	var maxAge time.Duration
	prune := &cobra.Command{Use: "prune", Args: cobra.NoArgs, Short: "Plan checkpoint retention; apply only a reviewed plan ID", RunE: func(cmd *cobra.Command, _ []string) error {
		if maxAge < 0 {
			return fmt.Errorf("max-age must be nonnegative")
		}
		retention.MaxAgeSeconds = int64(maxAge / time.Second)
		var out diskcheckpoint.RetentionPlan
		if err := engine.CheckpointMaintenance(cmd.Context(), engine.Socket(), "retention", retention, &out); err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
	}}
	prune.Flags().Int64Var(&retention.KeepBytes, "keep-bytes", 0, "Target logical archive bytes after cleanup; hard node capacity remains 16 GiB")
	prune.Flags().DurationVar(&maxAge, "max-age", 0, "Remove unpinned, unused checkpoints older than this age")
	prune.Flags().BoolVar(&retention.Apply, "apply", false, "Delete only after recomputing the reviewed plan")
	prune.Flags().StringVar(&retention.ExpectedPlan, "plan-id", "", "Reviewed plan ID required with --apply")
	var unpin bool
	pin := &cobra.Command{Use: "pin ID", Args: cobra.ExactArgs(1), Short: "Protect a checkpoint from retention cleanup", RunE: func(cmd *cobra.Command, args []string) error {
		var out any
		return engine.CheckpointMaintenance(cmd.Context(), engine.Socket(), "pin", diskcheckpoint.PinRequest{ID: args[0], Pinned: !unpin}, &out)
	}}
	pin.Flags().BoolVar(&unpin, "unpin", false, "Remove retention protection; explicit rm remains available")
	root.AddCommand(create, list, inspect, remove, prune, pin)
	rootCmd.AddCommand(root)
	runCmd.Flags().String("checkpoint", "", "Restore this disk checkpoint as a NEW worker (image is selected automatically)")
}

func applyCheckpointRun(cmd *cobra.Command) error {
	if cmd.Flags().Lookup("checkpoint") == nil {
		return nil
	}
	id, err := cmd.Flags().GetString("checkpoint")
	if err != nil {
		return err
	}
	if id == "" {
		return nil
	}
	if cmd.Flags().Changed("image") || cmd.Flags().Changed("template") {
		return fmt.Errorf("--checkpoint cannot be combined with --image or --template")
	}
	if runOpts.Runtime != "" && runOpts.Runtime != "kata" {
		return fmt.Errorf("checkpoint restoration requires Kata")
	}
	var info diskcheckpoint.Info
	if err := engine.Checkpoints(cmd.Context(), engine.Socket(), http.MethodGet, id, "", diskcheckpoint.Options{}, &info); err != nil {
		return err
	}
	runOpts.Image = info.Image
	runOpts.Environment.ImageID = info.Digest
	return nil
}
