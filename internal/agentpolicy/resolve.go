package agentpolicy

import (
	"fmt"
	"sort"
)

func Resolve(file File, name string) (Effective, error) {
	name = cleanName(name)
	if name == "" {
		name = cleanName(file.Default)
	}
	if name == "" {
		name = "interactive"
	}
	if err := ValidateFileShallow(file); err != nil {
		return Effective{}, err
	}
	profiles := mergedProfiles(file)
	if _, ok := profiles[name]; !ok {
		return Effective{}, fmt.Errorf("agent policy %q does not exist", name)
	}
	chain, err := inheritanceChain(profiles, name)
	if err != nil {
		return Effective{}, err
	}
	effective := Effective{Name: name, Chain: append([]string(nil), chain...), Sources: map[string]string{}}
	for _, source := range chain {
		apply(&effective, profiles[source], source)
	}
	return effective, nil
}

func ResolveAll(file File) (map[string]Effective, error) {
	if err := ValidateFileShallow(file); err != nil {
		return nil, err
	}
	profiles := mergedProfiles(file)
	out := make(map[string]Effective, len(profiles))
	for _, name := range profileNames(profiles) {
		effective, err := ResolveWithoutValidation(file, name)
		if err != nil {
			return nil, err
		}
		out[name] = effective
	}
	return out, nil
}

func ResolveWithoutValidation(file File, name string) (Effective, error) {
	profiles := mergedProfiles(file)
	chain, err := inheritanceChain(profiles, name)
	if err != nil {
		return Effective{}, err
	}
	effective := Effective{Name: name, Chain: append([]string(nil), chain...), Sources: map[string]string{}}
	for _, source := range chain {
		apply(&effective, profiles[source], source)
	}
	return effective, nil
}

func ValidateFileShallow(file File) error {
	if file.Version != 0 && file.Version != CurrentVersion {
		return ValidationError{Field: "version", Value: fmt.Sprint(file.Version), Problem: fmt.Sprintf("is unsupported; expected %d", CurrentVersion)}
	}
	for _, name := range profileNames(file.Profiles) {
		if err := ValidateProfile(name, file.Profiles[name]); err != nil {
			return err
		}
	}
	return nil
}

func inheritanceChain(profiles map[string]Profile, name string) ([]string, error) {
	var reverse []string
	seen := map[string]int{}
	current := name
	for current != "" {
		profile, ok := profiles[current]
		if !ok {
			return nil, fmt.Errorf("agent policy %q extends missing policy %q", name, current)
		}
		if at, found := seen[current]; found {
			cycle := append(append([]string(nil), reverse[at:]...), current)
			return nil, fmt.Errorf("agent policy inheritance cycle: %v", cycle)
		}
		seen[current] = len(reverse)
		reverse = append(reverse, current)
		current = cleanName(profile.Extends)
	}
	chain := make([]string, len(reverse))
	for i := range reverse {
		chain[len(reverse)-1-i] = reverse[i]
	}
	return chain, nil
}

func apply(e *Effective, p Profile, source string) {
	setString := func(path, value string, target *string) {
		if value != "" {
			*target = value
			e.Sources[path] = source
		}
	}
	setPointerString := func(path string, value *string, target *string) {
		if value != nil {
			*target = *value
			e.Sources[path] = source
		}
	}
	setPointerBool := func(path string, value *bool, target *bool) {
		if value != nil {
			*target = *value
			e.Sources[path] = source
		}
	}
	setString("description", p.Description, &e.Description)
	setString("agent", p.Agent, &e.Agent)
	setString("model", p.Model, &e.Model)
	setString("approval_mode", p.ApprovalMode, &e.ApprovalMode)
	setPointerString("codex.approvals_reviewer", p.Codex.ApprovalsReviewer, &e.Codex.ApprovalsReviewer)
	setPointerString("codex.sandbox_mode", p.Codex.SandboxMode, &e.Codex.SandboxMode)
	setPointerString("codex.reasoning_effort", p.Codex.ReasoningEffort, &e.Codex.ReasoningEffort)
	setPointerString("codex.reasoning_summary", p.Codex.ReasoningSummary, &e.Codex.ReasoningSummary)
	setPointerString("codex.web_search", p.Codex.WebSearch, &e.Codex.WebSearch)
	setPointerString("codex.service_tier", p.Codex.ServiceTier, &e.Codex.ServiceTier)
	setPointerString("codex.history_persistence", p.Codex.HistoryPersistence, &e.Codex.HistoryPersistence)
	if p.Codex.HistoryMaxBytes != nil {
		e.Codex.HistoryMaxBytes = *p.Codex.HistoryMaxBytes
		e.Sources["codex.history_max_bytes"] = source
	}
	setPointerString("codex.file_opener", p.Codex.FileOpener, &e.Codex.FileOpener)
	setPointerBool("codex.hide_agent_reasoning", p.Codex.HideAgentReasoning, &e.Codex.HideAgentReasoning)
	setPointerBool("codex.allow_login_shell", p.Codex.AllowLoginShell, &e.Codex.AllowLoginShell)
	setPointerString("claude.permission_mode", p.Claude.PermissionMode, &e.Claude.PermissionMode)
	setPointerString("claude.effort", p.Claude.Effort, &e.Claude.Effort)
	setPointerString("claude.fallback_model", p.Claude.FallbackModel, &e.Claude.FallbackModel)
	setPointerBool("claude.bare", p.Claude.Bare, &e.Claude.Bare)
	setPointerBool("claude.safe_mode", p.Claude.SafeMode, &e.Claude.SafeMode)
	setPointerBool("claude.restricted", p.Claude.Restricted, &e.Claude.Restricted)
	setPointerBool("claude.no_session_persistence", p.Claude.NoSessionPersistence, &e.Claude.NoSessionPersistence)
}

func List(file File) ([]Summary, error) {
	all, err := ResolveAll(file)
	if err != nil {
		return nil, err
	}
	builtins := BuiltIns()
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)
	defaultName := file.Default
	if defaultName == "" {
		defaultName = "interactive"
	}
	out := make([]Summary, 0, len(names))
	profiles := mergedProfiles(file)
	for _, name := range names {
		e := all[name]
		_, builtin := builtins[name]
		out = append(out, Summary{Name: name, Description: e.Description, BuiltIn: builtin, Default: name == defaultName, Extends: profiles[name].Extends, Agent: e.Agent, Warnings: Warnings(e)})
	}
	return out, nil
}
