# Per-execution usage limits

[日本語](../../guides/usage-limits.md) | **English**

- Available: **v0.0.4**; use matching CLI, node and authentication proxy.
- Both API and subscription authentication are enforced at the credential-holding proxy. No ledger/admin authority enters VMs.
- Defaults preserve existing behavior. Unsupported proxies reject limited executions rather than omitting enforcement.

## Limits

| CLI | Control | Precision and conditions |
|---|---|---|
| `--max-requests` | Accepted upstream requests | Journaled before forwarding; upstream errors do not refund admissions. Includes model discovery, token-count metadata and broker requests |
| `--max-concurrent` | Concurrent requests | Held until HTTP/SSE completion; 0 is unlimited, except observed-token budgets default to 1 |
| `--max-transfer-bytes` | Combined request/response body bytes | Reserved before forwarding; headers excluded. Excess streams terminate |
| `--max-observed-tokens` | New-request cutoff based on observed token totals | Stops subsequent AI requests once reached; **not a prepaid or exact monetary cap** |

- Zero fields are unlimited. Limits are immutable for the same execution.
- Applies to AI/broker traffic through the auth proxy, not general HTTP/CONNECT egress.
- The final inference and already-started concurrent requests can exceed the observed-token threshold. Revocation/disconnect cannot undo already-billed upstream work.
- No monetary conversion, subscription balance or provider billing amount is fetched.
- Limited executions reject WebSocket. Codex uses the existing HTTP/SSE configuration; Claude Code uses HTTP/SSE.
- Token budgets support fixed AI routes. Model discovery/token-count metadata consume request and body-byte budgets, not inference-token totals.

## CLI

Requires independent-profile `oaiso login` and `oaiso init` on a configured execution host. Run as an ordinary user.

- Independent login also configures admin authority automatically.
- Legacy proxies without an admin secret keep their unlimited behavior. To use limits, select an independent profile or configure `proxy.admin_secret_file`, drain workers and recreate the proxy.

```bash
oaiso run --name limited-job --detach --keep \
  --max-requests 100 --max-concurrent 2 \
  --max-transfer-bytes 10485760 --max-observed-tokens 50000 \
  --exec 'sleep 3600'
oaiso usage limited-job --json
```

Example before any requests:

```json
{"policy":{"max_requests":100,"max_concurrent":2,"max_bytes":10485760,"max_tokens":50000},"requests":0,"active":0,"bytes":0,"tokens":0,"unmetered_requests":0,"indeterminate":false}
```

| Result | Meaning and next step |
|---|---|
| HTTP 429 / `usage_limit` | Request, concurrency, body-byte or observed-token cap. Concurrency-only denial can be retried after active work ends |
| HTTP 503 / `usage_state_unavailable` | Failed persistence or uncertain usage. Never automatically resets a budget |
| `unmetered_requests` | Requests without complete token-usage records; distinct from zero tokens |
| `indeterminate: true` | Interrupted work left an unknown remainder; further AI requests for this execution are denied |

- With a token budget, absent/truncated/unsupported usage also denies further AI requests.
- Without a token budget, missing token usage does not disable request/concurrency/body limits.
- Normal stop/restart and token reissue/restore preserve counters.
- Recover artifacts and start a new worker name/execution ID to continue uncertain work. Do not delete the ledger to restore an existing execution.

## Go API and configuration

| Surface | Setting |
|---|---|
| Go API | `LaunchRequest.UsagePolicy = &identity.UsagePolicy{MaxRequests: 100, MaxConcurrent: 2, MaxBytes: 10485760, MaxTokens: 50000}` |
| Query | `client.Usage(ctx, workerID)`; returns no credentials |
| External Store proxy | `CaseProxyOptions.UsageStateDir`: owner-private host directory outside workspaces |
| Host config / `oaiso.toml` | `[run] max_requests`, `max_concurrent`, `max_transfer_bytes`, `max_observed_tokens` |
| Standalone proxy | `proxy serve --usage-state-dir PATH`; requires a separate admin listener and admin secret |

- CLI-managed state: `~/.local/state/codex-dock/usage/<proxy-name-hash>/`; directory 0700, files 0600.
- Stores only hashed execution IDs, policies, counters and uncertainty flags. No API keys, OAuth or request contents.
- One proxy exclusively owns a ledger. Invalid formats/permissions and symlinks fail closed.
- The node rejects overlaps with worker workspaces/mounts, including creating a new ledger in an existing worker share.
- Capacity: 10,000 executions / 8 MiB. Full/failed storage rejects new limited executions. Drain all executions, archive required records and use new execution IDs when reorganizing state.
- Does not resist host root or the same UID altering state. Existing [host trust](../security.md) assumptions remain.
