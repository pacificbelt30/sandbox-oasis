# Feature and security improvement candidates

[日本語](../../development/improvements.md) | **English**

- Baseline: published v0.0.3. This page tracks proposals, additional verification and work completed on main.
- P0 establishes guarantees; P1 improves daily use/security; P2 requires measured benefit. Adoption, effort and release dates are undecided.
- No named-product evaluation or ranking is published here.
- Preserve convenient local operation, the authentication proxy, isolation, revocation and cleanup.

## Establish guarantees first

| Priority | Item | Current state and remaining work | Acceptance |
|---|---|---|---|
| P0 | Remaining dependency findings | **Dependency updates and applicability review completed (Unreleased main)**. [Findings](dependency-security.md) reduced from 15 to 2; both concern unused daemon features | Automated regressions pass. No finding exclusions; rationale recorded. Follow-up real Kata coverage uses the network/revocation suite |
| P0 | Live-account authentication | Independent login, auth-only VM and encrypted storage implemented; live accounts/long refresh unverified | API/subscription, refresh conflicts, proxy restart, cancellation, active-stream revocation, no secrets in logs/files |
| P0 | Network/revocation acceptance | **Additional coverage completed (Unreleased main)**. [Scope and reproduction](network-acceptance.md) | Automated DNS/failed-stop checks; real Kata traffic, active SSE/WS revocation and quarantine pass. CLI text/JSON distinguish unconfirmed stops |
| P0 | WSL/outer VM/headless acceptance | Credential design differs from actual nested-VM execution requirements | Install→unlock→login→run→stop→upgrade→recover; diagnose missing KVM without silent weaker isolation |

- Live-account authentication/inference and real WSL acceptance remain unverified. Network/revocation uses real Kata with synthetic credentials.
- Existing evidence and gaps: [testing](../../testing.md) (Japanese).

## Strengthen existing capabilities

| Priority | Capability | Addition | Constraint |
|---|---|---|---|
| P1 | Per-run usage limits | Request/concurrency/usage ceilings alongside model allowlists | Separate estimated spending from exact cutoff. Revocation cannot undo already-billed upstream work |
| P1 | Avoid reaping active main jobs | **Completed (Unreleased main)**. `--protect-main` and CLI/Go activity renewals | [Contract](../sandbox-features.md#primary-process-protection-and-activity-leases). Leases expire within five minutes; maximum lifetime independent. Real Kata renewal/release/idle-stop checks pass |
| P1 | Aggregate diagnostics/observability | Persist/export existing status, denials, launch timing and memory data | Exclude secrets, bound retention, require no external permanent service |
| P1 | Credential-broker configuration | Fixed-origin/path/method templates, preflight and TUI support | Reuse existing broker; no secret display or unapproved-host injection |
| P1 | Candidate updates | Connect existing static drift detection to pinned-CLI/runtime acceptance | No adoption from static diffs alone; preserve current pins and rollback on failure |
| P2 | Startup/Exec/concurrency | Measure phase timings, waits, CPU, memory and I/O; optimize dominant costs | Comparable before/after measurements, credible ≥20% opportunity, no skipped isolation checks |

## Additional features

| Priority | Feature | Initial scope | Acceptance |
|---|---|---|---|
| P1 | Machine-readable embedding contract | Public Go resources, request IDs, states, errors and capabilities | Version mismatch, cancellation, lost response, retry and cleanup tests; no public management socket |
| P1 | Thin Python/TypeScript SDKs | Same-host create/execute/inspect/cleanup | Preserve Go retry/authority/failure semantics; no secrets in argv/stdout |
| P2 | Disk checkpoints | Save workspace disk state and restore into a new environment | Not RAM preservation; define mounts, concurrent writes, size, secret exclusion and restore compatibility |
| P2 | Prestarted environments | Small benefit-assessment prototype | No unassigned credentials/user data; revalidate at allocation; require acceptable standby memory and measured ≥20% benefit |
| P2 | Further resource controls | Connections, transfer, file descriptors and I/O limits | Distinct from existing CPU/memory/PID/body limits; preserve legitimate long streams and contain noisy workloads |

- Additional agent acceptance remains in [future work](../future-work.md). Never weaken credential separation merely to increase the agent count.

## Increase security strength

| Priority | Measure | Evidence and intent | Verification and residual limits |
|---|---|---|---|
| P1 | Encrypt VM–proxy traffic and bind authorization to execution origin | [Current boundary](../security.md) uses HTTP on a dedicated network and bearer tokens; consider short-lived authenticated execution channels | Copied bearer alone must fail from another origin. A fully compromised VM can still perform its authorized operations |
| P1 | Signed distributions, provenance and dependency inventory | [Current manifest](../security.md) is a checksum record, not an independent publisher signature | Verify trusted signatures before placement; reject mismatches, unknown keys and unauthorized rollback. Signing-key compromise needs separate controls |
| P1 | Retained audit records | Add creation/auth changes/policy changes/revocation/deletion outcomes to existing denial events | Store IDs/reasons/results, never values; bounded retention, gap detection and leak tests. Local storage alone cannot promise root-resistant integrity |
| P1 | Malformed-input, race and failure testing | Authorization, URL/path, JSON, Store updates, stop and cleanup are critical boundaries | Extend existing tests with missing fuzz/race/fault cases; fail closed on connection floods, long streams, crashes and disk-full |
| P2 | Protect credential-handling processes | [At-rest encryption](local-credential-store.md) excludes same UID, running memory and swap | Evaluate dedicated UID, dump restrictions, unlock expiry and key lifetime; do not promise elimination of all Go memory copies or host-root access |
| P2 | Finer network authority | [General CONNECT](../../proxy-policy.md) (Japanese) does not inspect TLS SNI/URL | First use fixed-origin broker path/method limits where sufficient. SNI matching alone cannot enforce encrypted HTTP Host/path |

- Real upstream API keys and OAuth access/refresh/ID tokens remain in the authentication proxy.
- Proposed work-VM credentials are short-lived, execution-scoped and proxy-only, unusable for upstream authentication. Host issuing keys never enter work VMs.
- Encryption does not replace authorization or cleanup. Record coverage/gaps in [security](../security.md).

## Larger contract changes need separate decisions

| Proposal | Contract change | Establish first |
|---|---|---|
| Live network policy | Current immutable policy plus whole-VM quarantine | Generations, apply acknowledgement, existing-connection termination, partial failure/restart recovery |
| Generic TLS decryption/L7 restrictions | Guest CA trust and supported protocols | Pinning, HTTP/2/gRPC/WS, signed requests, secret echo/logs and overhead |
| VM–proxy mutual authentication | Add execution-origin authentication to bearer authorization | Issuance/renewal/revocation, restart, clock skew, migration and connection-reuse latency |
| Memory snapshots/VM fork | Preserve/clone processes, RAM, connections, identity and deadlines | Reissue tokens, prevent secret residue, CPU compatibility, avoid duplicate external effects |
| Other OS/multiple hosts/tenants | Trusted single-host operator assumption | Authentication, authority, quota, placement, mutual authentication, audit and recovery |
| Dedicated credential UID | Profile/keyring/service ownership | Existing-profile migration, headless/WSL unlock, lock/cleanup and root trust |

## Order of work

1. Prioritize P0 acceptance and dependency work.
2. Add small increments for usage limits, audit, signed distribution and embedding contracts according to need.
3. Start performance work with measurements; prototype prestart/persistence to establish cost and benefit.
4. Reject changes that weaken authentication, isolation, revocation or cleanup.

- Already implemented: default-deny egress, token-scoped model restrictions, PID profiles, broker, Create→Start, PTY/files/jobs, independent login and encrypted storage. Do not propose these again as missing features.
- Additional controls/Go API first shipped in v0.0.2; independent login, keyring/encryption and consolidated status/distribution updates in v0.0.3. Existing feature pages retain detailed conditions.
