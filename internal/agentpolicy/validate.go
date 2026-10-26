package agentpolicy

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

var (
	namePattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
	modelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)
)

var allowed = map[string]map[string]struct{}{
	"agent":                     values("", "shell", "codex", "claude"),
	"approval_mode":             values("", "suggest", "auto-edit", "full-auto", "danger"),
	"codex.approvals_reviewer":  values("user", "auto_review"),
	"codex.sandbox_mode":        values("read-only", "workspace-write", "danger-full-access"),
	"codex.reasoning_effort":    values("none", "minimal", "low", "medium", "high", "xhigh"),
	"codex.reasoning_summary":   values("auto", "concise", "detailed", "none"),
	"codex.web_search":          values("disabled", "cached", "indexed", "live"),
	"codex.service_tier":        values("auto", "default", "fast", "flex", "priority"),
	"codex.history_persistence": values("save-all", "none"),
	"codex.file_opener":         values("none", "vscode", "vscode-insiders", "windsurf", "cursor"),
	"claude.permission_mode":    values("acceptEdits", "auto", "bypassPermissions", "manual", "dontAsk", "plan"),
	"claude.effort":             values("low", "medium", "high", "xhigh", "max"),
}

func values(items ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(items))
	for _, item := range items {
		out[item] = struct{}{}
	}
	return out
}

func ValidateFile(file File) error {
	if file.Version != 0 && file.Version != CurrentVersion {
		return ValidationError{Field: "version", Value: fmt.Sprint(file.Version), Problem: fmt.Sprintf("is unsupported; expected %d", CurrentVersion)}
	}
	if file.Default != "" {
		if err := validateName(file.Default); err != nil {
			return ValidationError{Field: "default", Value: file.Default, Problem: err.Error()}
		}
		if _, ok := mergedProfiles(file)[file.Default]; !ok {
			return ValidationError{Field: "default", Value: file.Default, Problem: "does not name a profile"}
		}
	}
	for _, name := range profileNames(file.Profiles) {
		if err := ValidateProfile(name, file.Profiles[name]); err != nil {
			return err
		}
	}
	_, err := ResolveAll(file)
	return err
}

func ValidateProfile(name string, p Profile) error {
	if err := validateName(name); err != nil {
		return ValidationError{Profile: name, Problem: err.Error()}
	}
	if len(p.Description) > 300 || !utf8.ValidString(p.Description) || strings.ContainsRune(p.Description, '\x00') {
		return ValidationError{Profile: name, Field: "description", Problem: "must be valid UTF-8 with at most 300 bytes and no NUL"}
	}
	if p.Extends != "" {
		if err := validateName(p.Extends); err != nil {
			return ValidationError{Profile: name, Field: "extends", Value: p.Extends, Problem: err.Error()}
		}
		if p.Extends == name {
			return ValidationError{Profile: name, Field: "extends", Value: p.Extends, Problem: "cannot extend itself"}
		}
	}
	for _, entry := range []struct{ field, value string }{
		{"agent", p.Agent}, {"approval_mode", p.ApprovalMode},
	} {
		if err := validateEnum(entry.field, entry.value, true); err != nil {
			return ValidationError{Profile: name, Field: entry.field, Value: entry.value, Problem: err.Error()}
		}
	}
	if p.Model != "" && !modelPattern.MatchString(p.Model) {
		return ValidationError{Profile: name, Field: "model", Value: p.Model, Problem: "contains unsupported characters or is too long"}
	}
	for _, entry := range []struct {
		field string
		value *string
	}{
		{"codex.approvals_reviewer", p.Codex.ApprovalsReviewer},
		{"codex.sandbox_mode", p.Codex.SandboxMode},
		{"codex.reasoning_effort", p.Codex.ReasoningEffort},
		{"codex.reasoning_summary", p.Codex.ReasoningSummary},
		{"codex.web_search", p.Codex.WebSearch},
		{"codex.service_tier", p.Codex.ServiceTier},
		{"codex.history_persistence", p.Codex.HistoryPersistence},
		{"claude.permission_mode", p.Claude.PermissionMode},
		{"claude.effort", p.Claude.Effort},
	} {
		if entry.value != nil {
			if err := validateEnum(entry.field, *entry.value, false); err != nil {
				return ValidationError{Profile: name, Field: entry.field, Value: *entry.value, Problem: err.Error()}
			}
		}
	}
	if p.Codex.HistoryMaxBytes != nil && (*p.Codex.HistoryMaxBytes < 0 || *p.Codex.HistoryMaxBytes > 1<<40) {
		return ValidationError{Profile: name, Field: "codex.history_max_bytes", Value: fmt.Sprint(*p.Codex.HistoryMaxBytes), Problem: "must be between 0 and 1 TiB"}
	}
	if p.Codex.FileOpener != nil {
		if err := validateEnum("codex.file_opener", *p.Codex.FileOpener, false); err != nil {
			return ValidationError{Profile: name, Field: "codex.file_opener", Value: *p.Codex.FileOpener, Problem: err.Error()}
		}
	}
	if p.Claude.FallbackModel != nil && *p.Claude.FallbackModel != "" && !modelPattern.MatchString(*p.Claude.FallbackModel) {
		return ValidationError{Profile: name, Field: "claude.fallback_model", Value: *p.Claude.FallbackModel, Problem: "contains unsupported characters or is too long"}
	}
	return nil
}

func validateName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("must match %s", namePattern.String())
	}
	return nil
}

func validateEnum(field, value string, empty bool) error {
	if value == "" && empty {
		return nil
	}
	set := allowed[field]
	if _, ok := set[value]; ok {
		return nil
	}
	choices := make([]string, 0, len(set))
	for choice := range set {
		if choice != "" {
			choices = append(choices, choice)
		}
	}
	sort.Strings(choices)
	return fmt.Errorf("must be one of %s", strings.Join(choices, ", "))
}

func mergedProfiles(file File) map[string]Profile {
	profiles := BuiltIns()
	for name, profile := range file.Profiles {
		profiles[name] = cloneProfile(profile)
	}
	return profiles
}

func Warnings(e Effective) []string {
	var out []string
	if e.ApprovalMode == "danger" {
		out = append(out, "agent approvals and the agent sandbox are bypassed")
	}
	if e.ApprovalMode == "full-auto" {
		out = append(out, "interactive agent approvals are disabled")
	}
	if e.Codex.SandboxMode == "danger-full-access" {
		out = append(out, "Codex inner sandbox allows unrestricted guest access")
	}
	if e.Claude.PermissionMode == "bypassPermissions" {
		out = append(out, "Claude permission checks are bypassed")
	}
	if e.Codex.WebSearch == "live" {
		out = append(out, "Codex may perform live web searches when network policy permits")
	}
	if e.Codex.AllowLoginShell {
		out = append(out, "Codex may run login shells")
	}
	return out
}
