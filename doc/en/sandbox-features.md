# Additional sandbox controls

- These controls are opt-in.
- General egress defaults to [denial](../proxy-policy.md) (Japanese); fixed AI routes use separate authorization.
- Deploy matching node, proxy and worker images before enabling new controls.
- File restrictions, image digests, idle activity and jobs reject unsupported nodes instead of silently skipping enforcement.

| Priority | Feature | Entry points |
|---|---|---|
| P0 A1 | Effective policy, explanation, preflight | `policy show/check/explain`, Go API |
| P0 A2 | Structured denial events | `policy events`, TUI E, Go API |
| P0 A3 | Explicit presets | `--policy-preset`, project config |
| P0 A4 | Diagnostics and capabilities | `doctor`, `capabilities`, `ProxyCapabilities` |
| P1 A5 | File read/write restrictions | `--deny-read`, `--deny-write` |
| P1 A6 | Exec/files/jobs/PTY/session | CLI and Go API |
| P1 A7 | Lifetime, idle, retention | Explicit settings plus `reap --watch` / `Reap` |
| P1 A8 | Environment locks, hooks, readiness | `environment lock`, launch settings |
| P1 A9 | Independent clones and recovery | `--clone-dir`, `artifact`, files API |
| P1 A10 | Fixed origin/path/header credential broker | `--credential-routes`, Go callbacks |
| P1 A11 | SSH/JVM, upstream proxy, custom CA | Compatibility flags and proxy settings |

| Outside these additional controls | Features |
|---|---|
| Mandatory defaults | File masks, clones and automatic deletion |
| Additional network features | TLS MITM and live policy changes |
| Additional execution backends | VM snapshots/forks, new OS backends and remote multi-user services |

## Inspect and configure network policy

```bash
oaiso policy check --json
oaiso policy show --worker WORKER --json
oaiso policy explain api.example.com:443 --resolve --json
oaiso policy events --worker WORKER --follow --json
oaiso policy presets
oaiso run --policy-preset api --egress-allow-domain api.example.com -d
oaiso run --policy-preset dependencies -d
oaiso run --policy-preset offline -d
oaiso proxy configure
```

- Worker inspection reads its actual selected proxy configuration and policy ID.
- Without `--worker`, commands inspect host settings for new workers.
- Explanation never opens a connection.
- Without `--resolve`, an allowed decision covers only the hostname and port.
- With it, every DNS answer is checked; connection establishment repeats those checks.

- `offline` and `test` disable general egress while preserving fixed AI routes.
- Configured fixed credential-broker routes also remain available independently of general egress rules.
- `dependencies` selects common npm, Python and Go registries; `api` requires explicit domains.
- Presets cannot widen the host allowlist or override denials/private-address blocking.

- In the existing TUI, **P** opens proxy settings and **E** opens denial history.
- Standalone `proxy configure` works without a running node.
- It edits the six allow/deny lists, empty-list default denial, upstream proxy URL and CA path, validates destinations and saves without exposing secrets or overwriting unrelated settings.
- Concurrent edits are detected.
- Unsupported multiline arrays are rejected safely and can be edited in a text editor.
- Saving does not update running proxies.
- Drain workers and explicitly recreate the selected proxies to apply changes.

- Denial events contain time, reason code, observed source address, destination authority and policy ID.
- They exclude paths, queries, headers and credentials.
- Egress proxy history is shared by its workers; source addresses are not authenticated run identities.
- Use separate proxies for cases requiring separate history.
- The in-process ring retains 512 records; CLI/API read the last 1000 retained console lines.
- Each proxy process writes at most 100000 events, emits `event_log_limit`, then stops writing events to bound diagnostic disk use.
- This is diagnostic history rather than a persistent audit log across proxy recreation.

## Stop communication and quarantine

To stop communication from an existing worker, run these commands on the host:

```bash
oaiso policy quarantine --worker WORKER
oaiso policy show --worker WORKER --json
```

- `policy revoke` is an alias. **This stops ALL execution in the selected worker, including general HTTP/CONNECT, fixed AI communication, background jobs and agents.**
- Other workers and shared proxies remain running.
- The quarantine operation retains the workspace, managed worktree, container and logs.
- Existing retention policies through `reap` and explicit deletion still apply; export artifacts that need longer preservation.
- Saving settings or using ordinary `stop` does not quarantine a worker.

- An updated node durably records the restart prohibition in its private OCI record before stopping without a grace period.
- The node rejects start, VM start, exec and background-job execution, and completes interrupted stops during node recovery.
- Accepted stops continue after a client disconnect with a node-owned 90-second deadline.
- A different client's `StateDir` or replaying the same RequestID cannot clear it.
- This requires a supporting node; reclaim retained workers before downgrading to an older node.
- Check `worker-quarantine-v1`; unsupported nodes reject the operation.

- Success requires the node's stop acknowledgment, stopped/quarantined status, AI placeholder revocation and Kata reservation release.
- Lost responses, failed stops or credential-management failures return errors; retry the same worker.
- A persisted request whose stop is unconfirmed reports `quarantine_pending`, and destination explanation cannot claim completed denial.

- Resume with a new worker and RequestID under the new policy.
- Unquarantine is not provided. Unreleased main supports [explicit live egress changes](guides/live-policy.md) while worker execution continues.
- VM termination stops further communication from the guest.
- It cannot undo operations already accepted by a remote service and does not wait for every upstream socket in the shared egress proxy to close.
- Preserve artifacts before reclaiming the retained worker with ordinary `rm` / `RemoveRequest`; removing a managed worktree also deletes that worktree.

## Files, execution and recovery

```bash
oaiso run --deny-read /workspace/.env --deny-write /workspace/config -d
oaiso exec --workdir /workspace --env MODE=test WORKER -- npm test
oaiso exec -it WORKER -- bash
oaiso files get WORKER src/main.go --local main.go.copy
oaiso files put WORKER input.json --local input.json
oaiso files list WORKER src
oaiso jobs start WORKER -- sh -c 'npm test'
oaiso jobs list WORKER
oaiso jobs logs WORKER JOB_ID --offset 0
oaiso jobs kill WORKER JOB_ID
oaiso artifact diff WORKER --output changes.patch
oaiso artifact commit WORKER --message 'Save staged changes'
oaiso run --clone-dir /absolute/new-copy -d
```

- File restrictions use OCI masked/readonly paths below `/workspace/`.
- Targets must already exist as regular files/directories inside the workspace; symlinks and resolutions outside it are rejected.
- These are not inherited rules for future files, nor inode labels for aliases/hardlinks.
- Avoid exposing the same sensitive content through additional mounts.
- File APIs execute with guest Python 3, reject symlinks in every component, accept regular files up to 64 MiB, and atomically replace uploads in the same directory.
- Directory listings allow at most 10000 entries.
- VM validation and guest permissions remain enforced.

- PTY streams combine stdout/stderr.
- Streaming executions/sessions end on cancellation or disconnection.
- Background jobs continue after client disconnection, with state and shim-owned logs recovered after node restart.
- They retain at most 16 MiB of combined output while draining subsequent output.
- Each logs call returns up to 1 MiB with the next offset and truncation status.
- Finished jobs remain until worker removal; the node retains at most 4096 execution sessions.

- Independent clones copy committed content without hardlinking Git objects.
- Destinations must be new absolute paths.
- Clones remain for recovery after launch failure or worker removal; retries never silently adopt an existing directory.
- Diff exports tracked staged/unstaged changes relative to HEAD.
- Retrieve untracked files explicitly, and stage desired changes/configure guest Git identity before committing.

## Lifecycle and reproducibility

```bash
oaiso run --max-lifetime 2h --idle-timeout 30m --retention 24h -d
oaiso reap --dry-run
oaiso reap --watch
oaiso environment lock IMAGE --source Dockerfile --source package-lock.json
oaiso run --image IMAGE --environment-lock codex-dock.lock.json -d
oaiso run --start-hook 'npm ci' --readiness 'test -f node_modules/.package-lock.json' --readiness-timeout 2m -d
```

- Lifecycle limits require `reap --watch` or periodic Go API `Reap` calls.
- No host service is installed automatically; limits stop being applied while the supervisor is down.
- Idle means time since management execution activity, not guest CPU/filesystem activity.
- Updated nodes protect registered jobs and exec/PTY sessions. Use `--protect-main` or bounded `activity` renewals below for other known work; guest CPU/filesystem activity is not inferred.
- Explicit maximum lifetime can still stop busy workers.
- Stop revokes tokens; removal uses existing ownership checks and deletion journals.
- Zero retention never deletes a worker.

- Environment locks pin the node OCI image digest and SHA-256 hashes of explicitly selected source files.
- New lock files are created exclusively.
- Source or image changes reject launch.
- Include dependency lockfiles to cover package versions.
- Start hooks run sequentially inside the guest after auth setup and before agent startup; launch completes after the readiness probe succeeds.
- Failures/deadlines roll back launch.
- The default hook/probe deadline is 60 seconds, within the existing 180-second launch deadline.
- Older images without the support label reject hooks.

- Project `[run]` supports `policy_preset`, `deny_read`, `deny_write`, `max_lifetime`, `idle_timeout`, `retention`, argv arrays for `start_hooks`/`readiness`, and `readiness_timeout`.
- Host secrets, extra mounts, runtime and proxy endpoints remain host settings.

## Credentials and compatibility

- `oaiso proxy run --credential-routes /absolute/private-routes.json` mounts a 0600 host JSON file read-only into the auth proxy alone.
- The egress proxy remains credential-free.
- Example values are synthetic:

```json
[{"Name":"service","Upstream":"https://api.example.com","Paths":["/v1"],"Methods":["GET","POST"],"Header":"X-Service-Key","Prefix":"","Provider":"openai","Secret":"HOST_SECRET"}]
```

- `ExactPaths` adds exact matching (unreleased main). Existing `Paths` include descendants; both allowlists are combined. See [examples](guides/credential-broker.md).
- Guests send live placeholder Bearer tokens to `http://AUTH_PROXY/broker/service/v1/...`.
- Fixed HTTPS origin, path boundaries, method and provider are checked before injecting credentials.
- Encoded paths, WebSockets and following upstream redirects are disallowed.
- Secrets are never returned by a credential retrieval API.
- Only configure trusted upstream APIs: an upstream that echoes its received secret can expose it.
- Changes require drain/recreation.
- Embedders can also use `StartCredentialBroker` with `CredentialRoute.Secret` callbacks, explicitly arranging listeners and worker network placement.

```bash
oaiso run --ssh-proxy --jvm-proxy --ca-bundle /absolute/extra-ca.pem -d
oaiso proxy run --upstream-proxy http://proxy.example.com:3128 --ca-bundle /absolute/extra-ca.pem
```

- SSH uses a `GIT_SSH_COMMAND` CONNECT helper through the egress policy.
- Host SSH keys/agent sockets are not shared automatically.
- JVM HTTP(S) proxy properties are opt-in.
- Worker CA bundles extend system trust for curl, Python and Node.
- When Java and keytool are installed, the worker copies the system JVM truststore into its HOME and imports the additional certificates.
- Proxy CA bundles extend system roots for both HTTP and WebSocket TLS verification.
- Proxy chaining sends CONNECT to locally verified destination IPs and preserves private-IP blocking.
- Credential-bearing upstream proxy URLs are rejected.

## Go API and performance

- The [Go API](go-api.md) adds `Policy`, `ExplainPolicy`, `PolicyEvents`, execution working directories/environments/TTY, `StartSession`/`Resize`, file and job operations, diff recovery and `Reap`.
- Existing methods remain available.
- Probe node and proxy capabilities separately.

- The network-policy benchmark identified per-request linear domain scans and repeated IP parsing.
- Compiled domain indexes preserve existing domain/IP matching semantics, covered by equivalence tests.
- For 1000 domains, three runs on the same host measured 65.9–181 µs, 48112 B and 1002 allocations per check before, versus 152–153 ns, 0 B and 0 allocations after.
- These are policy checks, not end-to-end network latency.
- Reproduce with:

```bash
go test ./internal/networkpolicy -run '^$' -bench . -benchmem -count=3
```

- TUI logs append incrementally instead of rebuilding history on each write, with 2000 lines and approximately 4 MiB per segment.
- Polling stops when the UI exits.

The [verification record](../testing.md) distinguishes fixture tests, native Kata acceptance, and external-service tests.

## Model authorization and staged startup

- Use repeated `oaiso proxy run --allow-model model-id`, host `[proxy].allowed_models`, or `CaseProxyOptions.AllowedModels` to restrict a whole auth proxy to exact submitted model IDs.
- Empty preserves legacy behavior.
- The immutable configuration requires draining workers and recreating the proxy to change.
- `ProxyCapabilities` reports support and active enforcement separately.

- When enabled, authenticated POST requests are checked before credential injection or upstream I/O.
- Only JSON bodies up to 8 MiB are accepted; duplicate keys, case variants of `model`, invalid JSON, compressed bodies and encoded paths are rejected.
- The only allowed query is exact `beta=true` on Claude Messages/count_tokens; every other query is denied.
- The original checked bytes are forwarded and redirects are not followed.
- Supported paths are `/v1/responses`, `/v1/responses/compact`, `/v1/chat/completions`, `/v1/completions`, `/v1/embeddings`, `/v1/moderations`, `/chatgpt/codex/responses`, `/chatgpt/codex/responses/compact`, `/anthropic/v1/messages`, and `/anthropic/v1/messages/count_tokens`.
- All other AI paths, model-listing/batch/file APIs and WebSockets are denied.
- Clients must support HTTP.
- This controls the submitted ID, not upstream alias resolution.
- Custom broker routes, general egress and separately supplied credentials have separate policies; disable or restrict egress and do not configure alternative AI broker routes when model enforcement is mandatory.

- `Create` materializes a Kata worker without starting a VM, hooks or agent or issuing a live worker token.
- It still creates networks, snapshots and a host reservation; defer `Create` too if resource allocation requires approval.
- Remove abandoned workers explicitly.
- Maximum lifetime counts from creation, including any approval wait.
- Create and Launch cannot share a request ID; retries of the same operation keep the same ID.
- `Start` verifies isolation, activates the token and waits for hooks/readiness.
- After cancellation or failure, reconcile with Request/Get and use a fresh context for cleanup.
- Launch retains its combined behavior.

## Supervised collection and memory observation

- The optional systemd user unit `deploy/kata/codex-dock-reaper.service` runs the installed `/usr/local/bin/oaiso reap --watch` and restarts on failure after five seconds.
- Copy it to `~/.config/systemd/user/`, reload the user manager and explicitly enable/start the unit.
- Monitor its journal and status.
- An administrator must configure user linger if it needs to survive logout.
- Nothing is enabled automatically.
- Multi-case products should periodically call Reap through clients holding their own admin credentials and monitor failures.

- Updated nodes protect pending execs, active exec/PTY sessions and detached jobs against idle collection, checking atomically with stopping so newly started work is protected too.
- Use `--protect-main` for the primary process or bounded `activity` renewals for work known to the caller; neither infers guest CPU/filesystem activity.
- Explicit maximum lifetime still applies to busy workers.

- ResourceUsage returns a verified running VM's host cgroup current memory, optional peak and OOM-kill count separately from guest/host capacity budgets.
- These are not guest RSS.
- The guest budget is the worker cgroup limit, separate from total VM RAM shown by /proc/meminfo.
- The standard default and small guest 1 GiB / host 2 GiB profile remain unchanged.
- An explicit compact profile provides guest 512 MiB / host 768 MiB, one CPU and 512 PIDs for lightweight tasks.
- Select CLI --profile-size compact or Go Profile: "compact" after querying Profiles on an updated node.
- This is not a guarantee that large dependency installs/builds fit; measure product-specific workloads, concurrency, peaks and OOM events.
- CLI vm inspect --json exposes the same host observations.

## Operator resource profiles

- Unreleased main: [connection, bandwidth, FD and block I/O limits](guides/resource-limits.md). `ResourcePolicies(ctx)` exposes complete profiles; `ResourceUsage.Policy` reports the running contract. Proxy TCP limits are separate from AI request budgets.

- PID limits are configured per profile.
- For example, `oaiso profile define compact-256 --base compact --pids-limit 256` prints a JSON array with compact's CPU/memory budgets and a 256-PID ceiling.
- It does not edit files or node configuration.
- If definitions already exist, add the generated element to their array.
- `oaiso profile --node --json` queries built-in and registered profiles on the running node; `oaiso profile compact-256 --node` displays one selected budget.
- Without `--node`, the command continues to display built-in defaults.

- Built-in `standard`, `small` and `compact` budgets are unchanged.
- Operators can register additional CPU, guest/host memory and PID combinations in a root-owned JSON file with no group/world write permission.
- Load it with `node serve --resource-profiles /etc/codex-dock/resource-profiles.json`:

```json
[{"name":"batch","cpu_cores":2,"guest_memory_bytes":536870912,"host_memory_bytes":805306368,"pid_limit":256}]
```

- Names start with a lowercase letter and contain lowercase letters, digits or hyphens, up to 63 characters.
- The file permits 32 definitions and 64 KiB; duplicate or built-in names are rejected.
- Bounds are 1–64 CPUs, 512–2048 MiB guest memory, host memory at least 256 MiB above guest memory and at most 3072 MiB, and 64–4096 PIDs.
- Memory must be MiB-aligned.
- These settings stay within pinned Dragonball assets and retain mandatory host/guest startup verification.

- Query `client.Profiles(ctx)` before selecting `Profile: "batch"`.
- CLI callers use `--profile-size batch` or global `run.profile_size`; completion queries the node.
- The package-level `cxd.Profiles()` returns built-in defaults only.

- Non-512 PID profiles require the updated guest hook and schema-v2 metadata bound to that image.
- `prepare_guest.py build` generates these assets; `runtime-lock` checks the image SHA256 and pins matching metadata as `guest-policy`.
- Legacy images/manifests retain 512 behavior.
- Registering non-512 limits on unsupported assets fails node startup.
- Check `dynamic-pid-profiles-v1` and `operator-resource-profiles-v1`.
- The node sets an OCI annotation and bootstrap verifies the actual guest limit before activating credentials, using the [pinned Kata hook state](https://github.com/kata-containers/kata-containers/blob/4.2.0/src/agent/rustjail/src/container.rs).

- Drain existing VMs before switching/resealing guest images.
- Allocation records pin the full custom budget.
- Adding names does not change asset stamps, but file changes require a node restart.
- Drain every reservation using a definition before modifying or deleting it: retained reservations cannot be reinterpreted.
- Restore the old definition before recovery if it was changed accidentally.
- Deployment and service restart are not automatic.

- When resealing, use `runtime-lock --logger /absolute/current/codex-dock-io-logger` to select the matching logger explicitly.
- An older helper selected through PATH may lack bounded-job logging options; pin the matching node-bundle helper.

## Primary-process protection and activity leases

- Available: **Unreleased (main)**; requires matching CLI/node support.
- Existing idle defaults remain unchanged. Explicitly protect workers that need it.

| Setting | Protected interval | On expiry/completion |
|---|---|---|
| `run --protect-main` | Primary process running or paused | Normal retention after exit; interactive waiting also counts as running |
| `activity WORKER --ttl 2m` | Two minutes from the node-confirmed time | Missing renewals expire; last activity and idle policy govern collection |
| `activity WORKER --ttl 0s` | Releases the renewable lease | Release time becomes the last activity |
| `--max-lifetime` | Independent of both protections | May stop busy workers |

- Renew from the operator CLI or host Go API; no management socket/upstream credentials enter VMs.
- TTL is an integer 0–300 seconds. Renew sooner than expiry and monitor failures.
- The node persists protection/expiry across node restarts. VM restart clears the previous renewable lease.
- The final node-side idle decision is atomic. Unknown state, persistence failure or an inconsistent acknowledgement is not success.
- Run `reap --watch` or periodic Go API `Reap` calls to enforce lifecycle limits.

On a configured execution host, protect a one-hour primary command from idle collection:

```bash
oaiso run --name long-job --detach --keep --idle-timeout 5m --protect-main --max-lifetime 2h --exec 'sleep 3600'
oaiso reap --dry-run
```

Replace `my-worker` with an existing worker name to renew an activity lease:

```bash
oaiso activity my-worker --ttl 2m
```

Example output (illustrative timestamp):

```json
{"worker":"my-worker","active_until":"2026-10-24T12:02:00Z"}
```

Release protection separately:

```bash
oaiso activity my-worker --ttl 0s
```

- Go API: `LaunchRequest.Lifecycle.ProtectMain = true`, `client.Heartbeat(ctx, workerID, 2*time.Minute)`.
- Project/host configuration: `[run] protect_main = true`.
