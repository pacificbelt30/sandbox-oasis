# `oaiso ui` — TUI Dashboard

> [日本語](../../commands/ui.md) | **English**
>
> [← Command Reference](../commands.md)

```bash
oaiso ui
```

Launches a terminal UI for real-time monitoring and management of all workers.

---

## Layout

**Example narrow-screen layout for unreleased main; v0.0.8 planned. Values are illustrative, not a captured screen.**

```text
┌─────────────────────────────────────────────────────────────────────┐
│ Oaiso  1 running / 2 total  2 shown  sort=name                       │
│ Filter:                                                            │
│ NAME                   STATUS          UPTIME                      │
│ sandbox-a              running         2m                          │
│ sandbox-b              exited          -                           │
│                                                                    │
│ [Actions F2]  N New  F Files  U Preview  I Info  / Filter            │
│               Enter Logs  C Connect  ? Help  Q Quit                │
└─────────────────────────────────────────────────────────────────────┘
```

At 110 columns or wider, the detail pane and additional columns are also shown. Press `I` for full details on a narrow screen.

## Key Bindings

The `C`, `X`, `/`, `O`, and `V` instance actions are available in v0.0.6 and later.

| Key | Action on the worker list |
|---|---|
| `I` | Scrollable full instance details (unreleased main; v0.0.8 planned) |
| `F2` / `Space` | Open actions; select with arrows, then Enter or the displayed shortcut (unreleased main; v0.0.8 planned) |
| `Tab` | Focus the Actions button; Tab again returns to the list (unreleased main; v0.0.8 planned) |
| `Esc` | Clear the list filter, or restore the previous query while editing (unreleased main; v0.0.8 planned) |
| `↑` / `↓` | Select worker |
| `Enter` | Fetch the selected worker's last 200 log lines |
| `C` | Open a separate login shell; `exit` returns to this TUI |
| `X` | Run a command and show output plus exit status |
| `/` | Filter text or `status:` / `branch:` / `task:` / `image:` / `policy:`; prefix `!` to exclude |
| `O` | Cycle name, status, age, branch, and policy ordering |
| `V` | Show lifecycle and metadata changes observed while the TUI is open |
| `N` | [Create a sandbox](../guides/sandbox-ui.md) (unreleased main; v0.0.8 planned) |
| Uppercase `J` | [Background jobs](../guides/sandbox-ui.md#manage-background-jobs): start, follow output and stop a selected job (unreleased main; v0.0.8 planned) |
| `W` | [Compare current HEAD or launch and recover artifacts](../guides/sandbox-ui.md#compare-with-launch-and-review-individual-files) (unreleased main; v0.0.8 planned) |
| `L` | [CPU, memory and stop evidence](../guides/sandbox-ui.md#cpu-memory-and-stop-evidence), lifetime, quota and selected-VM supervision (unreleased main; v0.0.8 planned) |
| `F` | Browse workspace files and transfer new files (unreleased main; v0.0.8 planned) |
| `U` | Manage authenticated web previews (unreleased main; v0.0.8 planned) |
| `M` | [Managed workspace](../guides/managed-workspaces.md) reservations, reuse and removal (unreleased main; v0.0.8 planned) |
| `Z` | List [recovery points](../guides/managed-workspaces.md#save-and-restore-a-stopped-vm) and restore into a new VM (unreleased main; v0.0.8 planned) |
| `K` | Review and save a recovery point for the selected stopped VM (unreleased main; v0.0.8 planned) |
| `T` | Open [tasks and result review](../guides/tasks.md) (unreleased main; v0.0.8 planned) |
| `S` | Stop selected worker with a 10-second grace period |
| `D` | Force-remove selected worker |
| `A` | Stop all running workers in the list |
| `R` | Restart a stopped worker |
| `E` | Fetch selected worker's proxy denial history |
| `P` | Open proxy settings |
| `B` | Open credential broker settings |
| `G` | Select, clone, edit, and review host agent policies (v0.0.7+) |
| `?` / `H` | Open dashboard help |
| `Q` | Quit; from log/denial view, return to the list |

- Letter keys generally accept either case. Jobs require uppercase `J`; lowercase `j` moves down the list.
- Remove and stop-all ask for confirmation; save required artifacts first.
- Connect creates a separate exec session instead of attaching to the primary agent. Exiting the shell leaves the agent and worker running.
- The `Enter` worker logs and `E` denial history are fetched on opening. Follow them with `oaiso logs WORKER --follow` or `oaiso policy events --worker WORKER --follow`. Uppercase `J` job output is fetched continuously; `Space` pauses/resumes automatic scrolling.
- `Q` leaves VMs and background jobs running, while closing localhost previews opened by the TUI. VM stop/removal and enforced lifetime limits still apply.
- When AI routes are enabled, authentication uses the same global settings as the CLI; [configure the profile and admin secret](../guides/authentication.md) first.

> **Refresh interval**: Container list updates every 2 seconds.

## Proxy settings

```bash
oaiso proxy configure
```

- Opens the editor directly, including when the node is stopped.
- Edits global settings, or the file selected by `oaiso --config PATH proxy configure`.
- Fields: allow/deny domains, CIDRs and ports; empty-grant default denial; upstream proxy URL; host CA path.

| Button or key | Action |
|---|---|
| Validate | Validate input syntax |
| Test destination | Check HOST:PORT by name/port only; no DNS or live connection |
| Save | Save settings; reject concurrent edits |
| Reload | Reload saved settings |
| Back / Esc | Return to the list, or exit the standalone editor |

**Successful Save output**

```text
Saved. Recreate the desired proxies/workers explicitly to apply the new settings.
```

- The file has been saved. Save worker results, then recreate affected proxies and workers.

- Does not edit API keys, real tokens or admin-secret values.
- Saving does not update live proxies; follow [network setup](../guides/networking.md) to recreate them.

---

## Related Documentation

- [Worker Management](worker.md)
- [`oaiso run`](run.md)
- [Host-managed agent policies](../guides/agent-policies.md)

## Inspect rootfs capacity

Available: **v0.0.7**. Details show host-observed writable rootfs bytes/inodes and limits. This excludes total image, shared workspace and private database usage. Failed queries show unavailable and suggest `oaiso runtime-check`. See [dedicated storage](../guides/storage-migration.md#inspect-usage).

## Choose actions without memorizing keys

Unreleased (main; v0.0.8 planned): click `Actions F2`, or press `F2` / `Space`, then use arrows and Enter or a displayed shortcut. Stopped instances offer Start instead of connection, files and previews. Delete retains its confirmation.

- Below 110 columns, the detail pane collapses and the list prioritizes name, status and uptime. Widening restores details. Press `I` for full details at any width.
- Esc closes the menu; Esc on the instance list clears its filter.
- If selection changes while the menu is open, choose the target again before acting.
