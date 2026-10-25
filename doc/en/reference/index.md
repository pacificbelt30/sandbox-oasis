# Reference

[日本語](../../reference/index.md) | **English**

- Look up command names, flags, defaults, configuration keys and API contracts.
- For task-oriented instructions, start with [Guides](../guides/index.md).

## CLI

| Topic | Reference |
|---|---|
| Find a command | [Command overview](../commands.md) |
| Launch, agents, mounts and approvals | [run](../commands/run.md) |
| List, restart, exec, wait, stop, remove and logs | [Worker management](../commands/worker.md) |
| Images and templates | [build](../commands/build.md) |
| Authentication proxy | [proxy](../commands/proxy.md) |
| Profiles, API keys and OAuth import | [auth](../commands/auth.md) |
| Networking, TUI, completion and CLI updates | [network](../commands/network-cmd.md), [ui](../commands/ui.md), [completion](../commands/completion.md), [update](../commands/update.md) |

## Configuration

| Topic | Reference |
|---|---|
| Project configuration and precedence | [Project settings](../configuration.md#project-settings) |
| Global configuration, environment variables and resource profiles | [Configuration](../configuration.md) |
| Network decision rules | [Network policy](../../proxy-policy.md) (Japanese) |

## Embedding API

| Topic | Reference |
|---|---|
| Machine-readable contract, Python and TypeScript | [Local API and SDKs](../local-api-sdk.md) |
| Create, launch, execute, stop, remove and retry | [Go API](../go-api.md) |
| Case credentials, credential versions and connection generations | [Credential Store](../credential-store.md) |
| Auth Proxy HTTP API | [Auth API](../auth-proxy/endpoints.md) |

- The Go API connects to the management service on the same Linux execution host.
- Authorization and tenant management for a public remote multi-user service belong to the embedding application.
