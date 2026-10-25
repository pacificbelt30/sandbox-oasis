# Manage proxies

[日本語](../../commands/proxy.md) | **English**

- `oaiso proxy` manages the auth and general egress proxies on containerd.
- See [auth proxy](../auth-proxy.md) and [update procedure](../../operations.md#cliプロキシを更新する) (Japanese).

- Release installations prepare required images from the installed static CLI during `init` / `proxy run`. Ordinary use does not require `proxy build`, a source checkout or Go.

## Start

```bash
oaiso proxy run
```

| Flag | Default | Purpose |
|---|---|---|
| `--name` | `codex-auth-proxy` | Auth container name |
| `--http-name` | `codex-http-proxy` | Egress container name |
| `--port`, `-p` | `18080` | Internal auth data port, not host-published |
| `--admin-port` | `18081` | Admin port published on host loopback |
| `--http-port` | `18082` | Internal egress port, not host-published |
| `--network` | `dock-net-proxy` | Shared egress network; created if missing |
| `--admin-secret` | Unset | Server secret; configure the same value in host clients |
| `--admin-secret-file` | Unset | Read admin authority from an owner-only 0600 file; mutually exclusive with a value |
| `--forward-default-deny` | `true` | Deny general egress without destination grants |
| `--allow-model` | Empty (unrestricted) | Repeat exact model IDs; constrained AI routes reject WebSocket |
| `--upstream-proxy` | Unset | Explicit HTTP(S) upstream proxy, using verified destination IPs |
| `--ca-bundle` | Unset | Additional proxy PEM roots |
| `--max-connections` / `--max-connections-per-source` | `0` | v0.0.4. Worker-facing TCP connections per proxy / source IP |
| `--network-bytes-per-second` | `0` | v0.0.4. Combined bytes/second per source IP. [Scope and behavior](../guides/resource-limits.md) |
| `--credential-routes` | Unset | Owner-only 0600 broker JSON; mounted into auth only |
| `--credential-store-socket` / `--case-id` | Unset | Case-specific Store connection; cannot combine with independent profiles |

- Policy flags are `--forward-allow-domain`, `--forward-deny-domain`, `--forward-allow-cidr`, `--forward-deny-cidr`, `--forward-allow-port` and `--forward-deny-port`.
- Domain/CIDR flags repeat; port flags repeat or accept comma-separated values.
- See [policy rules](../../proxy-policy.md) (Japanese).

- Legacy mode reads credentials as the caller's UID/GID through read-only binds under a private HOME.
- An [independent profile](auth.md#independent-profiles) selected by `--auth-profile NAME` mounts only its directory read-write for refresh and does not load host authentication.
- Old layouts, policy mismatches and missing worker-auth capability are rejected rather than silently updating existing containers.

## Build

**For development and custom proxy images built from source.**

```bash
oaiso proxy build
oaiso proxy build --tag my-proxy:v1 -f ./docker/proxy/Dockerfile
```

- `--tag/-t` overrides `proxy_image`; `--dockerfile/-f` selects the Dockerfile.
- Standalone BuildKit imports OCI output into containerd.
- Build from the repository root: the current directory is the context and Go source is required.
- The search order is `docker/proxy/Dockerfile`, legacy `auth-proxy.Dockerfile` / `docker/auth-proxy.Dockerfile`, then the config directory.

## Edit network settings

```bash
oaiso proxy configure
```

- Fields, saving and application: [proxy settings UI](ui.md#proxy-settings).
- Required order: [configure, check, recreate](../guides/networking.md).

## Stop and remove

```bash
# Both default proxies
oaiso proxy stop
oaiso proxy rm

# One named proxy
oaiso proxy stop --name codex-http-proxy
oaiso proxy rm --name codex-http-proxy
```

- Use `--name` for custom names.
- `proxy rm --force` removes a running container.
- Finish worker tasks first; these proxies are shared services.

## Local process

The internal `proxy serve` command is available for development and standalone use:

```bash
oaiso proxy serve --role auth \
  --listen 127.0.0.1:18080 --admin-listen 127.0.0.1:18081 \
  --admin-secret-file "$HOME/.config/codex-dock/proxy-admin-secret"
```

- Role `auth` serves AI/token/admin routes; `egress` forwards general traffic.
- [Create the admin-secret file first](auth.md#create-admin-authority-and-start-the-proxy); this keeps its value out of argv.
- The default data listen address is `0.0.0.0:18080`, so set loopback explicitly for local use.
- This command does not create Kata isolation or worker networks.
- See [standalone use](../proxy-standalone.md).

- Repeat `oaiso proxy run --allow-model model-id` to enforce exact model IDs in fixed AI request bodies.
- See [supported routes and limitations](../sandbox-features.md#model-authorization-and-staged-startup).
