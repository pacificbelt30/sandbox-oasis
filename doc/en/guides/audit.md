# Save operation audit records

[日本語](../../guides/audit.md) | **English**

- Available: Unreleased (main).
- Records contain hashed subject IDs, fixed reason/operation codes, intent and success/failure. No credentials, placeholders, keys, bodies, URLs or arbitrary error messages.

| Component | Recorded operations | Directory |
|---|---|---|
| Node | VM create/start/stop/delete, checkpoint create/delete | Node state directory, `audit/` |
| Managed auth proxy | Issue, policy issue, restore, revoke, policy denial | `usage-state-dir/audit/` |
| Independent profiles | Create, OAuth refresh, encryption migration, key method changes | Profile root, `audit/` |
| Embedded auth proxy | Proxy operations above | `Config.AuditDirectory`, otherwise `UsageStateDir/audit/`; no persistence when both are empty |

`proxy serve --audit-dir /absolute/path` explicitly selects a directory. Case proxies persist under `CaseProxyOptions.UsageStateDir/audit/`; OAuth refreshes through custom Stores are also recorded at the proxy boundary. Never share audit storage with workers. Hashing predictable IDs does not guarantee anonymity. Arbitrary guest commands and upstream processing are outside this audit's scope. Reasons are fixed codes such as `completed`, `failed`, `canceled`, `storage_full` and network denial codes. An `error` outcome is a failure response, not a guarantee of no state change.

## Verify and export

Run as the directory owner (administrator for node state). Replace the path with the actual audit directory.

```bash
oaiso audit --directory /absolute/path/audit > audit.json
```

- Exit 0: sequence, hash chain and independently saved head agree; no pending intents.
- Pending operations: JSON is emitted, then the command fails. Reconcile actual resource state before retrying.
- Missing, partial or mismatched records fail validation; no automatic repair.

| Condition | Behavior |
|---|---|
| Permissions | Owner-private 0700 directory, 0600 regular files; symbolic/hard links rejected |
| Capacity | 16 MiB per journal; no automatic deletion. Go Journal accepts 1 KiB–64 MiB |
| Full disk/capacity or broken evidence | Refuse new create/start, token issue/restore and credential changes |
| Stop/revoke/delete | Proceed even when evidence cannot be saved; report audit failure in service logs |
| Result persistence fails | The operation may have completed. Node returns 503; credential operations return an error. Inspect before retrying |
| Token issue result fails | Revoke the newly issued token and return an error |
| Process interruption | An intent without a result is pending; a mismatched head blocks new authority |
| Integrity | Sequence/hash/head detect gaps and edits. Local owner/root can rewrite or roll back the entire journal |

Stop the owning service before archiving the entire directory. Verify and retain the archive, then start with a new owner-private directory. Archives form separate recording periods; external retention and continuity are operator responsibilities. Local storage alone cannot guarantee root-resistant integrity or detect replacement with a previously valid complete journal.
