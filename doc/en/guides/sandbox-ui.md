# Use sandboxes from the TUI

[日本語](../../guides/sandbox-ui.md) | **English**

Available: **unreleased (main)**. Create Kata VMs, inspect and transfer workspace files, and preview web applications without AI credentials.

## Prerequisites and startup

- An administrator must complete [node setup](../../setup.md) (Japanese), and your user must have node socket access.
- Writable rootfs requires node-configured [dedicated quota-capable storage](storage-migration.md).
- Run as your normal user from the host project directory:

```bash
oaiso ui
```

## Create a VM

1. Select `New sandbox` from `Actions F2`, or press `N` on the instance list.
2. Enter the host project, image and optional startup command. A blank command keeps the VM alive; connect to a shell with `C` after creation.
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
- Configure [authentication](authentication.md) first if enabling AI routes.

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

1. Start a web server inside the VM, listening on a guest loopback port.
2. Select the VM on the instance list and press `U`.
3. Select the port and lifetime (5 minutes, 15 minutes, 30 minutes or 1 hour), then `Start`.
4. Open the displayed authenticated localhost URL in a browser on the host running this TUI.

- Default port: 3000; lifetime: 30 minutes. At most four previews, with duplicate VM/port pairs refused.
- Treat URLs as secrets. The browser is not opened automatically. A TUI running on an SSH host does not expose that URL on your local machine; see [preview networking](preview.md).
- Tab cycles fields, buttons and the URL list; Shift-Tab reverses direction. Ctrl-L directly focuses URLs. Esc returns to the instance list from either area.
- On the URL list, `Enter` asks to stop the selected preview. You can also select `Stop all for this worker`.
- Previews remain active when returning to the instance list. Explicit stop, expiry, VM stop and TUI exit close existing connections.
- Quitting with `Q` leaves VMs running. Stop or remove unwanted VMs separately.

For launch errors, check `oaiso runtime-check` and the displayed reason. For file errors, check VM state, permissions and size. For preview errors, check the guest listening port.
