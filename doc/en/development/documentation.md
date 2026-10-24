# Add or update documentation

[日本語](../../development/documentation.md) | **English**

- For contributors editing documentation.
- To use Oaiso, start with [Installation](../installation.md) or the [quick start](../getting-started.md).
- Follow the [shared rules in SKILL.md](https://github.com/pacificbelt30/sandbox-oasis/blob/main/SKILL.md#ドキュメント編集).

## Choose the content and format

| Content | Location or format |
|---|---|
| Product overview, installation and basic use | README; exclude release changes and new-version announcements |
| Release-specific changes | `doc/release-notes.md` and GitHub Releases |
| Feature usage and limitations | Feature documentation, including its first release |
| Improvement plans | Describe this product's requirements, limits and acceptance criteria; omit named third-party evaluations, rankings and source-size comparisons from public documentation |
| Comparisons and settings | Tables |
| Ordered actions | Numbered lists |
| Conditions and supporting notes | Short bullets, one point per item |

- Write Japanese documentation in simple, concise Japanese; avoid difficult phrasing.
- Mark each feature's first release, for example `Available since v0.0.3`. Label features separately when their introduction versions differ.
- Verify introduction versions against tags and history. Do not confuse the documentation update version with the feature's first release.
- Label unpublished features `Unreleased (main)` and replace this label with their first release after publication.

## Check the explanation

| Topic | Check |
|---|---|
| Reader and sequence | Distinguish first use, upgrades and development; cover prerequisites through cleanup |
| Implementation | Compare help, configuration examples and code; distinguish released behavior from main |
| Choices | Identify defaults, required and optional settings; show one working path first |
| Privileges and effects | Distinguish host/VM, user/admin, shared files, external traffic and deletion scope |
| Success and errors | Explain exit status and the next check; give recovery steps and a recheck command |
| Evidence | Distinguish starting, storage, connection, inference and artifacts; fixtures, skips and unexecuted tests do not establish live success |
| Duplication | Keep details in one place and align navigation, links, translations and configuration examples |
| Secrets | Use fake credentials, exclude authentication files and secret logs, and do not add live inference just to check documentation |

- Use [oaiso-docs](https://github.com/pacificbelt30/sandbox-oasis/blob/main/.agents/skills/oaiso-docs/SKILL.md) for documentation, or choose another workflow from the [skill index](https://github.com/pacificbelt30/sandbox-oasis/blob/main/SKILL.md#作業別のスキル).

## Chapters

| Chapter | Reader's question | Contents |
|---|---|---|
| Getting started | How do I run it the first time? | Installation, prerequisites, authentication and first worker |
| Guides | How do I complete a task? | Agents, files, images, credentials, networking and isolation |
| Reference | What is the exact interface? | CLI, configuration, Go API, Credential Store and auth API |
| Administration | How do I maintain the installation? | Daily operations, host updates, VM management, migration, troubleshooting and performance |
| Development | How does it work and how is it tested? | Architecture, networking, upstream compatibility, testing and future work |

## Write a page

1. Start with the purpose, prerequisites and first action; link required tools to their official installation instructions.
2. Use numbered steps and commands for procedures.
3. Use tables for choices, settings, defaults and limitations.
4. Keep supporting bullets short, with one point per item.
5. Link exact flags and contracts to Reference; link detailed verification records to Development.

- Distinguish implemented, planned, verified and unverified behavior.
- Keep real credentials and tokens out of examples.
- Add new feature pages to both their guide category and navigation.
- Keep chapter classifications aligned across languages; label Japanese-only pages.
- Preserve existing page paths and heading links.

## Write command and output examples

| Item | Convention |
|---|---|
| Copyable commands | `bash` blocks; keep prompts such as `$` and output separate |
| Human-readable output | A following `text` block, with success indicators and the next check |
| JSON output | `json` blocks; label formatting or excerpts |
| Environment-specific values | Identify example versions, IDs, PIDs, times and paths |
| Silent success | State that success produces no output and add a verification command |
| Errors | Show the failing check, recovery clue and recheck |
| Authentication and startup | Distinguish storage success, preparation, execution and task success |
| Multiple commands | Make the whole block runnable from top to bottom; state the user, directory and required substitutions first |
| Conditional or alternative paths | Give each path its own heading and code block; do not include commented-out alternatives in the same block |
| Interactive input, shell changes or re-login | Split blocks at the boundary and explain where to continue |

- Check local output with the real CLI, an isolated HOME and fake credentials.
- Check host-dependent output against implementation and existing evidence; adding an example does not establish live host validation.
- For long build logs, show only useful sections and label omitted content.

### Separate conditional command blocks

- Prerequisites: the host and selected agent's credentials are ready. Run as a normal user from the project directory.
- Choose exactly one block. Run its commands together, from top to bottom.

**For Codex**

```bash
oaiso init --agent codex
oaiso codex --version
```

**For Claude Code**

```bash
oaiso init --agent claude
oaiso claude --version
```

## Validate

- Requires [uv](https://docs.astral.sh/uv/getting-started/installation/). Use managed Python 3.12, as in host setup and CI.
- Run from the repository root.

```bash
uv python install 3.12
uv run --no-project --no-config --managed-python --python 3.12 --with-requirements requirements-docs.txt mkdocs build --strict
```

- Check navigation coverage, page links and heading links.
- Compare commands with the implementation; distinguish syntax checks from live host validation.
- Check that README has no release changes or announcements and that feature documentation records the correct introduction versions.
- Check command order, variables and working directories; keep alternative paths and interactive boundaries in separate blocks.
- Compare command/flag tables with normal subcommand `--help`; native `codex`/`claude` help is forwarded to the agent.
- Compare allowed keys, types, scope and precedence in both languages, including different global/project hook formats.
- Walk authentication and update procedures from prerequisites through settings, preparation and verification; link checks alone do not establish runtime correctness.
