# Configuration

[日本語](../configuration.md) | **English**

- User settings live in `~/.config/codex-dock/config.toml`.
- Use `oaiso --config PATH COMMAND` to select another file.
- `make install-config` installs the example without overwriting an existing file.

## Project settings

- **`oaiso.toml` is optional. Workers can start without it using user settings and built-in defaults.**
- Create it to customize a project or share settings with a team. The filename is lowercase.
- Without a project file, `project init` and `project check` are unnecessary.
- Run `oaiso project init` at the repository root to create `oaiso.toml`, and `oaiso project check` to validate it. Commit it to share settings.
- `init`, `run`, `codex`, and `build` read the file from the current directory; `run --project DIR` reads `DIR/oaiso.toml`.
- Parent directories are not searched.

```bash
oaiso project init
oaiso project check
git add oaiso.toml
```

**Example output**

```text
$ oaiso project init
/home/user/my-project/oaiso.toml
$ oaiso project check
Project config: /home/user/my-project/oaiso.toml
build.template = plain
run.image = codex-dock:latest
```

- Paths vary. `project check` validates settings without starting a VM or connecting to an AI provider.
- An old `codex-dock.toml` blocks loading until [renamed](../migration.md#プロジェクト設定を-oaisotoml-に変更する) (Japanese); its restrictions are never silently discarded.

You can also change the agent started by a plain `oaiso run` from the CLI. `oaiso default` shows the effective value and where it came from.

```bash
oaiso default codex          # global config
oaiso default claude --local # this project only
oaiso default shell          # restore the authenticated shell
```

- `--local` creates or updates `oaiso.toml` in this directory.
- Commit the file to share it.
- An explicit `--agent`, `--exec`, `IMAGE COMMAND`, or `-- COMMAND` overrides the default.
- `oaiso codex` always starts Codex.

```toml
[run]
image = "codex-dock:latest"
approval_mode = "suggest"

[build]
template = "plain"
```

- For a custom image, commit a project Dockerfile and use matching `run.image` and `build.tag` values.
- The project root is the build context.
- A Dockerfile using `FROM codex-dock:latest` automatically builds the bundled base image on a fresh clone.

```toml
[run]
image = "my-project:dev"
model = "your-model"

[build]
dockerfile = "Dockerfile.codex-dock"
tag = "my-project:dev"
```

- `packages.dock` is also loaded automatically.
- Pin dependency versions in the project Dockerfile or package list when repeatability matters.
- Project templates must be built-in or unchanged copies of built-in templates; commit a Dockerfile instead of depending on a locally edited template.
### Allowed project keys

| Category | Keys or values |
|---|---|
| Image and template | `run.image`, `run.template` |
| Agent and model | `run.agent` (`shell`, `codex`, `claude`), `run.model` |
| Approval | `run.approval_mode` (`suggest`, `auto-edit`) |
| Resource budget | `run.profile_size` (built-in `standard`, `small`, `compact`) |
| File restrictions | `run.read_only`, `run.read_only_rootfs`, `run.no_git_metadata`, `run.deny_read`, `run.deny_write` |
| Network restrictions | `run.no_internet`, `run.egress_allow_domains`, `run.policy_preset` |
| Lifecycle | `run.max_lifetime`, `run.idle_timeout`, `run.retention` |
| Startup | `run.start_hooks`, `run.readiness`, `run.readiness_timeout` |
| Watchers | `run.watch_polling` |
| Build | `build.dockerfile`, `build.template`, `build.tag` |

| Validation | Contract |
|---|---|
| Project `read_only` / `no_internet` | Only `true`; cannot relax host restrictions |
| `run.image` / `build.tag` | Must match |
| Credentials, proxy, runtime, token lifetime, extra host mounts | Global configuration or explicit flags only |
| Unknown keys | Fail validation |

### Startup syntax and restriction merging

| Location | `start_hooks` | `readiness` |
|---|---|---|
| Global `config.toml` | Shell strings, e.g. `["npm ci"]` | Shell string, e.g. `"test -d node_modules"` |
| Project `oaiso.toml` | Array of argv arrays, e.g. `[["npm", "ci"]]` | Argv array, e.g. `["test", "-d", "node_modules"]` |

- Project hooks run inside the guest; review cloned settings and shared paths before `init` or `run`.
- File denials append to host restrictions.
- Without explicit CLI overrides, project lifetime/idle values can only select a shorter positive limit; a positive project retention value is applied.
- Enforcement: [lifecycle and reproducibility](sandbox-features.md#lifecycle-and-reproducibility).

- `init` and `run` build missing images.
- For an existing project image, automatic rebuilds check the Dockerfile timestamp; run `oaiso build` after changing copied files or dependencies alone.

- Products can use [`PrepareProject` in the Go API](go-api.md) to validate the same project file and build missing images.
- The Go API does not load global settings; the product must apply host settings and authorization.
- It copies `egress_allow_domains` into `LaunchRequest.EgressAllowDomains`.

- Normal precedence: **explicit CLI flags > project config > global config > built-in defaults**.
- Project `read_only` and `no_internet` only add restrictions.

## Example

```toml
default_image = "codex-dock:latest"
proxy_image = "codex-dock-proxy:latest"

[run]
runtime = "kata"
user = "current"
approval_mode = "suggest"
token_ttl = 3600

[proxy]
container_url = "http://codex-auth-proxy:18080"
admin_url = "http://127.0.0.1:18081"
forward_allow_domains = ["github.com", "registry.npmjs.org"]
forward_allow_ports = [80, 443]
```

- Use the [credential sources](auth-proxy.md#credential-sources) for API keys and OAuth files.
- Explicit flags override config.
- This example uses legacy host authentication. For profiles, put `auth_profile` before the first table and merge `admin_secret_file` into the existing `[proxy]` table; follow [profile selection](commands/auth.md#fix-the-selection-for-daily-operations).
- Command-specific settings override global defaults.
- Only the documented environment overrides should be relied on; arbitrary `CODEX_DOCK_*` names are not supported.

## Global settings

| Key | Default | Purpose |
|---|---|---|
| `auth_profile` | Unselected | Independent profile; `--auth-profile` |
| `auth_profile_dir` | `~/.config/codex-dock/profiles` | Profile storage root; `--auth-profile-dir` |
| `default_image` | `codex-dock:latest` | Worker OCI image |
| `proxy_image` | `codex-dock-proxy:latest` | Image for both proxy roles |
| `default_template` | Unset | Template for run/build |
| `default_token_ttl` | `3600` | Token lifetime in seconds |
| `auto_build` | `true` | Rebuild changed on-disk templates |
| `default_shell_type` | Detect from host shell | bash/zsh |
| `verbose`, `debug` | `false` | CLI logging |

### `auto_build`

- Compare the template Dockerfile mtime to the image creation timestamp.
- Only on-disk templates are checked.
- Purely embedded templates have no file to compare.
- Missing images are built regardless of this setting.

### `default_shell_type`

- Without a setting, host `$SHELL` selects zsh when its basename is zsh, otherwise bash.
- Debug `--shell` defaults to bash unless explicitly overridden.

## Run settings

Under `[run]`:

| Key | Default or inherited value | Flag |
|---|---|---|
| `runtime` | `kata` | `--runtime` |
| `kata_runtime` | `io.containerd.kata.v2` | `--kata-runtime` |
| `profile_size` | `standard` | `--profile-size` |
| `image` | `default_image` | `--image` |
| `template` | `default_template` | `--template` |
| `agent` | `shell` | `--agent` |
| `token_ttl` | `default_token_ttl` | `--token-ttl` |
| `approval_mode` | `suggest` | `--approval-mode` |
| `user` | `current` | `--user` |
| `shell_type` | `default_shell_type` | `--shell-type` |
| `auto_build` | `auto_build` | Config only |
| `mounts` | Empty | `--mount` |
| `watch_polling` | `false` | `--watch-polling` |
| `model` | Unset | `--model` |
| `read_only` | `false` | `--read-only` |
| `read_only_rootfs` | `false` | `--read-only-rootfs` |
| `no_git_metadata` | `false` | `--no-git-metadata` |
| `no_internet` | `false` | `--no-internet` |
| `egress_allow_domains` | Empty | `--egress-allow-domain` |
| `policy_preset` | Unset | `--policy-preset` |
| `deny_read` / `deny_write` | Empty | `--deny-read` / `--deny-write` |
| `max_lifetime` / `idle_timeout` / `retention` | `"0s"` | Matching hyphenated flags; `"30m"`, `"2h"`; requires `reap` |
| `protect_main` | `false` | Unreleased main. `--protect-main`; protect the primary process from idle collection |
| `max_requests` / `max_concurrent` | `0` | Unreleased main. `--max-requests` / `--max-concurrent` |
| `max_transfer_bytes` / `max_observed_tokens` | `0` | Unreleased main. `--max-transfer-bytes` / `--max-observed-tokens`. [Limits](guides/usage-limits.md) |
| `environment_lock` | Unset | `--environment-lock` |
| `image_id` | Unset | `--image-id` |
| `start_hooks` | Empty | `--start-hook` |
| `readiness` | Unset | `--readiness` |
| `readiness_timeout` | `"0s"` (effective 60s) | `--readiness-timeout` |
| `ssh_proxy` / `jvm_proxy` | `false` | `--ssh-proxy` / `--jvm-proxy` |
| `ca_bundle` | Unset | `--ca-bundle`; additional worker PEM roots |

- These are trusted global settings; project keys are limited to the [allowlist](#allowed-project-keys).
- Durations use Go syntax, such as `24h`; `1d` is not supported.

Explicit `--image` wins over template image selection.
`runtime = "runc"` is an explicit compatibility mode on the same containerd node, without VM isolation.

### `run.mounts`

```toml
[run]
mounts = ["/home/user/datasets:/data:ro", "./cache:/cache:rw"]
```

- Relative host paths resolve from the command's working directory.
- An explicit `--mount` replaces the config array.
- `--read-only` controls the workspace; extra mounts have their own permissions.

- The legacy `network_name` setting is not used by current network commands, which use `dock-net-proxy`.
- Select a proxy network with `proxy run --network`.

## Build settings

- Under `[build]`, `template` inherits `default_template` and `tag` sets the output image name.
- Without an explicit tag, templates use their own tag; a plain build uses `codex-dock:latest`.
- See [build](commands/build.md).

## Proxy settings

- Trusted global `auth_profile` selects an independent profile; `auth_profile_dir` overrides its storage root (default `~/.config/codex-dock/profiles`).
- Corresponding flags are `--auth-profile` / `--auth-profile-dir`.
- An explicit selection disables host credential fallback.
- These keys are not allowed in project configuration.
- See [creation and startup](commands/auth.md#independent-profiles).

- Admin-secret files must be owner-only 0600 regular files.
- Supplying both a file and a value, including configured/environment values, fails.
- Explicit run secret flags override configuration.
- Keep these files and profile storage outside worker shares.
- Drain workers and explicitly recreate the proxy when changing admin authority.

Under `[proxy]`:

| Key | Default | Used by |
|---|---|---|
| `max_connections` / `max_connections_per_source` | `0` | Unreleased main. `proxy run --max-connections` / `--max-connections-per-source` |
| `network_bytes_per_second` | `0` | Unreleased main. `--network-bytes-per-second`. [Scope and behavior](guides/resource-limits.md) |
| `container_url` | `http://codex-auth-proxy:18080` | `run --proxy-container-url` |
| `admin_url` | `http://127.0.0.1:18081` | `run --proxy-admin-url`, worker management |
| `admin_secret` | Unset | Admin client; also supply the server secret with `proxy run --admin-secret` |
| `admin_secret_file` | Unset | Owner-only 0600 file; `proxy run --admin-secret-file`, `run --proxy-admin-secret-file`, init, worker management and TUI |
| `forward_default_deny` | `true` | `--forward-default-deny`; deny general egress when both domain and CIDR grants are empty |
| `forward_allow_domains`, `forward_deny_domains` | Empty | `--forward-allow-domain`, `--forward-deny-domain` |
| `forward_allow_cidrs`, `forward_deny_cidrs` | Empty | `--forward-allow-cidr`, `--forward-deny-cidr` |
| `forward_allow_ports`, `forward_deny_ports` | Empty | `--forward-allow-port`, `--forward-deny-port` |
| `allowed_models` | Empty (unrestricted) | `proxy run --allow-model`; exact upstream IDs; constrained tokens reject WebSocket |
| `upstream_proxy` | Unset | `proxy run --upstream-proxy`; explicit HTTP(S) upstream proxy |
| `ca_bundle` | Unset | `proxy run --ca-bundle`; additional proxy PEM roots |

- `run.ca_bundle` configures the worker; `proxy.ca_bundle` configures the proxy. BuildKit registry CA trust is configured separately by the administrator.

- The data URL is used by workers; the admin URL is used by the host CLI.
- General egress uses `run --http-proxy-url`.
- The legacy `firewall.proxy_container_url` key is still read, but use `[proxy]` for new config.

- On upgrade, configure host domain/CIDR grants before creating new workers, or explicitly opt into the previous behavior with `forward_default_deny = false`.
- Existing proxies, workers and CONNECT tunnels are not changed automatically.
- Missing explicitly selected configuration files, parse errors and invalid default-deny values fail CLI startup.

- Rules apply only to general egress.
- Denials win and all nonempty allowlists must match.
- `forward_default_deny` defaults to `true`, denying empty destination allowlists.
- Explicitly set `false` (CLI `--forward-default-deny=false`) to retain public-destination access without grants.
- A port alone is not a destination grant.
- Bare `example.com` includes subdomains; `=example.com` matches that name exactly, for both allow and deny rules.
- Private address blocking cannot be overridden by an allowlist.
- Policy denials return HTTP 403 for HTTP requests and CONNECT.
- Domain/port checks run before DNS; resolved addresses are checked before TCP connection.
- Direct worker traffic remains isolated at the network layer.
- See the [verification record](../testing.md#一般通信の既定拒否への変更2026-10-22) for automated checks and successful real Kata acceptance results.
- Recreate the proxy to apply changes.
- See [policy rules](../proxy-policy.md) (Japanese).

## Environment overrides

| Variable | Purpose |
|---|---|
| `CODEX_DOCK_RUNTIME` | Override `run.runtime` |
| `CODEX_DOCK_KATA_RUNTIME` | Override `run.kata_runtime` |
| `CODEX_DOCK_PROXY_ADMIN_SECRET` | Admin client secret |
| `CODEX_DOCK_KATA_NODE_SOCKET` | Absolute node socket path |
| `CODEX_DOCK_STATE_DIR` | Private absolute path for cleanup/retry records |
| `XDG_STATE_HOME` | State root when no dedicated state directory is set |
| `BUILDKIT_HOST` | Builder address; default `unix:///run/codex-dock-buildkit/buildkitd.sock` |
| HTTP/HTTPS/NO/ALL proxy variables, upper/lowercase | BuildKit build args |
| `CODEX_PROXY_CERT` | CA file passed as BuildKit `proxy_ca` secret |
| `OPENAI_API_KEY`, `ANTHROPIC_API_KEY` | Host proxy credentials |
| `CLAUDE_CODE_OAUTH_TOKEN` | Host auth proxy subscription access token from `claude setup-token` |
| `CODEX_DOCK_CODEX_VERSION` / `CODEX_DOCK_CLAUDE_VERSION` | Exact agent CLI version override for builds |

Runtime overrides take precedence over config, below explicit flags.
`DOCKER_HOST` and Docker contexts are not used.

- Claude Code accepts host `CLAUDE_CODE_OAUTH_TOKEN` for subscription authentication.
- Build overrides include `CODEX_DOCK_CLAUDE_VERSION`.
- See [Claude Code](claude-code.md) for source precedence and upgrade steps.

## Host service configuration

- Paths under `/etc/codex-dock/` below describe manual installation; automatic setup uses managed configuration under `/opt/codex-dock/releases/`.
- Check actual arguments with `systemctl cat codex-dock-node codex-dock-containerd codex-dock-buildkit`.

- Host services are configured separately from user settings: `node serve --help`, `/etc/codex-dock/containerd.toml`, `kata.toml`, `kata-assets.json` and `buildkit.toml`.
- `[kata].node_socket` configures the verification client.
- If using a custom path, set `CODEX_DOCK_KATA_NODE_SOCKET` to the same value for the engine client.
- See [host setup](../kata.md) (Japanese).

## Credential File Locations

| Path | Contents |
|---|---|
| `~/.config/codex-dock/apikey` | Saved OpenAI API key |
| `~/.codex/auth.json` | Codex login |
| `~/.config/codex-dock/anthropic-apikey` | Saved Anthropic API key |
| `~/.claude/.credentials.json` | Claude Code login |

- Do not include their contents in logs or images.
- See [auth proxy](auth-proxy.md).

- Host `[proxy].allowed_models` optionally allows exact upstream model IDs.
- Empty preserves legacy behavior; enabled policies deny unsupported AI routes and WebSockets.
- Explicit `proxy run --allow-model` flags take precedence.
- Recreate the proxy and check [supported routes](sandbox-features.md#model-authorization-and-staged-startup).

## Operator resource profiles

- [Additional FD and block I/O profile fields](guides/resource-limits.md): unreleased main. Existing built-in profiles retain their defaults.

- `run.profile_size` selects a built-in or node-registered name.
- The root node operator registers additional budgets through `node serve --resource-profiles /etc/codex-dock/resource-profiles.json`, independently of user TOML.
- See [bounds, guest assets and immutable reservations](sandbox-features.md#operator-resource-profiles).

- Generate registration JSON with `oaiso profile define compact-256 --pids-limit 256`; query effective values with `oaiso profile --node --json`.
- PID limits apply to both host and guest; generating JSON alone does not register it on the node.

- Generate registration JSON with `oaiso profile define compact-256 --pids-limit 256` and inspect active PID ceilings with `oaiso profile --node --json`.
- Limits apply on both host and guest.

- `proxy.credential_routes`: Unreleased main. Absolute [broker configuration file](guides/credential-broker.md) path; trusted host configuration only.
