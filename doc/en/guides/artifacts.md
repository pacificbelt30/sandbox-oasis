# Recover an artifact bundle

[日本語](../../guides/artifacts.md) | **English**

- Available: **v0.0.4**.
- Run on the host against a running worker whose workspace is a Git repository with HEAD and accessible Git metadata.
- Select explicit regular workspace files. Export their contents, their Git diff against HEAD, and a manifest containing worker/image/runtime IDs, HEAD, sizes and SHA-256 hashes. Add `--job JOB_ID` to include selected job IDs, running state and exit codes.

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
