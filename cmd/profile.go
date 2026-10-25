package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/pacificbelt30/codex-dock/internal/kataguard"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/spf13/cobra"
)

func newProfileCommand() *cobra.Command {
	var asJSON, fromNode bool
	command := &cobra.Command{
		Use: "profile [NAME]", Short: "Show or define bounded Kata worker resource profiles",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profiles := kataguard.ResourceProfiles()
			if fromNode {
				manager, err := sandbox.NewManager(managementConfig())
				if err != nil {
					return err
				}
				defer func() { _ = manager.Close() }()
				profiles, err = manager.ResourceProfiles(cmd.Context())
				if err != nil {
					return err
				}
			}
			if len(args) != 0 {
				found := false
				for _, profile := range profiles {
					if profile.Name == args[0] {
						profiles, found = []kataguard.ResourceProfile{profile}, true
						break
					}
				}
				if !found {
					return fmt.Errorf("unknown resource profile %q", args[0])
				}
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(profiles)
			}
			for _, profile := range profiles {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %d CPU, %.1f GiB guest RAM, %.1f GiB host limit, %d PIDs\n",
					profile.Name, profile.CPUCores, float64(profile.GuestMemoryBytes)/(1<<30), float64(profile.HostMemoryBytes)/(1<<30), profile.PIDLimit); err != nil {
					return err
				}
				if profile.FileDescriptorLimit != 0 {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "  File descriptors/process: %d (soft and hard)\n", profile.FileDescriptorLimit); err != nil {
						return err
					}
				}
				for _, limit := range profile.BlockIO {
					if _, err := fmt.Fprintf(cmd.OutOrStdout(), "  Host block I/O: %s\n", limit.Line()); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	command.Flags().BoolVar(&asJSON, "json", false, "Print a machine-readable resource profile")
	command.Flags().BoolVar(&fromNode, "node", false, "Query the running node, including operator-defined PID and resource limits")
	command.ValidArgsFunction = cobra.FixedCompletions([]cobra.Completion{"standard", "small", "compact"}, cobra.ShellCompDirectiveNoFileComp)
	command.AddCommand(newProfileDefinitionCommand())
	return command
}

func newProfileDefinitionCommand() *cobra.Command {
	var base string
	var pids int64
	var nofile uint64
	var ioLimits []string
	command := &cobra.Command{
		Use: "define NAME", Short: "Print a node resource-profile JSON array with PID, file descriptor and optional block I/O ceilings",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profile, ok := kataguard.LookupResourceProfile(base)
			if !ok {
				return fmt.Errorf("unknown built-in base profile %q", base)
			}
			profile.Name, profile.PIDLimit, profile.FileDescriptorLimit = args[0], pids, nofile
			for _, text := range ioLimits {
				limit, err := kataguard.ParseIOLimit(text)
				if err != nil {
					return err
				}
				profile.BlockIO = append(profile.BlockIO, limit)
			}
			if err := kataguard.ValidateCustomResourceProfile(profile); err != nil {
				return err
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode([]kataguard.ResourceProfile{profile})
		},
	}
	command.Flags().StringVar(&base, "base", "compact", "Built-in CPU/memory budget to copy: standard, small or compact")
	command.Flags().Int64Var(&pids, "pids-limit", 0, "Host and guest PID limit (64..4096; required)")
	_ = command.MarkFlagRequired("pids-limit")
	command.Flags().Uint64Var(&nofile, "nofile", 0, "Per-process guest file descriptor soft/hard ceiling (128..1048576; 0 preserves runtime default)")
	command.Flags().StringArrayVar(&ioLimits, "io-limit", nil, "Host block device I/O ceiling: 'MAJOR:MINOR rbps=N wbps=N riops=N wiops=N' (repeatable; omitted dimensions are unlimited)")
	return command
}

func init() { rootCmd.AddCommand(newProfileCommand()) }
