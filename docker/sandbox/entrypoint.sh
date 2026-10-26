#!/bin/bash
# Oaiso secure entrypoint
# Fetches short-lived credentials from the Auth Proxy and launches the selected
# agent (Codex CLI or Claude Code), or drops into an interactive shell.
#
# Agent selection is driven by DOCK_AGENT:
#   ""       → interactive shell (auth configured, no agent auto-launched)
#   "codex"  → launch Codex CLI
#   "claude" → launch Claude Code
#
# The interactive shell binary is driven by DOCK_SHELL ("bash", the default, or
# "zsh"); see --shell-type / default_shell_type in the CLI.
#
# When DOCK_AGENT is empty and DOCK_EXEC_CMD is set (--exec), that command runs
# once instead of the interactive shell. An agent, when selected, always takes
# priority over DOCK_EXEC_CMD.
set -euo pipefail

log() {
    echo "[oaiso] $*" >&2
}

DOCK_AGENT="${DOCK_AGENT:-}"

# Only the pinned bootstrap sets this after host and guest verification. Keep
# approvals independent of the inner namespace sandbox (which Docker/Kata's
# seccomp/capability policy may prevent from starting).
codex_sandbox_config() {
    if [[ "${CODEX_DOCK_VERIFIED_KATA:-}" == 1 ]]; then
        echo 'sandbox_mode = "danger-full-access"'
    fi
}

# Render only schema-validated keys supplied by the host policy store. The host
# passes JSON, not TOML, and this renderer owns every output key, preventing a
# profile value from injecting an arbitrary Codex setting or table.
codex_policy_config() {
    [[ -z "${OAISO_CODEX_POLICY_JSON:-}" ]] && return 0
    python3 - <<'PYCODEXPOLICY'
import json, os

data = json.loads(os.environ["OAISO_CODEX_POLICY_JSON"])
strings = {
    "approvals_reviewer": "approvals_reviewer",
    "sandbox_mode": "sandbox_mode",
    "model_reasoning_effort": "model_reasoning_effort",
    "model_reasoning_summary": "model_reasoning_summary",
    "web_search": "web_search",
    "service_tier": "service_tier",
    "file_opener": "file_opener",
}
if os.environ.get("CODEX_DOCK_VERIFIED_KATA") == "1":
    strings.pop("sandbox_mode", None)
for source, target in strings.items():
    value = data.get(source)
    if value:
        print(f"{target} = {json.dumps(value)}")
if data.get("hide_agent_reasoning"):
    print("hide_agent_reasoning = true")
if data.get("allow_login_shell"):
    print("allow_login_shell = true")
persistence = data.get("history_persistence")
max_bytes = data.get("history_max_bytes")
if persistence:
    print(f"history.persistence = {json.dumps(persistence)}")
if isinstance(max_bytes, int) and max_bytes >= 0:
    print(f"history.max_bytes = {max_bytes}")
PYCODEXPOLICY
}

resolve_codex_home() {
    local candidate
    for candidate in "${HOME:-}" "/var/tmp/codex-home" "/tmp/codex-home"; do
        [[ -z "$candidate" ]] && continue
        if mkdir -p "$candidate" 2>/dev/null && [[ -w "$candidate" ]]; then
            echo "$candidate"
            return 0
        fi
    done
    echo "/tmp"
}

# ── Package installation ────────────────────────────────────────────────────
if [[ -n "${CODEX_INSTALL_SCRIPT:-}" ]]; then
    log "Installing packages..."
    bash -c "$CODEX_INSTALL_SCRIPT"
fi

# ── Codex (OpenAI) auth acquisition ───────────────────────────────────────────
# Runs whenever the proxy issued an OpenAI/Codex token (agent=codex or shell).
if [[ -n "${CODEX_AUTH_PROXY_URL:-}" && -n "${CODEX_TOKEN:-}" ]]; then
    log "Fetching Codex credentials from Auth Proxy (${CODEX_AUTH_PROXY_URL})..."

    # The worker reaches the proxy over its dedicated internal network. Kata
    # uses its validated front IP; NO_PROXY sends this request directly.
    fetch_token() {
        local endpoint="$1"
        curl -fsS --connect-timeout 3 --max-time 10 \
            -H "X-Codex-Token: ${CODEX_TOKEN}" \
            "${endpoint}/token"
    }

    RESPONSE=$(fetch_token "${CODEX_AUTH_PROXY_URL}") || {
        log "ERROR: Failed to fetch credentials from Auth Proxy at ${CODEX_AUTH_PROXY_URL}"
        exit 1
    }

    # Detect OAuth mode: proxy returns oauth_access_token instead of api_key
    OAUTH_TOKEN=$(echo "$RESPONSE" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('oauth_access_token',''))" 2>/dev/null || true)

    if [[ -n "$OAUTH_TOKEN" ]]; then
        # OAuth mode: write auth.json with a placeholder access_token and a
        # dummy ID JWT containing only selected account/plan metadata.
        # Real access/ID/refresh tokens stay on the Auth Proxy host.
        # When Codex CLI needs to refresh, it calls CODEX_REFRESH_TOKEN_URL_OVERRIDE
        # which points to the proxy's /oauth/token endpoint. The proxy performs the
        # actual refresh using the host's refresh_token and returns only fresh
        # worker metadata and the access placeholder.
        CODEX_HOME="$(resolve_codex_home)"
        export HOME="${CODEX_HOME}"
        mkdir -p "${CODEX_HOME}/.codex"
        python3 -c "
import sys, json
d = json.load(open('/dev/stdin'))
out = {
    'auth_mode': 'chatgpt',
    'OPENAI_API_KEY': None,
    'tokens': {
        'id_token':      d.get('oauth_id_token', ''),
        'access_token':  d.get('oauth_access_token', ''),
        'refresh_token': '',
        'account_id':    d.get('oauth_account_id', ''),
    },
    'last_refresh': d.get('oauth_last_refresh', ''),
}
print(json.dumps(out))
" <<< "$RESPONSE" > "${CODEX_HOME}/.codex/auth.json"
        chmod 600 "${CODEX_HOME}/.codex/auth.json"

        # Write Codex CLI config so BOTH the Responses API (model requests) and
        # chatgpt.com/backend-api calls route through the Auth Proxy.
        #
        # openai_base_url is what actually redirects model traffic. Codex CLI does
        # NOT read the OPENAI_BASE_URL environment variable (it is deprecated and
        # ignored — openai/codex#16719); the Responses API endpoint is resolved
        # solely from this config key (codex-rs model-provider-info `to_api_provider`).
        # Without it, ChatGPT-OAuth mode connects directly to
        # https://chatgpt.com/backend-api/codex and sends the placeholder cdx-xxxx
        # token as a bearer, yielding "401 Could not parse your authentication token".
        # chatgpt_base_url only affects the ChatGPT login/backend-api flow, not the
        # model requests, so it is insufficient on its own.
        cat > "${CODEX_HOME}/.codex/config.toml" <<EOF
openai_base_url = "${CODEX_AUTH_PROXY_URL}/v1"
check_for_update_on_startup = false
$(codex_sandbox_config)
$(codex_policy_config)
chatgpt_base_url = "${CODEX_AUTH_PROXY_URL}/chatgpt/"

[features]
apps = false
$(if [[ "${CODEX_DOCK_CODEX_HTTP:-}" == "1" ]]; then printf 'responses_websockets = false\nresponses_websockets_v2 = false\nenable_request_compression = false\n'; fi)
EOF
        chmod 600 "${CODEX_HOME}/.codex/config.toml"

        log "Codex OAuth credentials acquired (access_token is a placeholder; proxy injects real credentials on outbound API requests)."
    else
        # API key mode: extract api_key and set as environment variable
        API_KEY=$(echo "$RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin)['api_key'])" 2>/dev/null || true)

        if [[ -z "$API_KEY" ]]; then
            log "ERROR: Auth Proxy returned neither oauth_access_token nor api_key"
            exit 1
        fi

        export OPENAI_API_KEY="$API_KEY"

        # Route the Responses API through the Auth Proxy. As in OAuth mode, Codex CLI
        # only honours the openai_base_url config key, never OPENAI_BASE_URL. Without
        # this the CLI would call api.openai.com directly with the placeholder key,
        # bypassing the proxy's credential injection and returning 401.
        CODEX_HOME="$(resolve_codex_home)"
        export HOME="${CODEX_HOME}"
        mkdir -p "${CODEX_HOME}/.codex"
        cat > "${CODEX_HOME}/.codex/config.toml" <<EOF
openai_base_url = "${CODEX_AUTH_PROXY_URL}/v1"
check_for_update_on_startup = false
$(codex_sandbox_config)
$(codex_policy_config)

[features]
apps = false
$(if [[ "${CODEX_DOCK_CODEX_HTTP:-}" == "1" ]]; then printf 'responses_websockets = false\nresponses_websockets_v2 = false\nenable_request_compression = false\n'; fi)
EOF
        chmod 600 "${CODEX_HOME}/.codex/config.toml"

        log "Codex placeholder key acquired (real API key injected by proxy on outbound requests)."
    fi

    # Clear the temporary token from environment for security
    unset CODEX_TOKEN
    unset CODEX_AUTH_PROXY_URL
fi

# ── Claude Code (Anthropic) auth setup ────────────────────────────────────────
# The official CLI selects API billing with ANTHROPIC_API_KEY and subscription
# authentication with CLAUDE_CODE_OAUTH_TOKEN. Both hold only placeholders.
if [[ -n "${ANTHROPIC_BASE_URL:-}" ]]; then
    CLAUDE_HOME="$(resolve_codex_home)"
    export HOME="${CLAUDE_HOME}"
    # Skip first-run onboarding/theme prompts so the agent starts unattended.
    if [[ ! -f "${CLAUDE_HOME}/.claude.json" ]]; then
        cat > "${CLAUDE_HOME}/.claude.json" <<'EOF'
{"hasCompletedOnboarding": true}
EOF
        chmod 600 "${CLAUDE_HOME}/.claude.json"
    fi
    # Approve only our issued placeholder to avoid the interactive API-key prompt.
    if [[ "${ANTHROPIC_API_KEY:-}" == cdx-* ]]; then
        python3 - <<'PYCLAUDE'
import json, os
path = os.path.join(os.environ['HOME'], '.claude.json')
with open(path) as src:
    settings = json.load(src)
responses = settings.setdefault('customApiKeyResponses', {})
key = os.environ['ANTHROPIC_API_KEY'][-20:]
approved = responses.setdefault('approved', [])
if key not in approved:
    approved.append(key)
responses['rejected'] = [item for item in responses.get('rejected', []) if item != key]
with open(path, 'w') as out:
    json.dump(settings, out)
os.chmod(path, 0o600)
PYCLAUDE
    fi
    log "Claude Code auth configured via proxy (${ANTHROPIC_BASE_URL}); real credential injected by proxy on outbound requests."
fi

# ── Build agent arguments ─────────────────────────────────────────────────────
# CODEX_APPROVAL_MODE: suggest (default), auto-edit, full-auto, danger
build_codex_args() {
    CODEX_ARGS=()
    case "${CODEX_APPROVAL_MODE:-suggest}" in
        auto-edit) CODEX_ARGS+=("--ask-for-approval" "on-request") ;;
        full-auto) CODEX_ARGS+=("--ask-for-approval" "never") ;;
        danger)    CODEX_ARGS+=("--dangerously-bypass-approvals-and-sandbox") ;;
        suggest|*) ;;
    esac
    # The verified Kata config supplies this default for direct Codex launches.
    # Leave native --sandbox, -s and -c sandbox_mode=... overrides to Codex.
    if [[ "${CODEX_DOCK_VERIFIED_KATA:-}" == 1 && "${CODEX_APPROVAL_MODE:-suggest}" != danger && "${DOCK_DIRECT_AGENT:-0}" != 1 ]]; then
        CODEX_ARGS+=("--sandbox" "danger-full-access")
    fi
    if [[ -n "${CODEX_MODEL:-}" ]]; then
        CODEX_ARGS+=("--model" "$CODEX_MODEL")
    fi
    if [[ -n "${CODEX_AGENTS_MD:-}" && -f "${CODEX_AGENTS_MD}" ]]; then
        CODEX_ARGS+=("--agents-md" "$CODEX_AGENTS_MD")
    fi
}

build_claude_args() {
    CLAUDE_ARGS=()
    if [[ -n "${OAISO_CLAUDE_POLICY_ARGS_JSON:-}" ]]; then
        mapfile -d '' -t CLAUDE_ARGS < <(python3 - <<'PYCLAUDEPOLICY'
import json, os, sys
for arg in json.loads(os.environ["OAISO_CLAUDE_POLICY_ARGS_JSON"]):
    if not isinstance(arg, str) or "\0" in arg:
        raise SystemExit("invalid Claude policy argument")
    sys.stdout.write(arg + "\0")
PYCLAUDEPOLICY
)
    fi
    case "${CODEX_APPROVAL_MODE:-suggest}" in
        auto-edit)        CLAUDE_ARGS+=("--permission-mode" "acceptEdits") ;;
        full-auto|danger) CLAUDE_ARGS+=("--dangerously-skip-permissions") ;;
        suggest|*)        ;;
    esac
    if [[ -n "${CODEX_MODEL:-}" ]]; then
        CLAUDE_ARGS+=("--model" "$CODEX_MODEL")
    fi
}

# launch_interactive launches an agent after bash job-control is ready so that
# Ctrl+Z suspends the agent and returns to an in-container bash prompt.
# Always uses bash regardless of DOCK_SHELL: the auto-launch trick relies on
# PROMPT_COMMAND, which is bash-specific (zsh has no equivalent hook wired up
# here). DOCK_SHELL only selects the shell for the no-agent interactive case
# below and the --shell debug mode.
launch_interactive() {
    local init="$1"
    export PROMPT_COMMAND="unset PROMPT_COMMAND; ${init}"
    exec bash -i
}

# resolve_shell prints the shell binary to use for the no-agent interactive
# shell and --exec: DOCK_SHELL if it exists in the image, otherwise bash.
# DOCK_SHELL can come from an explicit --shell-type or be auto-detected from
# the host's $SHELL (see cmd/run.go), so it may name a shell this particular
# image never installed; falling back here keeps that auto-detection from
# turning into a hard failure on images that only ship bash.
resolve_shell() {
    local requested="${DOCK_SHELL:-bash}"
    if command -v "$requested" >/dev/null 2>&1; then
        echo "$requested"
        return
    fi
    if [[ "$requested" != "bash" ]]; then
        log "WARNING: DOCK_SHELL='${requested}' not found in this image; falling back to bash."
    fi
    echo "bash"
}

# zsh starts zsh-newuser-install when an interactive shell has no user startup
# files. This is common when a numeric host UID uses the writable fallback HOME.
# Create an empty .zshrc only when none exists; custom and bind-mounted dotfiles
# remain untouched.
ensure_zsh_startup_file() {
    local shell_bin="$1"
    [[ "${shell_bin}" == "zsh" ]] || return 0

    local startup_dir="${ZDOTDIR:-${HOME}}"
    if [[ ! -e "${startup_dir}/.zshrc" ]]; then
        mkdir -p "${startup_dir}"
        : > "${startup_dir}/.zshrc"
    fi
}

if [[ -f "/workspace/AGENTS.md" ]]; then
    log "Found AGENTS.md in workspace."
fi

cd /workspace

# Explicit client compatibility: credentials and host SSH agents stay outside.
if [[ -n "${DOCK_CA_BUNDLE:-}" ]]; then
    mkdir -p "$HOME"
    cat /etc/ssl/certs/ca-certificates.crt "$DOCK_CA_BUNDLE" > "$HOME/.cxd-ca.pem"
    chmod 600 "$HOME/.cxd-ca.pem"
    export SSL_CERT_FILE="$HOME/.cxd-ca.pem" CURL_CA_BUNDLE="$HOME/.cxd-ca.pem" REQUESTS_CA_BUNDLE="$HOME/.cxd-ca.pem"
    export NODE_EXTRA_CA_CERTS="$DOCK_CA_BUNDLE"
    if command -v java >/dev/null 2>&1 && command -v keytool >/dev/null 2>&1; then
        python3 - <<'PYJAVACA'
import os, pathlib, re, shutil, subprocess, tempfile
java = pathlib.Path(shutil.which('java')).resolve()
base = java.parent.parent / 'lib/security/cacerts'
store = pathlib.Path(os.environ['HOME']) / '.cxd-cacerts'
if not base.is_file():
    raise SystemExit('cannot locate JVM system truststore')
shutil.copyfile(base, store)
os.chmod(store, 0o600)
certs = re.findall(r'-----BEGIN CERTIFICATE-----.*?-----END CERTIFICATE-----', pathlib.Path(os.environ['DOCK_CA_BUNDLE']).read_text(), re.S)
if not certs:
    raise SystemExit('custom CA bundle contains no certificates')
for index, cert in enumerate(certs):
    with tempfile.NamedTemporaryFile(mode='w') as f:
        f.write(cert);f.flush()
        subprocess.run(['keytool','-importcert','-noprompt','-storepass','changeit','-keystore',str(store),'-alias','cxd-extra-'+str(index),'-file',f.name], check=True, stdout=subprocess.DEVNULL)
PYJAVACA
        export JAVA_TOOL_OPTIONS="${JAVA_TOOL_OPTIONS:-} -Djavax.net.ssl.trustStore=\"$HOME/.cxd-cacerts\" -Djavax.net.ssl.trustStorePassword=changeit"
    fi
fi
if [[ "${DOCK_SSH_PROXY:-0}" == 1 ]]; then
    mkdir -p "$HOME"
    cat > "$HOME/.cxd-proxy-connect.py" <<'PYCONNECT'
import os,sys,socket,threading,urllib.parse,ipaddress,re
host,port=sys.argv[1:3]
try:
    ipaddress.ip_address(host)
except ValueError:
    if not re.fullmatch(r'[A-Za-z0-9.-]{1,253}',host):raise SystemExit('invalid SSH host')
port=int(port)
if not 0<port<65536:raise SystemExit('invalid SSH port')
proxy=urllib.parse.urlsplit(os.environ['HTTP_PROXY'])
if proxy.scheme!='http' or proxy.username or not proxy.hostname:raise SystemExit('unsupported SSH proxy')
connection=socket.create_connection((proxy.hostname,proxy.port or 80),timeout=30)
authority=('['+host+']' if ':' in host else host)+':'+str(port)
connection.sendall(('CONNECT '+authority+' HTTP/1.1\r\nHost: '+authority+'\r\n\r\n').encode('ascii'))
header=b''
while not header.endswith(b'\r\n\r\n'):
    byte=connection.recv(1)
    if not byte or len(header)>=65536:raise SystemExit('invalid proxy response')
    header+=byte
if header.split(b' ',2)[1]!=b'200':raise SystemExit('SSH destination denied or unavailable')
connection.settimeout(None)
def send():
    try:
        while True:
            data=os.read(0,65536)
            if not data:break
            connection.sendall(data)
        connection.shutdown(socket.SHUT_WR)
    except OSError:pass
threading.Thread(target=send,daemon=True).start()
while True:
    data=connection.recv(65536)
    if not data:break
    sys.stdout.buffer.write(data);sys.stdout.buffer.flush()
PYCONNECT
    chmod 700 "$HOME/.cxd-proxy-connect.py"
    printf -v CONNECT_HELPER '%q' "$HOME/.cxd-proxy-connect.py"
    export GIT_SSH_COMMAND="ssh -o 'ProxyCommand=python3 ${CONNECT_HELPER} %h %p'"
fi

# Optional project setup stays inside the worker, after auth and before agents.
if [[ -n "${DOCK_ENVIRONMENT_JSON:-}" ]]; then
    python3 - <<'PYHOOK'
import json, os, subprocess, time
settings = json.loads(os.environ['DOCK_ENVIRONMENT_JSON'])
timeout = settings.get('readiness_timeout', 0) / 1e9 or 60
deadline = time.monotonic() + timeout
for argv in settings.get('start_hooks', []):
    subprocess.run(argv, check=True, timeout=max(.01, deadline-time.monotonic()))
probe = settings.get('readiness', [])
while probe:
    remaining = deadline-time.monotonic()
    if remaining <= 0:
        raise SystemExit('worker readiness timed out')
    if subprocess.run(probe, timeout=remaining).returncode == 0:
        break
    time.sleep(min(.25, max(0, deadline-time.monotonic())))
ready = os.environ['DOCK_ENVIRONMENT_READY']
with open(ready, 'w') as f:
    f.write('ready\n')
PYHOOK
fi

# ── Launch the selected agent ─────────────────────────────────────────────────
case "${DOCK_AGENT}" in
    codex)
        build_codex_args
        if [[ "${DOCK_DIRECT_AGENT:-0}" == 1 ]]; then
            CODEX_EXTRA_ARGS=()
            if [[ -n "${DOCK_AGENT_ARGS_JSON:-}" ]]; then
                mapfile -d '' -t CODEX_EXTRA_ARGS < <(python3 -c '
import json, os, sys
for arg in json.loads(os.environ["DOCK_AGENT_ARGS_JSON"]):
    sys.stdout.buffer.write(arg.encode() + b"\0")
')
            fi
            exec codex "${CODEX_ARGS[@]}" "${CODEX_EXTRA_ARGS[@]}"
        fi
        log "Starting Codex CLI..."
        if [[ -n "${CODEX_TASK:-}" ]]; then
            log "Task: ${CODEX_TASK}"
            exec codex "${CODEX_ARGS[@]}" "$CODEX_TASK"
        fi
        INIT="codex"
        for arg in "${CODEX_ARGS[@]}"; do INIT+=" $(printf '%q' "$arg")"; done
        launch_interactive "$INIT"
        ;;
    claude)
        build_claude_args
        if [[ "${DOCK_DIRECT_AGENT:-0}" == 1 ]]; then
            CLAUDE_EXTRA_ARGS=()
            if [[ -n "${DOCK_AGENT_ARGS_JSON:-}" ]]; then
                mapfile -d '' -t CLAUDE_EXTRA_ARGS < <(python3 -c '
import json, os, sys
for arg in json.loads(os.environ["DOCK_AGENT_ARGS_JSON"]):
    sys.stdout.buffer.write(arg.encode() + b"\0")
')
            fi
            exec claude "${CLAUDE_ARGS[@]}" "${CLAUDE_EXTRA_ARGS[@]}"
        fi
        log "Starting Claude Code..."
        if [[ -n "${CODEX_TASK:-}" ]]; then
            log "Task: ${CODEX_TASK}"
            exec claude "${CLAUDE_ARGS[@]}" "$CODEX_TASK"
        fi
        INIT="claude"
        for arg in "${CLAUDE_ARGS[@]}"; do INIT+=" $(printf '%q' "$arg")"; done
        launch_interactive "$INIT"
        ;;
    ""|shell)
        SHELL_BIN="$(resolve_shell)"
        if [[ -n "${DOCK_EXEC_CMD:-}" ]]; then
            log "No agent selected; running command: ${DOCK_EXEC_CMD}"
            exec "${SHELL_BIN}" -c "${DOCK_EXEC_CMD}"
        fi
        ensure_zsh_startup_file "${SHELL_BIN}"
        log "No agent selected; starting interactive shell (codex and claude are available on PATH)."
        exec "${SHELL_BIN}" -i
        ;;
    *)
        log "ERROR: unknown DOCK_AGENT '${DOCK_AGENT}' (expected: codex, claude, or empty)"
        exit 1
        ;;
esac
