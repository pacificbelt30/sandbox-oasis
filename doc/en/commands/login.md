# Login and first authentication setup

[日本語](../../commands/login.md) | **English**

**Available in v0.0.3 or later.**

- Keyring storage is the default. On headless hosts, first open `oaiso auth keyring session`. [Setup and use](../guides/keyring.md).

## Interactive login

```bash
oaiso login
```

1. Choose `codex` or `claude`.
2. Choose `subscription` or `api-key`.
3. Follow the authentication instructions.
4. Save a new independent profile and select it in global settings.

| Method | Behavior |
|---|---|
| API key | Hidden input followed by an official models-list API check; no inference request |
| Subscription | Official pinned CLI login in a dedicated Kata; no host CLI installation or login required |

## Specify agent and method

```bash
oaiso login --agent codex --method subscription
oaiso login --agent claude --method subscription
oaiso login --agent claude --method api-key --profile work
```

- Choose one example. An omitted profile name is generated; existing profiles are never overwritten.
- Open the displayed URL in your browser and follow the official CLI. Codex uses a device code; Claude uses a URL and, when needed, a pasted code.
- Account and organization settings determine subscription eligibility.

| Flag | Behavior |
|---|---|
| `--agent codex\|claude` | Select agent |
| `--method api-key\|subscription` | Authentication method |
| `--profile NAME` | New profile name |
| `--key-stdin` | Explicit API key input; requires agent and method flags |
| `--skip-check` | Explicitly save an unverified API key, for example offline |
| `--encrypt` | Encrypt directly with a hidden passphrase |
| `--keyring` | Secret Service encryption; the default for new login profiles |
| `--plaintext` | Explicit 0600 plaintext storage; mutually exclusive with `--encrypt` and `--keyring` |
| `--no-select` | Save without changing global settings |

## Saved and connection-checked are different

```text
Profile saved: work (encrypted, 0600).
Connection check: passed. Inference: not tested.
Selected profile: work
Settings: /home/user/.config/codex-dock/config.toml
```

- Names and paths are examples. New login profiles use the [keyring](../guides/keyring.md) by default; use `oaiso auth keyring session` on headless hosts. Existing profiles are unchanged.
- A passed models-list check does not establish inference success or permission to use every model.
- Subscriptions report `Official CLI login: completed. Inference: not tested.`
- Normal errors do not include upstream response bodies or real keys.

## Automatic setup

- Create the independent profile and owner-only storage.
- Generate an owner-only admin-secret file if no admin authority is configured.
- Select global `auth_profile` and the required admin-secret setting.
- Retain existing network policy, unrelated settings and comments.
- Never automatically remove or replace existing profiles, admin authority, workers or proxies.
- If selection fails, retain the saved profile; retry with `oaiso auth profile use NAME`.

## Dedicated login Kata

- Retain the Kata attestation gate, non-root identity and resource limits.
- No workspace, host HOME/authentication, user templates or project hooks.
- Use the embedded recipe's fixed official CLIs, ignoring development CLI-version environment overrides.
- Read-only image root; credentials live in a guest tmpfs limited to 128 MiB.
- Dedicated credential-free egress restricted to authentication domains and port 443.
- Login and collection use streaming exec, outside persistent console logs.
- Remove the VM and dedicated egress on success, cancellation or failure. Report cleanup failure with a recoverable ID.
- Stopped login sessions cannot be resumed; start a new login.
- Ordinary work VMs continue to receive only short-lived placeholders through the Auth Proxy.

## Continue

```bash
oaiso init --agent claude
cd /path/to/your-project
oaiso claude
```

- Replace both occurrences of `claude` with `codex` for Codex.
- Drain old workers/proxies before initializing a different profile.
- Official instructions: [Codex authentication](https://learn.chatgpt.com/docs/auth), [Claude authentication](https://code.claude.com/docs/en/authentication).
