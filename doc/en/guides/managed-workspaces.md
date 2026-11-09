# Use bounded workspaces and recovery points

[日本語](../../guides/managed-workspaces.md) | **English**

Available: **v0.0.8**. Try current working files on a dedicated disk without sharing the original host directory. Save a stopped VM's filtered rootfs and optional workspace together, then restore them as new resources.

## Prerequisites and scope

- An administrator must configure [dedicated quota-capable storage](storage-migration.md) and deploy CLI, node, guard and logger from the same source bundle. Older nodes cannot provide this feature.
- Operate as a regular user in the node management group. Anyone with access to the management socket is a trusted host administrator.
- Complete [TUI and image preparation](sandbox-ui.md) first. Existing shared host folders and independent Git copies remain available.

| Mode | `/workspace` contents | Capacity and removal |
|---|---|---|
| Share host project | Original host directory | Host capacity; guest writes affect the original |
| Independent Git copy | Independent copy of a reviewed commit | Host capacity; copy survives VM removal |
| Managed current files (bounded) | Reviewed current regular files | Fixed disk and inode ceiling; survives VM removal |
| Existing managed workspace | Available node-owned workspace | Reuses its disk and numeric owner |

The host never mounts or interprets a guest-written filesystem. It passes a finite ext4 block disk to Kata. This ceiling is separate from the rootfs quota. Review extra mounts separately: they still expose host data. `Project` still selects host/project configuration even when its files are not mounted.

## Create from current files

1. Open `oaiso ui` and press `N`.
2. Choose `Managed current files (bounded)` under `Workspace source`. Set capacity and inode ceiling. No `New copy directory` is needed.
3. Press `Review` / `Ctrl-R`. Inspect every selected file, SHA-256, excluded path/reason, numeric owner and capacity. Use arrows / Page Up / Page Down to scroll.
4. Tab to `Confirm`. After `Created sandbox`, select it in the instance list and press `C` to connect. Cancel creates no disk.

| Input | Handling |
|---|---|
| Uncommitted, untracked, ignored and non-Git regular files | Copies current contents without invoking Git |
| `.git`, links and special files | Excluded, including hardlinks |
| `.env*`, `.ssh`, `.aws`, `.azure`, `.codex`, `.claude`, `.config`, credential JSON and private keys | Known paths excluded; secrets in arbitrary files cannot be detected |
| `node_modules`, `.venv`, `venv`, `__pycache__`, `.cache`, `lost+found` | Excluded; rebuild dependencies inside the VM |
| Files above 64 MiB | Excluded with a reason |
| Empty directories, xattrs, original ownership and special permissions | Not retained. Files become 0644 or 0755 when executable, owned by the VM user |
| Reviewed files changed before import | Size/hash mismatch rejects import; any allocated disk remains in the catalog |

- Maximum 8192 files; total bytes are limited to half the disk capacity or 512 MiB, whichever is smaller. Directories and filesystem metadata also require inode headroom.
- Capacity: 64 MiB–4 GiB in 64 MiB increments, default 1 GiB. Inodes: powers of two from 1024–65536, default 16384. The workspace/snapshot catalog reserves at most 16 GiB across 128 items.
- Host space is allocated in advance. Insufficient capacity rejects creation. Exhausting the guest filesystem returns `ENOSPC`. Shared folders and extra mounts are outside this disk ceiling.
- New copies resolve `current`, `codex`, `dir` or numeric `UID:GID` and show the owner before launch. Root is rejected. Reused/restored disks retain their numeric owner.

## Usage, reuse and deletion

| Action | Behavior |
|---|---|
| `L` on an instance | Fixed capacity, inode ceiling, lease and live guest-observed usage |
| `M` in the instance list | Catalog of workspaces/snapshots, reservations and VM leases |
| Select available workspace → `Use in new VM…` | Reuses the disk; a stopped retained VM still holds its exclusive lease |
| Delete VM with `D` | Releases its lease; preserves workspace data |
| `M` → `Delete permanently…` | Permanently deletes the disk after review; retained VM/point references prevent deletion |

Live usage comes from the guest and does not establish host reservation enforcement. Stopped-VM usage is unknown; its fixed reservation remains. VM retention expiry also retains managed workspace data.

Named environments save mode, capacity and inode ceiling. They exclude workspace IDs, recovery points, owners and copy plans; select and review those again after loading.

## Retrieve changes

For a managed workspace, `W` compares guest-observed regular-file hashes with the initial import manifest, reporting additions, modifications and deletions. Restored copies also compare with that initial import. Credential/cache paths and links are excluded. Comparison fails above 8192 files, 64 MiB per file or 512 MiB total.

`Export selected files…` writes only selected files to a new 0600 tar. Managed exports need no Git repository: `head` is `workspace:IMPORT_MANIFEST_ID` and `changes.patch` is empty. Limits and stale-plan rejection match the [artifact workflow](artifacts.md). `F` also supports individual downloads. No changes are automatically applied to the original host files.

## Save and restore a stopped VM

1. Stop the source VM with `S`. Select it and press `K` / `Save recovery point…`.
2. Enter a name and select `Include managed workspace` when applicable. Optionally add absolute rootfs exclusions.
3. Review and confirm the scope. Starting/removing the source is refused while its workspace is being saved.
4. Press `Z`, select a `ready` point and choose `Restore as new VM…`. Review the new name, networking and resource settings before creation.

| Saved | Excluded / restoration behavior |
|---|---|
| Filtered rootfs checkpoint | Homes, `/run`, temporary files, logs and all bind mounts are excluded; see [disk checkpoints](disk-checkpoint.md) |
| Entire selected managed workspace disk | Includes secrets created inside the VM. Initial-copy path exclusions are not applied again |
| Numeric owner and resource profile name | No old execution identity, RAM or host shared-folder state. Authentication, networking and launch verification are configured again |

- Restoration creates a new VM and a new workspace. It never rolls back the source VM, source workspace or host files. For a rootfs-only point, review the current host directory using the normal creation form.
- Snapshot disks cannot attach directly. Their SHA-256 is checked while cloning, without opening their ext4 filesystem on the host.
- Interrupted saves become `failed` after node restart. `empty`, `restoring` and `failed` items are not runnable completed workspaces. Inspect retained components before deletion/recreation.
- Removing a point deletes its rootfs and snapshot components; a retained VM using that rootfs prevents deletion. Restored workspace copies remain independent resources.
- Points provide recovery within the same node. They are not external backups against host failure or VM memory snapshots.

## CLI workflow

As a regular user, run this read-only plan from the source directory:

```bash
oaiso workspace plan . --bytes 1073741824
```

Review `Manifest.files` and `Skipped`, then enter `Manifest.id`:

```bash
read -r -p 'Reviewed copy plan ID: ' workspace_plan_id
```

```bash
oaiso workspace create . --name experiment --bytes 1073741824 \
  --inodes 16384 --plan-id "$workspace_plan_id"
oaiso workspace list
```

Enter the returned `id`, checking that its state is `ready` and lease is empty:

```bash
read -r -p 'Managed workspace ID: ' workspace_id
```

```bash
oaiso run --managed-workspace "$workspace_id" --runtime kata \
  --agent shell --no-ai --no-internet --image codex-dock:latest
```

`--managed-workspace` is CLI-only and is not persisted in host/project configuration. It cannot combine with host worktrees/clones or extra mounts overlapping `/workspace`. For CLI launches, match `--user UID:GID` to the disk owner. Per-path `deny_read` / `deny_write` masks are refused because their targets cannot be verified without opening guest data on the host. Use whole-workspace `--read-only` or the existing shared/copy mode.

See `oaiso recovery-point --help` for saving/listing/removing points, and `oaiso workspace --help` for reuse, snapshot cloning and deletion. After a lost response or failed launch, inspect names, IDs, leases and incomplete states before retrying.
