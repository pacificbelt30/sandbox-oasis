# Migrate to dedicated storage

[日本語](../../guides/storage-migration.md) | **English**

Available: **v0.0.7**. Writable rootfs storage requires dedicated XFS, separate from the OS filesystem, with project quota accounting and enforcement. See [resource limits](resource-limits.md).

## Separate the backup targets

| Target | Backup and restore |
|---|---|
| Worker rootfs | A stopped [disk checkpoint](disk-checkpoint.md); restore to a new VM with a compatible canonical entrypoint |
| Workspace and extra shares | Back up on the host separately, preserving numeric UID/GID and permissions; select restored paths explicitly |
| Private Codex database | On supported versions use [agent-state export/restore](agent-state.md#backup-and-restore-to-another-vm); excluded from rootfs checkpoints |
| HOME conversations, configuration and caches | Excluded from rootfs checkpoints; retrieve needed user data before stopping and review secrets |
| Credentials and agent policies | Preserve host profiles/configuration separately; do not mix the auth proxy Store or admin secret into VM backups |
| RAM, network, execution ID and tokens | Not restored; new identities are verified and issued |

Keep backups outside shared workspaces in an owner-only directory. There is no automatic discovery of arbitrary secrets or backup encryption. Hash validation checks content integrity, not the creator's identity.

## Export from the old release

As the normal user on the old host, save application data and replace `source-job` with your worker name.

```bash
install -d -m 0700 "$HOME/oaiso-backup"
oaiso stop source-job &&
  (umask 077; set -C; oaiso checkpoint create source-job > "$HOME/oaiso-backup/checkpoint.json")
```

v0.0.6 has no `checkpoint export`. An administrator uses the trusted installed `ctr` from the setup ledger. This example uses the default dedicated socket and namespace. Replace `/home/alice` with the backup owner's home.

```bash
sudo python3 - <<'PY'
import json, os, subprocess
from pathlib import Path
backup = Path('/home/alice/oaiso-backup')
record = json.loads(Path('/var/lib/codex-dock-setup/installed.json').read_text())
info = json.loads((backup / 'checkpoint.json').read_text())
output = backup / 'checkpoint.oci'
fd = os.open(output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
os.close(fd)
subprocess.run([str(Path(record['release']) / 'bin/ctr'),
               '--address', '/run/codex-dock-containerd/containerd.sock',
               '--namespace', 'codex-dock', 'images', 'export',
               '--skip-manifest-json', str(output), info['image']], check=True)
owner = backup.stat()
os.chown(output, owner.st_uid, owner.st_gid)
PY
```

- A failed export is incomplete. Retain the source checkpoint and investigate the error.
- Between supported new versions, use `oaiso checkpoint export ID --output FILE`.
- Back up the workspace separately and retain the source VM/storage until restoration is verified.

## Restore on new storage

1. An administrator prepares a **separate destination** with dedicated XFS mounted at `/var/lib/codex-dock-containerd` with project quotas. Setup does not convert or format ext4. Do not format existing data or hide it beneath a new mount.
2. Install the matching CLI/node/guard/logger. As the normal user, confirm the `storage` check in `oaiso runtime-check`; enforcement is queried from the kernel.
3. Transfer backups privately and restore workspace contents with numeric UID/GID and permissions intact.
4. After login/init on the new node, import as the normal user. Use the same numeric UID/GID as the source.

```bash
oaiso checkpoint import "$HOME/oaiso-backup/checkpoint.oci"
```

Use the returned ID and your restored workspace.

```bash
read -r -p 'Imported checkpoint ID: ' checkpoint_id
```

```bash
oaiso run --checkpoint "$checkpoint_id" --name restored-job \
  --project /home/alice/restored-project --detach --keep --exec 'sleep infinity' &&
  oaiso connect restored-job
```

- For a private database restore, create with `--create-only` before starting and follow the [database procedure](agent-state.md#backup-and-restore-to-another-vm).
- Verify artifacts, database integrity, writes, authentication and network policy before removing the old environment.
- Import validates OCI format, blob hashes, canonical entrypoint and exclusions. It never replaces an existing image tag. Failed imports leave the source and backup available.
- This does not downgrade newly created workers. To return to an old release, restore pre-upgrade backups in a separate compatible environment.

## Inspect usage

- `oaiso runtime-check --json`: quota enforcement, dedicated filesystem free capacity and worker limits.
- TUI details: used/limit bytes and inodes for writable rootfs changes; shared image sizes, workspaces and database disks are excluded.
- Unsupported nodes or failed quota queries show unavailable, never a measured zero.
