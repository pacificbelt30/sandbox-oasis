# Static upstream contract checks

- The development-only checker detects changes to Codex, Claude Code, public APIs, SDK types and authentication-related sources using deterministic rules.
- It does not change worker execution, proxy behavior or credentials.
- It does not run login, inference, OAuth refresh, tools or Kata acceptance.
- See [testing](../testing.md) for those checks.

## Monitored contracts

`compat/upstream/manifest.json` selects the contracts; `compat/upstream/baseline.json` stores the reference observation.

| Source | Selected declarations |
|---|---|
| OpenAI OpenAPI | POST `/responses`, `/responses/compact`, `/chat/completions`, `/completions`, `/embeddings`, `/moderations`, including referenced types, security and servers |
| Codex config | Provider selection/definitions, ChatGPT URL and the app/compression/WebSocket flags used by this product |
| Codex CLI | Root/exec help, including required flags and their signatures/help |
| Codex app-server | Generated Initialize, ThreadStart, TurnStart and TurnInterrupt schemas |
| OpenAI SDK | Responses request and stream-event declarations |
| Codex authentication | Fingerprints of upstream OAuth storage and refresh source files |
| Claude Code CLI | Root/auth-status help, including required flags and their signatures/help |
| Anthropic SDK | Messages/count_tokens request and stream-event declarations |
| Claude Agent SDK | Public Python SDK annotations and type aliases |
| Claude authentication | Fingerprint of official CHANGELOG lines mentioning OAuth, credentials, login and related terms |

- Floating GitHub refs are resolved to commit SHAs once per repository/ref per capture.
- Snapshots record immutable source URLs, original artifact hashes, capture time and actual CLI versions.
- Tracked branch observations and CLI releases are separate.
- The initial CLI baseline matches the worker image: Codex 0.163.0 and Claude Code 2.1.306.
- Recording a baseline does not certify upstream source correctness or live compatibility.

### Baseline storage

- The versioned baseline is a compact schema 2 source lock.
- Eleven public API/SDK/auth sources retain immutable commit URLs, raw artifact SHA256 hashes and normalized contract SHA256 hashes.
- Three CLI-generated sources (Codex/Claude help and Codex app-server schemas) retain their comparison data because an immutable public URL cannot reliably reproduce those outputs.
- The initial baseline shrank from 1.29 MB / 43,255 lines to approximately 92 KB / 2,425 lines.

- `capture` still writes a complete schema 1 candidate.
- `compare` restores reference contracts from an unchanged candidate, a verified cache, or the pinned URL, in that order, then performs the same structural comparison.
- Both hashes are checked for cached or downloaded data.
- It never substitutes latest sources: an unavailable or mismatching reference produces `error` (exit `2`).
- Changed sources require network access when their reference is not cached.

- The default cache is the Git-ignored `.kata-verification/upstream-contract-cache`; override it with `compare --cache-dir PATH`.
- CI caches restored references to reduce repeated downloads.
- The cache and 30-day artifacts are optional helpers; the versioned lock defines the baseline.
- Restoration without a cache depends on the upstream pinned URL remaining available.
- Complete schema 1 baselines remain supported.
- The compact format currently supports GitHub commit URLs.

## Rules

| Level | Examples | Result |
|---|---|---|
| `breaking` | Selected flag/operation/property removed, type changed, required request field added, required response field removed | Hold |
| `review` | Security/server change, SDK declaration change, changed composition or unknown constraints, authentication source/release-note drift | Hold |
| `info` | Optional property added, unused flag changed, CLI version changed, request enum widened | Record |
| `error` | Fetch/parse failure, unresolved reference, missing source or manifest mismatch | Inspection failed |

- Required-field and enum comparisons account for request/response direction.
- Local schema references and cycles are followed.
- External references fail inspection.
- Descriptions/examples and some ordering differences are ignored.
- Unproven compatibility is held conservatively.

- App-server, OpenAI SDK and Claude Agent SDK sources are advisory because this product does not directly interpret their public types.
- Their breaking differences become `review`, rather than claiming a demonstrated runtime failure.
- Coverage is limited to the operations, files and flags selected in the manifest; SDK imports are not recursively scanned.

## Run locally

- Use Python 3.12 or later.
- Offline rule tests and baseline/worker-pin validation:

```bash
make contracts-check
```

- To fetch upstream declarations, install the YAML development dependency and stage CLIs without updating global installations.
- Change the npm version selectors to inspect other candidates.

```bash
npm install --prefix .kata-verification/contracts-tools --ignore-scripts --no-audit --no-fund \
  @openai/codex@0.163.0 @anthropic-ai/claude-code@2.1.306
claude_installer=.kata-verification/contracts-tools/node_modules/@anthropic-ai/claude-code/install.cjs
if [ -f "$claude_installer" ]; then node "$claude_installer"; fi
uv run --no-project --no-config --managed-python --python 3.12 \
  --with-requirements requirements-contracts.txt python scripts/upstream-contracts.py capture \
  --codex .kata-verification/contracts-tools/node_modules/.bin/codex \
  --claude .kata-verification/contracts-tools/node_modules/.bin/claude \
  --out .kata-verification/contracts/candidate.json
uv run --no-project --no-config --managed-python --python 3.12 \
  --with-requirements requirements-contracts.txt python scripts/upstream-contracts.py compare \
  --candidate .kata-verification/contracts/candidate.json \
  --json .kata-verification/contracts/report.json \
  --markdown .kata-verification/contracts/report.md
```

- `install.cjs` selects the platform executable for native Claude releases; omit that step for older packages without it.
- CLI inspection uses a new HOME/config directory and an environment allowlist.
- Host API keys, OAuth variables, login files and SSH agents are not passed.
- The official CLI's help/version/schema code still executes: inspect trusted distributions in a dedicated environment.

- `compare` exits with `0` for informational changes only, `1` for a hold and `2` for inspection failure.
- Report status is `pass`, `hold` or `error`.
- Partial capture failures are saved in the candidate JSON.

## CI and baseline maintenance

- Normal CI runs offline rule tests and validates lock URLs, hash formats, embedded CLI contracts, the manifest and worker Dockerfile CLI pins.
- This offline check does not download or verify public artifact contents.
- Changing a worker CLI pin without updating its baseline fails validation.

- The read-only `Upstream static contracts` workflow runs daily or manually against latest upstream declarations and candidate CLIs.
- Manual runs accept CLI version selectors.
- Reports appear in the Actions summary and artifacts retained for 30 days.
- Review/breaking findings and inspection failures fail the job.
- It does not automatically update baselines, images or operating configuration, or create PRs.

- Update the baseline only when adopting reviewed CLI or contract changes.
- Daily inspection does not create Git changes.
- Upstream change frequency has not been measured; broad SDK type definitions may generate findings more frequently than CLI adoption.

- Review the precise changes and immutable sources, make required fixes, then run appropriate [compatibility tests](../testing.md).
- Convert the reviewed full candidate to a compact lock and commit it:

```bash
uv run --no-project --no-config --managed-python --python 3.12 \
  --with-requirements requirements-contracts.txt python scripts/upstream-contracts.py compact \
  --snapshot .kata-verification/contracts/candidate.json \
  --out compat/upstream/baseline.json
make contracts-check
```

- Update worker pins when adopting a CLI.
- A manifest change requires explicit coverage review and recapture.
- Snapshots from a different manifest are rejected.

## Limits

- Server-only changes to revocation, concurrent refresh, authorization, errors or stream disconnection require behavioral tests.
- Claude subscription refresh has no public schema monitored here.
- Unannounced changes and files outside the manifest are not covered.
- Authentication fingerprints can also flag comment-only changes.
- `pass` only means no blocking monitored static drift; it does not certify live API/subscription operation or overall security.

**Official references**

| Component | Reference |
|---|---|
| OpenAI OpenAPI | [OpenAI OpenAPI](https://developers.openai.com/api/docs/libraries) |
| Codex schema generation | [Codex schema generation](https://learn.chatgpt.com/docs/app-server) |
| Claude CLI | [Claude CLI](https://code.claude.com/docs/en/cli-reference) |
| Anthropic SDKs | [Anthropic SDKs](https://platform.claude.com/docs/en/cli-sdks-libraries/overview) |
## Runtime qualification

- Unreleased main: [Candidate qualification](development/candidate-qualification.md) connects static checks to isolated real-CLI and Kata fixtures. Scheduled CI prepares a plan; it never adopts pins automatically.
