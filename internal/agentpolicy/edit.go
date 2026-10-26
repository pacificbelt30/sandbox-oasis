package agentpolicy

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type Difference struct {
	Path        string `json:"path"`
	Left        string `json:"left,omitempty"`
	Right       string `json:"right,omitempty"`
	LeftSource  string `json:"left_source,omitempty"`
	RightSource string `json:"right_source,omitempty"`
	RiskChanged bool   `json:"risk_changed"`
}

func Diff(left, right Effective) []Difference {
	leftFields := indexFields(left.Fields())
	rightFields := indexFields(right.Fields())
	paths := map[string]struct{}{}
	for path := range leftFields {
		paths[path] = struct{}{}
	}
	for path := range rightFields {
		paths[path] = struct{}{}
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	differences := make([]Difference, 0, len(ordered))
	for _, path := range ordered {
		l, r := leftFields[path], rightFields[path]
		if l.Value == r.Value && l.Source == r.Source {
			continue
		}
		differences = append(differences, Difference{Path: path, Left: l.Value, Right: r.Value, LeftSource: l.Source, RightSource: r.Source, RiskChanged: l.Risk != r.Risk})
	}
	return differences
}

func indexFields(fields []Field) map[string]Field {
	out := make(map[string]Field, len(fields))
	for _, field := range fields {
		out[field.Path] = field
	}
	return out
}

func Clone(file File, source, destination string) (File, error) {
	source, destination = cleanName(source), cleanName(destination)
	if err := validateName(destination); err != nil {
		return File{}, ValidationError{Profile: destination, Problem: err.Error()}
	}
	if _, builtin := BuiltIns()[destination]; builtin {
		return File{}, fmt.Errorf("built-in agent policy %q cannot be replaced", destination)
	}
	profiles := mergedProfiles(file)
	if _, ok := profiles[source]; !ok {
		return File{}, fmt.Errorf("agent policy %q does not exist", source)
	}
	if _, exists := file.Profiles[destination]; exists {
		return File{}, fmt.Errorf("agent policy %q already exists", destination)
	}
	out := cloneFile(file)
	if out.Profiles == nil {
		out.Profiles = map[string]Profile{}
	}
	out.Profiles[destination] = Profile{Description: "Derived from " + source + ".", Extends: source}
	if err := ValidateFile(out); err != nil {
		return File{}, err
	}
	return out, nil
}

func SetField(profile Profile, path, raw string) (Profile, error) {
	path = strings.ToLower(strings.TrimSpace(path))
	raw = strings.TrimSpace(raw)
	stringPointer := func(target **string) { *target = String(raw) }
	boolPointer := func(target **bool) error {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("%s requires true or false", path)
		}
		*target = Bool(value)
		return nil
	}
	switch path {
	case "description":
		profile.Description = raw
	case "extends":
		profile.Extends = cleanName(raw)
	case "agent":
		profile.Agent = raw
	case "model":
		profile.Model = raw
	case "approval_mode", "approval-mode":
		profile.ApprovalMode = raw
	case "codex.approvals_reviewer":
		stringPointer(&profile.Codex.ApprovalsReviewer)
	case "codex.sandbox_mode":
		stringPointer(&profile.Codex.SandboxMode)
	case "codex.reasoning_effort", "codex.model_reasoning_effort":
		stringPointer(&profile.Codex.ReasoningEffort)
	case "codex.reasoning_summary", "codex.model_reasoning_summary":
		stringPointer(&profile.Codex.ReasoningSummary)
	case "codex.web_search":
		stringPointer(&profile.Codex.WebSearch)
	case "codex.service_tier":
		stringPointer(&profile.Codex.ServiceTier)
	case "codex.history_persistence", "codex.history.persistence":
		stringPointer(&profile.Codex.HistoryPersistence)
	case "codex.history_max_bytes", "codex.history.max_bytes":
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return Profile{}, fmt.Errorf("%s requires an integer", path)
		}
		profile.Codex.HistoryMaxBytes = Int64(value)
	case "codex.file_opener":
		stringPointer(&profile.Codex.FileOpener)
	case "codex.hide_agent_reasoning":
		if err := boolPointer(&profile.Codex.HideAgentReasoning); err != nil {
			return Profile{}, err
		}
	case "codex.allow_login_shell":
		if err := boolPointer(&profile.Codex.AllowLoginShell); err != nil {
			return Profile{}, err
		}
	case "claude.permission_mode":
		stringPointer(&profile.Claude.PermissionMode)
	case "claude.effort":
		stringPointer(&profile.Claude.Effort)
	case "claude.fallback_model":
		stringPointer(&profile.Claude.FallbackModel)
	case "claude.bare":
		if err := boolPointer(&profile.Claude.Bare); err != nil {
			return Profile{}, err
		}
	case "claude.safe_mode":
		if err := boolPointer(&profile.Claude.SafeMode); err != nil {
			return Profile{}, err
		}
	case "claude.restricted":
		if err := boolPointer(&profile.Claude.Restricted); err != nil {
			return Profile{}, err
		}
	case "claude.no_session_persistence":
		if err := boolPointer(&profile.Claude.NoSessionPersistence); err != nil {
			return Profile{}, err
		}
	default:
		return Profile{}, fmt.Errorf("unsupported agent policy field %q", path)
	}
	return profile, nil
}

func UnsetField(profile Profile, path string) (Profile, error) {
	path = strings.ToLower(strings.TrimSpace(path))
	switch path {
	case "description":
		profile.Description = ""
	case "extends":
		profile.Extends = ""
	case "agent":
		profile.Agent = ""
	case "model":
		profile.Model = ""
	case "approval_mode", "approval-mode":
		profile.ApprovalMode = ""
	case "codex.approvals_reviewer":
		profile.Codex.ApprovalsReviewer = nil
	case "codex.sandbox_mode":
		profile.Codex.SandboxMode = nil
	case "codex.reasoning_effort", "codex.model_reasoning_effort":
		profile.Codex.ReasoningEffort = nil
	case "codex.reasoning_summary", "codex.model_reasoning_summary":
		profile.Codex.ReasoningSummary = nil
	case "codex.web_search":
		profile.Codex.WebSearch = nil
	case "codex.service_tier":
		profile.Codex.ServiceTier = nil
	case "codex.history_persistence", "codex.history.persistence":
		profile.Codex.HistoryPersistence = nil
	case "codex.history_max_bytes", "codex.history.max_bytes":
		profile.Codex.HistoryMaxBytes = nil
	case "codex.file_opener":
		profile.Codex.FileOpener = nil
	case "codex.hide_agent_reasoning":
		profile.Codex.HideAgentReasoning = nil
	case "codex.allow_login_shell":
		profile.Codex.AllowLoginShell = nil
	case "claude.permission_mode":
		profile.Claude.PermissionMode = nil
	case "claude.effort":
		profile.Claude.Effort = nil
	case "claude.fallback_model":
		profile.Claude.FallbackModel = nil
	case "claude.bare":
		profile.Claude.Bare = nil
	case "claude.safe_mode":
		profile.Claude.SafeMode = nil
	case "claude.restricted":
		profile.Claude.Restricted = nil
	case "claude.no_session_persistence":
		profile.Claude.NoSessionPersistence = nil
	default:
		return Profile{}, fmt.Errorf("unsupported agent policy field %q", path)
	}
	return profile, nil
}

func (s *Store) Clone(source, destination string) error {
	return s.Update(func(file *File) error {
		updated, err := Clone(*file, source, destination)
		if err == nil {
			*file = updated
		}
		return err
	})
}

func (s *Store) Set(name, path, value string) error {
	name = cleanName(name)
	if _, builtin := BuiltIns()[name]; builtin {
		return fmt.Errorf("clone built-in policy %q before editing it", name)
	}
	return s.Update(func(file *File) error {
		profile, ok := file.Profiles[name]
		if !ok {
			return fmt.Errorf("agent policy %q does not exist", name)
		}
		updated, err := SetField(profile, path, value)
		if err != nil {
			return err
		}
		if err := ValidateProfile(name, updated); err != nil {
			return err
		}
		file.Profiles[name] = updated
		return nil
	})
}

func (s *Store) Unset(name, path string) error {
	name = cleanName(name)
	if _, builtin := BuiltIns()[name]; builtin {
		return fmt.Errorf("clone built-in policy %q before editing it", name)
	}
	return s.Update(func(file *File) error {
		profile, ok := file.Profiles[name]
		if !ok {
			return fmt.Errorf("agent policy %q does not exist", name)
		}
		updated, err := UnsetField(profile, path)
		if err != nil {
			return err
		}
		if err := ValidateProfile(name, updated); err != nil {
			return err
		}
		file.Profiles[name] = updated
		return nil
	})
}
