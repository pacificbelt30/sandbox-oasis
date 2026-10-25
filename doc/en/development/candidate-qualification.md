# Qualify candidate updates

[日本語](../../development/candidate-qualification.md) | **English**

- Available: **Unreleased (main)**. Use an isolated candidate checkout and test images.
- Never changes current pins, baseline or releases automatically.

| Stage | Checks | Meaning |
|---|---|---|
| Static | Existing monitored definitions/CLI declarations | No unreviewed monitored drift; runtime remains untested |
| `plan` | Static comparison and capture/checkout pin match | Produces a plan, qualification remains `pending` |
| `run`: CLI | Both real CLIs, fresh HOME, environment allowlist | Synthetic API/OAuth, proxy/refresh/SSE/tool fixtures |
| `run`: Kata | Compiles acceptance from this checkout; disposable node setup/network/diagnostics/broker/usage/update/recovery | Actual guest CLI versions, Codex SSE and Claude tool+SSE against synthetic upstreams |

- Required skipped/missing cases never pass.
- Records SHA-256 for capture, binaries, image archive, bundle manifests, acceptance executable/log, plus commit/tracked-patch/script hashes.
- These are operator diagnostics, not signed provenance.

## Prerequisites

| Asset | Requirement |
|---|---|
| Isolated checkout | Exact candidate versions in both `docker/sandbox/Dockerfile` ARGs; keep normal main unchanged |
| Capture and CLI binaries | [Static capture procedure](../upstream-compatibility.md); same versions, staged privately without global CLI updates |
| Developer tools | Go, uv-managed Python 3.12, populated Go module cache; fixture runs use `GOPROXY=off` |
| Real Kata assets | [Setup/update/recovery test assets](../../testing.md#新規導入更新復旧を検証する): two bundles, image archive, test node and runtime cache; matching CLI/proxy protocol/build versions |
| Worker | Both candidate CLIs and current entrypoint |
| Synthetic proxy | Test-only `internal/authproxy` fixture executable image; no real credentials |

## Plan

From the candidate checkout root, after capturing `.kata-verification/contracts/candidate.json`:

```bash
uv run --no-project --no-config --managed-python --python 3.12 \
  --with-requirements requirements-contracts.txt python scripts/qualify-upstream.py plan \
  --candidate .kata-verification/contracts/candidate.json \
  --out .kata-verification/candidate-plan
```

| Result | Action |
|---|---|
| `pending`, exit 0 | Plan only; no CLI/runtime compatibility verdict |
| `hold`, exit 1 | Review `static.md`; align candidate checkout pins/capture/images if mismatched |
| `error`, exit 2 | Repair acquisition/parsing/prerequisites; retain current version |

- Output must be a **new** directory under this checkout's `.kata-verification/`.
- To continue a reviewed static hold, explicitly supply `--reviewed-static-sha256` with the reviewed `static.json` digest. Never acknowledge unread differences.
- Baseline comparison may retrieve fixed public URLs. Fixtures do not perform live provider login/inference/OAuth refresh.

## Run the complete qualification

Replace asset paths/tags with your prepared fixtures. Docker is only the disposable outer host; product workers use Kata.

```bash
uv run --no-project --no-config --managed-python --python 3.12 \
  --with-requirements requirements-contracts.txt python scripts/qualify-upstream.py run \
  --candidate .kata-verification/contracts/candidate.json \
  --out .kata-verification/candidate-run \
  --codex .kata-verification/contracts-tools/node_modules/.bin/codex \
  --claude .kata-verification/contracts-tools/node_modules/.bin/claude \
  --bundle dist/node-bundle-a --next-bundle dist/node-bundle-b \
  --images .kata-verification/setup-e2e/images.tar \
  --cache .kata-verification/setup-e2e/cache \
  --proxy-image oaiso-candidate-proxy:verification \
  --network-proxy-image oaiso-candidate-fixture:verification \
  --worker-image oaiso-candidate-worker:verification \
  --node-image codex-dock-setup-node:verification
```

- Pass: exit 0, `qualification.json` has `status: pass`, both runtime stages `pass`, and `adopted: false`.
- Failure retains pins/baseline. Inspect stage logs and retry corrected assets with a new output directory.
- Controller reclaims only its own node/volume, leaving shared host services/workers unchanged.
- Adoption, baseline/pin changes and release remain separate explicit work.
- Scheduled CI creates the static result and plan artifact only; no KVM/live login/inference. Live subscriptions, provider production behavior, billing and WSL require separate validation.
- Keep generated captures/binaries/raw logs out of Git; record verified scope in [testing](../../testing.md).
