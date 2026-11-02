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
| Move within forms | Tab / Shift-Tab; Space toggles checkboxes |

Menus show actions appropriate to the VM state. Use `I` to inspect fields hidden on a narrow screen.

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

## Inspect and transfer files

Select a running VM and press `F`. Operations stay within guest `/workspace`.

| Key | Action |
|---|---|
| `Enter` | Open a real directory or display a text file |
| `Backspace` | Parent directory |
| `G` | Refresh |
| `D` | Download the selected file to a new host file |
| `U` | Upload a host file beneath the current workspace directory |
| `Esc` | Close the viewer/form or return to the list |

- Text preview shows at most 256 KiB. Binary/non-UTF-8 files require download. Control characters are escaped for display.
- Transfers allow up to 64 MiB per file. Neither side overwrites existing files or creates missing directories.
- Symlinks cannot be opened. Host transfer paths also refuse symlinks in every parent directory and the final component.
- Upload freezes the contents before confirmation, showing size and SHA-256. `Esc` cancels it.
- Download fetches after confirmation and publishes a new `0600` host file only on success. A guest modifying its file during transfer can still change the retrieved contents.
- Uploading into a shared workspace also changes the host project.

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
