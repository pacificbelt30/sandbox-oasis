package sandbox

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEntrypointWritesCodexConfigToCodexHome(t *testing.T) {
	entrypoint, err := os.ReadFile("../../docker/sandbox/entrypoint.sh")
	if err != nil {
		t.Fatalf("read entrypoint: %v", err)
	}

	script := string(entrypoint)
	if !strings.Contains(script, `"${CODEX_HOME}/.codex/config.toml"`) {
		t.Error("entrypoint must write config.toml under ~/.codex")
	}
	if strings.Contains(script, `"${CODEX_HOME}/.config/codex/config.toml"`) {
		t.Error("entrypoint writes config.toml to legacy ~/.config/codex path")
	}
}

// TestEntrypointSetsOpenAIBaseURLInConfig guards against regressing to the
// (broken) assumption that the OPENAI_BASE_URL environment variable redirects
// Codex CLI. Codex resolves the Responses API endpoint solely from the
// openai_base_url config key (openai/codex#16719), so the entrypoint must write
// it to config.toml. chatgpt_base_url alone is insufficient — it only affects the
// ChatGPT login/backend-api flow, not model requests.
func TestEntrypointSetsOpenAIBaseURLInConfig(t *testing.T) {
	entrypoint, err := os.ReadFile("../../docker/sandbox/entrypoint.sh")
	if err != nil {
		t.Fatalf("read entrypoint: %v", err)
	}

	script := string(entrypoint)
	if !strings.Contains(script, `openai_base_url = "${CODEX_AUTH_PROXY_URL}/v1"`) {
		t.Error("entrypoint must set openai_base_url in config.toml to route the Responses API through the proxy")
	}
	// openai_base_url is required in both OAuth and API-key modes; the string must
	// appear at least twice (once per branch).
	if n := strings.Count(script, `openai_base_url = "${CODEX_AUTH_PROXY_URL}/v1"`); n < 2 {
		t.Errorf("openai_base_url must be written in both OAuth and API-key modes, found %d occurrence(s)", n)
	}
}

// TestEntrypointUsesDockShellForInteractiveShell guards the no-agent shell
// case: it must launch the shell selected via DOCK_SHELL (defaulting to bash,
// and falling back to bash via resolve_shell if DOCK_SHELL names a binary
// that isn't installed in the image) rather than a hardcoded "bash -i".
func TestEntrypointUsesDockShellForInteractiveShell(t *testing.T) {
	entrypoint, err := os.ReadFile("../../docker/sandbox/entrypoint.sh")
	if err != nil {
		t.Fatalf("read entrypoint: %v", err)
	}

	script := string(entrypoint)
	if !strings.Contains(script, `exec "${SHELL_BIN}" -i`) {
		t.Error(`entrypoint's no-agent shell case must exec "${SHELL_BIN}" -i (SHELL_BIN from resolve_shell, which reads DOCK_SHELL)`)
	}
	if !strings.Contains(script, `SHELL_BIN="$(resolve_shell)"`) {
		t.Error("entrypoint's no-agent branch must set SHELL_BIN via resolve_shell")
	}
	if !strings.Contains(script, `local requested="${DOCK_SHELL:-bash}"`) {
		t.Error("resolve_shell must read the requested shell from DOCK_SHELL, defaulting to bash")
	}
	if !strings.Contains(script, `command -v "$requested"`) {
		t.Error("resolve_shell must verify the requested shell exists before using it, falling back to bash otherwise")
	}
}

func TestEntrypointPreventsZshNewUserWizard(t *testing.T) {
	entrypoint, err := os.ReadFile("../../docker/sandbox/entrypoint.sh")
	if err != nil {
		t.Fatalf("read entrypoint: %v", err)
	}

	script := string(entrypoint)
	if !strings.Contains(script, `ensure_zsh_startup_file "${SHELL_BIN}"`) {
		t.Error("entrypoint must ensure a startup file exists before launching interactive zsh")
	}
	if !strings.Contains(script, `[[ ! -e "${startup_dir}/.zshrc" ]]`) {
		t.Error("entrypoint must preserve an existing or bind-mounted .zshrc")
	}
}

func TestEntrypointDisablesAppsInConfig(t *testing.T) {
	entrypoint, err := os.ReadFile("../../docker/sandbox/entrypoint.sh")
	if err != nil {
		t.Fatalf("read entrypoint: %v", err)
	}

	script := string(entrypoint)
	if n := strings.Count(script, "[features]\napps = false"); n != 2 {
		t.Errorf("apps must be disabled in both OAuth and API-key modes, found %d occurrence(s)", n)
	}
}

func TestEntrypointDirectCodexExec(t *testing.T) {
	entrypoint, err := os.ReadFile("../../docker/sandbox/entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(entrypoint)
	if !strings.Contains(script, `exec codex "${CODEX_ARGS[@]}" "${CODEX_EXTRA_ARGS[@]}"`) {
		t.Error("direct Codex path must exec argv without a shell")
	}
	if !strings.Contains(script, `json.loads(os.environ["DOCK_AGENT_ARGS_JSON"])`) {
		t.Error("direct Codex path must decode structured argv")
	}
	if strings.Contains(script, "sleep 1") {
		t.Error("entrypoint must not add an unconditional startup delay")
	}
}

func TestDirectCodexUsesKataSandboxConfigAsDefault(t *testing.T) {
	entrypoint, err := os.ReadFile("../../docker/sandbox/entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	prefix, _, ok := strings.Cut(string(entrypoint), "# launch_interactive")
	if !ok {
		t.Fatal("could not isolate agent argument builder")
	}
	for _, tc := range []struct {
		name, direct string
		wantSandbox  bool
	}{
		{"direct", "1", false},
		{"managed", "0", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			command := exec.Command("bash", "-c", prefix+"\nbuild_codex_args\nprintf '%s\\0' \"${CODEX_ARGS[@]}\"\n")
			command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), "CODEX_DOCK_VERIFIED_KATA=1", "DOCK_DIRECT_AGENT=" + tc.direct}
			output, err := command.Output()
			if err != nil {
				t.Fatal(err)
			}
			hasSandbox := bytes.Contains(output, []byte("--sandbox\x00danger-full-access\x00"))
			if hasSandbox != tc.wantSandbox {
				t.Fatalf("direct=%s sandbox flag present=%t; want %t; args=%q", tc.direct, hasSandbox, tc.wantSandbox, output)
			}
		})
	}
}

func TestEntrypointRendersAllowlistedCodexPolicy(t *testing.T) {
	entrypoint, err := os.ReadFile("../../docker/sandbox/entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	prefix, _, ok := strings.Cut(string(entrypoint), "# ── Package installation")
	if !ok {
		t.Fatal("could not isolate policy renderer")
	}
	command := exec.Command("bash", "-c", prefix+"\ncodex_policy_config\n")
	command.Env = []string{
		"PATH=/usr/bin:/bin",
		`OAISO_CODEX_POLICY_JSON={"approvals_reviewer":"auto_review","web_search":"disabled","history_persistence":"none","history_max_bytes":4096,"unknown":"injected"}`,
	}
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	text := string(output)
	for _, want := range []string{`approvals_reviewer = "auto_review"`, `web_search = "disabled"`, `history.persistence = "none"`, `history.max_bytes = 4096`} {
		if !strings.Contains(text, want) {
			t.Errorf("policy output missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "unknown") || strings.Contains(text, "injected") {
		t.Fatalf("unknown policy key escaped allowlist: %s", text)
	}
}

func TestEntrypointVerifiedKataOverridesPolicySandbox(t *testing.T) {
	entrypoint, err := os.ReadFile("../../docker/sandbox/entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	prefix, _, _ := strings.Cut(string(entrypoint), "# ── Package installation")
	command := exec.Command("bash", "-c", prefix+"\ncodex_sandbox_config\ncodex_policy_config\n")
	command.Env = []string{"PATH=/usr/bin:/bin", "CODEX_DOCK_VERIFIED_KATA=1", `OAISO_CODEX_POLICY_JSON={"sandbox_mode":"read-only"}`}
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	if text := string(output); strings.Count(text, "sandbox_mode") != 1 || !strings.Contains(text, `danger-full-access`) {
		t.Fatalf("verified Kata sandbox output = %q", text)
	}
}

func TestEntrypointDirectClaudePreservesArgumentsAndApprovesOnlyPlaceholder(t *testing.T) {
	source, err := os.ReadFile("../../docker/sandbox/entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, oauth := range []bool{false, true} {
		root := t.TempDir()
		// Only relocate the fixed image workspace for a host-side script test.
		entrypoint := filepath.Join(root, "entrypoint.sh")
		if err := os.WriteFile(entrypoint, []byte(strings.Replace(string(source), "cd /workspace", `cd "${HOME}"`, 1)), 0700); err != nil {
			t.Fatal(err)
		}
		fake := filepath.Join(root, "claude")
		if err := os.WriteFile(fake, []byte("#!/bin/bash\nprintf '%s\\0' \"$@\"\n"), 0700); err != nil {
			t.Fatal(err)
		}
		command := exec.Command("bash", entrypoint)
		command.Env = []string{"PATH=" + root + ":/usr/bin:/bin", "HOME=" + root, "DOCK_AGENT=claude", "DOCK_DIRECT_AGENT=1", `DOCK_AGENT_ARGS_JSON=["-p","literal $text and spaces","--output-format","json"]`, "ANTHROPIC_BASE_URL=http://auth/anthropic"}
		if oauth {
			command.Env = append(command.Env, "CLAUDE_CODE_OAUTH_TOKEN=cdx-subscription")
		} else {
			command.Env = append(command.Env, "ANTHROPIC_API_KEY=cdx-api-key")
		}
		output, err := command.Output()
		if err != nil {
			t.Fatal(err)
		}
		if string(output) != "-p\x00literal $text and spaces\x00--output-format\x00json\x00" {
			t.Fatalf("Claude argv changed: %q", output)
		}
		config, err := os.ReadFile(filepath.Join(root, ".claude.json"))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(config, []byte("cdx-subscription")) {
			t.Fatal("OAuth placeholder written into API-key approvals")
		}
		if !oauth && !bytes.Contains(config, []byte("cdx-api-key")) {
			t.Fatal("API placeholder not approved")
		}
	}
}
