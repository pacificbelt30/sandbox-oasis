# Configure credential-broker routes

[日本語](../../guides/credential-broker.md) | **English**

- Inject credentials only into a fixed HTTPS origin and allowed paths/methods.
- Workers call the broker using issued placeholders. Real credentials remain in the authentication proxy.

| Feature | Availability |
|---|---|
| Fixed HTTPS origin, subtree paths and methods | v0.0.2+ |
| Templates, CLI and TUI helpers | v0.0.4 |
| Exact paths and CLI method selection | v0.0.4 |

## Choose the scope

| Input | Allowed scope |
|---|---|
| `--exact-path /v1/read` | Only `/v1/read`; reject children and `/v1/read-more` |
| `--path /v1/read` | `/v1/read` and `/v1/read/...`; reject `/v1/read-more` |
| Both | Either path list may authorize the path |
| No paths | Reject saving/startup |
| `--method GET` | GET only; omission defaults to GET and HEAD |
| `--method POST` | POST only; CONNECT/TRACE cannot be configured |

- Repeat methods or use `--method GET,HEAD`. Repeat `--exact-path` for multiple exact endpoints.
- Exact matching covers the URL path, excluding query/body content.
- Reject ambiguous paths: alternate path encodings, dot segments, repeated/trailing slashes and control characters.
- Do not follow redirects; reject WebSocket. Incoming Host and method-override headers are not forwarded.
- If an origin accepts destinations or operations through query/body data, review that API's own authorization.

## Create an exact read endpoint

Run in Bash as the regular operator. Replace the example origin/path. Secrets stay out of terminal output, history and command arguments.

```bash
read -r -s -p 'Broker secret: ' broker_secret
printf '\n'
printf '%s' "$broker_secret" | oaiso broker set service \
  --template bearer-readonly --origin https://api.example.com \
  --exact-path /v1/read --method GET --secret-stdin
unset broker_secret
oaiso broker check
```

Example output with one route:

```json
[{"name":"service","upstream":"https://api.example.com","paths":null,"exact_paths":["/v1/read"],"methods":["GET"],"header":"Authorization","prefix":"Bearer ","secret_set":true}]
```

- Exit 0 and `secret_set: true` indicate successful local configuration checks.
- `paths: null` means no subtree permission; only the exact path applies.
- No DNS, connectivity, provider authentication or inference is tested.

### Allow descendants instead

Update the saved `service` to permit GET/HEAD on its subtree, preserving its secret:

```bash
oaiso broker set service --origin https://api.example.com --path /v1/read
oaiso broker check
```

### Create a separate POST-only endpoint

Enter its secret through stdin. Permit `/v1/write` only, excluding descendants:

```bash
read -r -s -p 'Broker secret: ' broker_secret
printf '\n'
printf '%s' "$broker_secret" | oaiso broker set service-write \
  --origin https://api.example.com --exact-path /v1/write \
  --method POST --secret-stdin
unset broker_secret
oaiso broker check
```

## Edit in the TUI

```bash
oaiso broker templates
oaiso broker configure
```

| Field | Meaning |
|---|---|
| Template | `bearer-readonly` uses Authorization; `api-key-readonly` uses X-Api-Key. Both start with GET/HEAD |
| Path prefixes | Permit a path and its descendants |
| Exact paths | Permit that path only |
| Methods | Specify only required methods |
| New secret | Blank preserves the saved value; saved secrets are never displayed |

1. Enter name, HTTPS origin and paths.
2. Apply a template; adjust Methods if needed.
3. Validate, enter the secret, then Save.

- Dashboard `B` opens the same form. `broker configure` also works without a running node.
- `Load route` loads the named route. Reload after a concurrent-edit rejection.

## Apply to the proxy

Drain workers and explicitly remove old proxies before creating them again. Saving alone does not update running rules.

```bash
oaiso proxy run --credential-routes "$HOME/.config/codex-dock/broker/routes.json"
```

Host configuration alternative, adding to the existing `[proxy]` section:

```toml
[proxy]
credential_routes = "/home/alice/.config/codex-dock/broker/routes.json"
```

- Workers call `/broker/service/v1/read` on the authentication proxy with an issued placeholder Bearer token.
- Existing `Paths` retain subtree behavior. To narrow a route, move it to `ExactPaths` and remove overlapping `Paths`.
- Use `CredentialRoute.ExactPaths` in Go, `ExactPaths` in the stored JSON and `exact_paths` in `broker check` output.
- `--file PATH` edits/checks another file; provide the same absolute path to the proxy.

## Storage and protection

| Item | Scope |
|---|---|
| Default file | `~/.config/codex-dock/broker/routes.json` |
| Editor permissions/limits | Directory 0700, file 0600; at most 1 MiB/128 routes |
| Secrets | Plaintext in this private file, separate from agent age profiles |
| Worker mounts | Node rejects overlap with broker files; keep them outside workspaces |
| Authentication/revocation | Require a live issued token; revoke existing broker streams when it expires or is revoked |
| Model-restricted tokens | Reject broker routes because arbitrary APIs have no verifiable model schema |
| Origin | Trust the fixed upstream; it may echo received secrets in its response |

- Host root and the same UID are trusted.
- Review [CONNECT limits and bypass prevention](../../proxy-policy.md#pathmethodまで限定する場合) (Japanese). General egress and fixed AI routes retain separate authorization.
