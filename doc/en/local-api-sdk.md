# Local API and Python / TypeScript SDKs

[日本語](../local-api-sdk.md) | **English**

- Available: **unreleased (main)**. Packages are not published to public registries yet.
- Manage workers on the same Linux execution host through private pipes: SDK → `oaiso api stdio` → [public Go API](go-api.md) → existing node Unix socket.
- No TCP listener or management-socket forwarding is added. Existing socket permissions remain required and grant host authority, including arbitrary mounts. Use trusted callers only.

## Requirements and scope

| Item | Requirement |
|---|---|
| Execution host | [Prepared node](../setup.md), auth proxy and a built/imported worker image |
| Versions | Matching CLI, node and proxy builds; local protocol v1, node protocol v2 |
| Python | 3.12+; no runtime dependencies. [Install uv](https://docs.astral.sh/uv/getting-started/installation/) |
| TypeScript | Node.js 22+; no runtime dependencies. [Install Node.js](https://nodejs.org/en/download). Compiler/types are development dependencies |
| Configuration | Explicit Open options; no automatic loading of CLI configuration or credential profiles |
| Credentials | Read the existing owner-only 0600 proxy admin-secret file into `Options.ProxyAdminSecret`. Real provider credentials stay in the auth proxy |
| Workspace | Existing absolute `ProjectDir`; normal `/workspace` sharing and host-file effects apply |
| Included | Create, launch, start, exec, get, list, wait, stop, remove, request reconciliation, quarantine, resources and capabilities; files, jobs and disk checkpoints |
| Use Go for | PTY, streaming I/O, image builds, artifact bundles, previews, live policy changes, checkpoint retention, credential Store and case-proxy creation |

- For writable workspaces, set `LaunchRequest.User` to the owning numeric UID/GID. Omitting it uses the image user; the SDK does not change host ownership.
- General egress defaults to deny, as in Go. Host grants use `Options.EgressPolicy`.
- No credential-returning API is exposed. Options travel through stdin, never shell arguments. SDKs discard bridge stderr and return fixed error categories.
- Guest stdout/stderr are explicitly returned by `exec`; callers remain responsible for what their guest commands print.

## Install Python from source

Run as the operator from a checkout containing this feature. Create a new virtual environment; choose another directory if it already exists.

```bash
uv venv --python 3.12 .venv-sdk
uv pip install --python .venv-sdk/bin/python ./sdk/python
.venv-sdk/bin/python -c 'from oaiso import Client; print("SDK import OK")'
```

`SDK import OK` verifies the import, not node connectivity.

### Create, execute and clean up

Save as `example.py`. Place `oaiso` on PATH, prepare the proxy, replace `/srv/work/project` with a real directory and adjust the admin-secret path. Request IDs are examples: preserve the ID and options for retries; choose a new ID for a new task.

```python
from pathlib import Path
import os
from oaiso import Client

secret = (Path.home() / ".config/codex-dock/proxy-admin-secret").read_text().strip()
request = {
    "RequestID": "my-app/project-1/task-1/attempt-1",
    "ProjectDir": "/srv/work/project",
    "Image": "codex-dock:latest",
    "Profile": "compact",
    "User": f"{os.getuid()}:{os.getgid()}",
    "NoInternet": True,
}
with Client({"ProxyAdminSecret": secret}) as client:
    worker = client.create(request)
    try:
        client.start(worker["ID"])
        result = client.exec(worker["ID"], ["printf", "hello"])
        print(result["exit_code"], result["stdout"].decode())
    finally:
        client.remove_request(request["RequestID"], force=True)
```

```bash
.venv-sdk/bin/python example.py
```

Example output:

```text
0 hello
```

- `Client(...)` validates connectivity and versions only.
- `create` reserves resources without running guest code; `start` starts the guest.
- Closing the client closes handles; stop/remove workers explicitly.

## Install TypeScript from source

Run from the repository root:

```bash
npm --prefix sdk/typescript ci --ignore-scripts --no-audit --no-fund
npm --prefix sdk/typescript run build
```

Then run from your application directory, replacing the source path:

```bash
npm install /absolute/path/to/sandbox-oasis/sdk/typescript
```

Save this as `example.mjs`. TypeScript uses the same API with the included declarations. Apply the same workspace, secret-path and request-ID requirements as in Python.

```javascript
import { readFile } from 'node:fs/promises';
import { homedir } from 'node:os';
import { Client } from '@oaiso/local';

const secret = (await readFile(`${homedir()}/.config/codex-dock/proxy-admin-secret`, 'utf8')).trim();
const request = {
  RequestID: 'my-app/project-1/task-2/attempt-1',
  ProjectDir: '/srv/work/project',
  Image: 'codex-dock:latest',
  Profile: 'compact',
  User: `${process.getuid()}:${process.getgid()}`,
  NoInternet: true,
};
const client = await Client.open({ ProxyAdminSecret: secret });
try {
  const worker = await client.create(request);
  try {
    await client.start(worker.ID);
    const result = await client.exec(worker.ID, ['printf', 'hello']);
    console.log(result.exit_code, result.stdout.toString());
  } finally {
    await client.removeRequest(request.RequestID, true);
  }
} finally {
  await client.close();
}
```

```bash
node example.mjs
```

- Expected output: `0 hello`. Do not log options or put secrets in argv.
- TypeScript rejects fractions, non-finite values and unsafe integers before sending and rejects unsafe received numbers rather than returning rounded values. Use Go or Python for larger 64-bit integers.

## Cancellation and deadlines

| Operation | Cancellation means |
|---|---|
| `wait` | Cancel the wait only; the worker continues |
| `exec` | Propagate cancellation to the guest command; reconcile races with completion/disconnection |
| `create` / `launch` | Use Go cancellation/cleanup; query the request and remove remaining resources |
| `stop` / `remove` | A missing response leaves completion unconfirmed; query worker/request state |
| Cancel result `accepted: true` | The active call received cancellation; this does not confirm stop/cleanup |
| Disconnect / `close` | Cancel active calls and close handles; created workers may remain |

- Python: `pending = client.submit("wait", {"worker_id": worker_id})`, `pending.cancel().result()`, `pending.result()`.
- TypeScript: `const pending = client.submit('wait', {worker_id: workerID})`, `await pending.cancel()`, `await pending.result`.
- TypeScript also accepts `{signal: controller.signal}` in the final options argument.
- Server deadline: 180 seconds by default, at most 1,200 seconds. Configure Python `timeout_ms` / TypeScript `timeoutMs`; zero selects the default.
- Python `result(timeout=seconds)` requests cancellation on timeout. Mutations may already have completed and require reconciliation.
- Stop grace is separate: default 10 seconds; zero means immediate forced stop.

## Retry and errors

| Identifier / state | Meaning and next action |
|---|---|
| Call `id` | Connection-local response correlation, increasing from 1; assigned by SDK |
| `LaunchRequest.RequestID` | Durable, node-wide creation identity, 1–256 UTF-8 bytes; assigned by application |
| `pending` / `in_progress` | Running/recovering; query the same ID, do not duplicate with another ID |
| `complete` | Durable creation result; query `get` for current running state |
| `failed` | Prior creation failed; retry identical request/options |
| `cleanup-pending` / `cleanup_pending` | Retry cleanup by worker/request ID |
| `conflict` | Immutable options differ for an existing request |
| `request_gone` | Completed worker was removed; the same ID cannot recreate it |
| `outcome_unknown` / `transport_lost` | Reconcile via `request` / `get` before deciding to retry |
| `version_mismatch` | Install matching CLI/node/proxy builds, then reconnect |
| `permission_denied` | Check existing socket/file permissions; the SDK does not elevate privileges |
| `internal` | Unclassified Go failure; underlying text is withheld. Inspect state/audit on the trusted host |

- **No automatic retries**. Creation retries require identical request ID, immutable options and connection options. Mixing `create` and `launch` with the same ID conflicts.
- Replaying `exec` may execute twice; never retry it automatically after a lost response.
- Python `APIError`: `code`, `codes`, `requires_reconciliation`, `partial_result`. TypeScript: `code`, `codes`, `requiresReconciliation`, `partialResult`.
- Partial `Worker.ID` can be used for cleanup. Error strings omit raw arguments, credentials and underlying errors.
- Reconciliation flags describe the affected call; a false flag never proves unrelated workers stopped.
- Worker `Status` is an observed opaque string. An unknown status never means stopped.

## Machine-readable contract

| Item | Contract |
|---|---|
| Transport | Private NDJSON stdin/stdout pipes, local protocol v1; one JSON object per line |
| Request | `version`, `id`, `method`, `params`; optional `timeout_ms` |
| Response | `version`, `id`, `result` and/or `error`; errors may include partial results |
| Types | Existing Go JSON field names are preserved inside DTOs; consult the schema for `RequestID` versus `request_id` |
| Units | Bytes use base64; Go DTO durations use nanoseconds, call deadlines milliseconds, stop grace seconds |
| Bounds | 1 MiB request, 4 MiB response, 256 KiB each stdout/stderr, 16 concurrent calls; cancel bypasses active-call slots |
| Overflow | Cancel and return `output_limit` plus partial output. Use Go streaming APIs for large output/PTY |
| Validation | Reject unknown/duplicate fields and malformed frames. A malformed outer frame closes the connection; reconcile active mutations |
| Source | [JSON Schema](https://github.com/pacificbelt30/sandbox-oasis/blob/main/api/local-v1/schema.json), generated from Go DTOs and checked for CI drift |

```bash
oaiso api schema > local-api-v1.schema.json
```

- Schema export needs no node/auth access. It uses JSON Schema draft 2020-12.
- `x-oaiso` records per-method retry/cancel rules, request states and result types.
- The schema defines types and the wire contract; Go and the node enforce reservations, network/model authorization and path constraints at runtime. Breaking changes require a new local protocol version and contract; discover additive methods through capabilities.
- This is a deliberately limited adapter; check capabilities before using an operation. SDKs do not call the internal node HTTP API directly.

## Developer checks

Run from the repository root with Go, uv and Node.js installed:

```bash
go run ./api/generate
npm --prefix sdk/typescript ci --ignore-scripts --no-audit --no-fund
go test -race ./pkg/cxd/localapi
go test -c -o /tmp/oaiso-local-api.test ./pkg/cxd/localapi
uv run --no-project --no-config --managed-python --python 3.12 python scripts/verify-local-sdks.py --fixture /tmp/oaiso-local-api.test
```

- Both SDKs use the same Go adapter for version/retry/conflict/lost-response/cancel/output-bound/redaction/cleanup tests.
- See [verification records](../testing.md) for real Kata prerequisites, scope and results. Synthetic success is separate from real-account authentication/inference.

## Files, jobs and checkpoints

- Available: **unreleased (main)**; additive methods within local API v1.

| Operation | Python | TypeScript |
|---|---|---|
| Read / write / list | `read_file` / `write_file` / `list_files` | `readFile` / `writeFile` / `listFiles` |
| Background execution | `start_job` | `startJob` |
| Job lookup / list | `job` / `list_jobs` | `job` / `listJobs` |
| Logs with offset | `job_logs(worker_id, job_id, offset=0)` | `jobLogs(workerID, jobID, 0)` |
| Wait / kill | `wait_job` / `kill_job` | `waitJob` / `killJob` |
| Save / list disk checkpoints | `checkpoint` / `checkpoints` | `checkpoint` / `checkpoints` |
| Inspect / remove | `inspect_checkpoint` / `remove_checkpoint` | `inspectCheckpoint` / `removeCheckpoint` |
| Restore to a new VM | `restore_checkpoint(id, request)` | `restoreCheckpoint(id, request)` |

Inside an existing Python client context with a running `worker`:

```python
worker_id = worker["ID"]
client.write_file(worker_id, "input.json", b'{"task":"example"}')
print(client.list_files(worker_id))
job = client.start_job(worker_id, ["python3", "-c", "print('done')"])
finished = client.wait_job(worker_id, job["id"])
logs = client.job_logs(worker_id, job["id"], offset=0)
print(finished["exit_code"], logs["output"])
```

The TypeScript equivalent using an existing client/worker:

```typescript
await client.writeFile(worker.ID, "input.json", Buffer.from('{"task":"example"}'));
console.log(await client.listFiles(worker.ID));
const job = await client.startJob(worker.ID, ["python3", "-c", "print('done')"]);
const finished = await client.waitJob(worker.ID, job.id);
const logs = await client.jobLogs(worker.ID, job.id, 0);
console.log(finished.exit_code, logs.output);
```

| Contract | Behavior |
|---|---|
| Files | Relative to `/workspace`, no symlink traversal, at most 512 KiB per transfer. Go/CLI retain their separate 64 MiB limit |
| Larger files | Use Go/CLI; chunked SDK upload is not provided |
| Starting jobs | Never automatically retried. Reconcile uncertain outcomes using job IDs/listing |
| Logs | Use `next` for the next offset; `truncated` indicates a retention gap |
| Canceling job wait | Cancels waiting only, not the job |
| Checkpoint creation | Requires a stopped Kata worker. Inspect the catalog after losing a create response |
| Restore | New RequestID required; reject conflicting image/digest or runc |

See [disk checkpoint scope](guides/disk-checkpoint.md). RAM and connections are excluded.
