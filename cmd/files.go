package cmd

import (
	"encoding/json"
	"fmt"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/spf13/cobra"
	"os"
)

func init() {
	files := &cobra.Command{Use: "files", Short: "Read/write guest workspace files without following symlinks"}
	for _, op := range []string{"get", "put", "list"} {
		op := op
		var local string
		c := &cobra.Command{Use: op + " NAME RELATIVE_PATH", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
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
			out := cmd.OutOrStdout()
			if local != "-" {
				f, err := os.OpenFile(local, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
				if err != nil {
					return err
				}
				defer func() { _ = f.Close() }()
				out = f
			}
			if op == "get" {
				return m.ReadFile(cmd.Context(), args[0], args[1], out)
			}
			return m.ListFiles(cmd.Context(), args[0], args[1], out)
		}}
		c.Flags().StringVar(&local, "local", "-", "Local path; - uses stdin/stdout. Downloads require a new file.")
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
		out := cmd.OutOrStdout()
		if output != "-" {
			f, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			out = f
		}
		return m.ExportDiff(cmd.Context(), args[0], out)
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
		f, err := os.OpenFile(bundleOutput, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		complete := false
		defer func() {
			_ = f.Close()
			if !complete {
				_ = os.Remove(bundleOutput)
			}
		}()
		if err = m.ExportArtifacts(cmd.Context(), args[0], bundleOptions, f); err != nil {
			return err
		}
		if err = f.Sync(); err != nil {
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
		complete = true
		return nil
	}}
	for _, c := range []*cobra.Command{plan, bundle} {
		c.Flags().StringArrayVar(&bundleOptions.Paths, "include", nil, "Explicit regular workspace file (repeatable; credential paths refused)")
		c.Flags().StringArrayVar(&bundleOptions.JobIDs, "job", nil, "Include status and exit code of a selected job (repeatable; no logs or argv)")
		c.Flags().Int64Var(&bundleOptions.MaxBytes, "max-bytes", 32<<20, "Maximum combined file and patch bytes (up to 32 MiB)")
	}
	bundle.Flags().StringVar(&bundleOptions.ExpectedPlan, "plan-id", "", "Reviewed plan ID; changed contents are refused")
	bundle.Flags().StringVar(&bundleOutput, "output", "", "New local tar file (0600)")
	a.AddCommand(diff, commit, plan, bundle)
	rootCmd.AddCommand(a)
}
