# Save and export diagnostics

[日本語](../../guides/observability.md) | **English**

- Available: **v0.0.4**.
- Local JSONL, without an external monitoring service or permanent collector.

| Kind | Retained fields | Limits |
|---|---|---|
| `status` | Existing readiness check names/results | No additional connection, release, decryption or inference check |
| `resources` | Host cgroup current/optional peak memory, guest/host limits, OOM kills | Not guest RSS; unavailable data is `unknown` |
| `denial` | General-egress proxy reason codes | Reads up to 1000 retained console lines; a shared proxy does not prove per-worker attribution |
| `launch` | Total/per-phase launch durations and outcome | Excludes builds, foreground waiting and subsequent inference |
| `collection` | Unavailable denial history | Collection failure is not zero denials |

- Excludes credentials, commands, URLs, destinations, workspace paths and free-form errors. Worker IDs are SHA-256 hashes, not a promise of complete anonymity.
- Keeps current and previous files, default 1 MiB each. `--max-bytes` accepts 1 KiB–8 MiB per file. Older records are discarded.
- Directory 0700, files 0600; rejects unsafe permissions, symlinks and multiple hard links. This is diagnostic history, not a root/same-UID-resistant audit log.

## Capture and export

On a prepared node, as the regular operator, replace `my-worker` with an existing worker:

```bash
oaiso observe capture --directory "$HOME/.local/state/oaiso/observations" --worker my-worker
oaiso observe export --directory "$HOME/.local/state/oaiso/observations"
```

Status only:

```bash
oaiso observe capture --directory "$HOME/.local/state/oaiso/observations"
```

Twelve samples, five seconds apart:

```bash
oaiso observe capture --directory "$HOME/.local/state/oaiso/observations" \
  --worker my-worker --samples 12 --interval 5s
```

- Success prints `Diagnostics saved ...`. Inspect JSONL `kind` and `status`.
- Failed/unavailable checks still save available records, then return exit 1. Use `oaiso status` for recovery instructions.
- Deduplication is bounded within one capture. Repeated captures, truncated proxy history and downtime are not an exactly-once delivery contract.
- AI auth-proxy denials and request contents are not collected.

## Launch timing

```bash
oaiso run --name measured-job --detach --keep \
  --observe-dir "$HOME/.local/state/oaiso/observations" --exec 'sleep 300'
oaiso observe export --directory "$HOME/.local/state/oaiso/observations"
```

- Storage failure warns without changing launch results, worker ownership or cleanup.
- Go API: pass `LaunchWithTimings` results to `cxd.SaveLaunchObservation(directory, worker.ID, timings, err != nil)`; read with `cxd.ReadObservations(directory)`.
- Not yet in public release bundles. See [verification records](../../testing.md).
