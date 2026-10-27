# Preserve Codex SQLite state

[日本語](../../guides/agent-state.md) | **English**

Available: **unreleased (`main`)**, for newly created regular Kata workers after updating the matching CLI, node and worker-guard. Existing workers and data are not migrated automatically.

Codex SQLite databases use a private ext4 disk while HOME keeps its usual location. Shared workspace coherence settings remain in place. HOME configuration, authentication placeholders and conversation files retain their locations.

## Lifetime

| Operation or condition | Stored state |
|---|---|
| Stop/restart the same worker | SQLite databases and ordinary HOME retained |
| Restart the node process | Retained; missing runtime metadata is regenerated from the same disk |
| Remove the worker, including ordinary run exit cleanup | Disk removed too; use `--keep` to retain the worker |
| Create another worker | Separate empty disk, including when reusing a name |
| `--read-only-rootfs` | No state disk; existing temporary HOME behavior, lost when the VM stops |
| Authentication-only VM or runc | No private state disk |
| Rootfs checkpoint | State disk contents excluded; see [checkpoint scope](disk-checkpoint.md) |

## Interactive use and restart

Prerequisites: login/init completed as the regular host user. Use a new worker name, `codex-work`. This example explicitly retains a worker for shell connections.

1. Create and connect from the host:

```bash
oaiso run --agent shell --name codex-work --detach --keep --exec 'sleep infinity'
oaiso connect codex-work
```

2. Start Codex inside the guest shell:

```bash
codex
```

3. Quit Codex, then use `exit` in the guest shell to return to the host. Closing the connection leaves the worker running.

4. Stop and reconnect to the same worker from the host:

```bash
oaiso stop codex-work
oaiso connect --start codex-work
```

5. Resume inside the guest shell:

```bash
codex resume --last
```

If Codex asks you to trust the folder, review the target before confirming. To select non-interactive history created by `codex exec`, the pinned Codex version requires `codex resume --last --include-non-interactive`.

When finished, leave the guest shell and remove the worker from the host. This also removes its HOME and private database disk.

```bash
oaiso stop codex-work
oaiso rm codex-work
```

## Capacity and configuration

| Setting | Meaning |
|---|---|
| Default or `auto` | Reserve 512MiB for each new regular Kata worker |
| `--agent-state-size 1GiB` | Capacity at creation; integer sizes from 64MiB through 8GiB |
| `--agent-state-size off` | Disable when managing SQLite storage yourself |
| `[run].agent_state_size` | Host default; an explicit CLI flag wins |
| Go/local API `LaunchRequest.AgentStateSize` | Same values; an existing empty value preserves default behavior |

Regular user's host configuration:

```toml
[run]
agent_state_size = "1GiB"
```

Capacity is fixed at creation. Existing disks are not automatically resized or reformatted. Numeric capacities such as `64MiB` cannot be combined with temporary HOME or runc.

- The default database directory is `/var/lib/oaiso-agent-state/codex`. An image's explicit `CODEX_SQLITE_HOME` is preserved.
- Explicit Codex `sqlite_home` configuration takes precedence over the environment variable, following [OpenAI Docs](https://learn.chatgpt.com/docs/config-file/environment-variables). Explicit FUSE-backed paths retain their mmap limitation.
- Only the owning guest UID can access the mount. Host space is reserved at creation. This feature does not add storage encryption.

## Failures

| Condition | Next action |
|---|---|
| Host space reservation fails | Check host `df -h` and `oaiso ps -a`; remove only your unneeded workers or choose a smaller capacity for a new worker |
| Guest database disk full | Remove your unneeded files, handle SQLite's failure and retry; databases are never automatically deleted |
| Missing/replaced disk or changed permissions | Startup refuses and retains data for operator investigation; no automatic empty replacement |
| Conflicting runtime metadata or cleanup failure | Cleanup information is retained; inspect contents and ownership before retrying |
| Additional mount overlaps the database path | Choose a target that does not overlap `/var/lib/oaiso-agent-state`, or use `--agent-state-size off` when managing database storage yourself |
| Old node/bootstrap | Update and verify the matching components, then create a new worker |

Conditions and unverified cases are recorded in [testing (Japanese)](../../testing.md).
