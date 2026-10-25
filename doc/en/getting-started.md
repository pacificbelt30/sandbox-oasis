# Quick start

[日本語](../getting-started.md) | **English**

| Current state | Start here |
|---|---|
| No execution host yet | Complete [Installation](installation.md) first |
| Host is ready | Steps 1–4 start the first worker; steps 5–6 are optional |
| Updating an existing installation | [Operations](../operations.md) (Japanese) |

- This page covers v0.0.3 or later. A [complete installation-to-first-run guide](guides/first-run.md) is also available.

## 1. Check the host

- Account: a normal user in the operator group.
- Use the same account for credentials and normal CLI operations.
- Normal `run` / `proxy run` operations do not need `sudo`.

```bash
oaiso runtime-check
```

**Successful output (default Kata configuration)**

```text
[ok] egress-policy: Host rules are valid; private addresses stay blocked
[ok] runtime: io.containerd.kata.v2
[ok] host: linux/amd64
[ok] kvm: KVM device exists; the root node owns VM access
[ok] cgroups: cgroup v2 has cpu, memory and pids
[ok] node: pinned runtime, assets, resource profile and management socket verified
[ok] builder: BuildKit is reachable
[ok] forwarding: IPv4 forwarding enabled
```

- All checks `[ok]` and exit code `0`: continue.
- An `[error]`: follow its `Fix:` line, then rerun. See [diagnostic examples](../troubleshooting.md#複数の前提条件をまとめて調べる) (Japanese).
- Scope: host readiness. Provider authentication and live AI responses are checked later.

| Task | Requirements |
|---|---|
| Run workers | Linux x86_64, KVM, cgroup v2, dedicated containerd, Kata runtime-rs, Dragonball |
| Build images | BuildKit and `buildctl` |
| Build the CLI from source | Go 1.26.0 or later and Make |

- This page assumes the CLI and host are installed; otherwise return to [Installation](installation.md).
- Release installations can run `init` from any directory; launch agents from your own project.

## 2. Prepare credentials

**For new installations, use `login` to separate product and host credentials.**

1. Check the keyring. For SSH, WSL or headless use, open `session` and continue inside that shell.
2. Choose exactly one login command below.

```bash
oaiso auth keyring check
# If the keyring is unavailable:
oaiso auth keyring session
```

| Authentication | Command |
|---|---|
| Codex, API key | `oaiso login --agent codex --method api-key --profile work` |
| Codex, subscription | `oaiso login --agent codex --method subscription --profile work` |
| Claude Code, API key | `oaiso login --agent claude --method api-key --profile work` |
| Claude Code, subscription | `oaiso login --agent claude --method subscription --profile work` |

- Login encrypts storage with keyring plus age, selects the profile and prepares the admin secret.
- API keys use hidden input and a models-list connection check. Subscription login runs the official CLI inside temporary Kata.
- Run `init` in the same keyring session. See [passphrases and unlocking](guides/keyring.md).
- Do not additionally run manual `auth profile set/import` or admin-secret creation.

### Use an existing authentication method

| Mode | Setup | Credential source |
|---|---|---|
| Manual independent profile | [Authentication steps 1–3](guides/authentication.md) | Selected product Store only; no host fallback |
| Legacy host integration | Configure a source below | Host credentials using [source precedence](auth-proxy.md#credential-sources) |

**For legacy host integration**

| Agent | Sources |
|---|---|
| Codex | `OPENAI_API_KEY`, saved API key, `~/.codex/auth.json` |
| Claude Code | `ANTHROPIC_API_KEY`, `CLAUDE_CODE_OAUTH_TOKEN`, `~/.claude/.credentials.json`, saved API key |

- Login files: place under the invoking user's HOME.
- File access: the proxy uses that user's UID/GID; `0600` files need no permission changes.
- Workers receive placeholders, never real API keys or OAuth access/refresh tokens.
- Claude details: [API-key and subscription setup](claude-code.md).
- Saved Anthropic API keys are a compatibility input. `auth set` saves OpenAI keys only; use an independent profile for new Anthropic key storage.

### Allow general traffic before first startup, if needed

| Task | Default and preparation |
|---|---|
| AI inference only | Fixed AI routes need no general-egress grant |
| Git, npm or pip | General egress is denied by default; [allow required destinations](guides/networking.md) |
| Combine profile and network settings | Merge keys into the same global `[proxy]` table; retain the admin-secret setting |

## 3. Prepare images and start the proxies

**Run as your normal user. Release installations work from any directory.**

| Configured provider | Run exactly one |
|---|---|
| OpenAI | `oaiso init --agent codex` |
| Anthropic | `oaiso init --agent claude` |

- Do not run the other provider's preparation without its credentials.
- `Ready.` means host, image and proxy preparation succeeded; verify live inference in the next step.

**Final lines after successful Codex preparation**

```text
Ready. From your project run: oaiso codex
For host edit watchers add --watch-polling to `oaiso run` (Chokidar/Watchpack).
```

- For Claude, the first line is `Ready. From your project run: oaiso claude`.
- Host diagnostics, image checks and any required build logs appear before these lines.

| `init` action | Behavior or limit |
|---|---|
| Diagnose host and find credentials | Does not print credential contents or rewrite settings |
| Prepare worker image | Builds missing images and checks compatibility |
| Start proxies | Release installations package the installed static CLI into the proxy image |
| Choose agent | Explicit `--agent`, then configured default; a shell default prepares Codex |
| Existing proxy with different authentication | Running workers prevent a forced switch |
| Outside this check | Live provider authentication, connectivity and inference |

- Release installations create the proxy image from the installed static CLI. Source-built dynamic CLIs require their source checkout to build the proxy.
- Once ready: return to the project directory.
- Make Codex the default: `oaiso default codex`; add `--local` for this project only.

### Prepare each component separately

```bash
oaiso build --template plain --tag codex-dock:latest
oaiso proxy run
```

| Component | Purpose or endpoint |
|---|---|
| `codex-auth-proxy` | Authenticated AI requests |
| `codex-http-proxy` | General HTTP/CONNECT; no real credentials |
| Admin API | Host loopback `127.0.0.1:18081` |
| Worker-facing API | Not published to the host |

- `proxy run` creates the required network.
- Details: [auth proxy](auth-proxy.md).
- For an existing team's custom Dockerfile, review [project settings](configuration.md#project-settings) and run `init` in that project. Release installations prepare a missing proxy image from the installed static CLI.

## 4. Start a worker

- Run from the project directory.
- The project is shared at `/workspace`.
- Choose the invocation matching your prepared provider below.

```bash
oaiso codex
# Or:
oaiso claude

# With Codex credentials; a configured default can start an agent instead.
oaiso run
oaiso run -- ls -la
```

| Invocation | Result |
|---|---|
| `oaiso codex` | Start Codex |
| `oaiso claude` | Start Claude Code |
| `oaiso run` | Configured agent; built-in default is an authenticated shell |
| `oaiso run -- COMMAND` | Run a command once |
| Failed isolation check | Startup stops; no automatic fallback to runc |

- Send a first prompt to check a live response. For 401/502 failures, use [Troubleshooting](../troubleshooting.md) (Japanese).
- Foreground exit removes the worker by default; use the background example to try stop/restart.

### Share an environment with a team

- Optional: no `oaiso.toml` is needed for individual use with the default settings.

1. Run `oaiso project init` at the repository root.
2. Commit `oaiso.toml`.
3. Point `[build]` to a project Dockerfile for extra tools.
4. After cloning, prepare the host and credentials.
5. Run `oaiso project check`, then `oaiso init`.

[Project settings](configuration.md#project-settings)

## 5. Run in the background

- This example uses Codex; substitute `--agent claude` for Claude.
- It disables approval prompts; review the task and shared paths before running.

```bash
oaiso run --agent codex --name add-tests \
  --task "Add unit tests" --approval-mode full-auto -d

oaiso ps -a
oaiso logs add-tests --follow
```

**Background startup output (final line)**

```text
Container started: a1b2c3d4e5f6
```

- IDs vary. Find `add-tests` in `ps -a`; see the [worker list example](commands/worker.md#oaiso-ps).
- `running` means the worker is executing; check logs, artifacts and exit status for task success.
- Short tasks may already be `exited` when listed. Check their logs too.

- `full-auto` disables agent approval prompts.
- Mount permissions still determine which shared files are writable.
- Ctrl+C in `logs --follow` ends log following; the worker continues.
- To isolate branches, first review [worktree requirements](../workspace.md) (Japanese), then add `--worktree` and branch flags.

## 6. Stop, restart and remove

```bash
oaiso stop add-tests
oaiso start add-tests
oaiso stop add-tests
# Save results before removing the worker.
oaiso rm add-tests
```

**Example stop, restart and removal output**

```text
$ oaiso stop add-tests
Stopping add-tests...
$ oaiso start add-tests
add-tests
$ oaiso stop add-tests
Stopping add-tests...
$ oaiso rm add-tests
Removing add-tests...
```

- `Stopping...` and `Removing...` announce an attempt. Check exit code `0` and then `ps -a` to confirm completion.
- After stopping: `⏹ exited`. After restarting: `✅ running`. After removal: no row.
- With no workers, `ps -a` displays only column headers.

| Operation or condition | Result |
|---|---|
| Stop | Revoke the token; retain the container and snapshot |
| Restart | Recheck isolation and restore auth for the same worker ID/workspace |
| Remove | Clean up managed worktrees and networks; save results first |
| Foreground exit | Remove the worker unless `--keep` is set |
| Shared proxies | Keep running after workers exit |

- Restart reruns the main program; it does not resume suspended memory or execution state.
- `rm --force` also removes running workers; use `stop` then `rm` for normal cleanup.

## Next steps

| Task | Guide |
|---|---|
| Updates and cleanup | [Operations](../operations.md) (Japanese) |
| Diagnose errors | [Troubleshooting](../troubleshooting.md) (Japanese) |
| Configure Claude authentication | [API-key and subscription setup](claude-code.md) |
