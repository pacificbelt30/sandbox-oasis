# Dependency checks and remediation

[日本語](../../development/dependency-security.md) | **English**

- Checked 2026-10-24 on Linux amd64, all Go packages.
- Dependency updates, applicability review and boundary checks are included in **v0.0.4**.
- govulncheck v1.9.0, Go 1.27.3. No finding exclusions or database modifications.

## Results

| Target | Symbol findings | Interpretation |
|---|---:|---|
| Pre-age commit `1168123` | 19 | Historical baseline |
| age plus HTTP/2 patches, published v0.0.3 | 15 | Reproduced before this change |
| Current dependency updates | **2** | Patched versions remove 13 findings |
| Remaining two | 2 | Daemon plugin functionality; reviewed execution configuration lacks the required features |

- **This is not a zero-finding or vulnerability-free result.** Both remaining findings stay visible.
- Static findings, applicability and successful exploitation are different questions. This is not a completed repository-wide security audit.
- New database entries, other OS/runtime configurations or dependency changes may change the result.

## Updated dependencies

| Go module | Previous | v0.0.4 | Findings removed |
|---|---|---|---:|
| containerd SDK | v2.1.5 | v2.2.9 | 8 |
| gRPC | v1.78.0 | v1.83.1 | 2 |
| OpenTelemetry | v1.40.0 | v1.44.0 | 1 |
| moby/sys/user | v0.4.0 | v0.4.1 | 1 |
| SELinux library | v1.12.0 | v1.13.1 | 1 |

- Related API/runtime-spec/instrumentation/compression modules follow Go's dependency resolution; go.mod/go.sum updated.
- gRPC requires OpenTelemetry v1.44.0; the selected containerd SDK requires SELinux v1.13.1.
- SDK v2.2.9 includes fixes for all eight reviewed findings without increasing the minimum Go version.
- Minimum Go 1.26.0, development/CI Go 1.27.3 and pinned daemon 2.4.2 are unchanged. Running host services/distributions were not updated.
- age v1.3.2 and the existing patched x/net v0.60.0 remain pinned.

### Removed finding IDs

| Module | IDs |
|---|---|
| containerd SDK | GO-2026-6791, GO-2026-6597, GO-2026-5758, GO-2026-5622, GO-2026-5475, GO-2026-5378, GO-2026-5338, GO-2026-5064 |
| gRPC | GO-2026-6348, GO-2026-6061 |
| OpenTelemetry | GO-2026-5506 |
| moby/sys/user | GO-2026-6686 |
| SELinux | GO-2025-4098 |

See each ID in the [Go vulnerability database](https://pkg.go.dev/vuln/). Removal of a finding does not establish that the affected daemon feature was previously used.

## Remaining findings and applicability

| ID and primary advisory | Required condition | Observed product boundary |
|---|---|---|
| [GO-2026-4887](https://pkg.go.dev/vuln/GO-2026-4887), [AuthZ plugin advisory](https://github.com/moby/moby/security/advisories/GHSA-x744-4wpc-v9h2) | Daemon forwarding request bodies to an AuthZ plugin | No daemon/AuthZ implementation linked; SDK HTTP client/types communicate with the dedicated node |
| [GO-2026-4883](https://pkg.go.dev/vuln/GO-2026-4883), [plugin installation advisory](https://github.com/moby/moby/security/advisories/GHSA-pxq6-2prw-chj9) | Daemon privilege validation during plugin installation | No daemon/plugin implementation linked; node does not provide plugin APIs |

- Both are unreviewed module-wide entries covering all versions of the legacy SDK module. Primary advisories explicitly exclude configurations not using the affected features.
- Execution uses the [containerd/Kata node](../architecture.md). Legacy SDK v28.5.2 remains a compatibility dependency, not a daemon installation/runtime.
- `TestNativeEntrypointsDoNotLinkDockerDaemon` checks CLI/guard/logger/public Go API dependencies stay within legacy SDK client, API types and stdcopy.
- `TestNodeRejectsDaemonPluginEndpointsBeforeRPC` checks plugin/auth/unknown endpoints return 404 before RPC, for versioned/unversioned URLs and malformed bodies.
- Inspection also identified and fixed unknown API paths falling through to container handling.
- A new SDK with substantially changed client/API types is a separate migration, not a required remediation for these two daemon conditions. Adding daemon functionality invalidates this applicability assessment.

## Verification scope

| Check | Coverage |
|---|---|
| Dependency analysis | Before 15, after 2; 13 removed; no exclusions |
| Go | Build, full race tests, vet, lint, module checksum verification |
| Minimum Go version | CLI builds with Go 1.26.0 |
| Added regressions | No daemon implementation linked; unsupported APIs rejected before RPC |
| Cross-build | macOS arm64/amd64 and Linux arm64; not runtime acceptance on those systems |
| Acceptance | Follow-up [network/revocation suite](network-acceptance.md) passes on real Kata. No full eight-suite rerun, live-account or WSL acceptance |

See [existing verification scope](../../testing.md) (Japanese). Automated tests here do not establish new real-runtime acceptance.

## Repeat the analysis

- Requires the [development environment](toolchain.md), Go 1.27.3. Run as an ordinary user from repository root.
- Fetches public modules/database data; no AI authentication/inference.
- Installs the tool outside Git-tracked files.

```bash
mkdir -p .kata-verification/dependency-tools
GOBIN="$PWD/.kata-verification/dependency-tools" go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
.kata-verification/dependency-tools/govulncheck ./...
```

- With the same database, the two listed findings remain. Text mode returns exit 3 for findings; do not report a clean zero-finding result.
- Raw logs/JSON/build outputs: `.kata-verification/dependency-hardening-20261025/`. Large reports and real credentials are not committed.
