# Automatically test authentication, network and failure boundaries

[日本語](../../development/security-tests.md) | **English**

- Additional fuzz/load/stop-race coverage: v0.0.4; existing CAS and cleanup tests are reused.
- Synthetic credentials and local fixtures only. No real accounts or external AI inference.

| Boundary | Inputs/faults | Required behavior |
|---|---|---|
| Authentication | Unissued tokens, malformed headers | No upstream work or credential injection |
| Model policy | Duplicate/case-variant JSON and trailing values | Only permitted models; preserve accepted request bytes |
| Broker URL/path | Other origins, traversal, percent encodings, doubled slashes, forbidden methods | Fixed origin/path/method scope |
| Destinations | IPv4/IPv6/metadata, mixed DNS, malformed authorities | No dial to rejected destinations/addresses |
| Management JSON | Duplicates, Unicode field aliases, trailing/unknown data, size/depth excess | Reject before authentication/Store mutation |
| Token lifetime | Negative/oversized/TLS-excess TTL | Do not replace existing authority with invalid leases; omitted HTTP TTL uses configured default |
| Persistence | Injected ENOSPC, broken audit head, OAuth save failure | No fresh budgets/credentials; do not silently reset budgets after recovery |
| Load/long streams | 96 concurrent SSE requests and revocation | Close existing streams, admit no new upstream work |
| Stop races | Concurrent issuance/stop; issue/restore after stop | No remaining authority; end expiry-monitor goroutines |
| Store | Existing multiprocess CAS/failure tests | Reject conflicts and unpublished updates |
| Cleanup/cancel | Existing stop failure, deletion journal, lost response/retry tests | Keep unknown state explicit and retain cleanup retries |

ENOSPC is injected at the persistence boundary; shared disks are never filled. These tests do not claim every transport/policy/Store/failure combination is exhausted.

## Run

Prepare the [Go toolchain](toolchain.md), then run at repository root.

Regular race coverage:

```bash
make test
```

Five bounded fuzz targets:

```bash
make security-fuzz
```

- Five seconds/two workers per target, plus seed preparation and compilation.
- Fixed seeds also run with normal tests. Fuzz exploration and full race testing are separate.
- CI runs both. Reduce and sanitize failures before adding regression inputs; never commit raw private logs/credentials/bulk exploration caches.

## Real Kata

Use the [disposable node procedure](../../testing.md#新規導入更新復旧を検証する).

| Scope | Selection |
|---|---|
| TLS/network/revocation | `--acceptance-run '^TestKataWorkerMTLS$'` |
| Go API, case isolation, proxy audit | `--acceptance-run '^TestKataGoAPI$'` |
| Encryption/keyring | `TestKataEncryptedProfile`, `TestKataKeyringProfile` |

Signing CI contacts GitHub/Sigstore. Normal upstream CLI contract checks remain offline; real AI authentication/inference is separate. See [evidence and limits](../../testing.md).
