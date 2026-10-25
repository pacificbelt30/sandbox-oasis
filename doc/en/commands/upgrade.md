# `oaiso upgrade` — update a binary distribution

[日本語](../../commands/upgrade.md) | **English**

- **Available in v0.0.3 or later.**
- No Go installation required. The command reuses the distribution installer and explains host/image update order.

## Review a plan

**Query the latest stable release**

```bash
oaiso upgrade
```

**Plan a pinned release**

```bash
oaiso upgrade --version v0.0.3
```

- [Prepare GitHub CLI](../installation.md#2-prepare-tools) first. GitHub login is not required.
- Fetch `install.sh`, `SHA256SUMS` and `provenance.json`; verify the installer signature, source identity, tag and checksum before running the plan.
- The installer downloads and verifies the remaining assets. See [verification scope](../guides/signed-releases.md).
- Planning does not replace product binaries/services. It can prepare uv/Python in user storage and download release assets.

```text
Installed CLI: dev
Target release: v0.0.3

Host update: CLI, node, helpers and pinned runtime assets.
Images: rebuild the default worker after the host update; prepare proxies with the new CLI.
Custom images/templates: review separately.
Before applying: save work, then stop/remove retained workers and proxies with the installed CLI.
...
Plan only. Apply the same release with: oaiso upgrade --version v0.0.3 --apply
```

- Example output; versions and plans depend on your host.
- Profile encryption compatibility is checked before applying a distribution; incompatible distributions are refused. Use `--auth-profile-dir` to select another profile root.

## Apply

1. Review the plan and [release notes](../../release-notes.md).
2. Save work; stop and remove workers/proxies using the installed CLI.
3. Apply the reviewed version:

```bash
oaiso upgrade --version vX.Y.Z --apply
```

- Replace `vX.Y.Z` with the reviewed release tag.
- Run as the normal operator; the installer uses `sudo` for administrative changes.
- Existing installer checks block replacement while retained/live resources or installation inconsistencies remain.
- See [setup](../../setup.md) for recovery.

## Scope and order

| Order | Target | Action |
|---|---|---|
| 1 | CLI, node, guard, IO logger and pinned runtime assets | Distribution installer through `upgrade --apply` |
| 2 | Default worker image | New CLI: `oaiso build --template plain --tag codex-dock:latest` |
| 3 | Authentication proxy | New CLI: `oaiso init --agent codex` or `--agent claude` |
| 4 | Custom images/templates | Review and rebuild individually |
| 5 | Behavior | Verify connections/inference in a new worker |

- Images are rebuilt with the newly installed CLI, not the old CLI still in memory.
- [`oaiso update`](update.md) separately updates only a Go-installed CLI.
