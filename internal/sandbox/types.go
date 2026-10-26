package sandbox

import (
	"io"
	"time"

	"github.com/pacificbelt30/codex-dock/internal/agentpolicy"
	"github.com/pacificbelt30/codex-dock/internal/workerpolicy"

	"github.com/pacificbelt30/codex-dock/pkg/cxd/identity"
)

// ApprovalMode controls how Codex CLI asks for approval when executing actions.
type ApprovalMode string

const (
	// ApprovalModeSuggest is the default interactive mode: Codex asks for
	// approval according to the CLI default on-request policy.
	ApprovalModeSuggest ApprovalMode = "suggest"

	// ApprovalModeAutoEdit maps to modern Codex on-request: the model decides
	// when to ask. Claude maps this mode to acceptEdits.
	ApprovalModeAutoEdit ApprovalMode = "auto-edit"

	// ApprovalModeFullAuto never asks for approval
	// (maps to --ask-for-approval never).
	ApprovalModeFullAuto ApprovalMode = "full-auto"

	// ApprovalModeDanger bypasses all approval prompts and Codex sandbox
	// restrictions (maps to --dangerously-bypass-approvals-and-sandbox).
	// The selected worker runtime provides the safety boundary in this mode.
	ApprovalModeDanger ApprovalMode = "danger"
)

// Agent identifies which AI CLI the sandbox launches.
type Agent string

const (
	// AgentNone drops into an interactive shell with auth configured but no
	// agent auto-launched. This is the default when --agent is omitted.
	AgentNone Agent = ""
	// AgentCodex launches OpenAI Codex CLI.
	AgentCodex Agent = "codex"
	// AgentClaude launches Anthropic Claude Code.
	AgentClaude Agent = "claude"
)

// ValidAgent returns true if a is one of the recognised agent values.
func ValidAgent(a Agent) bool {
	switch a {
	case AgentNone, AgentCodex, AgentClaude:
		return true
	default:
		return false
	}
}

// ShellType selects the interactive shell binary launched inside the sandbox:
// the no-agent interactive shell (DOCK_AGENT="") and the --shell debug mode.
// It does not affect the bash-specific job-control wrapper used to
// auto-launch an agent (see entrypoint.sh's launch_interactive).
type ShellType string

const (
	// ShellBash is the default interactive shell.
	ShellBash ShellType = "bash"
	// ShellZsh launches zsh instead. Must be installed in the image (the
	// built-in "plain" and "pwn" templates include it).
	ShellZsh ShellType = "zsh"
)

// ValidShellType returns true if s is a recognised shell type.
func ValidShellType(s ShellType) bool {
	switch s {
	case ShellBash, ShellZsh:
		return true
	default:
		return false
	}
}

// ValidApprovalMode returns true if mode is one of the recognised values.
func ValidApprovalMode(mode ApprovalMode) bool {
	switch mode {
	case ApprovalModeSuggest, ApprovalModeAutoEdit, ApprovalModeFullAuto, ApprovalModeDanger:
		return true
	default:
		return false
	}
}

// MountSpec describes an additional host directory to bind-mount into the
// container beyond the project workspace (which is always mounted at
// /workspace). Source must be an absolute host path and Target an absolute
// path inside the container.
type MountSpec struct {
	Source   string
	Target   string
	ReadOnly bool
}

// RunOptions holds all options for starting a sandboxed container.
type RunOptions struct {
	// CreateOnly materializes a Kata worker without booting user code or issuing
	// a live token. StartContext performs verification and activation later.
	CreateOnly bool
	// authenticationSession is set only by RunAuthenticationContext. Login
	// sessions never inherit project mounts, entrypoints or provider secrets.
	authenticationSession bool
	Compatibility         Compatibility
	Filesystem            workerpolicy.Filesystem
	Lifecycle             workerpolicy.Lifecycle
	Environment           workerpolicy.Environment
	CloneDir              string
	pendingPlaceholder    string
	proxyHosts            []string
	// Runtime selects the isolation boundary, not the container management API.
	// Empty means Kata. Runc requires an explicit compatibility opt-in.
	Runtime            string
	KataRuntime        string
	ResourceProfile    string
	Image              string
	Packages           []string
	PkgFile            string
	ProjectDir         string
	WorktreePath       string
	UseWorktree        bool
	Branch             string
	NewBranch          bool
	ReuseBranchOnRetry bool
	Name               string
	// RequestKey and fingerprint are hashes of an embedding product's external
	// request identity and immutable launch options; no raw task data is stored.
	RequestKey         string
	RequestFingerprint string
	AllocationID       string
	Agent              Agent
	DirectAgent        bool     // exec the agent directly, without the interactive shell wrapper
	AgentArgs          []string // argv forwarded verbatim to the selected agent
	ExecCmd            string   // run this command instead of an interactive shell; ignored when Agent is set (the agent always takes priority)
	Task               string
	ApprovalMode       ApprovalMode
	AgentPolicy        agentpolicy.Effective
	Model              string
	WatchPolling       bool
	ReadOnly           bool
	ReadOnlyRootFS     bool
	NoGitMetadata      bool
	NoInternet         bool
	TokenTTL           int
	TokenIdentity      identity.Identity
	TokenModelPolicy   *identity.ModelPolicy
	TokenUsagePolicy   *identity.UsagePolicy
	TemporaryStorage   []workerpolicy.TemporaryStorage
	AgentsMD           string
	Detach             bool
	Parallel           int
	ShellMode          bool
	ShellType          ShellType // interactive shell binary; empty defaults to ShellBash
	ContainerUser      string    // uid[:gid] to run as inside the container; empty = image default
	Mounts             []MountSpec
}

// Worker represents a running or stopped codex-dock container.
type Worker struct {
	ID          string
	Name        string
	Status      string
	Image       string
	Branch      string
	Task        string
	AgentPolicy string
	StartedAt   *time.Time
	FinishedAt  *time.Time
}

// LogOptions specifies options for fetching container logs.
type LogOptions struct {
	Name   string
	Tail   int
	Follow bool
	Output io.Writer // destination for log output; defaults to os.Stdout if nil
}
