# Disk checkpoints

[日本語](../../guides/disk-checkpoint.md) | **English**

- Available: **v0.0.4**; matching CLI/node/proxy required.
- Saves a stopped Kata root filesystem for a **new VM on the same node**. No RAM, instruction pointer, live connection or stream resume.

## Saved and excluded state

| State | Contract |
|---|---|
| Ordinary rootfs files | Installed tools and data under paths such as `/opt`; deleted files remain absent |
| Basic metadata | Permissions, numeric UID/GID, modification times and symlinks retained; hard links materialized as independent files |
| Masked read-denied paths | Underlying files and hard-link aliases excluded even when original data remains on disk |
| Workspace/additional shared mounts | Contents excluded; explicitly supply new host directories at restore; host data is never rolled back |
| Private Codex database disk | Contents excluded; unreleased (`main`) [state storage](agent-state.md). Restored workers get an empty new disk |
| `/home`, `/root` | Contents excluded, including credentials, history, caches and user-local dependencies; only top-level home directory skeletons/ownership retained |
| `/run`, `/tmp`, `/var/tmp`, `/dev`, `/proc`, `/sys` | Contents excluded, including tmpfs, bootstrap files and kernel state |
| `/var/log`, hosts/hostname/resolv.conf | Excluded; necessary runtime settings are generated for the new VM |
| Environment, argv, credentials, network rules, usage limits, jobs | Not copied; configure a new execution |
| Extended attributes, ACLs, file capabilities, special files | Not retained; this is a filesystem archive, not a block-disk clone |

- Hard links to excluded regular files are also excluded. Guest symlink paths in exclusions also exclude their resolved in-root targets.
- Add `--exclude` for application secrets. Mandatory exclusions cannot be removed.
- **Arbitrary user-written secrets cannot be identified automatically.** Supply additional exclusions before saving.
- Proxy API/OAuth credentials, stores and ledgers are outside worker disk. Proxies, login-only VMs and quarantined workers cannot be captured.

## Create

As the regular operator after login/init, replace `source-job` with your retained worker:

1. Flush/save application data and stop databases as required. The application owns multi-file consistency.
2. Stop the worker; capture never automatically stops a running/paused VM.
3. Create a checkpoint.

```bash
oaiso stop source-job
oaiso checkpoint create source-job --exclude /opt/app/private.env
```

Example output (selected fields, illustrative IDs/size):

```json
{"format":"oaiso-disk-v1","id":"64-character checkpoint ID","image":"docker.io/library/oaiso-checkpoint:...","digest":"sha256:...","bytes":734003200,"entries":24000,"excluded":["/home","/workspace","..."]}
```

- Retain `id`. Source worker remains intact; start/stop/remove are blocked for that worker during capture. Other workers remain manageable.
- A confirmed stop does not prove application consistency after SIGKILL or forced shutdown.
- Failure before publication never exposes a completed checkpoint. Publication/reply failure can leave an unknown outcome: inspect `list` before retrying.

## Inspect and restore

```bash
oaiso checkpoint list
```

In Bash, enter a saved ID when prompted. Replace `/home/alice/new-project` with an existing directory you own:

```bash
read -r -p 'Checkpoint ID: ' checkpoint_id
oaiso checkpoint inspect "$checkpoint_id"
oaiso run --checkpoint "$checkpoint_id" --name restored-job \
  --project /home/alice/new-project --detach --keep --exec 'sleep 300'
```

- Original workspace contents are not transferred. Specify additional mounts, profile, model/network/usage policies for the new execution.
- Agents/dependencies retain their saved on-disk versions; compatibility with newer agent/application releases needs separate validation.
- Restore issues a fresh VM ID, network and proxy placeholder; normal host/guest verification gates execution.
- Cannot combine with `--image`, positional image, `--template` or `--environment-lock`. Kata only.
- Existing workers are never overwritten. Usage budgets are not restored/refunded; this is a separate execution.

## Capacity, removal and compatibility

| Property | Contract |
|---|---|
| Per archive | Default 4 GiB; `create --max-bytes` can narrow to 1 MiB–4 GiB; at most 500,000 entries |
| Node catalog | 16 GiB sum of published logical archive sizes, at most 1,024 checkpoints; one create/remove operation at a time |
| Physical disk | Not a filesystem-wide quota; original images, unpacked snapshots and temporary containerd content also need space |
| Storage | Local OCI image in the dedicated containerd namespace; no automatic registry upload |
| Cancellation/failure | Unpublished content becomes GC-eligible after lease release; physical space may not be reclaimed immediately |
| Node restart | Completed checkpoints persist; in-progress capture is not resumed |
| Compatibility | `oaiso-disk-v1`, Linux amd64, canonical entrypoint matching the current node; current Kata/guard/isolation checks apply |
| Scope | New VM on the same node; cross-node transfer, RAM snapshots and stream resume are not provided |

```bash
read -r -p 'Checkpoint ID: ' checkpoint_id
oaiso checkpoint rm "$checkpoint_id"
```

- Removal refuses any retained worker referencing the image, even if stopped. Recover its output and remove that worker first.
- Inspect/list after failures; use `oaiso status` and `runtime-check` for launch issues. Excluding required libraries or application configuration can prevent the application from working.
- Host root/node operators are trusted. This is not encrypted backup or tamper-resistant archival storage.

## Go API

| Operation | API |
|---|---|
| Capture | `client.Checkpoint(ctx, workerID, cxd.CheckpointOptions{Exclude: []string{"/opt/app/private.env"}})` |
| List/inspect | `client.Checkpoints(ctx)` / `client.InspectCheckpoint(ctx, checkpointID)` |
| Restore | `client.RestoreCheckpoint(ctx, checkpointID, newLaunchRequest)`; new RequestID/workspace/identity |
| Remove | `client.RemoveCheckpoint(ctx, checkpointID)` |
| Capability | `disk-checkpoint-v1` in `client.Capabilities().Features` |

- Unsupported old nodes fail explicitly.
- Capture response loss means unknown outcome; reconcile source ID/creation time through the catalog before retrying.

## Retention planning and pins

- Available: **v0.0.4**.
- The existing hard catalog capacity remains 16 GiB. `--keep-bytes` is a cleanup target, not a lower admission quota for future captures.
- Counts logical tar bytes, not total host/containerd content or expanded snapshot usage.

Get an ID from `oaiso checkpoint list`:

```bash
read -r -p 'Checkpoint ID to protect: ' checkpoint_id
```

```bash
oaiso checkpoint pin "$checkpoint_id"
oaiso checkpoint inspect "$checkpoint_id"
```

- `pinned: true` protects against retention cleanup.
- Remove protection with `oaiso checkpoint pin "$checkpoint_id" --unpin`.
- Explicit `checkpoint rm ID` remains available; pins protect automatic selection only.

Plan removal of checkpoints older than seven days and/or above an 8 GiB target:

```bash
oaiso checkpoint prune --max-age 168h --keep-bytes 8589934592
```

| Field | Meaning |
|---|---|
| `delete` | IDs selected oldest first |
| `protected` | Pinned or referenced by retained workers using the same image digest |
| `remaining_bytes` | Projected logical capacity after deletion |
| `target_met` | Whether the byte target can be met without deleting protected items |
| `id` | Reviewed retention-plan ID |

Enter the reviewed ID:

```bash
read -r -p 'Checkpoint retention plan ID: ' retention_plan_id
```

```bash
oaiso checkpoint prune --max-age 168h --keep-bytes 8589934592 \
  --apply --plan-id "$retention_plan_id"
oaiso checkpoint list
```

- No deletion without `--apply`. Periodic use requires an explicitly configured external scheduler.
- Recompute the plan before deletion. New captures, pins, references or age-boundary changes invalidate a stale plan.
- Partial failure may follow some deletions. Inspect the catalog and review a fresh plan before retrying.
- Inspection failure aborts cleanup instead of treating resources as unused. Shared content is not broadly pruned.
- Go: `RetainCheckpoints(ctx, cxd.CheckpointRetentionOptions{...})`, `PinCheckpoint(ctx, id, true)`; apply using `Apply: true` and `ExpectedPlan`.
