# Recover an artifact bundle

[日本語](../../guides/artifacts.md) | **English**

- Available: **v0.0.4**.
- Run on the host against a running worker. Ordinary workspaces require a Git repository with HEAD and accessible Git metadata.
- Unreleased (main; v0.0.8 planned): [managed workspaces](managed-workspaces.md) need no Git. `head` is `workspace:IMPORT_MANIFEST_ID`; `changes.patch` is empty. Selected-content hashes, bounds and stale-plan rejection still apply.
- Select explicit regular workspace files. Export their contents, their Git diff against current HEAD by default, and a manifest containing worker/image/runtime IDs, HEAD, sizes and SHA-256 hashes. Add `--job JOB_ID` to include selected job IDs, running state and exit codes.

| Limit | Value |
|---|---|
| Selection | 1–256 files, up to 32 job IDs; selection metadata up to 64 KiB; untracked files may be selected |
| Combined file and patch data | Up to 32 MiB |
| Relative path | Up to 94 UTF-8 bytes; no symlinks or directory recursion |
| Exclusions | Known credential paths including `.git`, `.ssh`, `.aws`, `.codex`, `.claude`, `.env*`, credential JSON and private-key extensions |
| Host output bounds | Enforce 1 MiB for a plan and the selected capacity plus 1 MiB for tar output, independently of the guest |
| Not exported | Environment variables, authentication files at known excluded paths, or execution logs |

- Exclusions do not detect secrets embedded in arbitrary selected files. Review the content yourself.
- Git external diff and textconv are disabled. Shared-workspace host changes are included.
- `changes.patch` compares with current HEAD by default, or the captured launch commit with `--since-start`. The manifest includes `base` for launch comparisons alongside the reviewed current `head`.

## Review the files

Example: recover `src/main.go` and `result.json` from `dev-worker`.

```bash
oaiso artifact plan dev-worker \
  --include src/main.go \
  --include result.json
```

- Check `files`, `patch_bytes`, `total_bytes`, and the plan `id`.
- Copy the reviewed ID at the following prompt.

```bash
read -r -p 'Artifact plan ID: ' artifact_plan_id
```

## Export the reviewed contents

```bash
oaiso artifact bundle dev-worker \
  --include src/main.go \
  --include result.json \
  --plan-id "$artifact_plan_id" \
  --output ./dev-worker-artifacts.tar
tar -tf ./dev-worker-artifacts.tar
```

```text
manifest.json
changes.patch
files/result.json
files/src/main.go
```

- Use the same `--job JOB_ID` selections when planning and exporting. Logs, arguments and environment values are excluded; an exit code is not final while `running: true`.
- Creates a new 0600 file; never overwrites an existing output.
- Refuses changed contents or a changed patch. Review another plan before retrying.
- Removes incomplete output on failure. Does not extract or commit automatically.
- Recover before deleting the worker/worktree.

| Go operation | Method |
|---|---|
| Plan | `client.PlanArtifacts(ctx, workerID, cxd.ArtifactOptions{Paths: paths})` |
| Export | `client.ExportArtifacts(ctx, workerID, options, writer)`; set `ExpectedPlan` |

Use [SDK file operations](../local-api-sdk.md#files-jobs-and-checkpoints) for individual Python/TypeScript transfers. Bundles use CLI/Go.

- Go writers may contain partial output after an error. Receive into a temporary file and accept it only after success.

## Recover changes since launch

Available: **unreleased (main; v0.0.8 planned)**. New Git-backed VMs can compare against the commit captured before user code starts. This includes changes already committed inside the guest. Without this option, comparison remains against current HEAD.

[Prepare matching development CLI, node and helper binaries](sandbox-ui.md#prerequisites-and-startup). On the host, target a running Git-backed worker named `dev-worker`; replace its name and selected path with your own. Use `--since-start` for both planning and export.

```bash
oaiso artifact plan dev-worker --include src/main.go --since-start
```

Copy the returned `id` into the following prompt. `base` identifies the launch commit; `head` identifies current HEAD during review.

```bash
read -r -p 'Artifact plan ID: ' artifact_plan_id
```

```bash
oaiso artifact bundle dev-worker --include src/main.go --since-start \
  --plan-id "$artifact_plan_id" --output ./dev-worker-since-start.tar
```

- Stop/restart retains the reference. Legacy workers never adopt their current HEAD retroactively. Missing references or Git objects produce an error.
- Managed workspaces keep their initial-import hash comparison and reject this Git-specific flag.
- In the TUI, choose [`W` → `Since launch` → `Browse changes…`](sandbox-ui.md#compare-with-launch-and-review-individual-files), select files, then review and save with the same comparison base.
- The Go API uses `ArtifactOptions.SinceLaunch = true`. Changes to selected content, patch, HEAD or comparison reference require a new review.

| Failure or message | What to check next |
|---|---|
| `launch Git baseline unavailable` | Verify that this is a new VM with a captured reference. For legacy VMs, explicitly review against current HEAD or create a new VM with matching components |
| Git comparison fails or the base object is missing | Check that the reference commit remains accessible inside the guest. Otherwise choose another comparison mode and review again |
| Export rejects a changed plan | Re-plan with the same file selection, comparison mode and `--job` options; use the new ID and an unused destination |
