# 御AgentISOlation (Oaiso) documentation

[日本語](../index.md) | **English**

| Item | At a glance |
|---|---|
| Purpose | Run Codex CLI and Claude Code in separate Kata VMs |
| Command | `oaiso` |
| Runtime | Containerd manages workers; Dragonball runs VMs |
| Images | Built with BuildKit |
| Host | Linux x86_64 |

- Components, directories and stop/restart behavior: [Overview and terminology](concepts.md).

## Browse chapters

| Chapter | Contents | Entry point |
|---|---|---|
| Getting started | Installation, credentials and first worker | [Installation](installation.md) → [Quick start](getting-started.md) |
| Guides | Agents, workspace, authentication, networking and isolation | [Guides](guides/index.md) |
| Reference | CLI, configuration and embedding API contracts | [Reference](reference/index.md) |
| Administration | Daily operations, host updates, VM management and recovery | [Administration](administration/index.md) |
| Development | Concepts, upstream compatibility, testing and future work | [Architecture](architecture.md), [upstream compatibility](upstream-compatibility.md) |

## Start here

- For v0.0.3+ keyring/login, use the [complete installation-to-first-run guide](guides/first-run.md). Published v0.0.2 uses manual authentication setup.

1. Follow [Installation](installation.md) to install the CLI and execution host, then verify the setup.
2. A user follows the [quick start](getting-started.md) to configure credentials and start a worker.
3. Use the [operations guide](../operations.md) (Japanese) for updates and cleanup.

- The execution host must be Linux x86_64.
- Cross-compiling the CLI does not make Kata available on macOS or Windows.

## Find a topic

| Task | Guide |
|---|---|
| Run Codex or Claude Code | [Agents](guides/agents.md), [Claude Code](claude-code.md) |
| Separate product authentication from the host | [Authentication and profiles](guides/authentication.md) |
| Move from Docker | [Migration](../migration.md) (Japanese) |
| Understand shared files and ownership | [Workspace](../workspace.md) (Japanese) |
| Inspect and manage VMs | [VM management](../vm.md) (Japanese) |
| Build a custom image | [Build command](commands/build.md) |
| Restrict outbound traffic | [Network](network.md), [policy rules](../proxy-policy.md) (Japanese) |
| Understand credentials | [Auth proxy](auth-proxy.md) |
| Use the proxy by itself | [Standalone proxy](proxy-standalone.md) |
| Look up flags and settings | [Commands](commands.md), [configuration](configuration.md) |
| Manage workers from a host product | [Go API](go-api.md) |
| Measure performance and compare revisions | [Performance and benchmarks](performance.md) |
| Diagnose a failure | [Troubleshooting](../troubleshooting.md) (Japanese) |
| Understand the design | [Architecture](architecture.md), [security](security.md) |
| Check test coverage | [Testing](../testing.md) (Japanese) |
| See planned coding agent integrations | [Future work](future-work.md) |
| Review feature and security proposals | [Improvement candidates](development/improvements.md) |
| Prepare tools for source builds | [Development tools and official installation instructions](development/toolchain.md) |
| Contribute documentation | [Structure and editing](development/documentation.md) |

[Additional sandbox controls](sandbox-features.md)

## Name and compatibility

| Item | Meaning or compatibility |
|---|---|
| Formal name | 御Agent**ISO**lation |
| English name / pronunciation | Oaiso / “oh-eye-so” |
| CLI | `oaiso` only; legacy commands are no longer provided |
| Project settings | `oaiso.toml`; rename the old `codex-dock.toml` |
| Existing user settings and credentials | Storage paths, service names and Go API names are unchanged |
| Details | [Migration compatibility](../migration.md#oaisoへの名称変更) (Japanese) |
