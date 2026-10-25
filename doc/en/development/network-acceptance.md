# Network and revocation acceptance

[日本語](../../development/network-acceptance.md) | **English**

- Additional checks: **v0.0.4**.
- Reuses existing tests and local synthetic upstreams; no live accounts.
- Automated tests, real Kata evidence and unverified conditions are recorded separately.

## Coverage

| Condition | Automated checks | Additional real Kata checks |
|---|---|---|
| DNS | Entire answer set, mixed A/AAAA, answer order, verified-IP dialing, changed answers on later connections, empty answers | DNS scenarios use automated fixtures; real Kata destinations use fixed IPs |
| IPv4/IPv6 | Private, loopback, link-local, IPv4-mapped and metadata denial | Guest HTTP/CONNECT denials and successful public IPv4/IPv6 CONNECT echo |
| Redirect | Cannot escape destination grants | Allowed IP redirecting to metadata is denied |
| CONNECT | Same destination/port restrictions as HTTP; allowed TLS tunnel | Active IPv4/IPv6 tunnels end on quarantine |
| SSE | Three AI routes × revoke, expiry, reissue, restore and proxy stop | Token revocation while the VM runs; closure observed at upstream peers |
| WebSocket | Revoke, expiry, reissue, restore and proxy stop | Concurrent SSE/WS peers close on revocation |
| Failed stop | Node errors, false ACK, persistence failure and unknown state never establish completed quarantine | Real successful stop, peer closure and restart denial; failures use automated fault injection |
| CLI reporting | Unknown/running/paused/restarting show `QUARANTINE PENDING`; JSON includes `quarantine_pending: true` | Real post-stop `show` and `explain` |

- General and AI traffic have separate authority. AI token revocation does not stop general CONNECT; use `policy quarantine` to stop all traffic.
- IPv6 evidence covers proxy destination validation and tunneling, not guest IPv6 address assignment or a live external IPv6 path.
- A test-only image exposes connection counts, never real API keys or request contents. It is not a production authentication proxy.

## Run automated checks

- Requires the [development tools](toolchain.md). Run as an ordinary user at repository root.
- No live AI authentication or inference.

```bash
go test -race ./internal/networkpolicy ./internal/authproxy ./internal/engine ./internal/sandbox ./internal/workeregress ./cmd
```

- This alone does not run real Kata acceptance.
- Main checks: `TestMixedDNSFamiliesFailClosedAndPublicAddressesArePinned`, `TestWorkerSSELeaseEndsOnRevocationAndExpiry`, `TestWorkerWebSocketLeaseEndsOnRevocationAndExpiry`, `TestPolicyCommandsNeverReportUnconfirmedStopAsBlocked`.
- Stop failures: `TestQuarantinePersistsBeforeStopAndBlocksStartAfterReload`, `TestQuarantineRequiresUpdatedNodeAndConfirmedTargetStop`.

## Run real Kata acceptance

1. Follow [disposable-node preparation](../../testing.md#新規導入更新復旧を検証する) (Japanese) to build the `setup-test-a`/`setup-test-b` bundles, matching acceptance binary and standard proxy/worker images.
2. Build the additional test-only proxy with the same `setup-test-a` version. Do not use it for production authentication.

```bash
mkdir -p .kata-verification/network-proxy
CGO_ENABLED=0 go test -c -ldflags "-X github.com/pacificbelt30/codex-dock/internal/buildinfo.Version=setup-test-a" \
  -o .kata-verification/network-proxy/fixture.test ./internal/authproxy
docker build -f deploy/kata/Dockerfile.proxy-fixture \
  --build-arg CODEX_DOCK_BUILD_VERSION=setup-test-a \
  -t oaiso-network-fixture:verification .kata-verification/network-proxy
docker save -o .kata-verification/setup-e2e/network-images.tar \
  codex-dock:kata-guard codex-dock-proxy:policy-verification oaiso-network-fixture:verification
```

3. Select network/revocation acceptance. The controller cleans up its own node and volume.

```bash
CODEX_DOCK_DISPOSABLE_SETUP=1 uv run --no-project --no-config --managed-python --python 3.12 \
  python scripts/verify-setup.py \
  --bundle dist/node-bundle-a --next-bundle dist/node-bundle-b \
  --images .kata-verification/setup-e2e/network-images.tar \
  --acceptance .kata-verification/setup-e2e/acceptance.test \
  --network-proxy-image oaiso-network-fixture:verification \
  --acceptance-run '^TestKataNetworkRevocation$'
```

- Success requires the named suite to pass and no remaining VMs, tasks, containers or dedicated networks after setup/update/recovery.
- Check `acceptance.txt`, including upstream peer counts reaching zero for SSE/WS and general traffic.
- Version mismatch, missing images or unavailable KVM are failures, never passing skips or runc substitutions.
- Results and evidence: [testing](../../testing.md) (Japanese).
