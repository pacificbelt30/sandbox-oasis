# Command Reference

> [日本語](../commands.md) | **English**

- Command reference for Oaiso.
- See the individual pages below for details on each command.

---

## Command Index

| Command | Description | Details |
|---|---|---|
| [`oaiso api`](local-api-sdk.md) | Local embedding API (unreleased) | `stdio` / `schema` |
| [`oaiso claude`](claude-code.md) | Run official Claude Code | Native flags; API or subscription |
| [`oaiso codex`](commands/run.md#launch-codex-cli-directly) | Pass Codex CLI arguments through unchanged | Mount current directory and exec Codex |
| [`oaiso run`](commands/run.md) | Start a sandbox container | Options, approval modes, parallel execution, package management |
| [`oaiso completion`](commands/completion.md) | Generate Tab completion scripts | Bash, Zsh, Fish, PowerShell |
| [`oaiso profile`](commands/run.md#resource-profiles) | Inspect profiles or generate definition JSON | `--node`, `--json`, `define --pids-limit` |
| [`oaiso default`](configuration.md#project-settings) | Show or change the agent started by `oaiso run` | Global config or `--local` |
| `oaiso project init/check` | Create and validate repository-local settings | [Configuration](configuration.md#project-settings) |
| [`oaiso setup`](../setup.md) | Install, update or restore the node | Plan, --apply, --rollback (Japanese guide) |
| [`oaiso login`](commands/login.md) | Independent login (development) | API/subscription, dedicated Kata, initial configuration |
| [`oaiso status`](commands/status.md) | Consolidated readiness (development) | `--json`, explicit connection/update checks |
| [`oaiso init`](getting-started.md) | Prepare the first worker | Readiness, credentials, image and proxies |
| [`oaiso runtime-check`](../troubleshooting.md) | Diagnose execution host | doctor alias, --json and recovery steps |
| [`oaiso image check`](commands/build.md) | Inspect image compatibility | Platform, tools and auth entrypoint |
| [`oaiso vm`](../vm.md) | Reconcile and manage Kata VMs | ps / inspect / start / stop / rm / prune |
| [`oaiso start`](commands/worker.md) | Restart retained worker | Same ID, restored authentication |
| [`oaiso exec`](commands/worker.md) | Run a command in an existing worker | Timeout, stdin, JSON result |
| [`oaiso wait`](commands/worker.md) | Wait for a worker to exit | Exit code, timeout, JSON result |
| [`oaiso proxy`](commands/proxy.md) | Manage Auth Proxy | build / run / serve / stop / rm |
| [`oaiso ps`](commands/worker.md#oaiso-ps) | List workers | Show running containers |
| [`oaiso stop`](commands/worker.md#oaiso-stop) | Stop containers | Single or all |
| [`oaiso rm`](commands/worker.md#oaiso-rm) | Remove containers | Stopped or forced |
| [`oaiso logs`](commands/worker.md#oaiso-logs) | View logs | Tail and follow |
| [`oaiso auth`](commands/auth.md) | Manage authentication | profile set/import/show/list, legacy show/set/rotate |
| [`oaiso network`](commands/network-cmd.md) | Manage network | create / rm / status |
| [`oaiso build`](commands/build.md) | Build sandbox image | Dockerfile auto-detection |
| [`oaiso ui`](commands/ui.md) | TUI dashboard | Key bindings |
| [`oaiso upgrade`](commands/upgrade.md) | Distribution host update plan/apply (development) | `--version`, `--apply`, image update guidance |
| [`oaiso update`](commands/update.md) | Update Go-installed CLI | Release installations use the [release installer](../setup.md#更新する) (Japanese) |
| [`oaiso policy`](sandbox-features.md) | Inspect policies, explain denials and quarantine | show / check / explain / events / presets / quarantine (alias revoke) |
| [`oaiso capabilities`](sandbox-features.md) | Validated node capabilities | JSON output |
| [`oaiso files`](sandbox-features.md#files-execution-and-recovery) | Workspace file operations | get / put / list |
| [`oaiso jobs`](sandbox-features.md#files-execution-and-recovery) | Persistent worker background commands | start / list / logs / kill |
| [`oaiso artifact`](sandbox-features.md#files-execution-and-recovery) | Recover Git changes | diff / commit |
| [`oaiso environment`](sandbox-features.md) | Pin worker environments | lock |
| [`oaiso reap`](sandbox-features.md) | Enforce lifetime, idle and retention policies | `--dry-run` / `--watch`; not automatically started |
| [`oaiso node`](../kata.md) | Host management service | serve; administrator only |
| [`oaiso runtime-lock`](../kata.md) | Pin verified runtime assets | Administrator only; not a substitute for publisher verification |
| `oaiso help [COMMAND]` | Show help | Usually also available through `COMMAND --help` |

---

## Global Options

- Apply to normal subcommands.
- `codex` and `claude` forward arguments to the official CLIs; use global configuration for Oaiso settings.

| Option | Short | Default | Description |
|---|---|---|---|
| `--verbose` | `-v` | `false` | Enable verbose logging |
| `--debug` | | `false` | Enable debug logging |
| `--config` | | `~/.config/codex-dock/config.toml` | Config file path |
| `--auth-profile` | | Unselected | Independent profile; never falls back to host credentials |
| `--auth-profile-dir` | | `~/.config/codex-dock/profiles` | Profile storage root |
| `--help` | `-h` | | Help; forwarded to the official CLI by `codex` and `claude` |
| `--version` | | | Product version through root `oaiso --version` |

---

## Related Documentation

- [Quick Start](getting-started.md)
- [Configuration Reference](configuration.md)
- [Using Auth Proxy Standalone](proxy-standalone.md)

For administrator-only `node serve` and `runtime-lock`, see [host setup](../kata.md) (Japanese).

- Removed `firewall` functionality: [migration](commands/firewall.md); not a current executable command.
