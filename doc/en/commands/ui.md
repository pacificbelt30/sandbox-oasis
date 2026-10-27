# `oaiso ui` — TUI Dashboard

> [日本語](../../commands/ui.md) | **English**
>
> [← Command Reference](../commands.md)

```bash
oaiso ui
```

Launches a terminal UI for real-time monitoring and management of all workers.

---

## Key Bindings

The `C`, `X`, `/`, `O`, and `V` instance actions are available in v0.0.6 and later.

| Key | Action on the worker list |
|---|---|
| `↑` / `↓` | Select worker |
| `Enter` | Fetch the selected worker's last 200 log lines |
| `C` | Open a separate login shell; `exit` returns to this TUI |
| `X` | Run a command and show output plus exit status |
| `/` | Filter text or `status:` / `branch:` / `task:` / `image:` / `policy:`; prefix `!` to exclude |
| `O` | Cycle name, status, age, branch, and policy ordering |
| `V` | Show lifecycle and metadata changes observed while the TUI is open |
| `S` | Stop selected worker with a 10-second grace period |
| `D` | Force-remove selected worker |
| `A` | Stop all running workers in the list |
| `R` | Restart a stopped worker |
| `E` | Fetch selected worker's proxy denial history |
| `P` | Open proxy settings |
| `B` | Open credential broker settings |
| `G` | Select, clone, edit, and review host agent policies (unreleased, main) |
| `?` / `H` | Open dashboard help |
| `Q` | Quit; from log/denial view, return to the list |

- Letter keys accept either case.
- Remove and stop-all ask for confirmation; save required artifacts first.
- Connect creates a separate exec session instead of attaching to the primary agent. Exiting the shell leaves the agent and worker running.
- Logs and denials are fetched on opening, not followed. For live following, use `logs --follow` or `policy events --worker WORKER --follow`.
- Authentication uses the same global settings as the CLI; [configure the profile and admin secret](../guides/authentication.md) first.

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
