# Feature and security improvement candidates

[日本語](../../development/improvements.md) | **English**

- Baseline: v0.0.6. This page tracks proposals, additional verification and included improvements.
- P0 establishes guarantees; P1 improves daily use/security; P2 requires measured benefit. Unimplemented proposals have undecided effort and release dates.
- No named-product evaluation or ranking is published here.
- Preserve convenient local operation, the authentication proxy, isolation, revocation and cleanup.

## UX tasks after v0.0.6

Target: **unreleased (`main`)**.

| Task | User-facing change | Acceptance |
|---|---|---|
| 1-A | Host agent-policy list, search, value sources, and explanations | Standalone editor; distinguish built-in and host policies |
| 1-B | Clone, edit, inherit, and select a default | Review diffs and affected descendants, confirm discard, reject concurrent edits, save with mode 0600 |
| 2-A | Terminal transport regressions | Esc without newline, special keys, UTF-8 paste, ANSI, live resize, restored terminal flags and stdin ownership |
| 2-B | Real TUI interactions and layout | Small screens, Japanese paste through a PTY, review/save, responsive dashboard after shell exit |
| 3 | Stage-specific startup guidance | Preserve underlying errors and suggest recovery commands using selected configuration |
| Critical review | Mistaken saves, conflicts, misleading recovery, and return failures | Real Kata, no secret display, owned-resource cleanup; distinguish live AI inference and physical-terminal visual checks |

Interaction design draws on [K9s search, Esc and help](https://k9scli.io/topics/commands/), [VS Code settings scopes, search and reset](https://code.visualstudio.com/docs/configure/settings), and [Docker troubleshooting guidance](https://docs.docker.com/desktop/troubleshoot-and-support/troubleshoot/). Oaiso adapts these ideas into source visibility, review before saving, and concrete checks for each startup stage.

## Establish guarantees first

| Priority | Item | Current state and remaining work | Acceptance |
|---|---|---|---|
| P0 | Durable Codex state and interactive startup | [SQLite startup fails with an ordinary HOME](../../testing.md#ux改善の検証2026-10-26). Temporary HOME does not meet persistence requirements | Preserve shared workspace coherence; design and verify VM disk storage across stop, restart, crashes, migration and cleanup |
| P0 | Remaining dependency findings | **Dependency updates and applicability review completed (v0.0.4)**. [Findings](dependency-security.md) reduced from 15 to 2; both concern unused daemon features | Automated regressions pass. No finding exclusions; rationale recorded. Follow-up real Kata coverage uses the network/revocation suite |
| P0 | Live-account authentication | Independent login, auth-only VM and encrypted storage implemented; live accounts/long refresh unverified | API/subscription, refresh conflicts, proxy restart, cancellation, active-stream revocation, no secrets in logs/files |
| P0 | Network/revocation acceptance | **Additional coverage completed (v0.0.4)**. [Scope and reproduction](network-acceptance.md) | Automated DNS/failed-stop checks; real Kata traffic, active SSE/WS revocation and quarantine pass. CLI text/JSON distinguish unconfirmed stops |
| P0 | WSL/outer VM/headless acceptance | Credential design differs from actual nested-VM execution requirements | Install→unlock→login→run→stop→upgrade→recover; diagnose missing KVM without silent weaker isolation |

- Live-account authentication/inference and real WSL acceptance remain unverified. Network/revocation uses real Kata with synthetic credentials.
- Existing evidence and gaps: [testing](../../testing.md) (Japanese).

## Strengthen existing capabilities

| Priority | Capability | Addition | Constraint |
|---|---|---|---|
| P1 | Per-run usage limits | **Completed (v0.0.4)**. [Request/concurrency/body-byte/observed-token limits](../guides/usage-limits.md) | Separate estimated spending from exact cutoff. Revocation cannot undo already-billed upstream work |
| P1 | Avoid reaping active main jobs | **Completed (v0.0.4)**. `--protect-main` and CLI/Go activity renewals | [Contract](../sandbox-features.md#primary-process-protection-and-activity-leases). Leases expire within five minutes; maximum lifetime independent. Real Kata renewal/release/idle-stop checks pass |
| P1 | Aggregate diagnostics/observability | **Completed (v0.0.4)**. [Local diagnostics/JSONL export](../guides/observability.md), CLI/Go launch timing retention | Exclude secrets, bound retention, require no external permanent service |
| P1 | Credential-broker configuration | **Completed (v0.0.4)**. [Templates, preflight and existing TUI](../guides/credential-broker.md), hidden saved secrets and stale-write detection | Reuse existing broker; no secret display or unapproved-host injection |
| P1 | Candidate updates | **Completed (v0.0.4)**. [Plan, isolated CLI and real-Kata qualification](candidate-qualification.md), retained results/artifact hashes | No adoption from static diffs alone; preserve current pins and rollback on failure |
| P2 | Startup/Exec/concurrency | **First stage complete (v0.0.4)**. [Measurements](../performance.md#measurements-on-2026-10-25): Exec 53.1%, two-worker Exec 43.3% faster by consolidating duplicate checks within an operation | Full asset/host/guest checks retained. Startup 8.2% faster; increased admission capacity and further startup improvements remain unimplemented |

## Additional features

| Priority | Feature | Initial scope | Acceptance |
|---|---|---|---|
| P1 | Machine-readable embedding contract | **Completed (v0.0.4)**. [Pipe-only API v1 and JSON Schema](../local-api-sdk.md), resources, request IDs, states, errors and capabilities | Version mismatch, cancellation, lost response, retry and cleanup tests; no public management socket |
| P1 | Thin Python/TypeScript SDKs | **Completed (v0.0.4)**. [Same-host create/execute/inspect/cleanup](../local-api-sdk.md); both SDKs verified with real Kata | Preserve Go retry/authority/failure semantics; no secrets in argv/stdout |
| P2 | Disk checkpoints | **Completed (v0.0.4)**. [Stopped rootfs capture/new VM restore](../guides/disk-checkpoint.md), CLI/Go API | Not RAM preservation; define mounts, concurrent writes, size, secret exclusion and restore compatibility |
| P2 | Prestarted environments | Small benefit-assessment prototype | No unassigned credentials/user data; revalidate at allocation; require acceptable standby memory and measured ≥20% benefit |
| P2 | Further resource controls | **Complete (v0.0.4)**. [TCP admission, per-source bandwidth, per-process FDs and device block I/O](../guides/resource-limits.md). CLI / Go API | Distinct from existing CPU/memory/PID/body limits; preserve legitimate long streams and contain noisy workloads |

- Additional agent acceptance remains in [future work](../future-work.md). Never weaken credential separation merely to increase the agent count.

## Increase security strength

| Priority | Measure | Evidence and intent | Verification and residual limits |
|---|---|---|---|
| P1 | Encrypt VM–proxy traffic and bind authorization to execution origin | **Implemented (v0.0.4)**. [TLS 1.3 and execution-bound auth-proxy certificates](../guides/worker-tls.md). General egress is outside this feature | Copied bearer alone must fail from another origin. A fully compromised VM can still perform its authorized operations |
| P1 | Signed distributions, provenance and dependency inventory | **Implemented (v0.0.4)**. [Signed distributions, provenance, SBOM and version floor](../guides/signed-releases.md). Release verification checks the published signatures and provenance | Verify trusted signatures before placement; reject mismatches, unknown keys and unauthorized rollback. Signing-key compromise needs separate controls |
| P1 | Retained audit records | **Implemented (v0.0.4)**. [Durable operation audit](../guides/audit.md): creation/auth changes/policy issue/revocation/deletion outcomes | Store IDs/reasons/results, never values; bounded retention, gap detection and leak tests. Local storage alone cannot promise root-resistant integrity |
| P1 | Malformed-input, race and failure testing | **Implemented (v0.0.4)**. [Fuzz/load/race/persistence-fault tests](security-tests.md), reusing existing CAS/cleanup coverage | Extend existing tests with missing fuzz/race/fault cases; fail closed on connection floods, long streams, crashes and disk-full |
| P2 | Protect credential-handling processes | [At-rest encryption](local-credential-store.md) excludes same UID, running memory and swap | Evaluate dedicated UID, dump restrictions, unlock expiry and key lifetime; do not promise elimination of all Go memory copies or host-root access |
| P2 | Finer network authority | **Implemented (v0.0.4)**. [Fixed-origin broker](../guides/credential-broker.md): exact paths, CLI methods, TUI, Go API and private file | Preserve subtree scopes; reject before reading secrets. General CONNECT TLS inspection and query/body restrictions remain outside scope |

- Real upstream API keys and OAuth access/refresh/ID tokens remain in the authentication proxy.
- Work-VM execution credentials are short-lived, execution-scoped and proxy-only, unusable for upstream authentication. Host issuing keys never enter work VMs.
- Encryption does not replace authorization or cleanup. Record coverage/gaps in [security](../security.md).

## Larger contract changes need separate decisions

| Proposal | Contract change | Establish first |
|---|---|---|
| Live network policy | v0.0.4: explicit proxy-wide live policy changes; whole-VM quarantine remains separate | Generations, apply acknowledgement, existing-connection termination, partial failure/restart recovery |
| Generic TLS decryption/L7 restrictions | Guest CA trust and supported protocols | Pinning, HTTP/2/gRPC/WS, signed requests, secret echo/logs and overhead |
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

## Workflow and SDK additions

Target: **v0.0.4**. Implementation adds SDK file/job/checkpoint operations, [reviewed artifact bundles](../guides/artifacts.md), [loopback previews](../guides/preview.md), [policy proposals and explicit live changes](../guides/live-policy.md), and [checkpoint retention/pins](../guides/disk-checkpoint.md#retention-planning-and-pins). Feature-specific automated tests and disposable Kata acceptance verify these paths.

- Live updates are proxy-wide, generation-checked and close existing general HTTP/CONNECT connections before reporting completion.
- File SDK transfers have a separate 512 KiB bound. Mutating calls are never automatically retried.
- Retention preserves the existing 16 GiB hard capacity; its byte target is a cleanup target.

### Verification of the additions

| Check | Result |
|---|---|
| Go | Build, vet, lint and uncached repository-wide race tests passed |
| SDK contracts | Nine Python and ten TypeScript tests passed against source and installed wheel/npm packages |
| Real Kata SDKs | Binary files, jobs/logs and checkpoint listing, plus lifecycle/replay/conflict/cancellation/lost-response/cleanup |
| Real Kata workflow | Reviewed artifacts with job status, authenticated HTTP/WebSocket preview, Origin/cookie/expiry/stop checks, CLI/Go plan parity and live HTTP/CONNECT termination, Python checkpoint capture, TypeScript restore/exec, pin/reference protection and reviewed deletion |
| Failure boundaries | Untrusted host-output bounds, isolated Python imports, stale/concurrent generations, persistence failure and incomplete-barrier retries passed |
| Setup/update/recovery | Final bundles passed fresh setup, upgrade, implicit-downgrade refusal, failed-activation recovery and explicit rollback; no owned VMs/tasks/containers/netns/firewall tables remained; shared service PIDs/start times unchanged |
| Existing checks | Five fuzz targets, 65 Python tests, CLI naming and offline upstream contracts passed; pins/baseline unchanged |

- Full SDK suite: 268.93 seconds. Final workflow subset after job-result/output-bound additions: 168.37 seconds.
- Synthetic credentials/local upstreams only; no real account authentication or AI inference.
- Candidate builds: `v0.0.0-p1-usability-a/b`; CLI, test binary and SDK hashes retained privately.
- Detailed scope and evidence: [testing report (Japanese)](../../testing.md#p1の作業支援通信規則の即時変更2026-10-25).
