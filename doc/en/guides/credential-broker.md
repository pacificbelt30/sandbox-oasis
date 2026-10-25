# Configure credential-broker routes

[日本語](../../guides/credential-broker.md) | **English**

- Configuration helpers: **Unreleased (main)**. The broker itself is available since v0.0.2.
- Inject credentials only into fixed HTTPS origins and permitted paths/methods. The [existing boundary](../../proxy-policy.md) remains: workers receive proxy placeholders.

| Template | Injected header | Methods |
|---|---|---|
| `bearer-readonly` | `Authorization: Bearer …` | GET, HEAD |
| `api-key-readonly` | `X-Api-Key: …` | GET, HEAD |

## Create a route

As the regular operator, replace the example origin/path with your service:

```bash
oaiso broker templates
oaiso broker configure
```

- Enter name, HTTPS origin and path; apply template, Validate, enter secret, Save.
- Dashboard `B` opens the same form. Use `broker configure` without a running node.
- `Load route` loads scopes by name, never the saved secret. A blank secret preserves it; new input replaces it.
- Paths include descendants. Restrict methods to those required. Concurrent edits reject stale saves; reload before retrying.

CLI alternative, in Bash with hidden input:

```bash
read -r -s -p 'Broker secret: ' broker_secret
printf '\n'
printf '%s' "$broker_secret" | oaiso broker set service \
  --template bearer-readonly --origin https://api.example.com \
  --path /v1/read --secret-stdin
unset broker_secret
```

## Validate and apply

```bash
oaiso broker check
```

- Exit 0 prints scope metadata and `secret_set: true`, never secret values.
- Checks owner/permissions/JSON/HTTPS origin/path/method/header/secret presence. No DNS, connection, authentication or inference request.
- Default: `~/.config/codex-dock/broker/routes.json`, directory 0700, file 0600, at most 1 MiB/128 routes. **Broker secrets are plaintext in this private file**, separate from agent age profiles.

Drain workers and explicitly remove old proxies before recreating them. Saving never changes running policy.

```bash
oaiso proxy run --credential-routes "$HOME/.config/codex-dock/broker/routes.json"
```

Host configuration alternative, adding to the existing `[proxy]` section:

```toml
[proxy]
credential_routes = "/home/alice/.config/codex-dock/broker/routes.json"
```

- `--file PATH` edits/checks another file; use the same absolute path in proxy flags/host configuration.
- Node rejects overlap between broker files and VM shares in either creation order. Keep the file outside workspaces.
- Host root/same UID are trusted. A configured upstream may echo a credential in its response; configure only trusted fixed origins.
