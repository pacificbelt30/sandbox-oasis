# Run tasks and adopt reviewed results

[日本語](../../guides/tasks.md) | **English**

Available: **v0.0.8**.

Queue instructions, execute them in a dedicated worktree with Kata, then select files or hunks to adopt onto a new branch.

## Requirements and storage

- Complete [first startup](first-run.md) and use your normal operator account. The Git project must have a commit.
- Use an ordinary `.git` directory or a reciprocally registered linked worktree. Handwritten pointers and symlinks to another repository are rejected. Saved tasks recheck paths and registration before operations and stop if these change. Check `git worktree list` and the registration in the original repository.
- Private task records, prompts and reviews live in `~/.config/codex-dock/tasks/`. Override with `task --store DIR`, outside the shared project.
- Each attempt creates a detached worktree at the commit recorded when the task was created. Uncommitted project edits are excluded. Retries start from that same commit.
- Git metadata uses the existing linked-worktree sharing model. This is not an independent repository or Git metadata isolation.
- Launches use normal `run` authentication, image, approval and network defaults. Batch tasks never relax approvals automatically. Select an appropriate [host agent policy](agent-policies.md) for unattended work.

## TUI workflow

```bash
oaiso task board
```

Press `T` in the `oaiso ui` worker dashboard to open the same board.

| Key | Task board operation |
|---|---|
| `N` | Enter title, project, agent, instructions and optional image, policy and test command; queue the task |
| `R` | Run the selected task, or retry a failed/cancelled task as a new attempt |
| `S` | Confirm and stop the agent and verification workers |
| `C` | Connect a separate shell to the running worker; `exit` returns |
| `T` | Execute the configured test command in a separate Kata worker sharing the attempt's worktree |
| `V` | Freeze and review the stopped task's changes |
| `G` | Reconcile saved state with the node |
| `Tab` | Switch between task list and details |
| `Q` / `Esc` | Return from the board; Esc cancels an input form without saving |

- Unfinished/unknown attempts refresh every three seconds while the board is open. Queued tasks require `R`; closing the TUI does not schedule background launches. Already launched workers continue running.
- `review` means the agent exited with code zero, not that the result is correct or tests passed.
- An unreachable node produces a synchronization error. It is not interpreted as a missing VM or permission to launch duplicates.
- Reconciling a completed process runs normal stop finalization to revoke its token and release Kata capacity. Worker storage and worktrees remain available and are not automatically deleted.
- If a task finishes while the TUI is closed, the next `sync` releases its reservation.

## Review and adoption

1. Wait for agent and test workers to stop, then press `V`.
2. Use `Space` to select a file or hunk, or `A` for all reviewable changes. Use `Tab` to scroll the diff.
3. Press `P` and choose an existing base branch and a new result branch.
4. Review the commit, base commit and file count, then choose `Confirm`. `Cancel` or `Esc` aborts.

Adoption creates only a new branch and commit. It preserves the current checkout, index and uncommitted edits, and never pushes. Changes to the reviewed worktree or base branch require a new review. Conflicts stop adoption without an automatic merge.

- Changes committed by the agent and untracked regular files are included relative to the recorded base. Ignored files are omitted.
- New/deleted/binary files and mode changes require whole-file selection. Symlink and submodule changes are rejected.
- Known credential paths such as `.env*`, `.ssh`, `.codex`, authentication JSON and private-key filenames are excluded. Arbitrary secrets embedded in source are not detected reliably; review content before adoption.
- Test results refer to the complete snapshot at test start. Later changes display `stale`. Passing tests for the full task do not establish that a selected subset of hunks passes.
- Reviews allow up to 512 files and 32 MiB of patches. The board is limited to 1 MiB, 512 tasks and 16 attempts per task. Retained worktrees and workers consume disk space.

## CLI workflow

Run this synthetic shell task from a Git project as the configured operator. The host needs `jq`. Authentication follows normal `run` settings.

```bash
TASK_ID=$(oaiso task create --title 'Review sample' --project "$PWD" --agent shell \
  --prompt 'printf "sample\n" > result.txt' --test-command 'test -s result.txt' | jq -r .id)
oaiso task run "$TASK_ID"
oaiso task sync "$TASK_ID"
```

Wait until `sync` reports `state: review`, then start verification:

```bash
oaiso task test "$TASK_ID"
oaiso task sync "$TASK_ID"
```

Once the latest attempt's `test_state` is `passed`, save a review and plan adoption. This example requires an existing `main` branch and an unused `oaiso/result-example` branch name.

```bash
REVIEW_ID=$(oaiso task review "$TASK_ID" | jq -r .id)
mkdir -m 700 -p "$HOME/oaiso-approval"
oaiso task plan "$TASK_ID" --review "$REVIEW_ID" --target main \
  --branch oaiso/result-example --plan-file "$HOME/oaiso-approval/plan.json"
```

Inspect the displayed commit and diff before approving:

```bash
oaiso task accept "$TASK_ID" --plan-file "$HOME/oaiso-approval/plan.json" --yes
git show oaiso/result-example
```

- `plan` selects all reviewable changes by default. A private `--selection-file` can contain `{"file.go":["hunk ID"]}`; `[]` selects the whole file.
- Use `create --prompt-file FILE` for long/private instructions: owner-private UTF-8, at most 32 KiB.
- `run --headless --agent codex/claude --task-file FILE` runs Codex `exec --json` or Claude `--print`. Existing interactive launches retain their behavior.
- `task list` and `show ID` read saved records; use `sync ID` for current runtime state.
- Remove unwanted workers using `oaiso rm --force WORKER_NAME`. The recorded `attempts[].workspace` remains for recovery. To remove a worktree, remove that attempt's workers first, then run `git worktree remove --force PATH`.

- Adoption branch names accept ASCII letters/digits and `/`, `_`, `-`, `.`, up to 180 bytes. Invalid Git ref names are rejected.
- Encrypted-profile `--passphrase-fd` input is forwarded through an inherited descriptor. Each launch needs fresh input; use a keyring profile for repeated TUI launches.
