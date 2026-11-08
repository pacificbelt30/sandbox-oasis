# Overview and terminology

[日本語](../concepts.md) | **English**

- Purpose: isolate development, tests and AI agents, normally in one Kata VM per worker.
- Flow: prepare host → choose a workflow → prepare images and any required credentials/networking → launch from your project.
- Next: [Installation](installation.md), or the [quick start](getting-started.md) on a prepared host.

## Choose a workflow

| Purpose | Preparation and startup |
|---|---|
| Development and tests without AI credentials | [Sandbox TUI](guides/sandbox-ui.md), unreleased (main; v0.0.8 planned). No auth proxy; general egress is disabled by default |
| Codex or Claude Code | [Prepare credentials, images and proxies](getting-started.md). Real credentials stay in the auth proxy |

- Installing tools in an image is separate from sharing host AI credentials.
- Writes to a shared project affect the host. Choose read-only sharing for inspection.

## Components

| Term | Meaning |
|---|---|
| Execution host | Linux/KVM environment running services, BuildKit and proxies; WSL/VM hosts must meet the same requirements |
| Operator | Trusted CLI user; node socket and BuildKit access grant root-equivalent host control |
| Node | Root management service that verifies and manages containerd, VMs, networking and resources |
| Worker | A task environment, normally with its own Kata VM; managed by name or ID |
| Agent | Codex CLI or Claude Code running inside the worker |
| Worker image | OCI image containing agents, tools and the entrypoint; built with BuildKit |
| Auth Proxy | Holds real credentials and mediates AI requests authorized with worker placeholders |
| Egress proxy | Mediates Git, npm, pip and other general traffic; holds no real credentials |
| Job | A background command inside an existing worker, not a separate VM |

## Two kinds of profiles

| Profile | Purpose | Selection |
|---|---|---|
| Authentication | Store API/OAuth credentials separately from host CLI logins | Global `auth_profile` or `--auth-profile` |
| Resources | Select CPU, memory and PID limits | `--profile-size standard`, `small`, `compact`, etc. |

- Selecting credentials does not change resource limits.
- Selecting resources does not change authentication or the provider account.

## Directories

| Location | Contents and use |
|---|---|
| Product source checkout | Development and custom builds; not required to install or initialize a release |
| Working project | Host folder used in the VM; launch the TUI or agent here and choose the shared scope |
| Guest `/workspace` | Shared project; writable by default, with changes reflected on the host |
| `~/.config/codex-dock/config.toml` | Host user settings: credential selection, proxies and launch defaults |
| Project `oaiso.toml` | Optional project settings; excludes credentials and extra host mounts |
| Independent profile storage | Owner-only `0700` directories and `0600` files; outside all worker shares |

## Stop and resume

| Action | Meaning |
|---|---|
| Foreground agent exit | Remove the worker by default; retain it with `--keep` |
| `stop` | Stop the VM, retain the worker and revoke its placeholder |
| `start` | Rerun the same worker's program; no suspended memory state is resumed |
| `rm` | Reclaim owned networks and worktrees; preserve required results first |
| Quit the TUI | VMs created by the TUI remain; previews owned by that TUI close (unreleased main; v0.0.8 planned) |

- Shared host files remain after worker removal, except for managed worktrees that are reclaimed.
- Worker `start` is separate from the agent's conversation-resume options.
- Details: [file sharing](../workspace.md) and [operations](../operations.md) (Japanese).
