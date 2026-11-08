# Guides

[日本語](../../guides/index.md) | **English**

- Prerequisites: complete [installation](../installation.md), then check the execution host with `oaiso runtime-check`.
- Choose the task you need below.
- First use of v0.0.3+ keyring/login: [installation through first startup](first-run.md).

## Use a sandbox

Available: **unreleased (main)**. Use Kata VMs for development and tests without preparing AI credentials.

| Task | Guide |
|---|---|
| Create a VM and connect to a shell | [Prepare and create](sandbox-ui.md#prerequisites-and-startup) |
| Run a background command and inspect its output later | [Background jobs](sandbox-ui.md#manage-background-jobs) |
| Review and recover changes since launch | [Per-file review](sandbox-ui.md#compare-with-launch-and-review-individual-files), [CLI artifact export](artifacts.md#recover-changes-since-launch) |
| Inspect CPU, memory, quota and stop evidence | [Resource observations](sandbox-ui.md#cpu-memory-and-stop-evidence) |
| Inspect and transfer files | [Workspace files](sandbox-ui.md#inspect-and-transfer-files) |
| Check a web application | [Authenticated localhost preview](sandbox-ui.md#preview-a-web-application) |
| Choose actions without memorizing keys | [TUI actions menu](../commands/ui.md#choose-actions-without-memorizing-keys) |

## Use an agent

| Task | Guide |
|---|---|
| Choose how to launch Codex or Claude Code | [Agents](agents.md) |
| Use Claude API keys or subscriptions | [Claude Code](../claude-code.md) |
| List, inspect logs, stop and restart workers | [Worker operations](../../operations.md) (Japanese) |
| Manage workers in the TUI | [TUI](../commands/ui.md) |
| Queue tasks and adopt reviewed changes | [Tasks and result review](tasks.md) |

## Prepare the workspace

| Task | Guide |
|---|---|
| Share project settings | [Project configuration](../configuration.md#project-settings) |
| Use workspace mounts and worktrees | [File sharing and ownership](../../workspace.md) (Japanese) |
| Add Dockerfiles, templates and packages | [Build images](../commands/build.md) |
| Use file restrictions, jobs, PTY and artifact recovery | [Additional sandbox controls](../sandbox-features.md) |

## Configure credentials and networking

| Task | Guide |
|---|---|
| Separate product and host authentication | [Authentication and profiles](authentication.md) |
| Allow or deny outbound destinations | [Network setup](networking.md), [policy rules](../../proxy-policy.md) (Japanese) |
| Check credential storage and isolation limits | [Security](../security.md) |
| Understand the credential proxy | [Auth proxy](../auth-proxy.md) |
| Use the proxy in another setup | [Standalone proxy](../proxy-standalone.md) |

## Next steps

- Exact flags, settings and APIs: [Reference](../reference/index.md).
- Host updates and recovery: [Operations](../../operations.md) (Japanese).
- Application integration: [Go API](../go-api.md).
