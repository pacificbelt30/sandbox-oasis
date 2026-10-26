package agentpolicy

// Built-ins are conservative starting points. They can be extended but not
// overwritten or removed from the host catalog.
func BuiltIns() map[string]Profile {
	return map[string]Profile{
		"interactive": {
			Description:  "Agent asks the operator when a command needs approval.",
			ApprovalMode: "suggest",
			Codex: CodexSettings{
				ApprovalsReviewer:  String("user"),
				ReasoningSummary:   String("auto"),
				WebSearch:          String("cached"),
				HistoryPersistence: String("save-all"),
			},
			Claude: ClaudeSettings{PermissionMode: String("manual")},
		},
		"approve-for-me": {
			Description:  "Use the agent's reviewer for approval decisions while retaining the VM boundary.",
			Extends:      "interactive",
			ApprovalMode: "suggest",
			Codex:        CodexSettings{ApprovalsReviewer: String("auto_review")},
			Claude:       ClaudeSettings{PermissionMode: String("auto")},
		},
		"autonomous": {
			Description:  "Allow the agent to proceed without interactive approval; Oaiso isolation remains active.",
			Extends:      "approve-for-me",
			ApprovalMode: "full-auto",
			Codex:        CodexSettings{ApprovalsReviewer: String("auto_review")},
			Claude:       ClaudeSettings{PermissionMode: String("dontAsk")},
		},
		"review": {
			Description: "Read-oriented review profile with persistent history and cached search.",
			Extends:     "interactive",
			Codex: CodexSettings{
				ReasoningEffort:  String("high"),
				ReasoningSummary: String("detailed"),
				WebSearch:        String("cached"),
			},
			Claude: ClaudeSettings{PermissionMode: String("plan"), Effort: String("high")},
		},
		"private": {
			Description: "Do not persist agent history and disable web search by default.",
			Extends:     "interactive",
			Codex: CodexSettings{
				WebSearch:          String("disabled"),
				HistoryPersistence: String("none"),
			},
			Claude: ClaudeSettings{NoSessionPersistence: Bool(true)},
		},
		"restricted": {
			Description: "Prefer the narrowest agent tool surface for investigation and documentation.",
			Extends:     "private",
			Codex:       CodexSettings{SandboxMode: String("read-only")},
			Claude:      ClaudeSettings{Restricted: Bool(true), PermissionMode: String("plan")},
		},
	}
}
