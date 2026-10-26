package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/instanceux"
	"github.com/pacificbelt30/codex-dock/internal/sandbox"
	"github.com/spf13/cobra"
)

var psAll bool
var psFilter string
var psSort string
var psFormat string
var psColumns string
var psWide bool
var psNoTruncate bool
var psSummary bool
var psPretty bool
var psSaveSnapshot string
var psCompareSnapshot string

var psCmd = &cobra.Command{
	Use:     "ps",
	Aliases: []string{"instances"},
	Short:   "List and inspect Oaiso worker instances",
	RunE: func(cmd *cobra.Command, _ []string) error {
		manager, err := sandbox.NewManager(managementConfig())
		if err != nil {
			return err
		}
		defer func() { _ = manager.Close() }()
		workers, err := manager.List(psAll)
		if err != nil {
			return fmt.Errorf("listing workers: %w", err)
		}
		query, err := instanceux.ParseQuery(psFilter)
		if err != nil {
			return err
		}
		sortKey, err := instanceux.ParseSortKey(psSort)
		if err != nil {
			return err
		}
		workers = instanceux.Sort(instanceux.Filter(workers, query), sortKey)
		snapshot := instanceux.NewSnapshot(workers, time.Now())
		if psCompareSnapshot != "" {
			previous, loadErr := instanceux.LoadSnapshot(psCompareSnapshot)
			if loadErr != nil {
				return loadErr
			}
			diff := instanceux.CompareSnapshots(previous, snapshot)
			if err := instanceux.WriteDiff(cmd.OutOrStdout(), diff, strings.EqualFold(psFormat, "json")); err != nil {
				return err
			}
			if psSaveSnapshot != "" {
				return instanceux.SaveSnapshot(psSaveSnapshot, snapshot)
			}
			return nil
		}
		columns, err := instanceux.ParseColumns(psColumns)
		if err != nil {
			return err
		}
		if psSaveSnapshot != "" {
			if err := instanceux.SaveSnapshot(psSaveSnapshot, snapshot); err != nil {
				return err
			}
		}
		if psWide && !cmd.Flags().Changed("columns") {
			columns = instanceux.WideColumns()
		}
		options := instanceux.RenderOptions{Columns: columns, NoTruncate: psNoTruncate || psWide}
		switch strings.ToLower(psFormat) {
		case "table":
			err = instanceux.WriteTable(cmd.OutOrStdout(), snapshot, options)
		case "json":
			err = instanceux.WriteJSON(cmd.OutOrStdout(), snapshot, psPretty)
		case "csv":
			err = instanceux.WriteCSV(cmd.OutOrStdout(), snapshot, options)
		default:
			return fmt.Errorf("unknown --format %q; use table, json, or csv", psFormat)
		}
		if err != nil {
			return err
		}
		if psSummary && psFormat == "table" {
			for _, line := range snapshot.Summary.Lines() {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), line)
			}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(psCmd)
	f := psCmd.Flags()
	f.BoolVarP(&psAll, "all", "a", false, "Show stopped workers as well as running workers")
	f.StringVarP(&psFilter, "filter", "f", "", "Filter text or field:value expression")
	f.StringVar(&psSort, "sort", "name", "Sort by name, status, newest, oldest, branch, or policy")
	f.StringVar(&psFormat, "format", "table", "Output format: table, json, or csv")
	f.StringVar(&psColumns, "columns", "", "Comma-separated table/CSV columns")
	f.BoolVarP(&psWide, "wide", "w", false, "Show every available column without truncation")
	f.BoolVar(&psNoTruncate, "no-truncate", false, "Do not truncate task text")
	f.BoolVar(&psSummary, "summary", false, "Print aggregate counts after the table")
	f.BoolVar(&psPretty, "pretty", false, "Indent JSON output")
	f.StringVar(&psSaveSnapshot, "save-snapshot", "", "Atomically save the current instance state as JSON")
	f.StringVar(&psCompareSnapshot, "compare-snapshot", "", "Show changes since a saved instance snapshot")
	registerCompletion(psCmd, "sort", cobra.FixedCompletions([]cobra.Completion{"name", "status", "newest", "oldest", "branch", "policy"}, cobra.ShellCompDirectiveNoFileComp))
	registerCompletion(psCmd, "format", cobra.FixedCompletions([]cobra.Completion{"table", "json", "csv"}, cobra.ShellCompDirectiveNoFileComp))
}
