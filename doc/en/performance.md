# Performance and benchmarks

[日本語](../performance.md) | **English**

- Measure Kata asset verification, public Go API Exec and proxy authentication separately.
- The [measurement and regression record](../testing.md#2026-10-21の性能計測) (Japanese) contains the before/after values, conditions and verified scope.

## How the optimizations work

### Dashboard refresh and checkpoint catalogs

- Scope: **unreleased (main; v0.0.8 planned)**, measured on 2026-11-03.
- The TUI refreshes one request at a time, merging bursts into one pending refresh. Listing and quota reads share a three-second deadline; quota reads use at most four concurrent workers. Rows with unavailable measurements remain visible.
- Checkpoint listings read recovery-point references once per request. Ownership, metadata and digest checks remain enforced for every checkpoint, and the next request rereads the ledger. No cache spans requests.

| Condition | Value |
|---|---|
| Baseline | Saved pre-change binaries based on `659feea9` with identical benchmarks added |
| Environment | Linux amd64, Go 1.27.3, GOMAXPROCS=4, warm page cache, no competing builds/tests |
| Samples | Median of three runs per condition, three operations per run; before/after, after/before, before/after |
| Checkpoint workload | Real private reference files and a local content store; OCI metadata listing excludes VMs, containerd socket communication and checkpoint creation |
| TUI workload | Fixture with a fixed 1ms quota-API delay; excludes real-node behavior and rendering |

| Operation | Before | After | Time reduction |
|---|---:|---:|---:|
| Checkpoint catalog: 8 items | 5.72ms | 0.78ms | 86.3% |
| Checkpoint catalog: 32 items | 76.00ms | 3.05ms | 96.0% |
| Checkpoint catalog: 128 items | 1150.83ms | 10.46ms | 99.1% |
| TUI quota collection: 4 workers | 4.43ms | 1.20ms | 73.0% |
| TUI quota collection: 20 workers | 22.19ms | 5.59ms | 74.8% |

- The 128-item catalog ranged from 1146.11–1156.07ms before to 10.44–10.52ms after. Allocations fell from 431.28MiB to 2.68MiB per operation; these are not RSS measurements.
- Twenty-worker quota collection ranged from 22.13–22.21ms before to 5.54–5.61ms after. It uses at most four worker goroutines; allocations increased from 10040 to 11330 bytes per operation.
- These results do not establish whole-product, VM startup or AI latency improvements. Evidence: `.kata-verification/product-quality-20261103/`.
- The [quality verification matrix](../testing.md#全機能の品質確認2026-11-03) (Japanese) tracks CLI, TUI, storage, networking, authentication and SDK acceptance separately from these microbenchmarks.

### Combine duplicate Exec verification

- Available: **v0.0.4**.
- Runtime and host attestation share one full asset verification per Exec.
- Every operation rereads all assets and checks SHA-256, ownership, paths, permissions and configuration. There is no timestamp cache.
- Worker configuration, host processes/cgroups and guest identity, privileges and resource limits are checked before the command starts.
- Nodes without `verify-runtime-v1` use the previous verification sequence. Failure on a supporting node never triggers a legacy retry.
- Assets are validated after admission-lock waiting, before supervisor handling in the same operation. Ownership, PID and resource-limit checks remain in place.
- Standalone supervisor handling during stop/delete retains independent asset validation.
- Startup also removes only duplication within one operation; asset checks before and after VM start remain separate.
- Resource reservations, startup bootstrap checks and credential activation order are preserved.

### Asset verification and proxy authentication

- Asset verification checks file ownership, paths, executable permissions and SHA-256 with at most `min(4, GOMAXPROCS)` concurrent readers per validation.
- Every validation reads every asset and checks configuration bindings.
- This uses spare CPU capacity while retaining the [security checks](security.md).
- Parallel asset verification alone does not reduce the total hashing work. Combining duplicate Exec checks reduces repeated work within that operation.
- Measure throughput under CPU saturation and performance on storage-bound hosts separately.

- The auth proxy looks up issuance records directly by token value.
- Issuance, restore and revocation update the index under the same lock, avoiding a scan of every worker.
- Expiry, provider/model restrictions and active-stream cancellation follow the [auth proxy contract](auth-proxy.md).

- These optimizations are enabled by the implementation and require no activation flag.
- Follow the [CLI, node and proxy update procedure](../operations.md#cliプロキシを更新する) (Japanese).
- Products using the Go API must also use the same source version as the node.

## Round two: destination preflight comparison

- Scope: **unreleased (main; v0.0.8 planned)**, 2026-11-03. The baseline is the source saved after round one, not a comparison between published releases.
- Same Linux amd64, Go 1.27.3 and GOMAXPROCS=4. Medians of three 300ms benchmark runs per condition, ordered before/after, after/before, before/after. Other builds/tests and the Kata node were stopped.
- Only the existing-destination case uses a fixed 1ms guest-call fixture. The new-file case writes, fsyncs, publishes and removes 32KiB in a local temporary directory. Neither measures network or real-VM transfer speed.

| Case | Before | After | Change |
|---|---:|---:|---|
| Reject existing destination, 1ms fixture | 1.201ms | 0.0106ms | 99.1% less; skips the guest call and staging |
| Publish a new 32KiB file | 0.0416ms | 0.0429ms | 3.1% more, about 1.3µs for preflight |

- New-file ranges were 0.0412–0.0420ms before and 0.0425–0.0432ms after. Allocations changed from 693 to 704 bytes per operation; existing-destination allocations fell from 712 to 333 bytes.
- Both versions retain exclusive publication and held parent directories. Files created after preflight are still protected from overwriting.
- Eight ordinary CLI cases were also measured three times per version in alternating order, with byte-identical output. Per-case medians ranged from 12.99–13.91ms before to 12.68–13.76ms after. These small-sample baselines do not establish a general CLI speedup.
- Evidence: `.kata-verification/product-quality-round2-20261103/transfer-benchmark-results.json` and `cli-performance.json`. See the [regression scope](../testing.md#第二回の互換性回帰確認2026-11-03) (Japanese).

## Real-VM check on 2026-11-03

- Scope: **unreleased (main; v0.0.8 planned)**, test BuildVersion `quality-a`. This is a current-version baseline, not a before/after speedup comparison.
- Core i5-8500 / 4 vCPUs, Linux amd64, Go 1.27.3, client GOMAXPROCS=4. Pinned Kata 4.3.0 / Dragonball, containerd 2.4.2 and BuildKit 0.34.0.
- Compact profile: 512MiB guest, 768MiB host reservation, one CPU. Dedicated node, prepared images, warm page cache, no general egress or Git sharing, synthetic credentials. No other builds/tests ran during measurement.
- Includes normal asset, host/guest and bootstrap verification. Excludes proxy creation, image fetching/import, removal and AI inference. Exec runs `/bin/true` through exit confirmation.

| Operation | Samples | Median | Range |
|---|---:|---:|---:|
| Sequential launch | 3 | 8.484s | 8.173–8.762s |
| Two-worker launch, both complete | 3 batches | 15.021s | 14.757–15.063s |
| Sequential Exec | 7 | 1.145s | 1.103–1.253s |
| Two-worker Exec, both complete | 5 batches | 1.914s | 1.830–2.031s |

- Final observed `memory.peak` values were 158.27 / 157.32MiB for the two VMs, with zero OOM kills. Peaks were not collected for every launch sample.
- See the [acceptance scope](../testing.md#全機能の品質確認2026-11-03) (Japanese). Evidence: `.kata-verification/product-quality-20261103/native-performance-results.json`.

### Authorization and policy microbenchmarks

- Same Go and CPU, GOMAXPROCS=4; medians of three 300ms runs per condition after VM measurement, with no competing builds/tests. Network, DNS and upstream APIs are excluded.
- Successful authorization took 929.3ns with one issuance record and 1110ns with 4096 records per request, both using 656 bytes and eight allocations. Unissued-token rejection took 128.7–143.1ns across conditions with 1–4096 records.
- Destination policy took 144.7ns with 10 rules and 152.2ns with 1000; address policy took 22.77ns per operation. All used zero allocations. These are current-version baselines, not speedup comparisons.
- Evidence: `.kata-verification/product-quality-20261103/proxy-policy-performance.json`.

## Measurements on 2026-10-25

- Scope: **v0.0.4**. Full asset verification within one Exec was consolidated from three reads to one.
- SHA-256 accounted for 94.0% of baseline node CPU profile time.

| Condition | Value |
|---|---|
| Host | Linux amd64, Core i5-8500, 4 vCPUs, KVM |
| Build | Go 1.27.3, client/node GOMAXPROCS=4, matching measurement BuildVersion |
| Runtime | Kata 4.2.0 / Dragonball, containerd 2.4.2, BuildKit 0.34.0 |
| Resources | compact: 512 MiB guest, 768 MiB host reservation, 1 CPU, 512 PIDs; two-worker admission limit |
| Order | Before → after → after → before on the same node, assets and worker image |
| Samples per version | 6 sequential launches, 6 two-worker launch batches, 14 sequential Execs, 10 two-worker Exec batches |
| Workload | Warm page cache, prepared image, no general egress/Git metadata/agent, synthetic credentials |
| Excluded | Client Open, proxy creation, image builds/imports, cleanup and AI inference |

- The baseline is a saved source tree based on `d9c0419e`, including the preceding P1 work. This does not compare published releases.
- Both versions enabled identical CPU profiling; no builds or other tests ran during measurement.

| Operation | Before, median | After, median | Time reduction |
|---|---:|---:|---:|
| Sequential launch | 7.740 s | 7.103 s | 8.2% |
| Two-worker launch, both complete | 13.721 s | 12.744 s | 7.1% |
| Public Go API Exec, `/bin/true` | 2.475 s | 1.161 s | **53.1%** |
| Two-worker Exec, both complete | 3.329 s | 1.888 s | **43.3%** |

- Sequential Exec range: 2.362–3.229 s before, 1.094–1.361 s after.
- Concurrent Exec range: 3.197–3.456 s before, 1.830–2.407 s after.
- Sequential launch range: 7.638–8.080 s before, 6.904–7.238 s after.
- Median Exec phases after the change: full assets/host verification 0.706 s, guest checks 0.203 s. Sequential VM-start API time was 3.670 → 3.667 s.
- The VM-start API includes node-side waits. Further startup improvements and greater worker density remain future work.

### CPU, memory and I/O costs

| Observation | Before | After |
|---|---:|---:|
| Node CPU per Exec | 3.062 CPU-s | 1.064 CPU-s, 65.3% less |
| Node logical reads per Exec | 1.197 GiB | 0.400 GiB, 66.6% less |
| Node plus two VMs, CPU per Exec | 3.256 CPU-s | 1.336 CPU-s, 59.0% less |
| Two VMs only, CPU per Exec | 0.194 CPU-s | 0.272 CPU-s |
| Node CPU queue wait, 14 Execs total | 0.883 s | 0.808 s |
| Node RSS at the end of Exec groups | 36.79 MiB | 38.84 MiB |
| Combined observed memory of two VMs | 313.31 MiB | 312.80 MiB |
| VM OOM kills | 0 | 0 |

- CPU/read counters are group totals divided by Exec count. RSS/memory are averages of two group-end observations.
- CPU queue wait is OS scheduler waiting, summed from `schedstat` for threads present at both group boundaries.
- VM CPU and node RSS increased; the measured CPU total decreased. VM reservations are unchanged.
- Individual VM observed memory was 155.09–157.45 MiB, maximum observed peak 157.83 MiB. This does not capture startup peaks for every VM.
- Node physical disk reads during Exec were zero in both versions. Fewer logical reads do not establish cold-storage speedup.
- Launch-group node CPU/I/O counters include cleanup between samples and were not compared as launch-only costs.
- The CPU sum excludes host processes outside the node and these VM cgroups.
- AI latency, cold startup, other CPUs/profiles and more than two workers are unmeasured.
- See [real-Kata regression coverage](../testing.md#2026-10-25の高速化と回帰確認) (Japanese).

## Choose a measurement

| Benchmark | Includes | Excludes |
|---|---|---|
| `BenchmarkWorkerRequest` | Authentication header parsing, record lookup, expiry/route checks, request context creation and release | Network and upstream API communication |
| `BenchmarkLoadPinnedManifest` | Reading real assets; checksum, ownership, path and configuration checks | VM startup and guest execution |
| `BenchmarkKataExec` | Per-exec asset, host and guest checks; running `/bin/true` and checking completion | VM creation, client Open, warmup and AI inference |
| `BenchmarkKataLaunch` | A fresh Launch and its sequential phases; first Exec is reported separately | Client Open, proxy creation, image builds/imports, removal and AI inference |
| `BenchmarkCheckpointCatalog` | Catalog reading private recovery-point files and OCI metadata | VMs, containerd socket calls and checkpoint creation |
| `BenchmarkDashboardQuotaCollection` | Four or twenty quota reads with a synthetic 1ms delay | Real-node measurements and TUI rendering |

- The proxy benchmark measures accepted and unissued tokens with 1, 32, 256 and 4,096 issuance records.
- Allocation figures reported by the Exec benchmark cover the client process.

## Locate startup costs

- The Go API `client.LaunchWithTimings(ctx, req)` returns `Worker`, `LaunchTimings` and `error`.
- It uses the same request identity, verification, authentication and cleanup as `Launch`.
- Measurement does not affect the request fingerprint.
- Ordinary `Launch` does not allocate a timing record.

```go
worker, timing, err := client.LaunchWithTimings(ctx, req)
// Preserve worker.ID for recovery on failure, following the existing API contract.
for _, phase := range timing.Phases {
    fmt.Printf("%s: %s\n", phase.Name, phase.Duration)
}
```

- Phases partition elapsed wall time without overlap; their sum equals `timing.Duration`.
- JSON durations are in nanoseconds.
- They distinguish preflight, network setup, capacity reservation, container creation, created-worker verification, VM start, host/guest verification, token activation, bootstrap release, readiness and request confirmation.
- `vm_start` covers the entire node start API, not pure VMM boot time.
- `host_guest_verification` includes asset revalidation, host checks and the guest probe.
- An idempotent retry reports only the phases actually executed, such as existing-worker lookup.
- Failures return partial measurements including cleanup and request-abort confirmation.

- Reports contain fixed phase names and durations only, without paths, arguments, request values, tokens or error text.
- The caller decides whether to store or transmit them.
- Phase names are diagnostics and may change between versions.
- Use the existing API outcome for readiness and authentication decisions.

## Locate Exec costs

- Available: **v0.0.4**.
- `ExecWithTimings(ctx, workerID, request)` returns the exit code, `ExecTimings` and error with normal `Exec` semantics for streams, exit status, cancellation and failures.
- Records contain fixed phase names and durations only: no arguments, paths, output, credentials or error text. Concurrent calls use separate records.

```go
code, timing, err := client.ExecWithTimings(ctx, worker.ID, cxd.ExecRequest{
    Argv: []string{"/bin/true"},
})
fmt.Printf("exit=%d ok=%t\n", code, err == nil)
for _, phase := range timing.Phases {
    fmt.Printf("%s: %s\n", phase.Name, phase.Duration)
}
```

| Phase | Includes |
|---|---|
| `request_validation` | Arguments, working directory and environment validation |
| `exec_lookup` | Worker lookup and running/managed/quarantine checks |
| `exec_runtime` | Runtime registration, resource profile and cgroup version |
| `exec_configuration` | Configuration, mounts, networking and resource limits |
| `exec_host` | Full asset/host attestation and node identity checks |
| `exec_guest` | Guest probe and restriction checks |
| `exec_create`, `exec_attach` | Command creation and stream attachment |
| `exec_io`, `exec_result` | Command I/O, waiting and exit-status inspection |

- `exec_verification` covers the short verification entry step.
- Phases do not overlap and sum to `timing.Duration`; failures return partial timings.
- `exec_io` includes command execution, potentially including AI response waits.
- CPU time, memory and I/O counters require separate node-side profiles and cgroup observations.

## Run the benchmarks

- Run from the repository root with the [development Go environment](../testing.md#コードを確認する) (Japanese).
- The following `CODEX_DOCK_BENCH_*` variables are inputs to benchmarks only.

### Proxy authentication

No Kata node or real upstream API connection is required.

```bash
go test ./internal/authproxy -run '^$' \
  -bench '^BenchmarkWorkerRequest$' -benchmem -count=5 -benchtime=500ms
```

### Catalogs and quota collection

Run on Linux. These benchmarks need no Kata node or real credentials and create temporary fixtures. Use identical commands for both versions and stop other builds/tests during measurement.

```bash
GOMAXPROCS=4 go test ./internal/engine -run '^$' \
  -bench '^BenchmarkCheckpointCatalog$' -benchmem -count=3 -benchtime=3x
GOMAXPROCS=4 go test ./internal/ui -run '^$' \
  -bench '^BenchmarkDashboardQuotaCollection$' -benchmem -count=3 -benchtime=3x
```

### Pinned asset verification

- On Linux, specify an existing sealed manifest.
- The benchmark reads files without modifying them.
- A root-owned manifest requires the node operator's read permissions.
- Run from trusted source.

```bash
CODEX_DOCK_BENCH_MANIFEST=/etc/codex-dock/kata-assets.json \
  go test ./internal/kataguard -run '^$' \
    -bench '^BenchmarkLoadPinnedManifest$' -benchmem -benchtime=3x -count=5
```

### Exec in a real Kata VM

- On a prepared test host, start an idle managed Kata worker through the normal workflow and obtain its full ID.
- The benchmark runs `/bin/true` in that VM.
- The caller starts the worker and stops/removes it after measurement.
- Replace `FULL_WORKER_ID` with its actual ID and set the `dev` value in `-ldflags` to the running node's BuildVersion.

```bash
CODEX_DOCK_KATA_NODE_SOCKET=/run/codex-dock-node.sock \
CODEX_DOCK_BENCH_WORKER=FULL_WORKER_ID \
  go test -ldflags '-X github.com/pacificbelt30/codex-dock/internal/buildinfo.Version=dev' \
    ./pkg/cxd -run '^$' -bench '^BenchmarkKataExec$' -benchmem -benchtime=3x -count=5
```

- Omitting the manifest path or worker ID skips the corresponding benchmark.
- A skip is not a successful measurement.

### Fresh startup in real Kata

- Use an isolated test node with no other users or concurrent launches.
- Preload the worker and proxy images.
- The benchmark creates a CredentialStore containing only an invalid API key, a dedicated case proxy, and a fresh worker for every sample, then removes them.
- It uses no real account or upstream AI.
- Workers have no general egress, Git metadata sharing or running agent; they remain idle.

```bash
CODEX_DOCK_KATA_NODE_SOCKET=/run/codex-dock-node.sock \
CODEX_DOCK_BENCH_LAUNCH=1 CODEX_DOCK_BENCH_PROFILE=compact \
CODEX_DOCK_KATA_TEST_IMAGE=codex-dock:latest \
CODEX_DOCK_KATA_PROXY_IMAGE=codex-dock-proxy:latest \
GOMAXPROCS=4 \
  go test -ldflags '-X github.com/pacificbelt30/codex-dock/internal/buildinfo.Version=dev' \
    ./pkg/cxd -run '^$' -bench '^BenchmarkKataLaunch$' -benchtime=3x -count=5 -timeout=20m -v
```

- Match `dev` to the node's BuildVersion.
- Omitting `CODEX_DOCK_BENCH_LAUNCH=1` skips this benchmark.
- `launch_ms/op` and phase-specific `*_ms/op` report launch times.
- `first_exec_ms/op` separately measures the first `/bin/true` execution and completion after launch.
- Each `launch_timing` JSON record contains individual measurements.
- The Go benchmark also performs one calibration launch before its measured iterations.
- Use `CODEX_DOCK_BENCH_PROFILE` to select a profile other than the default `compact`.
- Results with preloaded images and a warm page cache do not establish first image-unpack or real task readiness latency.

### Measure startup, Exec and two-worker concurrency

- Available: **v0.0.4**.
- Use a dedicated node with capacity for at least two more workers; avoid shared services.
- Prepare the pinned worker/proxy images as above. No real credentials or AI inference are used.
- The test measures three sequential launches, three two-worker launch batches, seven sequential Execs and five two-worker Exec batches.
- It removes its own workers and case proxy afterward.

```bash
OAISO_KATA_PERFORMANCE=1 GOMAXPROCS=4 \
CODEX_DOCK_KATA_NODE_SOCKET=/run/codex-dock-node.sock \
CODEX_DOCK_KATA_TEST_IMAGE=codex-dock:latest \
CODEX_DOCK_KATA_PROXY_IMAGE=codex-dock-proxy:latest \
  go test -ldflags '-X github.com/pacificbelt30/codex-dock/internal/buildinfo.Version=dev' \
    ./pkg/cxd -run '^TestKataPerformance$' -count=1 -timeout=20m -v
```

- Match `dev` to the node BuildVersion. Client `GOMAXPROCS` does not change the node setting.
- `perf_sample` contains phase timings; `perf_resources` reports budgets, observed memory and OOM kills.
- Wall-time samples exclude image builds/imports, proxy creation and cleanup.
- Prepared-image, warm-page-cache results are separate from installation, cold startup and AI inference.
- Node operators must collect CPU, disk I/O and scheduler waiting separately; this test alone does not collect them.

## Compare revisions

### Choose the next optimization

- [Fresh-start measurements](../testing.md#新規kata起動の工程別計測) (Japanese) identify VM start as the main wait.
- Prebooting unused VMs is the next candidate.
- Warm pools, memory snapshots and VM forks are not implemented.
- Existing `Create` creates a stopped worker; `Start` still boots its VM.

- A warm pool needs to keep bootstrap closed and authentication inactive during preparation, then recheck assets, host/guest restrictions and policy at assignment.
- It must preserve the current immutable workspace, mounts, UID, network, image, model authorization and execution identity.
- Do not reassign workers to different requests or return used VMs to the pool.
- Count waiting VMs against reservations.
- Verify expiry, cancellation, ownership recovery after node restart and exclusive assignment before enabling it.
- Compare assignment through first Exec against ordinary startup under identical conditions, including verification, replenishment and reserved-memory costs.
- VM start's current share is not an achieved speedup.

### Comparison conditions

- Use the same Go version, CPU conditions, assets and worker profile, with no competing builds or tests.
- Build the node and client with the same measurement BuildVersion.
- Alternate the order of old/new revisions within pairs and compare medians across samples.
- Distinguish warm page-cache measurements from runs that include initial disk reads.

- Run [regression checks](../testing.md) (Japanese) after measurement.
- Runtime changes require `make kata-e2e`.
- Authentication changes also need active-connection cancellation checks for revocation, expiry, replacement, restore and proxy stop.
- Remove test VMs, containers and networks before deleting test services or ownership records.
