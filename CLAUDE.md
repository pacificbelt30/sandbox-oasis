# Working on codex-dock

codex-dock runs Codex CLI and Claude Code in Kata VM-isolated workers.
Read [the documentation index](doc/index.md) for usage and [architecture](doc/architecture.md) for component boundaries.
This file describes development conventions; [SKILL.md](SKILL.md) has reusable implementation patterns.

For recurring work, select the matching [repository skill](SKILL.md#作業別のスキル) under `.agents/skills/`: documentation, change verification, real Kata, performance, upstream compatibility, release, or Git synchronization. Read only the relevant workflows; using a skill does not expand the user's authorization.

## Execution model

- Linux x86_64 + KVM + cgroup v2.
- CLI/TUI → root node Unix socket → dedicated containerd namespace `codex-dock`.
- Workers use Kata runtime-rs with embedded Dragonball. Proxies use runc on the same containerd.
- Standalone BuildKit builds OCI images and imports them through the node.
- Docker Engine, Docker CLI and QEMU are not runtime dependencies. Docker SDK types remain an internal compatibility surface.
- Kata failures stop startup. Runc compatibility must be explicitly selected.

## Code map

| Directory | Responsibility |
|---|---|
| `cmd/`, `internal/ui` | Cobra CLI and tview/tcell TUI wiring |
| `internal/sandbox` | Worker lifecycle, bootstrap gate, mounts, token restore, deletion journal |
| `internal/engine` | Containerd SDK, images/snapshots/tasks, native networking, persistent console I/O |
| `internal/kataguard` | Pinned assets, allocation ledger, process/cgroup verification, orphan recovery |
| `internal/authproxy` | AI credentials, token/admin API, reverse HTTP/SSE/WS, general forwarding |
| `internal/networkpolicy` | Destination rules, DNS answer validation, verified-IP dialing |
| `internal/worktree`, `internal/template` | Git worktrees and image templates |
| `docker/` | Embedded OCI image recipes and entrypoint |
| `deploy/kata/` | Host service/configuration examples and guest preparation |
| `doc/`, `mkdocs.yml` | Japanese/English documentation and navigation |

## Invariants

Real API keys and OAuth access/refresh tokens belong only in the auth proxy.
Workers receive short-lived placeholders and synthetic client-compatible ID metadata; never return real OAuth ID tokens.
The auth proxy runs as the caller's UID/GID with a private HOME. Legacy credential binds are read-only; independent profiles have an exclusive writable store for durable refresh. Keep `0600` files readable without adding capabilities.

Every AI HTTP/WS route must validate a live issued worker token before connecting upstream or injecting credentials.
Pass the route-specific injector to both HTTP and WebSocket paths.
Revoke, expiry, replacement, restore and proxy stop must cancel existing connections.
Keep admin listeners separate from worker-facing data listeners.

The egress proxy has no credentials. General destination policy belongs in `internal/networkpolicy`.
Preserve private-address blocking, DNS checks, redirect/CONNECT checks and verified-IP dialing.
`--no-internet` disconnects general egress while preserving fixed AI routes.

Preserve the Kata bootstrap gate, non-root identity, capability removal, NNP, seccomp and guest/host resource limits.
Do not expose management sockets, host runtime assets or private ledgers to guests.
Node and BuildKit operators are trusted and root-equivalent.
Only recover resources with verified ownership; unavailable containerd means unknown, not orphan.
Persist deletion intent before removing resources, and retain retry information on failure.

## Coding conventions

Keep business logic under `internal/` and commands thin. Do not add global state.
Use Cobra/Viper for flags and settings, but document only environment overrides actually wired in code.
Return contextual errors, check type assertions and close HTTP bodies. Use contexts for HTTP requests.
Run gofmt for Go changes and follow `.golangci.yml`.

User templates override built-ins by name. Preserve template validation, explicit image precedence, matching-tag auto-build and on-disk staleness checks.
The TUI already supports restart and live logs; do not treat them as unimplemented.
Known limits and verified behavior belong in [security](doc/security.md) and [testing](doc/testing.md), not duplicated status tables.

## Checks

Go 1.26.0 or later is required. Run checks appropriate to the change:

```bash
make build
make test
make vet
make lint
```

`make test` covers all current runtime/proxy packages with race detection and coverage.
If dependencies change, run `go mod tidy` and review go.mod/go.sum.
Real runtime changes require `make kata-e2e` on a prepared execution host; see [testing](doc/testing.md).
Do not substitute the legacy Docker E2E script for Kata acceptance.
Cross-build success does not establish non-Linux runtime support.

For documentation changes:

Follow the [documentation rules in SKILL.md](SKILL.md#ドキュメント編集) and the [editing guide](doc/development/documentation.md), including README scope, feature introduction versions and separate command blocks for alternatives.

```bash
mkdocs build --strict
```

Keep Japanese and English behavior descriptions consistent. Prefer one authoritative page per topic and link to it from summaries.
Do not include credentials or private test logs in documentation or commits.
