# Worker Management — ps / stop / rm / logs

> [日本語](../../commands/worker.md) | **English**
>
> [← Command Reference](../commands.md)

---

## `oaiso ps`

```bash
oaiso ps --all --filter 'status:running !policy:restricted' --sort newest --wide
oaiso ps --all --format json --pretty
oaiso ps --all --save-snapshot ~/.local/state/oaiso/instances.json
oaiso ps --all --compare-snapshot ~/.local/state/oaiso/instances.json
```

Lists running (and optionally stopped) containers.

Example `oaiso ps -a` output; names, elapsed times, tasks and images are illustrative:

```text
NAME               STATUS      UPTIME  BRANCH  TASK            IMAGE              POLICY
add-tests          ✅ running  5m      -       Add unit tests  codex-dock:latest  approve-for-me
codex-calm-beacon  ⏹ exited   2m      main    (interactive)   codex-dock:latest  interactive
```

- `✅ running`: executing; does not establish task success.
- `⏹ exited`: finished; use `ps -a`, then check logs and exit status.
- `BRANCH` of `-`: no managed worktree branch metadata.
- `UPTIME`: currently elapsed time since creation; it keeps increasing after exit and does not reset on restart.
- No workers: only column headers are displayed.

Filtering supports plain text and `name:`, `status:`, `branch:`, `task:`, `image:`, `id:`, or `policy:` predicates; prefix a predicate with `!` to exclude it. Output can be table, JSON, or CSV. Snapshots are owner-private JSON and comparison reports added, removed, started, stopped, restarted, and metadata-changed workers.

## `oaiso connect`

```bash
oaiso connect worker-name
oaiso connect --start --shell zsh worker-name
```

- Opens a new PTY exec session rather than attaching to the primary agent.
- Exiting returns to the host while the worker and agent continue.
- A stopped worker is rejected unless `--start` is explicit.
- `--workdir` and repeatable `--env KEY=value` are available.

---

## `oaiso start`

```bash
oaiso start NAME_OR_ID
```

- Restart a retained worker with the same ID and workspace.
- Kata checks run again and the saved placeholder is restored before user code is released.
- If a pinned proxy IP changed, create a new worker instead.
- `oaiso vm start` remains available for Kata VM inventory recovery.

## `oaiso exec`

```bash
oaiso exec --json worker-name -- sh -c 'echo ok; echo warning >&2'
```

```json
{"exit_code":0,"stdout":"ok\n","stderr":"warning\n"}
```

- Output streams are separate. This example writes to stderr but succeeds with `exit_code: 0`.
- Replace `worker-name` with a running worker's name.
- The CLI returns the guest exit code; timeout is 124, cancellation is 130.

## `oaiso wait`

```bash
oaiso wait --json worker-name
```

For a worker whose main program exited successfully:

```json
{"exit_code":0}
```

- `exit_code: 0` reports successful worker termination.
- `wait` itself exits with `0` when waiting succeeds; read its output value for the worker's result.
- Timeout is 124, cancellation is 130.

## `oaiso stop`

```bash
oaiso stop [NAME|ID...] [--all] [--timeout 10]
```

> Stopping a container also immediately revokes its Auth Proxy token.
> See [Token Lifecycle](../auth-proxy/tokens.md).

---

## `oaiso rm`

```bash
oaiso rm [NAME|ID...] [--force]
```

---

## `oaiso logs`

```bash
oaiso logs NAME|ID [--tail 100] [--follow]
```

---

## Related Documentation

- [TUI Dashboard](ui.md)
- [`oaiso run`](run.md)
- [Quick Start](../getting-started.md)

See [VM management](../../vm.md) (Japanese) for host process and allocation reconciliation.
