# Authentication and profiles

[日本語](../../guides/authentication.md) | **English**

- Prerequisite: complete [installation](../installation.md).
- Configure authentication on the trusted host, including a WSL or VM host.

- For new v0.0.3+ installations, follow [install → keyring → login → first startup](first-run.md). Login selects the profile and prepares the admin secret automatically.
- The steps below cover manual registration with `set/import`; do not combine them with the `login` flow.

## 1. Choose credential separation

| Mode | Credential storage | Use it for |
|---|---|---|
| Independent profile (recommended for separation) | Product directory with mode `0700`; files with mode `0600` | Separate product authentication from host CLI logins |
| Legacy host integration | Host credential files and authentication environment variables | Reuse existing host authentication |
| Embedding product Store | Storage provided by the embedding application | Manage per-case credentials through the [Go API](../credential-store.md) |

- Selected independent profiles never fall back to host authentication.
- The manual `set/import` examples below use 0600 plaintext. New v0.0.3+ `login` profiles default to [keyring plus age](keyring.md).
- Keep credentials and profile selection out of project `oaiso.toml` files.

## 2. Choose the agent and authentication method

| Agent | API key | Subscription |
|---|---|---|
| Codex CLI | [Register](../commands/auth.md#register-an-api-key) an OpenAI key | [Import](../commands/auth.md#import-oauth) Codex OAuth credentials |
| Claude Code | [Register](../commands/auth.md#register-an-api-key) an Anthropic key | [Import](../commands/auth.md#import-oauth) Claude OAuth credentials; see [Claude setup-token](../claude-code.md) |

**For an independent profile, run exactly one command below. All alternatives use the name `work`.**

| Authentication | Command |
|---|---|
| OpenAI API key | `oaiso auth profile set work --provider openai` |
| Anthropic API key | `oaiso auth profile set work --provider anthropic` |
| Codex OAuth | `oaiso auth profile import work --format codex --credentials-stdin < /private/login/auth.json` |
| Claude OAuth | `oaiso auth profile import work --format claude --credentials-stdin < /private/login/.credentials.json` |

**Successful registration output**

| Operation | Output |
|---|---|
| Register API key | `Profile created (plaintext, 0600).` |
| Import OAuth | `Profile imported (plaintext, 0600).` |

- API-key entry shows `API key:`; typed characters are hidden.
- This confirms storage, not key validity or subscription access.

- Replace `/private/login/...` with the actual JSON path from a prior official CLI login.
- OAuth import separates the copied credentials from the host CLI's storage; later logins are not automatically synchronized.
- `auth profile set`: hidden TTY input; keep real keys out of command arguments.
- `auth profile import`: explicit stdin input; source files are not automatically discovered or deleted.
- Legacy host integration: [prepare credentials](../getting-started.md#2-prepare-credentials).

## 3. Configure admin authority and profile selection

1. [Create the admin secret](../commands/auth.md#create-admin-authority-and-start-the-proxy) in a separate `0600` file.
2. [Select the profile globally](../commands/auth.md#fix-the-selection-for-daily-operations).
3. Set `auth_profile` to the created name, `work`. Replace `/home/user/...` with your actual absolute path.
4. Check the selected authentication mode:

```bash
oaiso auth show
```

**Output for a selected OpenAI API-key profile**

```text
Profile: work
Storage: plaintext (0600)
OpenAI: api-key
Anthropic: disabled
```

| Registered credentials | Expected line |
|---|---|
| OpenAI API key | `OpenAI: api-key` |
| Codex OAuth | `OpenAI: oauth` |
| Anthropic API key | `Anthropic: api-key` |
| Claude OAuth | `Anthropic: oauth` |
| Provider without credentials | `disabled`; cannot launch that provider |

- Expected in profile mode: `Profile: work`, and `api-key` or `oauth` for the selected provider.
- Creating a profile does not select it. Before selection, `auth show` reports legacy OpenAI information.
- For login through a dedicated Kata VM, see [login and API connection checks](../commands/login.md).

## 4. Continue to the first worker

1. If needed, [allow general network destinations](networking.md).
2. Return to [image and proxy preparation](../getting-started.md#3-prepare-images-and-start-the-proxies).
3. Prepare Codex for an OpenAI profile, or Claude for an Anthropic profile.

- `init` checks for a credential source; the first live agent request separately checks provider authentication.

## Refresh and switching

| Operation | Behavior |
|---|---|
| Normal OAuth refresh | CAS save to the same Store; retain the connection generation |
| Re-login or account switch | Import into a new profile, then [replace the workers and proxy](../commands/auth.md#refresh-switching-and-sharing) |
| Credentials passed to workers | Short-lived placeholders only; no real API key or access/refresh/ID token |
| Login Kata, automated login and API connection checks | [Implemented in v0.0.3](../commands/login.md); live-account verification remains separate |
| Local encryption and key backends | [age and keyring in v0.0.3+](encrypted-credentials.md); external Vault integration uses an embedding Store |

- Storage, permissions and limitations: [Security](../security.md).
- Flags and file paths: [Authentication command reference](../commands/auth.md).
