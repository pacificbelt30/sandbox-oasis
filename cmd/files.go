package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/pacificbelt30/codex-dock/internal/sandboxux"
	"github.com/spf13/cobra"
	"io"
	"os"
)

func init() {
	files := &cobra.Command{Use: "files", Short: "Read/write guest workspace files without following symlinks"}
	for _, op := range []string{"get", "put", "list"} {
		op := op
		var local string
		c := &cobra.Command{Use: op + " NAME RELATIVE_PATH", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
			if err := sandboxux.GuestPath(args[1], op == "list"); err != nil {
				return err
			}
			m, err := sandbox.NewManager(managementConfig())
			if err != nil {
				return err
			}
			defer func() { _ = m.Close() }()
			if op == "put" {
				in := cmd.InOrStdin()
				if local != "-" {
					f, err := os.Open(local)
					if err != nil {
						return err
					}
					defer func() { _ = f.Close() }()
					in = f
				}
				return m.WriteFile(cmd.Context(), args[0], args[1], in)
			}
			write := func(out io.Writer) error {
				if op == "get" {
					return m.ReadFile(cmd.Context(), args[0], args[1], out)
				}
				return m.ListFiles(cmd.Context(), args[0], args[1], out)
			}
			if local != "-" {
				return sandboxux.SaveNewHostFile(local, write)
			}
			return write(cmd.OutOrStdout())
		}}
		c.Flags().StringVar(&local, "local", "-", "Local path; - uses stdin/stdout. Downloads require a new file.")
		c.ValidArgsFunction = completeGuestPath(op, false)
		files.AddCommand(c)
	}
	rootCmd.AddCommand(files)
	var output, message string
	a := &cobra.Command{Use: "artifact", Short: "Recover worker changes"}
	diff := &cobra.Command{Use: "diff NAME", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		m, err := sandbox.NewManager(managementConfig())
		if err != nil {
			return err
		}
		defer func() { _ = m.Close() }()
		write := func(out io.Writer) error { return m.ExportDiff(cmd.Context(), args[0], out) }
		if output != "-" {
			return sandboxux.SaveNewHostFile(output, write)
		}
		return write(cmd.OutOrStdout())
	}}
	diff.Flags().StringVar(&output, "output", "-", "New patch file, or - for stdout")
	commit := &cobra.Command{Use: "commit NAME", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if message == "" {
			return fmt.Errorf("--message is required")
		}
		m, err := sandbox.NewManager(managementConfig())
		if err != nil {
			return err
		}
		defer func() { _ = m.Close() }()
		return m.Commit(cmd.Context(), args[0], message)
	}}
	commit.Flags().StringVarP(&message, "message", "m", "", "Commit staged guest changes")
	var bundleOptions sandbox.ArtifactOptions
	plan := &cobra.Command{Use: "plan NAME", Args: cobra.ExactArgs(1), Short: "Review explicitly selected artifact files and their hashes", RunE: func(cmd *cobra.Command, args []string) error {
		m, err := sandbox.NewManager(managementConfig())
		if err != nil {
			return err
		}
		defer func() { _ = m.Close() }()
		p, err := m.PlanArtifacts(cmd.Context(), args[0], bundleOptions)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(p)
	}}
	var bundleOutput string
	bundle := &cobra.Command{Use: "bundle NAME", Args: cobra.ExactArgs(1), Short: "Export a reviewed plan as a private tar archive", RunE: func(cmd *cobra.Command, args []string) error {
		if bundleOutput == "" {
			return fmt.Errorf("--output is required")
		}
		m, err := sandbox.NewManager(managementConfig())
		if err != nil {
			return err
		}
		defer func() { _ = m.Close() }()
		return sandboxux.SaveNewHostFile(bundleOutput, func(out io.Writer) error {
			return m.ExportArtifacts(cmd.Context(), args[0], bundleOptions, out)
		})
	}}
	for _, c := range []*cobra.Command{plan, bundle} {
		c.Flags().BoolVar(&bundleOptions.SinceLaunch, "since-start", false, "Compare selected files against the captured launch Git commit")
		c.Flags().StringArrayVar(&bundleOptions.Paths, "include", nil, "Explicit regular workspace file (repeatable; credential paths refused)")
		c.Flags().StringArrayVar(&bundleOptions.JobIDs, "job", nil, "Include status and exit code of a selected job (repeatable; no logs or argv)")
		c.Flags().Int64Var(&bundleOptions.MaxBytes, "max-bytes", 32<<20, "Maximum combined file and patch bytes (up to 32 MiB)")
		c.ValidArgsFunction = completeWorkerArgument(func(w sandbox.Worker) bool { return w.Status == "running" })
		registerCompletion(c, "include", completeGuestPath("get", true))
	}
	bundle.Flags().StringVar(&bundleOptions.ExpectedPlan, "plan-id", "", "Reviewed plan ID; changed contents are refused")
	bundle.Flags().StringVar(&bundleOutput, "output", "", "New local tar file (0600)")
	a.AddCommand(diff, commit, plan, bundle)
	rootCmd.AddCommand(a)
}
