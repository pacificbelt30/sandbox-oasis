# Go API for embedding oaiso

[日本語](../go-api.md) | **English**

- `github.com/pacificbelt30/codex-dock/pkg/cxd` lets a trusted product on the same host manage workers through the existing `oaiso node serve` Unix socket.
- It does not expose a new network service.
- Access to the root managed socket permits arbitrary images, commands, and host mounts, so grant it only to trusted host processes.

- Python, TypeScript and the machine-readable contract: [Local API and SDKs](local-api-sdk.md) (unreleased).

## Before connecting

1. [Install the execution host](../setup.md) (Japanese) with the node, containerd, Kata, and BuildKit.

    - Give the product access to the node socket at `/run/codex-dock-node.sock`, or set `Options.NodeSocket` to another path.

2. Start the [auth proxy](getting-started.md) and build or import the worker image.

    - `Open` does not start a proxy or create an image.

3. Deploy the product's Go package with a compatible node.

    - `Open` validates the node and its Go client protocol; an incompatible version returns `ErrVersionMismatch`.
    - Update the embedded package from the same source revision when updating the CLI and node.

- The default proxy endpoints are `http://127.0.0.1:18081` for the host admin API, `http://codex-auth-proxy:18080` for guest authentication, and `http://codex-http-proxy:18082` for general egress.
- Set `Options.ProxyAdminURL`, `ProxyContainerURL`, and `HTTPProxyURL` if your deployment differs.
- Keep `Options.ProxyAdminSecret` in the host product process.

## Choose an operation

| Method | Contract |
|---|---|
| `Launch(ctx, request)` | Creates and starts a worker under a stable `RequestID`; `ProjectDir` must be absolute. Use `Create` → `Start` to separate creation from initial start |
| `Get` / `List` | Inspect worker state |
| `Exec` / `Wait` | Execute a command in a worker / wait for its main process |
| `Start` / `Stop` / `Remove` | Restart a stopped worker / stop it and revoke its auth token / remove its resources |
| `Request` / `RemoveRequest` | Query durable launch state by request ID / retry cleanup after a lost response |
| `Quarantine(ctx, worker)` | Stop ALL execution/network access and persist restart denial at the node; retain workspace/worktree/logs. Resume with a new worker under new policy |
| `Profiles(ctx)` | Query the running node's CPU, guest RAM, host memory reservation, and PID limits. These are reservation limits, not measured RSS |
| `Capabilities()` / `ProxyCapabilities(ctx)` | Query node build, protocol, and features / connected auth proxy features |

## Example

- General egress defaults to deny even with zero-value `Options.EgressPolicy`.
- Empty host domain and CIDR grants disable general egress, and a worker cannot widen that empty grant.
- With grants present, the host maximum is enforced by a managed proxy even if the worker omits its own list.
- Bare `example.com` includes subdomains; `=example.com` is exact.
- Explicitly set `Options.EgressPolicy.AllowPublicDestinations: true` to permit public destinations without grants.
- `DefaultDeny: true` takes precedence over that opt-in.
- Fixed AI routes use independent authentication and model authorization.

```go
package main

import (
    "context"
    "errors"
    "os"
    "time"

    "github.com/pacificbelt30/codex-dock/pkg/cxd"
)

func main() {
    ctx := context.Background()
    client, err := cxd.Open(ctx, cxd.Options{
        ProxyAdminSecret: os.Getenv("CODEX_DOCK_PROXY_ADMIN_SECRET"),
    })
    if err != nil { panic(err) }
    defer client.Close()

    worker, err := client.Launch(ctx, cxd.LaunchRequest{
        RequestID:     "product/project-7/job-123/attempt-1",
        ProjectDir:    "/srv/work/project-7",
        Image:         "codex-dock:latest",
        Profile:       "small",
        NoGitMetadata: true,
    })
    if errors.Is(err, cxd.ErrInProgress) {
        // Retry later with the same RequestID.
        return
    }
    if err != nil { panic(err) }
    defer client.Remove(context.Background(), worker.ID, true)

    command, err := client.CodexHTTPCommand(ctx, worker.ID)
    if err != nil { panic(err) }
    command.Argv = append(command.Argv, "exec", "Fix the tests")
    code, err := client.Exec(ctx, worker.ID, cxd.ExecRequest{
        Argv: command.Argv,
        Env: command.Env,
        Stdout: os.Stdout,
        Stderr: os.Stderr,
    })
    if err != nil { panic(err) }
    if code != 0 { panic("codex exited unsuccessfully") }
    if err := client.Stop(ctx, worker.ID, 10*time.Second); err != nil { panic(err) }
}
```

| Request or operation | Execution behavior |
|---|---|
| No `Agent` in `Launch` | Idle worker for repeated `Exec` calls |
| `Agent: "codex"` / `"claude"` | Run the CLI as the main process |
| `Exec` | Return the guest exit code separately from transport errors |
| `Wait` | Wait for the main process, not an individual `Exec` |

- This example sends a live AI request. Replace `/srv/work/project-7` with a real project and prepare an OpenAI-authenticated proxy first.
- `CodexHTTPCommand` supplies explicit HTTP/SSE settings. Never log `command.Env`, which contains the worker placeholder.
- CLI `auth_profile` and `admin_secret_file` are not loaded automatically. Read admin authority on the host and supply `Options`, or configure [case proxies and a Store](credential-store.md).

- `LaunchRequest` also supports model, approval mode, extra mounts, read-only rootfs, and disabling shared Git metadata.
- Its default image is `codex-dock:latest` and default resource profile is `standard`.
- Query `client.Profiles(ctx)` before selecting the smaller `small` or lightweight `compact` (guest 512 MiB / host 768 MiB) profile.

| Image preparation | Contract |
|---|---|
| `client.PrepareProject(ctx, dir, requestID, log)` | Validate `oaiso.toml`, build missing/stale images through BuildKit, return `LaunchRequest` for product review |
| `client.Build` / `client.BuildTemplate` | Available separately |
| `Launch` prerequisites | Built image and running auth proxy |

- `PrepareProject` does not load global settings or real credentials.
- It copies project `egress_allow_domains` into `LaunchRequest.EgressAllowDomains`.
- The Go API starts or reuses a dedicated egress proxy identified by the same policy ID as the CLI.
- `Options.EgressPolicy` sets host limits that a worker allowlist cannot expand.
- Build the matching proxy image before launch.
- The API does not interactively start the auth proxy.
- Keep real API keys and OAuth tokens out of `LaunchRequest` and the VM; keep the admin secret in host `Options` only.
- The product applies its own host restrictions and authorizes users, cases, and runs.
- For case credentials and execution identity, see [case-scoped credentials](credential-store.md).

## Retries and recovery

- Make each `RequestID` unique across the node.
- Include the user or case, project, run, attempt, and connection version when those distinctions matter to your product.
- Reusing an ID with different immutable options returns `ErrConflict`.
- The node stores hashes of the ID and settings in a durable ledger, not the original ID, task text, or proxy admin secret.

- With `UseWorktree: true` and no branch name, the API derives a branch name from the request ID.
- A safe retry can reuse that branch after cleanup of a failed launch.

- Concurrent launches with the same ID allow only one launcher to proceed; the other gets `ErrInProgress`.
- If the response is lost, retry with the same ID to find the existing worker.
- After a launcher disappears, the node reconciles its ledger with containerd and reservations before allowing a retry.
- While the outcome is uncertain, it returns `ErrInProgress` and creates no second VM.
- A completed worker removed later cannot be recreated with the same ID; the API returns `ErrRequestGone`.
- Use a new ID for a new attempt.

- While the five-minute launch lease is valid, an existing worker alone does not mean startup has completed.
- If the launcher disappears before recording completion, retries may return `ErrInProgress` until the lease expires.
- Once completion is stored in the ledger, retries return that result.

| Launch result | Recovery |
|---|---|
| Error with a `Worker.ID` | Inspect with `Get`; clean up with `Remove` |
| `ErrOutcomeUnknown` | Confirmation was lost; retry with the same ID |
| `ErrCleanupPending` | Retry cleanup |

- `Request(ctx, requestID)` reports the durable `pending`, `complete`, `failed`, or `cleanup-pending` state without exposing the live lease.
- `RemoveRequest(ctx, requestID, true)` retries cleanup of the associated worker and deletion journal.
- It also succeeds if only the prior cleanup response was lost.
- Keep `Options.StateDir` stable across product restarts.

- Canceling the `Exec` context stops the guest command.
- Canceling `Wait` stops only the wait.
- After a canceled `Launch`, the API still attempts cleanup.
- Call `Stop` to stop a VM and revoke its token; call `Remove` to release its resources.

## Errors and compatibility

- Use `errors.Is` for `ErrNotFound`, `ErrConflict`, `ErrInProgress`, `ErrCleanupPending`, `ErrRequestGone`, `ErrCapacity`, `ErrVersionMismatch`, and `ErrOutcomeUnknown`.
- Context cancellation and deadline errors are preserved.
- Use `errors.Unwrap` to inspect the cause.

- The public Go signatures do not expose internal node DTOs.
- Deploy the CLI, library, node, and proxy image from matching builds.
- The Go node protocol is v2; `Open` returns `ErrVersionMismatch` for incompatible node versions.
- `Capabilities()` reports the validated node and `ProxyCapabilities(ctx)` reports the connected auth proxy.
- The node's HTTP/JSON protocol over the Unix socket is internal and has no external compatibility guarantee.

[Additional policy, execution and recovery APIs](sandbox-features.md)

- `Create(ctx, request)` creates a stopped Kata worker; `Start(ctx, worker)` boots and verifies it before token activation and hook/agent execution.
- `Launch` retains its combined create/start behavior.
- `ResourceUsage(ctx, worker)` separates observed host cgroup current/peak memory and OOM kills from the profile budgets.
- See [operating conditions](sandbox-features.md#model-authorization-and-staged-startup).

- Use `LaunchWithTimings(ctx, req)` for sequential startup measurements.
- It returns `Worker`, `LaunchTimings` and `error` with the same contract as `Launch`.
- See [measurement scope and reproduction](performance.md#locate-startup-costs).

### Per-execution model authorization

- Set `LaunchRequest.ModelPolicy` to `&identity.ModelPolicy{AllowedModels: []string{"model-id"}}` to constrain the actual upstream request model per token.
- The effective set intersects the proxy-wide allowlist.
- A `nil` policy preserves inherited behavior; an explicitly empty policy denies every model.
- `Model` selects an agent model and is not authorization.

- Check `ProxyCapabilities.TokenModelPolicy` for support.
- Unsupported proxies reject the request.
- Policies are immutable creation inputs and survive stop/restart.
- Restricted tokens permit validated HTTP/SSE AI routes only; WebSocket and redirects are denied.
- General egress has a separate policy: use `NoInternet` or appropriate destination restrictions when bypass prevention is required.

A worker model policy must fit within 4 KiB including its serialized JSON and label name.

### Connection metadata and Codex HTTP/SSE

- `Connections(ctx, workerID)` returns the guest URL, authentication header/scheme, token environment variable name, transports and model-policy status.
- It contains no token values or host credentials.
- A created worker still requires `Start` before token activation.

- `CodexHTTPCommand(ctx, workerID)` returns explicit HTTP/SSE `Argv` and `Env` containing only a short-lived placeholder.
- Append `exec` or `app-server` and its arguments, then pass both to `ExecRequest`.
- Connection settings use CLI `-c` overrides even when a caller ignores generated user configuration.
- Do not log `Env`.
- Verification targets the worker image's pinned Codex 0.163.0.

- Normal launches with model restrictions disable WebSocket and request compression; unrestricted defaults are preserved.
- The explicit helper also disables compression so the proxy can validate JSON.
- See the [official configuration reference](https://learn.chatgpt.com/docs/config-file/config-reference) and [CLI configuration overrides](https://learn.chatgpt.com/docs/config-file/config-advanced).

### Claude Code connections

- `ClaudeCommand(ctx, workerID)` returns an explicit command/environment for the saved API-key or subscription mode, independent of user/project settings.
- Append native Claude flags and pass both `Argv` and `Env` to `Exec`.
- Do not log `Env`.
- `Connections` reports `ANTHROPIC_API_KEY` with `X-Api-Key` for API mode, or `CLAUDE_CODE_OAUTH_TOKEN` with Bearer for subscriptions.
- See [Claude Code](claude-code.md).

### Credential Store conformance

- Call `pkg/cxd/credentialstore/storetest.Run(t, factory)` from host-adapter tests.
- The factory returns isolated test storage and a `storetest.Harness` controlling login replacement, disconnect, reopen and write-failure injection.
- `Reopen` must construct a fresh adapter over the same durable database.
- Never run the suite against production data or real credentials.

- The suite checks refresh for both providers, stable connection generations, changing credential versions, exactly one winner among 12 concurrent CAS operations across instances, stale-write/reconnect rejection, durability after reopen, pre-commit failure/cancellation atomicity and snapshot ownership.
- The caller supplies its database adapter and backend-specific failure controls.
- Assertions never print credential values.

### Bounded temporary storage

Declare a guest path and filesystem ceiling in `LaunchRequest.TemporaryStorage`:

```go
TemporaryStorage: []cxd.TemporaryStorage{{
    Target: "/run/build-cache", MaxBytes: 64 << 20,
    Medium: cxd.StorageMemory, Lifetime: cxd.StorageLifetimeVM,
    Executable: true,
}},
```

- Only `memory` tmpfs with `vm` lifetime is supported.
- Capacity is a filesystem ceiling, not preallocated memory.
- Actual usage competes with applications within the existing guest memory budget.
- The total of all declared ceilings must fit that budget; leave room for application memory.
- Disk media, persistence and host storage reservations are unsupported and fail explicitly.

- Targets must be clean absolute paths.
- Sizes range from 4 KiB to 2 GiB in 4 KiB increments, with at most 16 filesystems.
- Overlapping filesystems, hidden bind mounts, overlap with read-only binds or filesystem restrictions, and bootstrap/kernel paths are rejected.
- Mounts use the worker UID/GID, initial mode 0700 and `nosuid,nodev`; `noexec` is the default.
- `Executable` controls direct execution, not interpreters reading scripts.

- `Create` does not use the filesystem.
- During `Start`, the bootstrap verifies its type, capacity, owner and mount flags before hooks or user code run.
- Data survives node restart and is discarded when the VM stops.
- A later `Start` gets empty filesystems and reruns hooks.
- Storage settings are immutable creation inputs.

- This requires Kata and an updated pinned bootstrap.
- Drain workers before installing the matching worker-guard and repin assets with `runtime-lock --launcher ... --logger ...`.
- `Capabilities()` advertises `bounded-tmpfs-v1` only when that bootstrap supports capacity verification.
- Unsupported nodes reject requests before creation.
- Workers without declarations retain their existing behavior.

### Runnable embedding example

- [`examples/embedding/main.go`](https://github.com/pacificbelt30/sandbox-oasis/blob/main/examples/embedding/main.go) uses only public APIs: capability/profile checks → Create → Start → cancel a streaming Exec → run another command in the same worker → Stop → RemoveRequest.
- Cleanup uses a separate one-minute context.
- Add `-model` for per-token authorization and `-temporary-mib` for temporary storage.
- Only `-prompt` invokes Codex over explicit HTTP/SSE; the default test command makes no AI request.

```bash
go build -o /var/tmp/oaiso-embedding ./examples/embedding
/var/tmp/oaiso-embedding -project /srv/work/demo -request-id demo/attempt-1 -profile compact
# Requires an updated bootstrap and an allowed model; uses the real upstream.
/var/tmp/oaiso-embedding -project /srv/work/demo -request-id demo/attempt-2 \
  -profile compact -temporary-mib 64 -model MODEL_ID -prompt 'Run the project tests.'
```

- The example reads `CODEX_DOCK_KATA_NODE_SOCKET`, `CXD_EXAMPLE_PROXY_ADMIN_URL`, `CXD_EXAMPLE_PROXY_CONTAINER_URL`, `CODEX_DOCK_PROXY_ADMIN_SECRET` and `CXD_EXAMPLE_STATE_DIR` from its host environment, falling back to `Open` defaults.
- Case proxies also require `CXD_EXAMPLE_CASE_ID`, `CXD_EXAMPLE_RUN_ID` and `CXD_EXAMPLE_CONNECTION_VERSION` together.
- `CXD_EXAMPLE_*` settings belong only to this example executable.
- It does not display admin secrets or helper `Env` values.

- The example removes its request at exit.
- Use a new RequestID for another completed attempt.
- After an uncertain response or cleanup failure, use the reported ID with `Request` / `RemoveRequest`.
- Caller authorization, business state, database adapters, review/approval and Git workflows stay in the host application.
- Reuse the conformance suite above for Credential Store adapters.

- `make kata-api-e2e VERSION=<matching-node-version>` builds and runs this executable with synthetic upstreams in real Kata.
- Select it alone with `scripts/verify-go-api.py --binary ./oaiso --version <version> --run '^TestKataAPIIntegration/embedding-example$'`.
- A run with no executed subtests or only skips fails.
- See the [acceptance setup](../testing.md#公開go-apiの障害系受入).

- See [quarantine](sandbox-features.md#stop-communication-and-quarantine) for completion, retries and retained worktree cleanup.
- `Policy.Quarantined` means the prohibition was persisted; `QuarantinePending` means its stop is unconfirmed.
- Never treat an error from `Quarantine` as success.

- Unreleased main: `LaunchRequest.UsagePolicy` specifies request/concurrency/body-byte/observed-token caps; query with `Usage(ctx, workerID)`. Set `CaseProxyOptions.UsageStateDir` to a 0700 directory outside workspaces. [Usage limits](guides/usage-limits.md).

- Unreleased main: `LaunchRequest.Lifecycle.ProtectMain` protects the primary process from idle reaping. `Heartbeat(ctx, workerID, ttl)` renews/releases a 0–5 minute activity lease without extending maximum lifetime. [Contract and examples](sandbox-features.md#primary-process-protection-and-activity-leases).

## Retaining diagnostic results

- Unreleased main: `SaveLaunchObservation` and `ReadObservations` save/read launch timings. See [format and limits](guides/observability.md). Handle storage failure separately from the launch result.

## Disk checkpoints

- Unreleased main: `Checkpoint`, `Checkpoints`, `InspectCheckpoint`, `RestoreCheckpoint`, `RemoveCheckpoint`. See [saved state, capacity and restore conditions](guides/disk-checkpoint.md).

## Worker TLS

- Available: Unreleased (main). `EnsureCaseProxy` enables TLS; query `ProxyCapabilities().WorkerMTLS`.
- [Expiry, restart and legacy worker recreation](guides/worker-tls.md).

- TLS worker connection URLs refer to that guest’s loopback relay. Do not reuse them on the host/another VM or use URLs as execution IDs; use worker/run IDs.

## Resource controls

- Unreleased main: [connection, bandwidth, FD and block I/O limits](guides/resource-limits.md). `ResourcePolicies(ctx)` exposes complete profiles; `ResourceUsage.Policy` reports the running contract. Proxy TCP limits are separate from AI request budgets.
