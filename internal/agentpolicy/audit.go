package agentpolicy

import (
	"fmt"
	"sort"
	"strings"
)

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityLow      Severity = "low"
	SeverityMedium   Severity = "medium"
	SeverityHigh     Severity = "high"
	SeverityCritical Severity = "critical"
)

type AuditFinding struct {
	Code           string   `json:"code"`
	Severity       Severity `json:"severity"`
	Title          string   `json:"title"`
	Detail         string   `json:"detail"`
	Recommendation string   `json:"recommendation,omitempty"`
	Fields         []string `json:"fields,omitempty"`
}

type AuditReport struct {
	Policy       string           `json:"policy"`
	Grade        string           `json:"grade"`
	Score        int              `json:"score"`
	Boundary     string           `json:"boundary"`
	Findings     []AuditFinding   `json:"findings"`
	Capabilities CapabilityMatrix `json:"capabilities"`
	Execution    ExecutionPlan    `json:"execution"`
}

type CapabilityMatrix struct {
	InteractiveApprovals bool `json:"interactive_approvals"`
	AutomatedReview      bool `json:"automated_review"`
	AgentSandbox         bool `json:"agent_sandbox"`
	PersistentHistory    bool `json:"persistent_history"`
	LiveWebSearch        bool `json:"live_web_search"`
	LoginShell           bool `json:"login_shell"`
	ClaudeCodeExecution  bool `json:"claude_code_execution"`
	ClaudeWebFetch       bool `json:"claude_web_fetch"`
}

type ExecutionPlan struct {
	Profile      string      `json:"profile"`
	Agent        string      `json:"agent,omitempty"`
	Model        string      `json:"model,omitempty"`
	ApprovalMode string      `json:"approval_mode"`
	CodexConfig  CodexConfig `json:"codex_config"`
	ClaudeArgs   []string    `json:"claude_args"`
	Precedence   []string    `json:"precedence"`
	Notes        []string    `json:"notes,omitempty"`
}

func Audit(e Effective) AuditReport {
	report := AuditReport{
		Policy:       e.Name,
		Score:        100,
		Boundary:     "Oaiso worker isolation and egress policy remain authoritative.",
		Capabilities: capabilities(e),
		Execution:    Plan(e),
	}
	add := func(finding AuditFinding, deduction int) {
		report.Findings = append(report.Findings, finding)
		report.Score -= deduction
	}
	switch e.ApprovalMode {
	case "danger":
		add(AuditFinding{Code: "AP001", Severity: SeverityCritical, Title: "Agent safeguards bypassed", Detail: "The portable danger mode bypasses both approval prompts and the Codex inner sandbox.", Recommendation: "Prefer approve-for-me or autonomous inside Kata unless a specific workflow requires danger mode.", Fields: []string{"approval_mode"}}, 40)
	case "full-auto":
		add(AuditFinding{Code: "AP002", Severity: SeverityHigh, Title: "Interactive approval disabled", Detail: "The selected agent proceeds without operator approval.", Recommendation: "Use approve-for-me when automatic review is sufficient.", Fields: []string{"approval_mode"}}, 22)
	case "auto-edit":
		add(AuditFinding{Code: "AP003", Severity: SeverityLow, Title: "Edits may proceed automatically", Detail: "Routine edits can proceed while higher-risk actions retain agent-specific review behavior.", Fields: []string{"approval_mode"}}, 4)
	}
	if e.Codex.ApprovalsReviewer == "auto_review" {
		add(AuditFinding{Code: "CX001", Severity: SeverityMedium, Title: "Approval decisions delegated", Detail: "Codex uses its reviewer instead of waiting for the user for supported approval requests.", Recommendation: "Keep Kata and restrictive egress enabled; auto review does not replace either boundary.", Fields: []string{"codex.approvals_reviewer"}}, 8)
	}
	if e.Codex.SandboxMode == "danger-full-access" {
		severity, deduction := SeverityHigh, 20
		if e.ApprovalMode == "danger" {
			severity, deduction = SeverityCritical, 28
		}
		add(AuditFinding{Code: "CX002", Severity: severity, Title: "Codex inner sandbox unrestricted", Detail: "Codex can access the entire guest filesystem allowed by the worker.", Recommendation: "Rely on this only in verified Kata workers; otherwise use workspace-write or read-only.", Fields: []string{"codex.sandbox_mode"}}, deduction)
	}
	if e.Codex.WebSearch == "live" {
		add(AuditFinding{Code: "CX003", Severity: SeverityMedium, Title: "Live web search enabled", Detail: "Codex may query the live web when the worker egress policy permits it.", Recommendation: "Keep the per-worker domain allowlist narrow.", Fields: []string{"codex.web_search"}}, 7)
	}
	if e.Codex.AllowLoginShell {
		add(AuditFinding{Code: "CX004", Severity: SeverityMedium, Title: "Login shells allowed", Detail: "Login startup files can influence commands launched by Codex.", Recommendation: "Disable login shells unless the toolchain depends on profile initialization.", Fields: []string{"codex.allow_login_shell"}}, 6)
	}
	if e.Codex.HistoryPersistence != "none" {
		add(AuditFinding{Code: "CX005", Severity: SeverityInfo, Title: "Codex history persists in the guest", Detail: "Conversation history remains in the worker filesystem until the worker is removed.", Recommendation: "Use the private profile for sensitive one-off work.", Fields: []string{"codex.history_persistence"}}, 1)
	}
	switch e.Claude.PermissionMode {
	case "bypassPermissions":
		add(AuditFinding{Code: "CL001", Severity: SeverityCritical, Title: "Claude permissions bypassed", Detail: "Claude Code does not enforce its permission checks.", Recommendation: "Prefer auto, acceptEdits, or dontAsk according to the workflow.", Fields: []string{"claude.permission_mode"}}, 35)
	case "dontAsk":
		add(AuditFinding{Code: "CL002", Severity: SeverityHigh, Title: "Claude will not prompt", Detail: "Operations needing a prompt are handled without an interactive question.", Recommendation: "Use auto when unattended review behavior is desired.", Fields: []string{"claude.permission_mode"}}, 18)
	case "auto":
		add(AuditFinding{Code: "CL003", Severity: SeverityMedium, Title: "Claude chooses permission decisions", Detail: "Claude's automatic permission mode handles supported decisions.", Fields: []string{"claude.permission_mode"}}, 7)
	}
	if e.Claude.Bare && e.Claude.SafeMode {
		add(AuditFinding{Code: "CL004", Severity: SeverityLow, Title: "Overlapping Claude isolation modes", Detail: "Both bare and safe mode are enabled; this is valid but usually redundant.", Recommendation: "Keep only the mode whose customization boundary you need.", Fields: []string{"claude.bare", "claude.safe_mode"}}, 2)
	}
	if e.Claude.Restricted && e.Claude.PermissionMode == "bypassPermissions" {
		add(AuditFinding{Code: "CL005", Severity: SeverityMedium, Title: "Conflicting Claude intent", Detail: "Restricted tools and bypassed permission checks express opposing safety intent.", Recommendation: "Use plan or manual permission mode with restricted tools.", Fields: []string{"claude.restricted", "claude.permission_mode"}}, 8)
	}
	if e.Agent == "shell" && (e.Model != "" || e.Codex.ApprovalsReviewer != "" || e.Claude.PermissionMode != "") {
		add(AuditFinding{Code: "GN001", Severity: SeverityInfo, Title: "Agent defaults are dormant for shell", Detail: "The profile selects a shell, so model and agent-specific values apply only when an agent is selected explicitly.", Fields: []string{"agent"}}, 0)
	}
	if report.Score < 0 {
		report.Score = 0
	}
	report.Grade = grade(report.Score)
	sort.SliceStable(report.Findings, func(i, j int) bool {
		left, right := severityRank(report.Findings[i].Severity), severityRank(report.Findings[j].Severity)
		if left != right {
			return left > right
		}
		return report.Findings[i].Code < report.Findings[j].Code
	})
	return report
}

func capabilities(e Effective) CapabilityMatrix {
	return CapabilityMatrix{
		InteractiveApprovals: e.ApprovalMode == "suggest" || e.Codex.ApprovalsReviewer == "user" || e.Claude.PermissionMode == "manual",
		AutomatedReview:      e.Codex.ApprovalsReviewer == "auto_review" || e.Claude.PermissionMode == "auto",
		AgentSandbox:         e.Codex.SandboxMode != "danger-full-access" && e.ApprovalMode != "danger",
		PersistentHistory:    e.Codex.HistoryPersistence != "none" || !e.Claude.NoSessionPersistence,
		LiveWebSearch:        e.Codex.WebSearch == "live",
		LoginShell:           e.Codex.AllowLoginShell,
		ClaudeCodeExecution:  !e.Claude.Restricted,
		ClaudeWebFetch:       !e.Claude.Restricted,
	}
}

func Plan(e Effective) ExecutionPlan {
	plan := ExecutionPlan{
		Profile:      e.Name,
		Agent:        e.Agent,
		Model:        e.Model,
		ApprovalMode: e.ApprovalMode,
		CodexConfig:  e.CodexConfig(),
		ClaudeArgs:   e.ClaudeArgs(),
		Precedence:   []string{"native agent arguments", "oaiso command-line flags", "repository oaiso.toml portable settings", "global config.toml", "host agent policy", "built-in defaults"},
	}
	if e.Codex.ApprovalsReviewer == "auto_review" {
		plan.Notes = append(plan.Notes, "Codex approval review is automatic; Oaiso sandboxing and egress controls are unchanged.")
	}
	if e.Codex.SandboxMode == "danger-full-access" {
		plan.Notes = append(plan.Notes, "Verified Kata launches already use danger-full-access inside the guest because Kata is the isolation boundary.")
	}
	if e.Codex.WebSearch != "" {
		plan.Notes = append(plan.Notes, "Agent web-search configuration cannot grant network destinations denied by Oaiso.")
	}
	return plan
}

func grade(score int) string {
	switch {
	case score >= 95:
		return "A"
	case score >= 85:
		return "B"
	case score >= 70:
		return "C"
	case score >= 50:
		return "D"
	default:
		return "F"
	}
}

func severityRank(severity Severity) int {
	switch severity {
	case SeverityCritical:
		return 5
	case SeverityHigh:
		return 4
	case SeverityMedium:
		return 3
	case SeverityLow:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

func (r AuditReport) String() string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "Policy %s: grade %s (%d/100)\n", r.Policy, r.Grade, r.Score)
	fmt.Fprintf(&builder, "Boundary: %s\n", r.Boundary)
	if len(r.Findings) == 0 {
		builder.WriteString("No policy-level concerns detected.\n")
		return strings.TrimRight(builder.String(), "\n")
	}
	for _, finding := range r.Findings {
		fmt.Fprintf(&builder, "[%s] %s %s: %s\n", strings.ToUpper(string(finding.Severity)), finding.Code, finding.Title, finding.Detail)
		if finding.Recommendation != "" {
			fmt.Fprintf(&builder, "  Recommendation: %s\n", finding.Recommendation)
		}
	}
	return strings.TrimRight(builder.String(), "\n")
}
