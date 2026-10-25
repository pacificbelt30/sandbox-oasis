# Connection, bandwidth, FD and I/O limits

[日本語](../../guides/resource-limits.md) | **English**

- Available: **v0.0.4**, with matching CLI, node, guard and proxy.
- Optional. Unset settings retain existing behavior.
- Separate from CPU, memory, PIDs, request-body size and [per-execution AI usage limits](usage-limits.md).

## Scope and behavior

| Resource | Scope | At the ceiling |
|---|---|---|
| TCP connections | Per proxy and socket source IP | Close new connections; retain existing HTTP/SSE/WS/CONNECT |
| Bandwidth | Combined directions per proxy/source IP, bytes/second | Wait before delivery; initial 32 KiB burst; no new stream lifetime timeout |
| File descriptors (FD) | Each guest process, through an operator-defined profile | Equal soft/hard `RLIMIT_NOFILE`; additional opens fail with `EMFILE`. Exec and child processes use the same ceiling |
| Block I/O | Host VM cgroup, per selected block device | `io.max` delays reads/writes at BPS/IOPS ceilings |

- A separate admin listener is outside these limits. Legacy single-listener configurations share the same budget with admin connections.
- Connection counts include idle and TLS sockets. HTTP/2 streams within one socket are not separate connections. Use existing `--max-concurrent` for AI request concurrency.
- Bandwidth counts worker-facing socket bytes, including auth TLS handshakes/records and opaque CONNECT payloads. It excludes IP/TCP headers, retransmissions and the separate upstream hop.
- Kernel socket buffers mean this is not an exact instantaneous NIC throughput cap.
- Auth and general-egress proxies have independent budgets; they do not provide one combined VM bandwidth ceiling.
- Existing `--max-transfer-bytes` limits **cumulative AI/broker body bytes**, terminating an over-budget stream. Bandwidth throttling limits bytes/second without a cumulative cap.
- Token/certificate expiry, revocation, explicit maximum lifetimes/idle reaping and clients' own timeouts remain effective.

## Configure proxy connections and bandwidth

As the ordinary operator after independent login and initialization. Before changing existing proxies, finish and remove their workers, then [explicitly recreate the proxies (Japanese)](../../operations.md#cliプロキシを更新する).

```bash
oaiso proxy run --max-connections 512 --max-connections-per-source 32 \
  --network-bytes-per-second 1048576
```

| CLI (`proxy run` / `proxy serve`) | Host `[proxy]` key | Values |
|---|---|---|
| `--max-connections` | `max_connections` | 0 unlimited, 1–65536 |
| `--max-connections-per-source` | `max_connections_per_source` | 0 unlimited, 1–65536; cannot exceed the nonzero global limit |
| `--network-bytes-per-second` | `network_bytes_per_second` | 0 unlimited, 1024–1099511627776 |

Alternatively, edit the host's `~/.config/codex-dock/config.toml`:

```toml
[proxy]
max_connections = 512
max_connections_per_source = 32
network_bytes_per_second = 1048576
```

- Explicit flags win. Host settings also apply to automatic startup and policy-specific proxies; project `oaiso.toml` cannot supply them.
- A mismatch with an existing proxy fails instead of silently retaining old limits. Editing configuration does not interrupt existing connections.
- Sources use socket IPs, never forwarding headers. Managed Kata VMs use distinct addresses on individual networks.
- NAT clients sharing an address share a budget. This is not authenticated execution-ID accounting; tokens and certificates still provide authorization.
- Source bookkeeping holds at most 4096 entries. Reconnects do not reset unrefilled bandwidth buckets. Recreating the proxy resets bandwidth state; this is not a durable cumulative usage ledger.
- Refusals happen at TCP admission, so an HTTP 429 is not guaranteed. Closed connections release their slots.

## Define an FD ceiling

As an ordinary operator in a scratch directory. If profiles already exist, append the generated element to their JSON array, preserving existing definitions.

```bash
oaiso profile define limited --base compact --pids-limit 512 --nofile 1024 \
  > resource-profiles.json
```

- `--nofile`: 0 retains the runtime default, otherwise 128–1048576.
- Generating JSON does not register it. Complete the operator registration below.
- Startup and restart compare the OCI setting and trusted guard observations before user code and authentication are activated.
- This is per guest process, not a VM-wide aggregate FD count or a limit on the host Kata runtime/proxy.

## Alternatively, include block I/O ceilings

The operator must identify the host device. Replace the example `8:0` with its actual `MAJOR:MINOR`. The `io` controller must be delegated down to the node's subtree; unavailable requested limits fail closed.

```bash
oaiso profile define limited-io --base compact --pids-limit 512 --nofile 1024 \
  --io-limit '8:0 rbps=67108864 wbps=8388608 riops=4096 wiops=1024' \
  > resource-profiles.json
```

| Dimension | Meaning |
|---|---|
| `rbps` / `wbps` | Read/write bytes per second; positive values up to 2^40 |
| `riops` / `wiops` | Read/write operations per second; positive values up to 2^30 |
| Omitted / `max` | Unlimited dimension; at least one finite dimension per device |
| Repeat `--io-limit` | Up to 16 devices; duplicate devices/keys and unknown keys fail |

- Only block I/O attributed to the host VM cgroup on the selected device is covered. Cached reads, tmpfs, other devices and work performed by separate services are outside that scope.
- Device mapping, writeback and shared inode attribution affect results. Check `io.stat` and your real workload on the correct device.
- The kernel permits temporary bursts. This is neither an exact one-second window nor a minimum bandwidth guarantee for other VMs.
- Specifications: [Linux cgroup v2 I/O controller](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html#io-interface-files), [OCI process rlimits](https://github.com/opencontainers/runtime-spec/blob/main/config.md#posix-process).

## Register and check the profile

1. Drain affected VMs/reservations, preserve existing definitions and install the JSON:

```bash
sudo install -d -o root -g root -m 0755 /etc/codex-dock
sudo install -o root -g root -m 0600 resource-profiles.json \
  /etc/codex-dock/resource-profiles.json
sudo systemctl cat codex-dock-node
sudo systemctl edit codex-dock-node
```

2. In the editor, preserve the original `ExecStart` command/arguments and append the resource-profile flag. The empty assignment clears the previous command. Replace the placeholder with the command you inspected:

```ini
[Service]
ExecStart=
ExecStart=<original command you inspected> --resource-profiles /etc/codex-dock/resource-profiles.json
```

3. After saving, reload and restart. Recheck overridden binary/manifest references after release upgrades:

```bash
sudo systemctl daemon-reload
sudo systemctl restart codex-dock-node
for attempt in $(seq 1 60); do
  if oaiso profile --node --json; then break; fi
  sleep 1
done
oaiso profile --node --json
```

For the FD-only example, as the ordinary operator in the project directory:

```bash
oaiso run --name limited-job --profile-size limited --detach --keep --exec 'sleep 600'
oaiso exec limited-job -- sh -c 'ulimit -Sn; ulimit -Hn'
oaiso vm inspect limited-job --json
```

For the I/O example instead:

```bash
oaiso run --name limited-job --profile-size limited-io --detach --keep --exec 'sleep 600'
oaiso exec limited-job -- sh -c 'ulimit -Sn; ulimit -Hn'
oaiso vm inspect limited-job --json
```

- Both examples print `1024` twice for FD limits. In VM inspection, `io.max` is configuration and `io.stat` is an observation, not proof that all storage is covered.
- Remove your VM afterwards with `oaiso rm -f limited-job`.
- Drain reservations before modifying/removing definitions. Retained budgets cannot be reinterpreted. See [operator-defined profiles](../sandbox-features.md#operator-resource-profiles).

## Go API

| API | Setting / observation |
|---|---|
| Case auth proxy | `CaseProxyOptions.NetworkResources = cxd.ProxyResourceLimits{MaxConnections: 512, MaxConnectionsPerSource: 32, BytesPerSecond: 1048576}` |
| Managed general egress | `Options.EgressNetworkResources` uses the same type; provide explicit destination rules in `EgressPolicy` |
| Complete profile contract | `client.ResourcePolicies(ctx)`, including `FileDescriptorLimit` and `BlockIO` |
| Running VM contract | The returned `ResourceUsage.Policy`; check the method's error. Existing `Profile` retains its comparable CPU/memory/PID contract |
| Launch selection | Existing `LaunchRequest.Profile` |

- [Verification and remaining limits (Japanese)](../../testing.md). These controls do not defend against local root or a trusted operator changing the configuration.
