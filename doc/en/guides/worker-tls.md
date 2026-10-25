# Encrypt worker-to-auth-proxy traffic

[日本語](../../guides/worker-tls.md) | **English**

- Available: Unreleased (main).
- Enabled for auth proxies created by standard `proxy run` and Go `EnsureCaseProxy`.

| Hop or credential | Behavior |
|---|---|
| Codex/Claude → guest relay | HTTP on guest loopback `127.0.0.1:18083`; never published outside the VM |
| Guest relay → auth proxy | TLS 1.3, client certificate bound to both the worker token and its current public key |
| Real provider API/OAuth credentials | Remain in the auth proxy |
| Guest private key | Disposable execution credential; never the proxy signing key |
| AI HTTP/SSE/WS, token retrieval, OAuth refresh, broker | Same encrypted hop and binding; existing model, scope, budget and revocation checks remain |
| General egress proxy | Existing HTTP proxy/CONNECT behavior; outside this TLS feature |
| Host admin hop | Existing separate management path; no new public management TLS service |

Copied tokens alone, or combined with another execution's certificate, are rejected. Full guest compromise can still use that guest's authorized token and short-lived key. Host root, node operators and the same host UID remain trusted.

The verified bootstrap starts the loopback relay after verification/token activation, before releasing user code. Agent-specific mTLS configuration is unnecessary.

## Coverage by entrypoint

| Entrypoint | Worker-facing transport |
|---|---|
| Auth proxy created by `oaiso proxy run` | Automatically enables mTLS |
| Go `EnsureCaseProxy` | Automatically enables mTLS |
| Direct `oaiso proxy serve` | Explicit `--worker-mtls`, separate admin listener and protected admin secret required |
| Custom in-process proxy | Explicit `WorkerMTLS` configuration required |
| Go `StartCredentialBroker` | Current helper uses HTTP; do not use for VM paths requiring TLS |

- Configure fixed-origin broker routes through `--credential-routes` on the standard auth proxy to receive the same mTLS protection.
- The legacy plaintext `StartCredentialBroker` path is not automatically converted to TLS.
- Custom integrations should check `ProxyCapabilities().WorkerMTLS` and reject unsupported proxies when TLS is required.
- Existing deployments are not automatically changed; align component versions and recreate them as below.

## Upgrade and use

Save work and drain/remove old workers/proxies before upgrading matching CLI/node/guard/proxy components. Existing environments are not automatically stopped or replaced.

On a host configured for the new CLI:

```bash
oaiso proxy run
oaiso run --name tls-example --detach --keep --no-internet --exec 'sleep 300'
oaiso stop tls-example
oaiso start tls-example
```

- Admin secrets must be at least 32 bytes. When unset, managed proxies create `~/.config/codex-dock/proxy-admin-secret` with mode 0600. Implicit reads apply only to the default local admin endpoint, never external endpoints. See [authentication](authentication.md).
- Worker/proxy TLS mode mismatch requires worker recreation; no HTTP fallback.
- Direct `proxy serve` integrations select `--worker-mtls` with a separate admin listener and protected secret.
- Specify the admin secret file explicitly for non-default admin URLs (`oaiso run --proxy-admin-secret-file /absolute/path`).
- Check `ProxyCapabilities().WorkerMTLS` in Go. Custom legacy/in-process proxies are not changed without explicit configuration.

| Lifecycle | Behavior |
|---|---|
| Token/client certificate | At most 3600 seconds; longer TTLs rejected for new TLS workers |
| Expiry/revoke/replacement | Reject new requests and close active streams |
| Worker stop/start | Issue fresh key/certificate after verification; old key rejected |
| Proxy restart | New in-memory authority; old certificates untrusted. Stop/start workers to reauthenticate |
| CA/server private keys | Proxy memory only, generated per process |
| Guest execution key | Transferred over admin/exec stdin, saved 0600 at `/run/codex-dock-control/transport.json` on tmpfs; absent from argv, images and shares |
| Checkpoint | `/run` is excluded; restored workers get fresh execution credentials |
| Invalid certificate/time skew/relay failure | Refuse connection/startup; check node and guest clocks |

Credentials are not automatically renewed indefinitely. Save work and stop/start before expiry for longer sessions. See [security](../security.md) and [test evidence](../../testing.md).
