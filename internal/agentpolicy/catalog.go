package agentpolicy

import (
	"fmt"
	"sort"
	"strings"
)

type ValueType string

const (
	ValueString  ValueType = "string"
	ValueBoolean ValueType = "boolean"
	ValueInteger ValueType = "integer"
	ValueEnum    ValueType = "enum"
)

type FieldDefinition struct {
	Path        string    `json:"path"`
	Type        ValueType `json:"type"`
	Agent       string    `json:"agent"`
	Values      []string  `json:"values,omitempty"`
	UpstreamKey string    `json:"upstream_key,omitempty"`
	CLIFlag     string    `json:"cli_flag,omitempty"`
	Description string    `json:"description"`
	Security    string    `json:"security"`
	Inheritable bool      `json:"inheritable"`
}

func FieldCatalog() []FieldDefinition {
	definitions := []FieldDefinition{
		{Path: "description", Type: ValueString, Agent: "oaiso", Description: "Human-readable purpose shown in list and TUI output.", Security: "Informational only.", Inheritable: false},
		{Path: "extends", Type: ValueString, Agent: "oaiso", Description: "Parent policy resolved before local settings.", Security: "Cycles and missing parents are rejected.", Inheritable: false},
		{Path: "agent", Type: ValueEnum, Agent: "oaiso", Values: []string{"shell", "codex", "claude"}, CLIFlag: "--agent", Description: "Default interactive program for new workers.", Security: "Does not authorize a provider credential.", Inheritable: true},
		{Path: "model", Type: ValueString, Agent: "both", CLIFlag: "--model", Description: "Default model passed to the selected agent.", Security: "Proxy model policy remains authoritative.", Inheritable: true},
		{Path: "approval_mode", Type: ValueEnum, Agent: "both", Values: []string{"suggest", "auto-edit", "full-auto", "danger"}, CLIFlag: "--approval-mode", Description: "Portable Oaiso approval behavior.", Security: "full-auto and danger remove interactive checks.", Inheritable: true},
		{Path: "codex.approvals_reviewer", Type: ValueEnum, Agent: "codex", Values: []string{"user", "auto_review"}, UpstreamKey: "approvals_reviewer", Description: "Select the user or Codex reviewer for approval decisions.", Security: "auto_review does not change sandboxing.", Inheritable: true},
		{Path: "codex.sandbox_mode", Type: ValueEnum, Agent: "codex", Values: []string{"read-only", "workspace-write", "danger-full-access"}, UpstreamKey: "sandbox_mode", CLIFlag: "--sandbox", Description: "Codex inner filesystem/process sandbox mode.", Security: "Kata remains the outer boundary.", Inheritable: true},
		{Path: "codex.reasoning_effort", Type: ValueEnum, Agent: "codex", Values: []string{"none", "minimal", "low", "medium", "high", "xhigh"}, UpstreamKey: "model_reasoning_effort", Description: "Reasoning effort requested from supported Codex models.", Security: "May affect latency and usage.", Inheritable: true},
		{Path: "codex.reasoning_summary", Type: ValueEnum, Agent: "codex", Values: []string{"auto", "concise", "detailed", "none"}, UpstreamKey: "model_reasoning_summary", Description: "Reasoning-summary visibility.", Security: "Detailed summaries may expose more task context in logs.", Inheritable: true},
		{Path: "codex.web_search", Type: ValueEnum, Agent: "codex", Values: []string{"disabled", "cached", "indexed", "live"}, UpstreamKey: "web_search", Description: "Codex web search behavior.", Security: "Cannot bypass Oaiso egress policy.", Inheritable: true},
		{Path: "codex.service_tier", Type: ValueEnum, Agent: "codex", Values: []string{"auto", "default", "fast", "flex", "priority"}, UpstreamKey: "service_tier", Description: "Requested API service tier.", Security: "May affect service cost and availability.", Inheritable: true},
		{Path: "codex.history_persistence", Type: ValueEnum, Agent: "codex", Values: []string{"save-all", "none"}, UpstreamKey: "history.persistence", Description: "Conversation history persistence inside the guest.", Security: "none reduces residual guest data.", Inheritable: true},
		{Path: "codex.history_max_bytes", Type: ValueInteger, Agent: "codex", UpstreamKey: "history.max_bytes", Description: "Maximum persisted history size from zero through one TiB.", Security: "Limits local guest disk growth.", Inheritable: true},
		{Path: "codex.file_opener", Type: ValueEnum, Agent: "codex", Values: []string{"none", "vscode", "vscode-insiders", "windsurf", "cursor"}, UpstreamKey: "file_opener", Description: "Editor URI integration identifier.", Security: "A fixed URI scheme selector, not a command.", Inheritable: true},
		{Path: "codex.hide_agent_reasoning", Type: ValueBoolean, Agent: "codex", UpstreamKey: "hide_agent_reasoning", Description: "Hide raw reasoning events in the terminal.", Security: "Presentation only; provider behavior is unchanged.", Inheritable: true},
		{Path: "codex.allow_login_shell", Type: ValueBoolean, Agent: "codex", UpstreamKey: "allow_login_shell", Description: "Permit login-shell command execution.", Security: "Startup files can affect command behavior.", Inheritable: true},
		{Path: "claude.permission_mode", Type: ValueEnum, Agent: "claude", Values: []string{"acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan"}, CLIFlag: "--permission-mode", Description: "Claude Code permission mode.", Security: "bypassPermissions disables Claude checks.", Inheritable: true},
		{Path: "claude.effort", Type: ValueEnum, Agent: "claude", Values: []string{"low", "medium", "high", "xhigh", "max"}, CLIFlag: "--effort", Description: "Claude Code effort level.", Security: "May affect latency and usage.", Inheritable: true},
		{Path: "claude.fallback_model", Type: ValueString, Agent: "claude", CLIFlag: "--fallback-model", Description: "Fallback model when the primary is unavailable.", Security: "Proxy model policy remains authoritative.", Inheritable: true},
		{Path: "claude.bare", Type: ValueBoolean, Agent: "claude", CLIFlag: "--bare", Description: "Disable most local Claude customizations.", Security: "Reduces influence from guest customization.", Inheritable: true},
		{Path: "claude.safe_mode", Type: ValueBoolean, Agent: "claude", CLIFlag: "--safe-mode", Description: "Disable non-policy Claude customizations.", Security: "Managed policy settings still apply.", Inheritable: true},
		{Path: "claude.restricted", Type: ValueBoolean, Agent: "claude", CLIFlag: "--restricted", Description: "Remove built-in code-running and web-fetch tools.", Security: "Narrows the agent tool surface.", Inheritable: true},
		{Path: "claude.no_session_persistence", Type: ValueBoolean, Agent: "claude", CLIFlag: "--no-session-persistence", Description: "Disable Claude session persistence.", Security: "Reduces residual guest data.", Inheritable: true},
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Path < definitions[j].Path })
	return definitions
}

func FindField(path string) (FieldDefinition, bool) {
	path = normalizeFieldPath(path)
	for _, definition := range FieldCatalog() {
		if definition.Path == path {
			return definition, true
		}
	}
	return FieldDefinition{}, false
}

func SearchFields(query string) []FieldDefinition {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return FieldCatalog()
	}
	var matches []FieldDefinition
	for _, definition := range FieldCatalog() {
		haystack := strings.ToLower(strings.Join([]string{definition.Path, definition.Agent, definition.UpstreamKey, definition.CLIFlag, definition.Description, definition.Security, strings.Join(definition.Values, " ")}, " "))
		if strings.Contains(haystack, query) {
			matches = append(matches, definition)
		}
	}
	return matches
}

func normalizeFieldPath(path string) string {
	path = strings.ToLower(strings.TrimSpace(path))
	aliases := map[string]string{
		"approval-mode":                 "approval_mode",
		"codex.model_reasoning_effort":  "codex.reasoning_effort",
		"codex.model_reasoning_summary": "codex.reasoning_summary",
		"codex.history.persistence":     "codex.history_persistence",
		"codex.history.max_bytes":       "codex.history_max_bytes",
	}
	if canonical := aliases[path]; canonical != "" {
		return canonical
	}
	return path
}

func FormatDefinition(definition FieldDefinition) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "%s (%s, %s)\n", definition.Path, definition.Type, definition.Agent)
	if len(definition.Values) > 0 {
		fmt.Fprintf(&builder, "  values: %s\n", strings.Join(definition.Values, ", "))
	}
	if definition.UpstreamKey != "" {
		fmt.Fprintf(&builder, "  upstream: %s\n", definition.UpstreamKey)
	}
	if definition.CLIFlag != "" {
		fmt.Fprintf(&builder, "  CLI: %s\n", definition.CLIFlag)
	}
	fmt.Fprintf(&builder, "  %s\n", definition.Description)
	fmt.Fprintf(&builder, "  security: %s", definition.Security)
	return builder.String()
}
