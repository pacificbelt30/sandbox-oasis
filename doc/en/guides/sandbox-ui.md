# Use sandboxes from the TUI

[日本語](../../guides/sandbox-ui.md) | **English**

Available: **unreleased (main)**. Create Kata VMs, inspect and transfer workspace files, and preview web applications without AI credentials.

## Prerequisites and startup

- Creation, file operations and preview management on this page are **unreleased (main)**. Installing the public release does not add them. Follow [source installation](../../setup.md#ソースからビルドする場合) (Japanese) and install the CLI, node, guard and logger from the same bundle.
- An administrator must prepare the Linux/KVM node and grant node socket access. Use your normal operator-group account for routine commands.
- Writable rootfs requires [dedicated quota-capable storage](storage-migration.md). Review migration instructions before changing a host with existing data.
- Run from the host project directory:

```bash
oaiso --version
oaiso runtime-check
```

Continue when `runtime-check` exits with code 0. Otherwise follow the displayed recovery instructions. Keep Kata verification enabled.

## Prepare the worker image

If a compatible image is not ready, build the built-in `plain` template as the same normal user. For a project Dockerfile, follow the [image guide](../commands/build.md).

```bash
oaiso build --template plain --tag codex-dock:latest
```

- Skip this build if a compatible image has already been prepared and checked.
- Builds need host access to registries and package sources, separately from the VM's Offline setting.
- `plain` also contains Codex/Claude Code tooling. `--no-ai` avoids host AI credentials and the auth proxy; it does not uninstall tools.
- This credential-free workflow does not require login, credential-profile registration, init or auth-proxy startup.

## Open the TUI

After preparing the image, launch from the project directory to share:

```bash
oaiso ui
```

| Action | Input |
|---|---|
| Choose from a menu | Click `Actions F2`, or press `F2` / `Space` |
| Select, confirm, cancel in menus | Arrows select; Enter or a displayed shortcut confirms; Esc returns |
| Create and connect | `N` creates; select a VM and press `C` for a shell |
| Files, web and details | `F` files, `U` previews, `I` full details |
| Changes, artifacts and limits | `W` changes/export, `L` lifetime/storage/supervision |
| One-off command | `X` opens the form; `Run` executes. Before execution, Esc cancels from the input or either button |
| Move within forms | Tab / Shift-Tab; Space toggles checkboxes |

Menus show actions appropriate to the VM state. Use `I` to inspect fields hidden on a narrow screen.

The list's detail pane preserves its scroll position when the same VM refreshes and returns to the top when another VM is selected. Names and tasks display strings such as `[red]` literally. Command and agent-policy edit dialogs shrink to fit the terminal width.

## Create a VM

1. Select `New sandbox` from `Actions F2`, or press `N` on the instance list.
2. Enter the host project, prepared image (`codex-dock:latest` in the example above) and optional startup command. A blank command keeps the VM alive; connect to a shell with `C` after creation.
3. Check CPU/memory, shared folders and networking, then select `Review`.
4. Confirm creation. After `Created sandbox`, return to the list, select it and use `C` to connect or `Enter` to view logs.

| Setting | Default and meaning |
|---|---|
| Runtime | Kata, with the normal startup verification |
| Name | Generated when blank |
| Image | Blank uses host/project resolution rules |
| Command | Blank runs `exec sleep infinity` to keep the sandbox alive; supplied text travels through stdin rather than host process arguments |
| CPU / memory | Select a node-approved resource profile; default inherits existing settings |
| Project access / Image rootfs | Inherit existing settings, read-only, or writable |
| Storage | Shows node rootfs byte/inode ceilings; shared host folders are outside this quota |
| Internet access | Offline by default; host policy, dependency registries or explicit domains are also available. Host deny rules remain effective |
| Enable AI services (optional) | Off by default: no auth proxy or AI credentials. Allowed general egress uses a credential-free proxy |
| Extra mounts | One normal `--mount` specification per line; review the shared scope |

- Launch uses normal `oaiso run`. Unspecified options inherit host/project defaults; host settings are not changed.
- `Esc` cancels input or confirmation. Wait for an in-progress launch. If an error names a sandbox, inspect the list before retrying.
- Writable projects expose guest edits to the shared host folder. Use a copied project or read-only sharing when needed.
- Configure [authentication](authentication.md) first if enabling `Enable AI services (optional)`.

## Try changes without modifying the original project

Available: **unreleased (main)**. Select `Independent Git copy` under `Workspace source` and enter a new absolute `New copy directory` in the creation form.

1. As your normal user, prepare a private parent directory outside the shared source project. This example creates a new directory; skip it when using an existing owner-private directory.

    ```bash
    mkdir -m 700 "$HOME/oaiso-copies"
    ```

2. Choose a destination such as `/home/alice/oaiso-copies/experiment-1`, replacing `alice` with your username. Its parent must be owned by you with mode 0700; the destination must not exist.
3. Choose `Review` or press `Ctrl-R` to check the source, destination and pinned commit. Scroll through the settings, then Tab to Cancel or Confirm.
4. Confirmation creates the independent copy and shares it at `/workspace`. The original project is not mounted automatically. Extra mounts can still expose host data.

| Data or action | Copy behavior |
|---|---|
| Committed files | Pinned to the reviewed commit, including any secrets committed to Git |
| Uncommitted, untracked and ignored files | Not copied |
| Git metadata | Independent objects; external `.git` pointers, linked worktrees, shared object stores and metadata symlinks are rejected; metadata is limited to 100,000 entries and 64 directory levels |
| Launch failure, VM removal or retention expiry | The host copy remains; retries never overwrite or automatically adopt it |
| Normal sharing | Remains available, including read-only Project access |

For destination errors, check parent ownership, mode 0700, symlinks and existing paths. The source must be a standalone repository root with a real `.git` directory. Cancelling review does not create a copy.

For current uncommitted/untracked files or a non-Git folder, choose a [bounded managed workspace](managed-workspaces.md). Its dedicated disk avoids sharing the original and supports stopped-VM save/new-VM restore. Use `M` for management, `K` to save a stopped VM, and `Z` to restore.

## Save reusable environments

Available: **unreleased (main)**. Fill in creation settings, choose `Save environment` / `Ctrl-S`, enter a name and review before saving. Choose `Load environment` / `Ctrl-O` and review the saved settings before loading. Loading still requires a separate normal launch review.

| Saved | Excluded or reset when loading |
|---|---|
| Image, startup command and resource profile | No worker name, project path or copy destination |
| Workspace mode, managed capacity/inode ceiling and read-only settings | Form-entered extra mounts are excluded and cleared on load |
| Network mode, explicit domains and lifecycle | No AI credentials, authentication settings or AI enablement; AI is disabled after loading |

- Host/project defaults still apply. Loading does not disable extra mounts or other defaults configured on the host.
- Avoid embedding secrets in startup commands. Save review displays the full command.
- Names support Japanese and other letters/numbers, plus `.`, `_` and `-`, with 1–64 characters and a letter/number first. For example, `Python検証`.
- The host file is `~/.config/codex-dock/sandbox-environments/config.json`, or under `XDG_CONFIG_HOME` when configured. The parent is 0700, the file 0600, and at most 32 environments can be stored.
- `D` on the saved list confirms deletion of settings only; existing VMs are unaffected. Saving an existing name explicitly reviews replacement. Concurrent changes reject the save and retain the draft; reopen the save form and review again.
- Loading rejects a resource profile unavailable on the node. Host egress permission limits remain in force.

## Inspect and manage lifetime and storage

Available: **unreleased (main)**. New VMs can optionally set these fields. Leave all three duration fields blank to preserve host/project defaults. When overriding, fill in all three, using `0s` to disable a limit.

| Creation field | Behavior and example |
|---|---|
| Maximum lifetime | Since creation, e.g. `2h`; restarting does not reset its origin |
| Idle timeout | Since management exec/activity, e.g. `30m`; not guest CPU, filesystem or network idle |
| Retention after exit | Time until removal after stopping/exiting, e.g. `24h` |
| Protect running main from idle | Protect a running primary process from idle collection; never extend maximum lifetime |
| Supervise this VM while TUI is open | Enforce policy every two seconds for this newly created VM only |

- Press `L` on a selected VM for deadlines, remaining time at refresh, and host-observed rootfs bytes/inode usage and limits. Use Refresh to update. Failed observations are shown as unavailable. Shared folders and independent copies use host storage and are outside rootfs quota.
- `Supervise this VM…` confirms supervision of that one VM's existing policy. Already expired policy can immediately stop or remove it. `Stop TUI supervision` stops only this TUI's supervision.
- `Review due action…` previews that VM's due action. Approval rechecks current state before applying it. A reviewed stop cannot silently become retention removal; a changed action requires another review. Stop retains the VM; retention removal also reclaims managed worktrees. Independent copies remain.
- **Saving a lifetime policy does not start automatic collection.** This TUI's supervisor ends on exit. For continued enforcement, run the following in another normal-user host terminal or supervised service. This command covers all workers with explicitly configured lifecycle policies.

    ```bash
    oaiso reap --watch
    ```

- This page does not write new lifecycle settings into existing VMs. See [lifetime and reproducibility](../../sandbox-features.md#寿命と再現性) (Japanese).

## Inspect and transfer files

Select a running VM and press `F`. Operations stay within guest `/workspace`.

| Key | Action |
|---|---|
| `Enter` | Open a real directory or display a text file |
| `Backspace` | Parent directory, selecting the folder just left |
| `G` | Refresh, retaining the selected name when it still exists |
| `D` | Download the selected file to a new host file |
| `U` | Upload a host file beneath the current workspace directory |
| `Esc` | Close the viewer/form or return to the list |

- Text preview shows at most 256 KiB. Binary/non-UTF-8 files require download. Control characters are escaped for display.
- A failed directory read keeps the previous path, listing and selection. Close the error to continue; press `G` to retry an initial listing failure.
- Transfers allow up to 64 MiB per file. Neither side overwrites existing files or creates missing directories.
- Symlinks cannot be opened. Host transfer paths also refuse symlinks in every parent directory and the final component.
- Upload freezes the contents before confirmation, showing size and SHA-256. `Esc` cancels it.
- Download fetches after confirmation and publishes a new `0600` host file only on success. A guest modifying its file during transfer can still change the retrieved contents.
- Uploading into a shared workspace also changes the host project.

## Inspect changes and recover selected artifacts

Available: **unreleased (main)**. Select a running VM and press `W`.

1. Inspect guest Git status and **uncommitted diff against its current HEAD**, up to 256 KiB. This is not a history comparison of commits created by the guest and does not display untracked file contents. Guest Git configuration is never executed on the host for inspection.
2. Tab to `Export selected files…`. Enter workspace-relative file paths, one per line, and choose a new host tar file.
3. Choose Review export to inspect selected files, size, guest HEAD and destination; approve from the scrollable review page.
4. `Saved reviewed artifacts` confirms completion. The VM and independent copy remain. Inspecting/adopting the saved content and removing the VM are separate actions.

- Existing host files, symlinks, credential paths and selections over 256 files / 32 MiB are rejected. A change to selected content, diff or HEAD after review rejects export without publishing a partial file.
- The tar contains selected files, their `changes.patch` and a manifest. The host file is mode 0600. No changes are automatically applied to the original project.
- Managed workspaces support [initial-import hash comparison and non-Git export](managed-workspaces.md#retrieve-changes). Other non-Git workspaces or oversized diffs display an error. Use `F` for individual file recovery. See [artifact specifications](artifacts.md).

## Preview a web application

To try serving the workspace with the built-in `plain` image, paste this into the **Command field when creating the VM**. Do not run it in the host terminal.

```bash
exec python3 -u -m http.server 3000 --bind 127.0.0.1 --directory /workspace
```

Review the settings and create the VM to start the server inside it. For your own app, substitute its startup command and port.

1. Start a web server inside the VM, listening on a guest loopback port.
2. Select the VM on the instance list and press `U`.
3. Select the port and lifetime (5 minutes, 15 minutes, 30 minutes or 1 hour), then `Start`.
4. Open the displayed authenticated localhost URL in a browser on the host running this TUI.

- Default port: 3000; lifetime: 30 minutes. At most four previews, with duplicate VM/port pairs refused.
- Treat URLs as secrets. The browser is not opened automatically. A TUI running on an SSH host does not expose that URL on your local machine; see [preview networking](preview.md).
- Tab cycles fields, buttons and the URL list; Shift-Tab reverses direction. Ctrl-L directly focuses URLs. Esc returns to the instance list from either area.
- On the URL list, `Enter` asks to stop the selected preview. You can also select `Stop all for this worker`.
- Previews remain active when returning to the instance list. Explicit stop, expiry, VM stop and TUI exit close existing connections.
- Stopping a preview does not stop its guest server. Use `S` / `D` on the instance list to stop or remove an unwanted VM.
- Quitting with `Q` leaves VMs running. Stop or remove unwanted VMs separately.

For launch errors, check `oaiso runtime-check` and the displayed reason. For file errors, check VM state, permissions and size. For preview errors, check the guest listening port.

## Check a single command from the CLI

Unreleased (main): use the same prepared host and image without opening the TUI. Run as your normal user from the project directory. This example shares the workspace read-only.

```bash
oaiso run --runtime kata --agent shell --no-ai --no-internet \
  --image codex-dock:latest --read-only \
  -- python3 -I -c 'import os; print("sandbox-ok", os.getuid())'
```

Example output excerpt; the UID depends on the environment:

```text
sandbox-ok 1000
```

Exit code 0 means this command succeeded inside the VM. The completed worker is removed by default. This does not check AI authentication or inference. See [`run`](../commands/run.md#sandbox-without-ai-credentials) for flags.

## Distinguish exit, stop and removal

| Action | Effect on the VM and saved data |
|---|---|
| `exit` in a connected shell | Return to the TUI; the VM keeps running |
| `Q` on the instance list | Close that TUI's previews; keep its created VMs |
| Select a VM and press `S` | Stop it and retain it for restart |
| `R` on a stopped VM | Rerun its saved startup command; memory state is not resumed |
| `D` on a selected VM | Remove the VM and managed resources after confirmation; recover needed files first |

Ordinary shared projects remain on the host. Managed Git worktrees are removed. Review [file sharing and ownership](../../workspace.md) (Japanese), and download needed files before removal.
