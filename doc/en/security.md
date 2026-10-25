# Security

[日本語](../security.md) | **English**

| Boundary | Implementation |
|---|---|
| Worker isolation | Separate guest kernel with Kata runtime-rs / Dragonball |
| Admission | Root node verifies assets, host processes and resource limits before releasing user code |
| Failed checks | Stop startup; no fallback to runc or QEMU |

## Worker boundary

| Control | Default |
|---|---|
| User | Non-root; caller's UID/GID |
| Capabilities | All sets cleared |
| Guest restrictions | `NoNewPrivs=1`, `Seccomp=2` |
| Guest resources | 1 CPU, 2 GiB memory, 512 PIDs |
| Host VM envelope | 1 CPU, 3 GiB memory, no swap, 512 PIDs |
| Network | No direct internet, host or peer access; selected proxies only |
| Management | No node/containerd/BuildKit sockets or allocation records in workers |

- Verified Kata workers: Codex's inner sandbox uses `danger-full-access` to avoid nested namespace/seccomp conflicts.
- Agent approval settings remain separate.
- Shared files remain accessible under the selected mount permissions.

## Credentials

### Storage and worker-visible fields

| Information | Handling |
|---|---|
| Real API keys and OAuth access/refresh/ID tokens | Retained by the auth proxy; never returned to workers |
| Worker credentials | Short-lived placeholders |
| Codex ID field | Fresh dummy JWT: account ID, plan and FedRAMP flag only |
| Original JWT headers/signature, email, user IDs and arbitrary claims | Never forwarded |
| Dummy JWT | Client metadata only; not used for upstream authentication |
| Egress proxy | Receives no real credentials |

### Acquisition, refresh and existing workers

- Initial acquisition and refresh return only selected fields.
- Refresh errors retain HTTP status and known CLI recovery codes.
- Issuer bodies and transport error details are not forwarded.
- Tests: [pinned CLI verification](../testing.md#codexのidトークン分離) (Japanese).
- Existing files/logs are not automatically erased: update the proxy, then stop and recreate old workers.

### Files and mounts

| Item | Contract |
|---|---|
| Legacy auth files | Caller UID/GID, private HOME, read-only mounts; owner-only `0600` retained |
| Independent profiles | No host auth mounts/environment; only the selected `0700` directory mounted read-write for refresh |
| Credentials and lock files | `0600`; CAS, fsync and atomic rename |
| Worker restart | Verify profile and connection generation |
| Capabilities and source modes | No added capabilities or source-permission changes |
| Admin authority | Optional separate `0600` file, read-only mount into auth only; value stays out of argv |
| Worker shares | CLI rejects profile-storage overlaps; embedding callers must also keep credentials/admin authority outside shares |
| At-rest encryption | New v0.0.3+ login profiles default to Secret Service plus age/HMAC-SHA256; explicit passphrase mode remains available. No ciphertext or keys in work VMs |

- Trusted control environments may run inside WSL or a Linux VM.
- Untrusted work VMs must never receive real credentials.
- Requirements and deployment checks: [WSL, VMs and headless systems](credential-store.md#independent-credential-management-wsl-vms-and-headless-systems).

### Encryption limits

- Protect inactive credential files; host root/same-UID process access, live memory, swap and outer VM snapshots are outside this protection.
- Never store a passphrase alongside ciphertext. Lost keys require reauthentication.
- Unlock failure never switches to host credentials, plaintext or another backend. Coexisting migration records fail closed.
- Keyring storage encryption/locking depend on OS configuration.
- [Usage](guides/encrypted-credentials.md), [specification](development/local-credential-store.md), [verification gaps](../testing.md).

### Token and admin boundaries

| Condition | Behavior |
|---|---|
| HTTP / WebSocket AI requests | Validate issued, unexpired token before contacting upstream |
| Revocation, expiry or replacement | Cancel active streams and tunnels |
| Admin operations | Separate listener, unreachable from worker networks |

## Trusted host operators

| Item | Trust or verification |
|---|---|
| Node socket / BuildKit access | Root-equivalent; operators choose images, commands and host mounts |
| Runtime assets, tools and configuration | Pinned in a root-owned manifest |
| Manifest | Does not replace release provenance or checksum verification |
| Asset validation | Reread and hash every asset on every validation |
| Parallel readers | At most GOMAXPROCS and four readers per validation |
| Timestamp cache | Never used to skip verification |
| `cgroup.kill` recovery | Verify all occupants of a dedicated resource group first |
| Other processes | Do not adopt arbitrary PIDs or other products' VMs |

[Measurement guide](performance.md)

## Limits

### Communication and authorization

| Item | Limit or default |
|---|---|
| Worker-to-auth-proxy traffic | Unreleased main managed proxies use TLS 1.3 and execution-bound client certificates; [usage/migration](guides/worker-tls.md). General egress retains HTTP proxy transport |
| Worker token binding | Managed TLS routes verify token and current execution public key. A copied token alone is insufficient. A compromised guest retains its own allowed authority. Custom legacy HTTP proxies remain unchanged |
| General egress | Default deny unless the host explicitly disables it |
| Empty destination grants | Disconnect general egress |
| Worker-specific allowlists | Select policy-specific proxies; identical policies can share |
| Rule updates | Immutable per proxy; no hot reload |
| Model authorization | Opt-in at the host auth proxy; a CLI model flag alone does not enforce authorization |
| Shared files / Git metadata | Remain writable where explicitly shared read-write |

- Model routes, WebSocket denial and separate paths: [additional controls](sandbox-features.md#model-authorization-and-staged-startup).

### Quarantine

| Operation or condition | Effect |
|---|---|
| `policy quarantine` / Go API `Quarantine` | Stop one worker and durably prohibit restart at the node |
| Work terminated | Jobs, general communication and AI communication |
| Workspace | Retained; create a new worker to resume under new rules |
| Error | Does not establish completed isolation; retry |

[Quarantine semantics](sandbox-features.md#stop-communication-and-quarantine)

### Verification scope

| Status | Coverage |
|---|---|
| [Recorded real Kata / AI tests](../testing.md) (Japanese) | Real Kata acceptance and existing-auth.json WebSocket/HTTP SSE inference |
| [Additional network/revocation checks](development/network-acceptance.md) (Unreleased main) | Automated DNS/failed-stop tests; real Kata denials, active SSE/WS revocation and general-traffic closure on quarantine; synthetic upstreams |
| Requires separate verification | Long-running real OAuth refresh, interactive keyboard use, arbitrary custom images |
| Claude fixture coverage | Distinct API/subscription placeholders; no real access/refresh tokens in workers |
| Outside Claude fixture coverage | Live Anthropic inference and subscription usage accounting |
| Ongoing maintenance | Host kernel, KVM, Kata and guest kernel updates |

[Claude Code](claude-code.md) · [Acceptance record](../testing.md#claude-codeの受入) (Japanese)

## Operation audit

- Unreleased (main): [Persistence, gap detection, capacity and failure behavior](guides/audit.md). Local root-resistant integrity is not guaranteed.
