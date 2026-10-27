# `oaiso run` — Start a Sandbox Container

> [日本語](../../commands/run.md) | **English**
>
> [← Command Reference](../commands.md)

| Goal | Invocation |
|---|---|
| Start Codex directly | `oaiso codex` |
| Start Claude Code directly | `oaiso claude` |
| Select image, worktree or mounts | `oaiso run --agent codex` / `--agent claude` |
| Authenticated shell | `oaiso run` (built-in default) |
| Run a command once | `oaiso run -- COMMAND...` |

| Item | Default or setting |
|---|---|
| Runtime | Linux x86_64, containerd, Dragonball; `--runtime kata` |
| Network | Dedicated bridges, namespaces and nftables block direct access; selected proxies provide routes |
| Default agent | `oaiso default codex`; add `--local` for this project; inspect with `oaiso default` |
| Images | Missing images are built from the matching template or default Dockerfile |
| Rebuilds | [auto_build](../configuration.md#auto_build) |
| Project settings | `[run]` / `[build]` in the current repository's `oaiso.toml`; `--project DIR` selects that directory |

- Initial setup: [quick start](../getting-started.md).
- Network: [network guide](../network.md).
- Custom images: [build](build.md).
- Configuration: [project settings](../configuration.md#project-settings).

## Launch Codex CLI directly

```bash
oaiso codex
oaiso codex "Inspect this repository"
oaiso codex exec --json "Summarize the changes"
```

- Arguments are forwarded verbatim to Codex.
- The current directory is mounted at `/workspace`, and the worker execs Codex without an interactive shell wrapper.
- Configure worker defaults under `[run]`; use `oaiso run --agent codex` for image, worktree, mount and detach options.
- Starting a fresh VM takes longer than running Codex on the host.

```bash
oaiso run [OPTIONS] [IMAGE [COMMAND...]]
oaiso run [OPTIONS] -- COMMAND...  # Run in the default image
```

- IMAGE and COMMAND follow Docker's positional order.
- Put Oaiso flags before IMAGE.
- With `--image` or `--template`, positional arguments are treated as the command.
- The command is equivalent to `--exec` without quoting it as one string.

```bash
oaiso run -d --name worker codex-dock:latest
oaiso run codex-dock:latest npm test -- --watch=false
oaiso run -- npm test -- --watch=false
```

---

## When startup fails

Stage-specific recovery hints are **unreleased (`main`)**. Startup failures from `run`, `codex`, and `claude` retain the cause and print `Next:` steps.

| Stage | Next check |
|---|---|
| `agent policy` | Validate the selected file or open `agent-policy configure` |
| `node` / `runtime` | Run `runtime-check` for KVM, cgroups, and the node; inspect service status |
| `authentication` / `auth proxy` | Run `status`, then review `login --help` and `init --help` |
| `image` | Inspect the build error and selected source; adapt the suggested build command to the actual Dockerfile |
| `worker launch` | Inspect `runtime-check`, `ps -a`, and `vm ps -a` before retrying |

- Explicit config, policy-file, and credential-profile selections are carried into relevant recovery commands.
- Hints do not execute repairs. See [readiness](status.md) and [setup/recovery](../../setup.md).

## Resource profiles

| Built-in name | CPU | Guest memory | Host reservation/limit | Guest/host PID ceiling |
|---|---|---|---|---|
| `standard` (default) | 1 | 2 GiB | 3 GiB | 512 |
| `small` | 1 | 1 GiB | 2 GiB | 512 |
| `compact` | 1 | 512 MiB | 768 MiB | 512 |

```bash
oaiso profile --node --json
oaiso run --profile-size small --agent codex
```

- Bare `profile` shows CLI defaults; `--node` queries live values, including operator-defined profiles.
- Reservations and limits are not observed memory usage; large installs/builds may exceed `compact`.
- [Operator PID configuration](../sandbox-features.md#operator-resource-profiles): `profile define` only generates JSON; it does not register a node profile.
- Review [memory measurements](../sandbox-features.md) before choosing.

## Options

- To separate authentication from the host, use `oaiso --auth-profile NAME run --proxy-admin-secret-file /private/admin-secret ...` or trusted global configuration.
- See [independent profile setup](auth.md#independent-profiles).

| Option | Short | Default | Description |
|---|---|---|---|
| `--runtime` | | `kata` | Worker isolation; `runc` is explicit compatibility mode |
| `--kata-runtime` | | `io.containerd.kata.v2` | Kata registration on dedicated containerd |
| `--image` | `-i` | `codex-dock:latest` | OCI image for the sandbox; also accepted positionally |
| `--template` | `-T` | | Sandbox image template (`plain`, `pwn`, etc.). Resolved to the template's image tag; an explicit `--image` always takes precedence. Auto-built if not present locally |
| `--pkg` | `-p` | | Additional packages (repeatable): `apt:<pkg>`, `pip:<pkg>`, `npm:<pkg>` |
| `--pkg-file` | | | Path to a package definition file (`packages.dock`) |
| `--project` | | `.` (current dir) | Directory to mount at `/workspace` |
| `--worktree` | `-w` | `false` | Use git worktree to isolate containers |
| `--branch` | `-b` | | Branch name to check out (requires `--worktree`) |
| `--new-branch` | `-B` | `false` | Create a new branch (requires `--worktree` and `--branch`) |
| `--name` | `-n` | auto-generated | Container name (defaults to `codex-<adj>-<noun>`) |
| `--agent` | | `shell` (configurable) | AI agent to launch: `shell` (auth-configured shell), `codex`, or `claude` |
| `--task` | `-t` | | Initial task prompt passed to the selected agent |
| `--exec` | `-e` | | Command to run once instead of the authenticated shell ([details](#--exec)) |
| `--approval-mode` | | `suggest` | Agent approval mode ([details](#--approval-mode)) |
| `--full-auto` | | `false` | **Deprecated**: use `--approval-mode full-auto` |
| `--model` | `-m` | | Model name passed to the selected agent |
| `--read-only` | | `false` | Mount project directory read-only |
| `--read-only-rootfs` | | `false` | Make the image rootfs read-only; use tmpfs for HOME and temporary files |
| `--profile-size` | | `standard` | Kata VM resource profile (`standard`, `small`, or `compact`) |
| `--no-git-metadata` | | `false` | Do not automatically share linked worktree Git metadata |
| `--mount` | | | Additional host directory to mount (repeatable): `<host-path>:<container-path>[:ro\|rw]` ([details](#--mount)) |
| `--no-internet` | | `false` | Disconnect general egress; keep fixed AI API routes |
| `--keep` | | `false` | Keep the container and its per-worker network after a foreground run exits (default: remove them so networks don't accumulate) |
| `--force-recreate-proxy` | | `false` | Recreate the Auth Proxy when its credential state differs from the host even if workers are running (existing workers lose authentication) |
| `--token-ttl` | | `3600` | Auth Proxy token TTL in seconds |
| `--agents-md` | | | Path to `AGENTS.md` file |
| `--detach` | `-d` | `false` | Run in background (no log output); the old `-D` still works |
| `--parallel` | `-P` | `1` | Number of parallel workers |
| `--shell` | `-s` | `false` | Start a raw (unauthenticated) shell, bypassing `entrypoint.sh` (debugging) |
| `--shell-type` | | auto-detected from `$SHELL`, else `bash` | Interactive shell to launch ([details](#--shell-type)) |
| `--user` | | `current` | Container execution user ([details](#--user)) |

### Network, isolation and startup options

| Flag | Default | Purpose or limit |
|---|---|---|
| `--policy-preset` | Unset | `offline`, `test`, `dependencies`, `api`; never widens host grants |
| `--egress-allow-domain` | Unset | Narrow per-worker egress; repeatable |
| `--deny-read` / `--deny-write` | Empty | Mask/read-only existing paths below `/workspace`; repeatable |
| `--watch-polling` | `false` | Chokidar/Watchpack 1-second polling |
| `--ssh-proxy` / `--jvm-proxy` | `false` | SSH CONNECT or JVM proxy settings |
| `--ca-bundle` | Unset | Host path to additional worker PEM roots |
| `--clone-dir` | Unset | Independent clone at a new absolute path; not with parallel launch |
| `--environment-lock` | Unset | Validate lock and image digest |
| `--image-id` | Unset | Require a `sha256:...` image digest |
| `--start-hook` | Empty | Guest shell hook before startup; repeatable |
| `--readiness` | Unset | Guest shell readiness probe |
| `--readiness-timeout` | 0 (effective 60s) | Hook/probe deadline |
| `--max-lifetime` / `--idle-timeout` / `--retention` | `0` | Lifetime, idle, exit retention; durations such as `30m` or `2h`; requires `reap` |
| `--protect-main` | `false` | v0.0.4. Protect a live primary process from idle collection; maximum lifetime still applies |
| `--max-requests` / `--max-concurrent` | `0` | v0.0.4. Per-execution request/concurrency caps at the auth proxy |
| `--max-transfer-bytes` / `--max-observed-tokens` | `0` | v0.0.4. Combined body-byte/observed-token caps. [Precision and conditions](../guides/usage-limits.md) |
| `--proxy-admin-url` | `http://127.0.0.1:18081` | Host admin API |
| `--proxy-container-url` | `http://codex-auth-proxy:18080` | Worker fixed AI routes |
| `--http-proxy-url` | `http://codex-http-proxy:18082` | Worker general egress |
| `--proxy-admin-secret` | Unset | Direct admin-secret value; prefer file-based authority |
| `--proxy-admin-secret-file` | Unset | Owner-only 0600 admin-secret file |

- Detailed contracts: [additional sandbox controls](../sandbox-features.md).
- Idle does not measure guest CPU/filesystem activity. Use `--protect-main` for primary work or `oaiso activity` for caller-managed renewal.

- When the host and Auth Proxy credential modes differ, `run` automatically recreates the Auth Proxy if no workers are running.
- If workers are running it stops with an error so their in-memory tokens are not invalidated.
- Pass `--force-recreate-proxy` to recreate it anyway, then create new workers if their pinned proxy IP changed.

---

## Docker CLI differences

- `run -d`, `run IMAGE COMMAND`, `ps -a`, `logs -f`, `start`, `stop`, `rm -f`, and `build -t` use Docker style.
- Replace the old `run -d PATH` project shorthand with `run --project PATH`.
- The old `-D` still means detach.
- In this CLI, `run -p` selects packages, `run -i` selects an image, `run -t` sets an agent task, and `-v` enables verbose output.
- Host port publishing is not supported.
- Use `--mount HOST:CONTAINER[:ro|rw]` for extra bind mounts.
- The current project is shared at `/workspace` by default, and a foreground worker is removed after exit unless you pass `--keep`.

---

## `--approval-mode`

- Controls the selected agent's approval settings.
- The selected worker runtime provides the outer isolation boundary.

| Value | Codex CLI flag | Claude Code flag | Behavior |
|---|---|---|---|
| `suggest` (default) | No approval flag | No approval flag | Use the agent's defaults |
| `auto-edit` | `--ask-for-approval on-request` | `--permission-mode acceptEdits` | Codex decides when to ask; Claude accepts file edits |
| `full-auto` | `--ask-for-approval never` | `--dangerously-skip-permissions` | Skip agent approval prompts |
| `danger` | `--dangerously-bypass-approvals-and-sandbox` | `--dangerously-skip-permissions` | Skip agent approvals and Codex's inner sandbox; preserve Kata and host restrictions |

- In a verified Kata worker, Codex's inner sandbox is set to `danger-full-access` to avoid nested namespace/seccomp conflicts.
- Approval settings are separate; noninteractive exec may choose its own approval behavior.
- Shared files remain writable according to mount permissions.
- See [security](../security.md).

---

## `--exec`

- The configured default agent does not apply to `oaiso run IMAGE COMMAND`, `oaiso run -- COMMAND`, or `--exec`; these run the command.
- An explicit `--agent codex` or `--agent claude` takes priority over the command.
- Use `--agent shell` to request the authenticated shell when a different default is configured.

- Runs a single command inside the sandbox instead of an interactive shell.
- Passed into the container as the `DOCK_EXEC_CMD` environment variable, which `entrypoint.sh` only consults in its no-agent branch (the auth-configured shell case).

- If `--agent codex` or `--agent claude` is given, the agent takes priority and `--exec` is ignored.
- With `--agent shell`, the command runs.
- Combining it with `--shell` (the raw debug shell that bypasses `entrypoint.sh`
  entirely) is an error, since `entrypoint.sh` — and therefore `--exec` — never runs.
- The command runs via the shell selected by `--shell-type` (default `bash`), through its `-c` flag.
- Mutually exclusive with a trailing `-- command...` on the command line — use whichever is
  more convenient; the latter avoids having to quote a multi-word command as a single string.

```bash
# Run one command in the auth-configured environment, then exit
oaiso run --exec "ls -la"

# Equivalent, without needing to quote the whole command as one string
oaiso run -- ls -la

# Run in the background
oaiso run --exec "npm test" --detach

# --agent takes priority over --exec, so this launches Codex CLI, not `ls -la`
oaiso run --agent codex --exec "ls -la"
```

---

## `--shell-type`

- Selects the shell binary used for the sandbox's interactive shell: both the auth-configured shell (when `--agent` is omitted) and the `--shell` debug mode.
- Passed into the container as the `DOCK_SHELL` environment variable, which `entrypoint.sh` reads.

| Value | Behavior |
|---|---|
| `bash` | Launches bash |
| `zsh` | Launches zsh; requires zsh to be installed in the image |

- When neither the flag nor a config value (`default_shell_type` / `run.shell_type`) sets a value, it is auto-detected from the basename of the host's `$SHELL` environment variable — `zsh` maps to `zsh`, anything else (including an unset or unrecognized `$SHELL`) falls back to `bash`. **This auto-detection does not apply to `--shell` debug mode**, which always defaults to `bash` when the flag is omitted: `--shell` overrides the container's `Entrypoint` directly with the shell binary, bypassing `entrypoint.sh` entirely, so there's no way for it to fall back if that binary isn't present in the image — the engine fails to start the container.
- Pass `--shell --shell-type zsh` explicitly if you want zsh there.

- The built-in `plain` template includes zsh, and `pwn` inherits it (`FROM codex-dock:latest`), so `--shell-type zsh` works out of the box with either.
- For a custom template, install zsh in its Dockerfile to use it.
- Outside `--shell` mode (i.e. the no-agent shell and `--exec`), `entrypoint.sh` checks whether the requested shell binary actually exists before using it and logs a warning and falls back to bash if not, so a missing zsh there never fails the container outright.

> The job-control wrapper that lets Ctrl+Z suspend an agent (`--agent
> codex`/`claude`) back to a shell prompt (`launch_interactive`) always uses
> bash internally, regardless of `--shell-type` — it relies on bash's
> `PROMPT_COMMAND`. `--shell-type` only affects the no-agent interactive shell
> and `--shell` debug mode.

```bash
# Launch the interactive shell with zsh
oaiso run --shell-type zsh

# Raw debug shell with zsh (bypasses entrypoint.sh)
oaiso run --shell --shell-type zsh
```

The default can be changed via `default_shell_type` (global) / `run.shell_type`
(`run`-only) in `~/.config/codex-dock/config.toml` — see the
[Configuration Reference](../configuration.md#default_shell_type).

---

## `--user`

Runs the container process as a specific uid:gid for filesystem permission consistency.

| Value | Behavior |
|---|---|
| `""` (omitted) | Image default user (`codex` uid:1001) |
| `current` | Automatically uses the uid:gid of the user running `oaiso` |
| `dir` | Automatically uses the uid:gid of the `--project` directory owner |
| `uid` or `uid:gid` | Explicit value (e.g., `1000`, `1000:1000`) |

---

## `--mount`

- `--mount` bind-mounts an arbitrary host directory into the container, in addition to `/workspace` (`--project`).
- Repeat the flag to mount multiple directories.

```
--mount <host-path>:<container-path>[:ro|rw]
```

- `<host-path>`: directory on the host (relative paths are resolved against the current
  working directory when `oaiso run` is invoked)
- `<container-path>`: absolute path inside the container; `/workspace` is reserved for
  `--project` and cannot be used here
- `:ro` / `:rw`: read-only / read-write (defaults to `rw` when omitted)
- Mounting the same `<container-path>` more than once is an error

```bash
# Mount a shared dataset directory read-only
oaiso run --mount /home/user/datasets:/data:ro

# Mount multiple directories, mixing read-write and read-only
oaiso run --mount ./cache:/cache --mount /opt/models:/models:ro
```

> Additional mounts can be independently read-only regardless of `/workspace`.
> `--read-only` only affects the `/workspace` mount.

- `--read-only-rootfs` does not change `/workspace` or additional mount permissions.
- Install needed packages in the image first; startup package installation is rejected.
- HOME, `/tmp`, and `/var/tmp` use temporary tmpfs storage that does not survive worker stop, restart, or removal.
- With `--no-git-metadata`, a linked worktree's shared Git metadata is not mounted; provide private metadata and mounts if Git must run inside the VM.

- `oaiso profile --json` reports the CPU, guest RAM, host limit, and PID limit of each profile.
- `small` provides 1 CPU, 1 GiB guest RAM, and a 2 GiB host limit; `standard` provides 1 CPU, 2 GiB guest RAM, and a 3 GiB host limit.
- `--profile-size small` requires Kata.
- Measure actual memory use and startup performance on the execution host.

- This can also be set as a default via `mounts` in the `[run]` section of the config file (`~/.config/codex-dock/config.toml`); an explicit `--mount` always overrides it.
- See [`doc/en/configuration.md`](../configuration.md#runmounts) for details.

```toml
[run]
mounts = ["/home/user/datasets:/data:ro", "./cache:/cache"]
```

## Codex SQLite startup error

With the pinned Codex 0.163.0 tested on 2026-10-26, an ordinary Kata rootfs HOME fails to start the interactive UI with `disk I/O error (code: 5386)` for `state_5.sqlite`. The `virtio_fs_cache="none"` setting needed for coherent shared workspaces conflicts with SQLite WAL's writable shared mmap. See the [verification record](../../testing.md#ux改善の検証2026-10-26).

For trials that do not require HOME settings or history to survive stopping the worker, explicitly select the existing temporary HOME mode. Install required packages in the image first. Shared files in `/workspace` retain their usual persistence.

```bash
oaiso run --read-only-rootfs
```

This option is not applied automatically. If durable Codex state is required, wait for VM disk storage support instead of using this workaround. Changing the shared workspace cache settings is not an adopted workaround.

---

## Package Format

```
apt:libssl-dev          # install via apt
pip:requests            # install via pip
npm:lodash              # install via npm
libssl-dev              # no prefix → treated as apt (default)
```

---

## Parallel Workers

```bash
oaiso run --parallel 2 --worktree --branch myfeature
```

- The default node limit is 2 simultaneous workers.
- Creates branches: `myfeature-1`, `myfeature-2`.

---

## Examples

```bash
# Basic run (mount current directory)
oaiso run

# Run with task in full-auto mode
oaiso run --agent codex --task "Write unit tests" --approval-mode full-auto

# Use git worktree on a feature branch
oaiso run --worktree --branch feature-auth --new-branch

# Run 2 parallel workers
oaiso run --parallel 2 --worktree

# Install additional packages
oaiso run --pkg "apt:libssl-dev" --pkg "pip:requests"

# Run in background
oaiso run --agent codex --task "Refactor" --approval-mode full-auto --detach

# Secure run: read-only, no internet
oaiso run --agent codex --read-only --no-internet --task "Code review"

# Use a template image (auto-built if not present)
oaiso run --image codex-dock:pwn

# Same thing via --template
oaiso run --template pwn

# Works for your own templates under ~/.config/codex-dock/templates/ too
oaiso run --template mytool
```

---

## Related Documentation

- [Quick Start](../getting-started.md)
- [Architecture Overview](../architecture.md) — Startup sequence
- [Auth Proxy Specification](../auth-proxy.md)
- [Network Specification](../network.md)
- [Configuration Reference](../configuration.md)

- `--observe-dir PATH`: v0.0.4. Save redacted launch timings. [Diagnostics](../guides/observability.md).

- `--checkpoint ID`: v0.0.4. Start a new Kata VM from a [disk checkpoint](../guides/disk-checkpoint.md), with new mounts/authentication/policies.
