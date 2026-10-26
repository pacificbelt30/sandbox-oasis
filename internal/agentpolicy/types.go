// Package agentpolicy manages host-owned, allowlisted defaults for coding agents.
// It deliberately models supported settings instead of accepting arbitrary agent
// configuration, so a shared project cannot smuggle host-level policy into a VM.
package agentpolicy

import (
	"fmt"
	"sort"
	"strings"
)

const CurrentVersion = 1

type File struct {
	Version  int                `toml:"version"`
	Default  string             `toml:"default"`
	Profiles map[string]Profile `toml:"profiles"`
}

type Profile struct {
	Description  string         `toml:"description,omitempty"`
	Extends      string         `toml:"extends,omitempty"`
	Agent        string         `toml:"agent,omitempty"`
	Model        string         `toml:"model,omitempty"`
	ApprovalMode string         `toml:"approval_mode,omitempty"`
	Codex        CodexSettings  `toml:"codex,omitempty"`
	Claude       ClaudeSettings `toml:"claude,omitempty"`
}

// Pointer fields preserve the difference between an omitted setting and an
// explicit false/zero value while profiles are inherited.
type CodexSettings struct {
	ApprovalsReviewer  *string `toml:"approvals_reviewer,omitempty"`
	SandboxMode        *string `toml:"sandbox_mode,omitempty"`
	ReasoningEffort    *string `toml:"reasoning_effort,omitempty"`
	ReasoningSummary   *string `toml:"reasoning_summary,omitempty"`
	WebSearch          *string `toml:"web_search,omitempty"`
	ServiceTier        *string `toml:"service_tier,omitempty"`
	HistoryPersistence *string `toml:"history_persistence,omitempty"`
	HistoryMaxBytes    *int64  `toml:"history_max_bytes,omitempty"`
	FileOpener         *string `toml:"file_opener,omitempty"`
	HideAgentReasoning *bool   `toml:"hide_agent_reasoning,omitempty"`
	AllowLoginShell    *bool   `toml:"allow_login_shell,omitempty"`
}

type ClaudeSettings struct {
	PermissionMode       *string `toml:"permission_mode,omitempty"`
	Effort               *string `toml:"effort,omitempty"`
	FallbackModel        *string `toml:"fallback_model,omitempty"`
	Bare                 *bool   `toml:"bare,omitempty"`
	SafeMode             *bool   `toml:"safe_mode,omitempty"`
	Restricted           *bool   `toml:"restricted,omitempty"`
	NoSessionPersistence *bool   `toml:"no_session_persistence,omitempty"`
}

type Effective struct {
	Name         string
	Description  string
	Chain        []string
	Agent        string
	Model        string
	ApprovalMode string
	Codex        EffectiveCodex
	Claude       EffectiveClaude
	Sources      map[string]string
}

type EffectiveCodex struct {
	ApprovalsReviewer  string
	SandboxMode        string
	ReasoningEffort    string
	ReasoningSummary   string
	WebSearch          string
	ServiceTier        string
	HistoryPersistence string
	HistoryMaxBytes    int64
	FileOpener         string
	HideAgentReasoning bool
	AllowLoginShell    bool
}

type EffectiveClaude struct {
	PermissionMode       string
	Effort               string
	FallbackModel        string
	Bare                 bool
	SafeMode             bool
	Restricted           bool
	NoSessionPersistence bool
}

type Field struct {
	Path        string `json:"path"`
	Value       string `json:"value"`
	Source      string `json:"source"`
	Description string `json:"description"`
	Risk        string `json:"risk"`
}

type Summary struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	BuiltIn     bool     `json:"built_in"`
	Default     bool     `json:"default"`
	Extends     string   `json:"extends,omitempty"`
	Agent       string   `json:"agent,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
}

type ValidationError struct {
	Profile string
	Field   string
	Value   string
	Problem string
}

func (e ValidationError) Error() string {
	prefix := "agent policy"
	if e.Profile != "" {
		prefix += " " + fmt.Sprintf("%q", e.Profile)
	}
	if e.Field != "" {
		prefix += "." + e.Field
	}
	if e.Value != "" {
		return fmt.Sprintf("%s=%q %s", prefix, e.Value, e.Problem)
	}
	return prefix + " " + e.Problem
}

func String(v string) *string { return &v }
func Bool(v bool) *bool       { return &v }
func Int64(v int64) *int64    { return &v }

func cloneProfile(p Profile) Profile {
	return Profile{
		Description:  p.Description,
		Extends:      p.Extends,
		Agent:        p.Agent,
		Model:        p.Model,
		ApprovalMode: p.ApprovalMode,
		Codex:        p.Codex,
		Claude:       p.Claude,
	}
}

func cloneFile(f File) File {
	out := File{Version: f.Version, Default: f.Default, Profiles: make(map[string]Profile, len(f.Profiles))}
	for name, profile := range f.Profiles {
		out.Profiles[name] = cloneProfile(profile)
	}
	return out
}

func profileNames(profiles map[string]Profile) []string {
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func cleanName(name string) string { return strings.ToLower(strings.TrimSpace(name)) }
