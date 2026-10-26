package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/pacificbelt30/codex-dock/internal/agentpolicy"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var agentPolicyFile string
var agentPolicyName string
var agentPolicyJSON bool
var agentPolicyInitForce bool
var createPolicy agentpolicy.Profile
var createCodexApprovalsReviewer string
var createCodexSandbox string
var createCodexReasoningEffort string
var createCodexReasoningSummary string
var createCodexWebSearch string
var createCodexServiceTier string
var createCodexHistory string
var createCodexHistoryBytes int64
var createCodexFileOpener string
var createCodexHideReasoning bool
var createCodexLoginShell bool
var createClaudePermission string
var createClaudeEffort string
var createClaudeFallback string
var createClaudeBare bool
var createClaudeSafe bool
var createClaudeRestricted bool
var createClaudeNoHistory bool

var agentPolicyCmd = &cobra.Command{
	Use:     "agent-policy",
	Aliases: []string{"agent-policies"},
	Short:   "Manage host-owned defaults for Codex and Claude",
}

var agentPolicyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List built-in and host-defined agent policies",
	RunE: func(cmd *cobra.Command, _ []string) error {
		store, snapshot, err := loadAgentPolicyStore()
		if err != nil {
			return err
		}
		rows, err := agentpolicy.List(snapshot.File)
		if err != nil {
			return err
		}
		if agentPolicyJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintln(w, "DEFAULT\tNAME\tKIND\tAGENT\tDESCRIPTION")
		for _, row := range rows {
			mark := ""
			if row.Default {
				mark = "*"
			}
			kind := "host"
			if row.BuiltIn {
				kind = "built-in"
			}
			description := row.Description
			if len(row.Warnings) > 0 {
				description += " [warning: " + strings.Join(row.Warnings, "; ") + "]"
			}
			_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", mark, row.Name, kind, dash(row.Agent), description)
		}
		if err := w.Flush(); err != nil {
			return err
		}
		if !snapshot.Exists {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Using built-ins; initialize %s to save host policies.\n", store.Path)
		}
		return nil
	},
}

var agentPolicyShowCmd = &cobra.Command{
	Use:   "show [NAME]",
	Short: "Show an effective policy and where each value came from",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, snapshot, err := loadAgentPolicyStore()
		if err != nil {
			return err
		}
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		effective, err := agentpolicy.Resolve(snapshot.File, name)
		if err != nil {
			return err
		}
		if agentPolicyJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(struct {
				Name        string              `json:"name"`
				Description string              `json:"description"`
				Chain       []string            `json:"chain"`
				Fields      []agentpolicy.Field `json:"fields"`
				Warnings    []string            `json:"warnings,omitempty"`
			}{effective.Name, effective.Description, effective.Chain, effective.Fields(), agentpolicy.Warnings(effective)})
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), effective.Explain())
		return err
	},
}

var agentPolicyValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate the host policy file and every inheritance chain",
	RunE: func(cmd *cobra.Command, _ []string) error {
		store, snapshot, err := loadAgentPolicyStore()
		if err != nil {
			return err
		}
		if err := agentpolicy.ValidateFile(snapshot.File); err != nil {
			return err
		}
		resolved, err := agentpolicy.ResolveAll(snapshot.File)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s: valid (%d policies)\n", store.Path, len(resolved))
		return err
	},
}

var agentPolicyPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the host agent-policy file path",
	RunE: func(cmd *cobra.Command, _ []string) error {
		store, err := agentPolicyStore()
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), store.Path)
		return err
	},
}

var agentPolicyInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create an owner-private policy file with a documented example",
	RunE: func(cmd *cobra.Command, _ []string) error {
		store, err := agentPolicyStore()
		if err != nil {
			return err
		}
		if err := store.Initialize(agentPolicyInitForce); err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), store.Path)
		return err
	},
}

var agentPolicyDefaultCmd = &cobra.Command{
	Use:   "default NAME",
	Short: "Select the host default agent policy",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := agentPolicyStore()
		if err != nil {
			return err
		}
		if err := store.SetDefault(args[0]); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "default agent policy: %s\n", strings.ToLower(args[0]))
		return err
	},
}

var agentPolicyCreateCmd = &cobra.Command{
	Use:   "create NAME",
	Short: "Create a host policy from allowlisted settings",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		profile := createPolicy
		setString := func(flag, value string, target **string) {
			if cmd.Flags().Changed(flag) {
				*target = agentpolicy.String(value)
			}
		}
		setBool := func(flag string, value bool, target **bool) {
			if cmd.Flags().Changed(flag) {
				*target = agentpolicy.Bool(value)
			}
		}
		setString("codex-approvals-reviewer", createCodexApprovalsReviewer, &profile.Codex.ApprovalsReviewer)
		setString("codex-sandbox", createCodexSandbox, &profile.Codex.SandboxMode)
		setString("codex-reasoning-effort", createCodexReasoningEffort, &profile.Codex.ReasoningEffort)
		setString("codex-reasoning-summary", createCodexReasoningSummary, &profile.Codex.ReasoningSummary)
		setString("codex-web-search", createCodexWebSearch, &profile.Codex.WebSearch)
		setString("codex-service-tier", createCodexServiceTier, &profile.Codex.ServiceTier)
		setString("codex-history", createCodexHistory, &profile.Codex.HistoryPersistence)
		if cmd.Flags().Changed("codex-history-max-bytes") {
			profile.Codex.HistoryMaxBytes = agentpolicy.Int64(createCodexHistoryBytes)
		}
		setString("codex-file-opener", createCodexFileOpener, &profile.Codex.FileOpener)
		setBool("codex-hide-reasoning", createCodexHideReasoning, &profile.Codex.HideAgentReasoning)
		setBool("codex-login-shell", createCodexLoginShell, &profile.Codex.AllowLoginShell)
		setString("claude-permission-mode", createClaudePermission, &profile.Claude.PermissionMode)
		setString("claude-effort", createClaudeEffort, &profile.Claude.Effort)
		setString("claude-fallback-model", createClaudeFallback, &profile.Claude.FallbackModel)
		setBool("claude-bare", createClaudeBare, &profile.Claude.Bare)
		setBool("claude-safe-mode", createClaudeSafe, &profile.Claude.SafeMode)
		setBool("claude-restricted", createClaudeRestricted, &profile.Claude.Restricted)
		setBool("claude-no-session-persistence", createClaudeNoHistory, &profile.Claude.NoSessionPersistence)
		store, err := agentPolicyStore()
		if err != nil {
			return err
		}
		if err := store.Put(args[0], profile); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "saved agent policy %s\n", strings.ToLower(args[0]))
		return err
	},
}

var agentPolicyDeleteCmd = &cobra.Command{
	Use:     "delete NAME",
	Aliases: []string{"rm"},
	Short:   "Delete a host-defined agent policy",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := agentPolicyStore()
		if err != nil {
			return err
		}
		if err := store.Delete(args[0]); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "deleted agent policy %s\n", strings.ToLower(args[0]))
		return err
	},
}

var agentPolicyCloneCmd = &cobra.Command{
	Use:   "clone SOURCE NAME",
	Short: "Create an editable policy inheriting from another policy",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := agentPolicyStore()
		if err != nil {
			return err
		}
		if err := store.Clone(args[0], args[1]); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "cloned %s as %s\n", strings.ToLower(args[0]), strings.ToLower(args[1]))
		return err
	},
}

var agentPolicySetCmd = &cobra.Command{
	Use:   "set NAME FIELD VALUE",
	Short: "Set one allowlisted field on a host policy",
	Args:  cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := agentPolicyStore()
		if err != nil {
			return err
		}
		if err := store.Set(args[0], args[1], args[2]); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s.%s updated\n", strings.ToLower(args[0]), strings.ToLower(args[1]))
		return err
	},
}

var agentPolicyUnsetCmd = &cobra.Command{
	Use:   "unset NAME FIELD",
	Short: "Remove one field so its inherited value applies",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := agentPolicyStore()
		if err != nil {
			return err
		}
		if err := store.Unset(args[0], args[1]); err != nil {
			return err
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s.%s now inherits\n", strings.ToLower(args[0]), strings.ToLower(args[1]))
		return err
	},
}

var agentPolicyDiffCmd = &cobra.Command{
	Use:   "diff LEFT RIGHT",
	Short: "Compare two effective policies",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, snapshot, err := loadAgentPolicyStore()
		if err != nil {
			return err
		}
		left, err := agentpolicy.Resolve(snapshot.File, args[0])
		if err != nil {
			return err
		}
		right, err := agentpolicy.Resolve(snapshot.File, args[1])
		if err != nil {
			return err
		}
		differences := agentpolicy.Diff(left, right)
		if agentPolicyJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(differences)
		}
		if len(differences) == 0 {
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "effective policies are identical")
			return err
		}
		writer := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		_, _ = fmt.Fprintf(writer, "FIELD\t%s\t%s\tSOURCE CHANGE\n", strings.ToUpper(left.Name), strings.ToUpper(right.Name))
		for _, difference := range differences {
			_, _ = fmt.Fprintf(writer, "%s\t%s\t%s\t%s -> %s\n", difference.Path, dash(difference.Left), dash(difference.Right), dash(difference.LeftSource), dash(difference.RightSource))
		}
		return writer.Flush()
	},
}

var agentPolicyAuditCmd = &cobra.Command{
	Use:   "audit [NAME]",
	Short: "Explain the effective safety posture of an agent policy",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, snapshot, err := loadAgentPolicyStore()
		if err != nil {
			return err
		}
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		effective, err := agentpolicy.Resolve(snapshot.File, name)
		if err != nil {
			return err
		}
		report := agentpolicy.Audit(effective)
		if agentPolicyJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(report)
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), report.String())
		return err
	},
}

var agentPolicyFieldsCmd = &cobra.Command{
	Use:   "fields [SEARCH]",
	Short: "List the allowlisted host-policy fields and safety semantics",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := ""
		if len(args) == 1 {
			query = args[0]
		}
		definitions := agentpolicy.SearchFields(query)
		if agentPolicyJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(definitions)
		}
		if len(definitions) == 0 {
			return fmt.Errorf("no agent policy fields match %q", query)
		}
		for index, definition := range definitions {
			if index > 0 {
				_, _ = fmt.Fprintln(cmd.OutOrStdout())
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), agentpolicy.FormatDefinition(definition))
		}
		return nil
	},
}

var agentPolicyDoctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Validate and inspect policy inheritance maintainability",
	RunE: func(cmd *cobra.Command, _ []string) error {
		_, snapshot, err := loadAgentPolicyStore()
		if err != nil {
			return err
		}
		report := agentpolicy.Doctor(snapshot.File)
		if agentPolicyJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(report)
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), report.String())
		return err
	},
}

var agentPolicyPlanCmd = &cobra.Command{
	Use:   "plan [NAME]",
	Short: "Preview the allowlisted settings applied to each agent",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, snapshot, err := loadAgentPolicyStore()
		if err != nil {
			return err
		}
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		effective, err := agentpolicy.Resolve(snapshot.File, name)
		if err != nil {
			return err
		}
		plan := agentpolicy.Plan(effective)
		if agentPolicyJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(plan)
		}
		data, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return err
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&agentPolicyFile, "agent-policy-file", "", "Host agent-policy file (default: ~/.config/codex-dock/agent-policies/config.toml)")
	rootCmd.AddCommand(agentPolicyCmd)
	agentPolicyCmd.PersistentFlags().BoolVar(&agentPolicyJSON, "json", false, "Output machine-readable JSON")
	agentPolicyCmd.AddCommand(agentPolicyListCmd, agentPolicyShowCmd, agentPolicyValidateCmd, agentPolicyPathCmd, agentPolicyInitCmd, agentPolicyDefaultCmd, agentPolicyCreateCmd, agentPolicyDeleteCmd, agentPolicyCloneCmd, agentPolicySetCmd, agentPolicyUnsetCmd, agentPolicyDiffCmd, agentPolicyAuditCmd, agentPolicyFieldsCmd, agentPolicyDoctorCmd, agentPolicyPlanCmd)
	agentPolicyInitCmd.Flags().BoolVar(&agentPolicyInitForce, "force", false, "Replace the existing policy file")
	f := agentPolicyCreateCmd.Flags()
	f.StringVar(&createPolicy.Description, "description", "", "Human-readable policy purpose")
	f.StringVar(&createPolicy.Extends, "extends", "interactive", "Parent policy")
	f.StringVar(&createPolicy.Agent, "agent", "", "Default agent: shell, codex, or claude")
	f.StringVar(&createPolicy.Model, "model", "", "Default model")
	f.StringVar(&createPolicy.ApprovalMode, "approval-mode", "", "Oaiso approval mode")
	f.StringVar(&createCodexApprovalsReviewer, "codex-approvals-reviewer", "", "Codex approval reviewer: user or auto_review")
	f.StringVar(&createCodexSandbox, "codex-sandbox", "", "Codex inner sandbox mode")
	f.StringVar(&createCodexReasoningEffort, "codex-reasoning-effort", "", "Codex reasoning effort")
	f.StringVar(&createCodexReasoningSummary, "codex-reasoning-summary", "", "Codex reasoning summary mode")
	f.StringVar(&createCodexWebSearch, "codex-web-search", "", "Codex web search mode")
	f.StringVar(&createCodexServiceTier, "codex-service-tier", "", "Codex service tier")
	f.StringVar(&createCodexHistory, "codex-history", "", "Codex history persistence")
	f.Int64Var(&createCodexHistoryBytes, "codex-history-max-bytes", 0, "Codex history byte limit")
	f.StringVar(&createCodexFileOpener, "codex-file-opener", "", "Codex editor integration")
	f.BoolVar(&createCodexHideReasoning, "codex-hide-reasoning", false, "Hide Codex raw reasoning")
	f.BoolVar(&createCodexLoginShell, "codex-login-shell", false, "Allow Codex login shells")
	f.StringVar(&createClaudePermission, "claude-permission-mode", "", "Claude permission mode")
	f.StringVar(&createClaudeEffort, "claude-effort", "", "Claude effort")
	f.StringVar(&createClaudeFallback, "claude-fallback-model", "", "Claude fallback model")
	f.BoolVar(&createClaudeBare, "claude-bare", false, "Enable Claude bare mode")
	f.BoolVar(&createClaudeSafe, "claude-safe-mode", false, "Enable Claude safe mode")
	f.BoolVar(&createClaudeRestricted, "claude-restricted", false, "Enable Claude restricted mode")
	f.BoolVar(&createClaudeNoHistory, "claude-no-session-persistence", false, "Disable Claude session persistence")
}

func agentPolicyStore() (*agentpolicy.Store, error) {
	path := agentPolicyFile
	if path == "" && viper.IsSet("agent_policy.file") {
		path = viper.GetString("agent_policy.file")
	}
	return agentpolicy.NewStore(path)
}

func loadAgentPolicyStore() (*agentpolicy.Store, agentpolicy.Snapshot, error) {
	store, err := agentPolicyStore()
	if err != nil {
		return nil, agentpolicy.Snapshot{}, err
	}
	snapshot, err := store.Load()
	return store, snapshot, err
}

func resolveHostAgentPolicy() (agentpolicy.Effective, error) {
	_, snapshot, err := loadAgentPolicyStore()
	if err != nil {
		return agentpolicy.Effective{}, err
	}
	name := agentPolicyName
	if name == "" && viper.IsSet("run.agent_policy") {
		name = viper.GetString("run.agent_policy")
	}
	return agentpolicy.Resolve(snapshot.File, name)
}

func completeAgentPolicies(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	store, err := agentPolicyStore()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	names, err := store.ExportNames()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	sort.Strings(names)
	return names, cobra.ShellCompDirectiveNoFileComp
}

func dash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}
