# `oaiso auth` — Authentication Management

> [日本語](../../commands/auth.md) | **English**
>
> [← Command Reference](../commands.md)

| Goal | Command or setting |
|---|---|
| Store a product-specific API key | `auth profile set NAME --provider openai\|anthropic` |
| Import OAuth credentials | `auth profile import NAME --credentials-stdin --format codex\|claude\|credentials` |
| Inspect redacted metadata | `auth profile show NAME` / `auth profile list` |
| Select a profile | `--auth-profile NAME` or global `auth_profile` |
| Keep legacy host integration | Leave profiles unselected; use `auth show` / `auth set` |

## Keyring

- `oaiso auth keyring check`: store/read/delete probe without displaying secrets.
- `oaiso auth keyring session [-- COMMAND...]`: private headless session.
- [Dependencies, unlocking and session lifetime](../guides/keyring.md).

## Independent profiles

### Behavior and storage

| Item | Contract |
|---|---|
| Separation | Never automatically reuse host CLI logins or authentication variables |
| Missing profile or unsafe permissions | Fail; never fall back to host credentials |
| Example file | `~/.config/codex-dock/profiles/work/work.json` |
| Root and profile directories | `0700` |
| JSON and lock files | `0600`, plaintext |
| Override the root | `--auth-profile-dir` or trusted global `auth_profile_dir` |
| WSL | Private Linux-side storage, outside shared/synced folders |
| Input | No keys in arguments; noninteractive input requires explicit stdin |

### Register an API key

```bash
oaiso auth profile set work --provider openai  # hidden TTY input
oaiso auth profile set claude-work --provider anthropic
oaiso auth profile show work
oaiso auth profile list
```

- Interactive: hidden TTY input.
- Noninteractive: `--key-stdin` with a protected input source.
- This command only saves the key. v0.0.3+ [login](login.md) also checks the live API connection during registration.
- Success: `Profile created (plaintext, 0600).` See the [profile inspection example](../guides/authentication.md#3-configure-admin-authority-and-profile-selection).

### Import OAuth

```bash
oaiso auth profile import work-oauth --format codex --credentials-stdin < /private/product-login/auth.json
oaiso auth profile import claude-oauth --format claude --credentials-stdin < /private/product-login/.credentials.json
```

| `--format` | Input |
|---|---|
| `codex` | Native `tokens` and `last_refresh` |
| `claude` | `claudeAiOauth` |
| `credentials` (default) | `credentialstore.Credentials` JSON; can contain both providers |

- `--credentials-stdin`: required.
- Source files: never automatically discovered or deleted.
- This command explicitly imports existing JSON. v0.0.3+ [login](login.md) authenticates through a dedicated Kata.
- Success: `Profile imported (plaintext, 0600).` Check for `oauth` on the registered provider's line.

### Create admin authority and start the proxy

- Store admin authority in a separate `0600` file.
- Pass its path, keeping the secret value out of process arguments.
- The generator refuses to overwrite an existing file; skip generation when reusing an existing secret.
- Run on the host, outside the worker.

```bash
python3 - <<'PY'
import os, pathlib, secrets
path = pathlib.Path.home() / '.config/codex-dock/proxy-admin-secret'
path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), 'w') as f:
    f.write(secrets.token_hex(32) + '\n')
PY
```

- Successful generation produces no output. Check only permissions and location:

```bash
stat -c '%a %n' "$HOME/.config/codex-dock/proxy-admin-secret"
```

```text
600 /home/user/.config/codex-dock/proxy-admin-secret
```

- `/home/user` is replaced by your HOME. You do not need to display the secret contents.

### Fix the selection for daily operations

**Location: trusted global `~/.config/codex-dock/config.toml`.**

- Create the file if absent; merge these keys into existing settings without replacing unrelated values.
- Put `auth_profile` before the first table, such as `[run]` or `[proxy]`.
- If `[proxy]` already exists, add `admin_secret_file` there; do not duplicate the table.
- Replace `work` and `/home/user/...` with the real profile name and absolute path; do not assume `$HOME` or `~` expands inside TOML.

```toml
auth_profile = "work"
[proxy]
admin_secret_file = "/home/user/.config/codex-dock/proxy-admin-secret"
```

| Command | Selection or output |
|---|---|
| `init`, `run`, `start`, TUI | Share the global settings above |
| `oaiso run` | Also accepts explicit profile flags |
| `oaiso codex` / `oaiso claude` | Forward arguments to the agent; select profiles through global settings |
| `auth show` | Redacted metadata for the selected profile |

- Never put authentication configuration in the project's `oaiso.toml`.

**After configuring, run one preparation command as your normal user (any directory for release installations):**

| Stored credentials | Preparation |
|---|---|
| OpenAI | `oaiso init --agent codex` |
| Anthropic | `oaiso init --agent claude` |

- Then change to your project and run `oaiso codex` or `oaiso claude`.
- An explicit one-off invocation is `oaiso --auth-profile work run --agent codex --proxy-admin-secret-file "$HOME/.config/codex-dock/proxy-admin-secret"`. These flags do not carry over to later `stop`, `start` or TUI commands; use global settings for daily operations.

### Refresh, switching and sharing

| Operation or boundary | Behavior |
|---|---|
| Creation/import | Create-only; never overwrite an existing profile |
| Normal OAuth refresh | CAS-save to the same Store; preserve connection generation |
| Re-login | Import under a new profile name |
| Retained workers | No reauthorization under another profile or login generation |
| Auth proxy | Only the selected directory is mounted read-write for refresh |
| Workers and egress | Never receive credential storage |
| Workspaces and extra mounts | Must exclude credential storage and admin-secret files |

**Switch after re-login**

1. Create/import a new profile name.
2. Stop and remove old workers.
3. With the old settings, run `oaiso proxy stop`, then `oaiso proxy rm`.
4. Change global `auth_profile` to the new name.
5. Run `init` for the selected agent as your normal user to recreate the proxy.
6. Create new workers from the project directory.

- The following legacy commands apply without profile selection.
- Profile mode rejects legacy `auth set` / `auth rotate`.

## `auth show`

```bash
oaiso auth show
```

- Shows the current authentication source.
- Never displays actual keys or tokens.
- Without profile selection, reports legacy OpenAI metadata only; it does not check Anthropic credentials.
- This metadata does not establish the running proxy's selected mode or successful live authentication.
- With both legacy OpenAI OAuth and an API key present, display precedence differs from proxy selection; check the [runtime precedence](../auth-proxy.md#credential-sources).

```text
Auth source: OPENAI_API_KEY env
Key configured: true
```

## `auth set`

```bash
export OPENAI_API_KEY=sk-...
oaiso auth set
```

| Item | Value |
|---|---|
| Source | `OPENAI_API_KEY` |
| Destination | `~/.config/codex-dock/apikey` |
| Permissions | `0600` |
| Condition | No independent profile selected |

## `auth rotate`

```bash
oaiso auth rotate
```

| Goal | Operation |
|---|---|
| Write a local update marker | `auth rotate`; does not immediately revoke a running proxy's tokens |
| Revoke immediately | Stop the worker or use `/admin/revoke` |
| Update the proxy | [Operations](../../operations.md) (Japanese) |

## Related Documentation

| Topic | Guide |
|---|---|
| Source precedence and credential protection | [Auth proxy](../auth-proxy.md) |
| Configuration values and files | [Configuration](../configuration.md) |
| Initial setup | [Quick start](../getting-started.md) |

## Encrypted storage (v0.0.3+)

- [Backends and operation](../guides/encrypted-credentials.md).
- New: `oaiso login --encrypt` / `--keyring`.
- Migrate plaintext: `oaiso auth profile encrypt NAME`.
- Migrate legacy ciphertext to age: `oaiso auth profile migrate-age NAME`.
- Change passphrase: `oaiso auth profile passwd NAME`.
- Stop affected workers/proxies and lock: `oaiso auth profile lock NAME`.
- Metadata inspection neither decrypts nor displays secrets.
