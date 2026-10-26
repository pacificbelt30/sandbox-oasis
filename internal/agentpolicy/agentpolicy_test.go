package agentpolicy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltInsResolveAndValidate(t *testing.T) {
	file := Empty()
	if err := ValidateFile(file); err != nil {
		t.Fatal(err)
	}
	rows, err := List(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(BuiltIns()) {
		t.Fatalf("got %d built-ins; want %d", len(rows), len(BuiltIns()))
	}
	approve, err := Resolve(file, "approve-for-me")
	if err != nil {
		t.Fatal(err)
	}
	if approve.Codex.ApprovalsReviewer != "auto_review" {
		t.Fatalf("reviewer = %q", approve.Codex.ApprovalsReviewer)
	}
	if approve.Claude.PermissionMode != "auto" {
		t.Fatalf("Claude mode = %q", approve.Claude.PermissionMode)
	}
	if approve.ApprovalMode != "suggest" {
		t.Fatalf("portable approval mode = %q", approve.ApprovalMode)
	}
	private, err := Resolve(file, "private")
	if err != nil {
		t.Fatal(err)
	}
	if private.Codex.HistoryPersistence != "none" || !private.Claude.NoSessionPersistence {
		t.Fatalf("private policy = %#v", private)
	}
}

func TestResolveInheritanceSourcesAndCycle(t *testing.T) {
	file := Empty()
	file.Profiles["parent"] = Profile{Extends: "interactive", Model: "model-a", Codex: CodexSettings{ReasoningEffort: String("high")}}
	file.Profiles["child"] = Profile{Extends: "parent", Model: "model-b", Codex: CodexSettings{WebSearch: String("disabled")}}
	effective, err := Resolve(file, "child")
	if err != nil {
		t.Fatal(err)
	}
	if effective.Model != "model-b" || effective.Sources["model"] != "child" {
		t.Fatalf("model resolution = %#v", effective)
	}
	if effective.Codex.ReasoningEffort != "high" || effective.Sources["codex.reasoning_effort"] != "parent" {
		t.Fatalf("effort resolution = %#v", effective)
	}
	if got := strings.Join(effective.Chain, "/"); got != "interactive/parent/child" {
		t.Fatalf("chain = %s", got)
	}
	file.Profiles["parent"] = Profile{Extends: "child"}
	if _, err := Resolve(file, "child"); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle error = %v", err)
	}
}

func TestStrictDecodeAndValidation(t *testing.T) {
	if _, err := Decode([]byte("version=1\nunknown=true\n")); err == nil {
		t.Fatal("unknown field accepted")
	}
	file := Empty()
	file.Profiles["bad"] = Profile{Extends: "interactive", Codex: CodexSettings{ApprovalsReviewer: String("robot")}}
	if err := ValidateFile(file); err == nil || !strings.Contains(err.Error(), "auto_review") {
		t.Fatalf("validation error = %v", err)
	}
	file.Profiles["bad"] = Profile{Extends: "missing"}
	if err := ValidateFile(file); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing parent error = %v", err)
	}
}

func TestStoreLifecycleAndPermissions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "policies.toml")
	store, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Initialize(false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
	if err := store.Clone("approve-for-me", "team-auto"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("team-auto", "model", "gpt-test"); err != nil {
		t.Fatal(err)
	}
	if err := store.Set("team-auto", "codex.web_search", "disabled"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDefault("team-auto"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	effective, err := Resolve(snapshot.File, "")
	if err != nil {
		t.Fatal(err)
	}
	if effective.Name != "team-auto" || effective.Model != "gpt-test" || effective.Codex.WebSearch != "disabled" {
		t.Fatalf("effective = %#v", effective)
	}
	if err := store.Unset("team-auto", "model"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDefault("interactive"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete("team-auto"); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentAndArgumentsAreStructured(t *testing.T) {
	effective, err := Resolve(Empty(), "approve-for-me")
	if err != nil {
		t.Fatal(err)
	}
	env, err := effective.Environment()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, `"approvals_reviewer":"auto_review"`) {
		t.Fatalf("environment = %s", joined)
	}
	args := effective.ClaudeArgs()
	if got := strings.Join(args, " "); got != "--permission-mode auto" {
		t.Fatalf("Claude args = %q", got)
	}
	if strings.Contains(joined, "\napproval_policy") {
		t.Fatal("configuration injection present")
	}
}

func TestEditDiffAuditDoctorAndCatalog(t *testing.T) {
	file := Empty()
	file.Profiles["team"] = Profile{Extends: "approve-for-me", Description: "team", Codex: CodexSettings{WebSearch: String("live")}}
	left, err := Resolve(file, "interactive")
	if err != nil {
		t.Fatal(err)
	}
	right, err := Resolve(file, "team")
	if err != nil {
		t.Fatal(err)
	}
	diff := Diff(left, right)
	if len(diff) == 0 {
		t.Fatal("no differences")
	}
	audit := Audit(right)
	if audit.Grade == "A" || len(audit.Findings) == 0 {
		t.Fatalf("audit = %#v", audit)
	}
	doctor := Doctor(file)
	if !doctor.Valid || doctor.HostProfiles != 1 {
		t.Fatalf("doctor = %#v", doctor)
	}
	definition, ok := FindField("codex.history.persistence")
	if !ok || definition.Path != "codex.history_persistence" {
		t.Fatalf("definition = %#v, %v", definition, ok)
	}
	updated, err := SetField(file.Profiles["team"], "claude.restricted", "true")
	if err != nil || updated.Claude.Restricted == nil || !*updated.Claude.Restricted {
		t.Fatalf("updated = %#v err=%v", updated, err)
	}
	updated, err = UnsetField(updated, "claude.restricted")
	if err != nil || updated.Claude.Restricted != nil {
		t.Fatalf("unset = %#v err=%v", updated, err)
	}
}
