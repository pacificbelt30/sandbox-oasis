package agentpolicy

import (
	"fmt"
	"sort"
	"strings"
)

type Diagnostic struct {
	Code       string   `json:"code"`
	Level      Severity `json:"level"`
	Profile    string   `json:"profile,omitempty"`
	Field      string   `json:"field,omitempty"`
	Message    string   `json:"message"`
	Suggestion string   `json:"suggestion,omitempty"`
}

type DoctorReport struct {
	Valid        bool         `json:"valid"`
	Default      string       `json:"default"`
	BuiltIns     int          `json:"built_ins"`
	HostProfiles int          `json:"host_profiles"`
	MaximumDepth int          `json:"maximum_depth"`
	Diagnostics  []Diagnostic `json:"diagnostics"`
}

func Doctor(file File) DoctorReport {
	report := DoctorReport{Valid: true, Default: file.Default, BuiltIns: len(BuiltIns()), HostProfiles: len(file.Profiles)}
	if report.Default == "" {
		report.Default = "interactive"
	}
	if err := ValidateFile(file); err != nil {
		report.Valid = false
		report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "FILE_INVALID", Level: SeverityCritical, Message: err.Error(), Suggestion: "Fix the reported field before selecting this policy file."})
		return report
	}
	profiles := mergedProfiles(file)
	children := map[string][]string{}
	for name, profile := range profiles {
		if profile.Extends != "" {
			children[profile.Extends] = append(children[profile.Extends], name)
		}
		chain, _ := inheritanceChain(profiles, name)
		if len(chain) > report.MaximumDepth {
			report.MaximumDepth = len(chain)
		}
		if len(chain) > 6 {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "DEEP_CHAIN", Level: SeverityLow, Profile: name, Message: fmt.Sprintf("inheritance chain has %d profiles", len(chain)), Suggestion: "Flatten deeply layered policies when their effective values become hard to review."})
		}
	}
	for _, names := range children {
		sort.Strings(names)
	}
	for _, name := range profileNames(file.Profiles) {
		profile := file.Profiles[name]
		if name != report.Default && len(children[name]) == 0 {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "LEAF_PROFILE", Level: SeverityInfo, Profile: name, Message: "host profile is neither the default nor a parent", Suggestion: "Keep it if selected explicitly; otherwise consider removing stale policies."})
		}
		for _, shadow := range shadowedValues(profile, profiles) {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "REDUNDANT_OVERRIDE", Level: SeverityInfo, Profile: name, Field: shadow, Message: "value is identical to the inherited value", Suggestion: "Unset this field to make inheritance intent clearer."})
		}
		if strings.TrimSpace(profile.Description) == "" {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{Code: "MISSING_DESCRIPTION", Level: SeverityLow, Profile: name, Field: "description", Message: "host policy has no description", Suggestion: "Describe when operators should choose this policy."})
		}
	}
	sort.SliceStable(report.Diagnostics, func(i, j int) bool {
		left, right := severityRank(report.Diagnostics[i].Level), severityRank(report.Diagnostics[j].Level)
		if left != right {
			return left > right
		}
		if report.Diagnostics[i].Profile != report.Diagnostics[j].Profile {
			return report.Diagnostics[i].Profile < report.Diagnostics[j].Profile
		}
		return report.Diagnostics[i].Code < report.Diagnostics[j].Code
	})
	return report
}

func shadowedValues(profile Profile, profiles map[string]Profile) []string {
	if profile.Extends == "" {
		return nil
	}
	parentFile := Empty()
	for name, candidate := range profiles {
		if _, builtin := BuiltIns()[name]; !builtin {
			parentFile.Profiles[name] = candidate
		}
	}
	parent, err := ResolveWithoutValidation(parentFile, profile.Extends)
	if err != nil {
		return nil
	}
	probe := Effective{Sources: map[string]string{}}
	apply(&probe, profile, "local")
	parentFields, localFields := indexFields(parent.Fields()), indexFields(probe.Fields())
	var shadowed []string
	for path, local := range localFields {
		if inherited, ok := parentFields[path]; ok && inherited.Value == local.Value {
			shadowed = append(shadowed, path)
		}
	}
	sort.Strings(shadowed)
	return shadowed
}

func (r DoctorReport) String() string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "valid=%t default=%s built-ins=%d host=%d max-depth=%d\n", r.Valid, r.Default, r.BuiltIns, r.HostProfiles, r.MaximumDepth)
	if len(r.Diagnostics) == 0 {
		builder.WriteString("No policy maintenance issues found.")
		return builder.String()
	}
	for _, diagnostic := range r.Diagnostics {
		location := diagnostic.Profile
		if diagnostic.Field != "" {
			location += "." + diagnostic.Field
		}
		if location == "" {
			location = "file"
		}
		fmt.Fprintf(&builder, "[%s] %s %s: %s\n", strings.ToUpper(string(diagnostic.Level)), diagnostic.Code, location, diagnostic.Message)
	}
	return strings.TrimRight(builder.String(), "\n")
}
