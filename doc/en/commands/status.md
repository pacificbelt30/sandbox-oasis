# `oaiso status` — consolidated readiness

[日本語](../../commands/status.md) | **English**

- **Available in v0.0.3 or later.**
- Distinguish saved credentials, checked connections and tested inference without displaying secrets.

```bash
oaiso status
oaiso status --json
```

| Check | Default behavior |
|---|---|
| CLI version | Show running version |
| Selected profile | Show saved state without decrypting encrypted profiles |
| Runtime | Inspect local node, Kata and builder |
| Authentication proxy | Check admin authentication, worker-token enforcement and matching profile |
| Workers | Count retained workers/sessions |
| Updates | Unchecked; no GitHub request |
| Connection/inference | Unchecked; no provider request |

```text
[ok] version: v0.0.3
[ok] profile: work (encrypted (passphrase, age), 0600); saved, locked for this command
[unchecked] connection: Credentials were not decrypted; connection and inference not tested
...
[ok] proxy: worker-token enforcement is active; inference not tested
[ok] workers: 0 retained workers/sessions
[unchecked] updates: No external release request was made.
```

- Partial example; actual status/errors include appropriate fixes.
- `locked for this command` means this check has not decrypted credentials; it does not mean a running proxy was stopped.
- Failed runtime/profile checks return exit code 1. `unchecked` alone is not treated as failure.

## Explicit external checks

```bash
oaiso status --check-connection
oaiso status --check-updates
```

| Flag | Endpoint and limits |
|---|---|
| `--check-connection` | OpenAI/Anthropic models-list endpoints using saved API keys; encrypted profiles require unlocking |
| `--check-updates` | Fetch GitHub's latest stable release; display it alongside the installed version |

- API checks perform no inference and do not establish inference/all-model permissions.
- No automatic OAuth refresh or live subscription validation; check using the actual agent separately.
- [Login](login.md), [encrypted storage](../guides/encrypted-credentials.md), [upgrade](upgrade.md).
