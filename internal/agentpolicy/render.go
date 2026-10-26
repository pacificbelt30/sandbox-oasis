package agentpolicy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	EnvProfile     = "OAISO_AGENT_POLICY"
	EnvCodexConfig = "OAISO_CODEX_POLICY_JSON"
	EnvClaudeArgs  = "OAISO_CLAUDE_POLICY_ARGS_JSON"
)

// CodexConfig is the fixed, allowlisted payload passed to entrypoint.sh. It is
// JSON rather than a TOML fragment so values cannot escape into new keys.
type CodexConfig struct {
	ApprovalsReviewer  string `json:"approvals_reviewer,omitempty"`
	SandboxMode        string `json:"sandbox_mode,omitempty"`
	ReasoningEffort    string `json:"model_reasoning_effort,omitempty"`
	ReasoningSummary   string `json:"model_reasoning_summary,omitempty"`
	WebSearch          string `json:"web_search,omitempty"`
	ServiceTier        string `json:"service_tier,omitempty"`
	HistoryPersistence string `json:"history_persistence,omitempty"`
	HistoryMaxBytes    *int64 `json:"history_max_bytes,omitempty"`
	FileOpener         string `json:"file_opener,omitempty"`
	HideAgentReasoning bool   `json:"hide_agent_reasoning,omitempty"`
	AllowLoginShell    bool   `json:"allow_login_shell,omitempty"`
}

func (e Effective) CodexConfig() CodexConfig {
	var historyMaxBytes *int64
	if _, configured := e.Sources["codex.history_max_bytes"]; configured {
		historyMaxBytes = Int64(e.Codex.HistoryMaxBytes)
	}
	return CodexConfig{
		ApprovalsReviewer:  e.Codex.ApprovalsReviewer,
		SandboxMode:        e.Codex.SandboxMode,
		ReasoningEffort:    e.Codex.ReasoningEffort,
		ReasoningSummary:   e.Codex.ReasoningSummary,
		WebSearch:          e.Codex.WebSearch,
		ServiceTier:        e.Codex.ServiceTier,
		HistoryPersistence: e.Codex.HistoryPersistence,
		HistoryMaxBytes:    historyMaxBytes,
		FileOpener:         e.Codex.FileOpener,
		HideAgentReasoning: e.Codex.HideAgentReasoning,
		AllowLoginShell:    e.Codex.AllowLoginShell,
	}
}

func (e Effective) ClaudeArgs() []string {
	var args []string
	if e.Claude.PermissionMode != "" {
		args = append(args, "--permission-mode", e.Claude.PermissionMode)
	}
	if e.Claude.Effort != "" {
		args = append(args, "--effort", e.Claude.Effort)
	}
	if e.Claude.FallbackModel != "" {
		args = append(args, "--fallback-model", e.Claude.FallbackModel)
	}
	if e.Claude.Bare {
		args = append(args, "--bare")
	}
	if e.Claude.SafeMode {
		args = append(args, "--safe-mode")
	}
	if e.Claude.Restricted {
		args = append(args, "--restricted")
	}
	if e.Claude.NoSessionPersistence {
		args = append(args, "--no-session-persistence")
	}
	return args
}

func (e Effective) Environment() ([]string, error) {
	codex, err := json.Marshal(e.CodexConfig())
	if err != nil {
		return nil, fmt.Errorf("encoding Codex policy: %w", err)
	}
	claude, err := json.Marshal(e.ClaudeArgs())
	if err != nil {
		return nil, fmt.Errorf("encoding Claude policy: %w", err)
	}
	return []string{EnvProfile + "=" + e.Name, EnvCodexConfig + "=" + string(codex), EnvClaudeArgs + "=" + string(claude)}, nil
}

func (e Effective) Fields() []Field {
	descriptions := fieldDescriptions()
	risk := fieldRisks(e)
	values := map[string]string{
		"description":                   e.Description,
		"agent":                         e.Agent,
		"model":                         e.Model,
		"approval_mode":                 e.ApprovalMode,
		"codex.approvals_reviewer":      e.Codex.ApprovalsReviewer,
		"codex.sandbox_mode":            e.Codex.SandboxMode,
		"codex.reasoning_effort":        e.Codex.ReasoningEffort,
		"codex.reasoning_summary":       e.Codex.ReasoningSummary,
		"codex.web_search":              e.Codex.WebSearch,
		"codex.service_tier":            e.Codex.ServiceTier,
		"codex.history_persistence":     e.Codex.HistoryPersistence,
		"codex.history_max_bytes":       strconv.FormatInt(e.Codex.HistoryMaxBytes, 10),
		"codex.file_opener":             e.Codex.FileOpener,
		"codex.hide_agent_reasoning":    strconv.FormatBool(e.Codex.HideAgentReasoning),
		"codex.allow_login_shell":       strconv.FormatBool(e.Codex.AllowLoginShell),
		"claude.permission_mode":        e.Claude.PermissionMode,
		"claude.effort":                 e.Claude.Effort,
		"claude.fallback_model":         e.Claude.FallbackModel,
		"claude.bare":                   strconv.FormatBool(e.Claude.Bare),
		"claude.safe_mode":              strconv.FormatBool(e.Claude.SafeMode),
		"claude.restricted":             strconv.FormatBool(e.Claude.Restricted),
		"claude.no_session_persistence": strconv.FormatBool(e.Claude.NoSessionPersistence),
	}
	paths := make([]string, 0, len(e.Sources))
	for path := range e.Sources {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	out := make([]Field, 0, len(paths))
	for _, path := range paths {
		out = append(out, Field{Path: path, Value: values[path], Source: e.Sources[path], Description: descriptions[path], Risk: risk[path]})
	}
	return out
}

func fieldDescriptions() map[string]string {
	return map[string]string{
		"description":                   "Human-readable purpose of the effective policy.",
		"agent":                         "Default agent selected for an interactive worker.",
		"model":                         "Default model passed to the selected agent.",
		"approval_mode":                 "Oaiso's portable approval behavior across agents.",
		"codex.approvals_reviewer":      "Who reviews Codex approval requests.",
		"codex.sandbox_mode":            "Codex's inner sandbox; the Oaiso VM remains separate.",
		"codex.reasoning_effort":        "Codex reasoning effort hint.",
		"codex.reasoning_summary":       "Visibility of Codex reasoning summaries.",
		"codex.web_search":              "Codex web-search mode, still subject to worker egress policy.",
		"codex.service_tier":            "Requested Codex API service tier.",
		"codex.history_persistence":     "Whether Codex persists conversation history in the guest.",
		"codex.history_max_bytes":       "Maximum guest history file size.",
		"codex.file_opener":             "Editor URI integration name.",
		"codex.hide_agent_reasoning":    "Hide raw agent reasoning events.",
		"codex.allow_login_shell":       "Permit login-shell execution in Codex.",
		"claude.permission_mode":        "Claude Code permission mode.",
		"claude.effort":                 "Claude Code effort hint.",
		"claude.fallback_model":         "Claude Code fallback model.",
		"claude.bare":                   "Disable most local Claude customizations.",
		"claude.safe_mode":              "Disable non-policy Claude customizations.",
		"claude.restricted":             "Remove Claude code-running and web-fetch tools.",
		"claude.no_session_persistence": "Do not persist Claude sessions.",
	}
}

func fieldRisks(e Effective) map[string]string {
	out := map[string]string{}
	if e.ApprovalMode == "full-auto" || e.ApprovalMode == "danger" {
		out["approval_mode"] = "high"
	}
	if e.Codex.SandboxMode == "danger-full-access" {
		out["codex.sandbox_mode"] = "high"
	}
	if e.Codex.ApprovalsReviewer == "auto_review" {
		out["codex.approvals_reviewer"] = "medium"
	}
	if e.Codex.WebSearch == "live" {
		out["codex.web_search"] = "medium"
	}
	if e.Claude.PermissionMode == "bypassPermissions" || e.Claude.PermissionMode == "dontAsk" {
		out["claude.permission_mode"] = "high"
	}
	if e.Claude.PermissionMode == "auto" {
		out["claude.permission_mode"] = "medium"
	}
	for path := range e.Sources {
		if out[path] == "" {
			out[path] = "low"
		}
	}
	return out
}

func (e Effective) Explain() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Policy: %s\n", e.Name)
	if e.Description != "" {
		fmt.Fprintf(&b, "Description: %s\n", e.Description)
	}
	fmt.Fprintf(&b, "Inheritance: %s\n", strings.Join(e.Chain, " -> "))
	for _, field := range e.Fields() {
		fmt.Fprintf(&b, "%-32s = %-18s source=%-16s risk=%s\n", field.Path, field.Value, field.Source, field.Risk)
	}
	warnings := Warnings(e)
	if len(warnings) > 0 {
		b.WriteString("Warnings:\n")
		for _, warning := range warnings {
			fmt.Fprintf(&b, "  - %s\n", warning)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
