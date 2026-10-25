# Preview a guest web application

[日本語](../../guides/preview.md) | **English**

- Available: **unreleased (main)**.
- Requires a running worker with an HTTP/WebSocket app reachable at guest `127.0.0.1`.
- Listens only on host `127.0.0.1`; this is not public hosting.

Example: the app is already listening on guest port 3000.

```bash
oaiso preview dev-worker 3000 --lifetime 30m
```

Example output; port and token vary:

```text
http://127.0.0.1:49152/__oaiso_login?token=...
```

1. Open the URL in a browser on the execution host.
2. Login sets a cookie and redirects to the app root.
3. Press `Ctrl-C` in the CLI to close the preview.

| Control | Behavior |
|---|---|
| Authentication | Random per-preview token; HttpOnly, SameSite=Strict cookie |
| Lifetime | Default 30 minutes, maximum one hour; closes connections on expiry/CLI exit |
| Target | Exactly one worker-loopback port, through verified Exec streams |
| Host and Origin | Reject values different from the issued loopback origin |
| Capacity | Eight concurrent requests, sixteen TCP connections, 8 MiB request bodies |
| Worker stop/quarantine | Refuse new requests; close existing connections after state checks every second |
| Secrets | Strip the preview cookie before forwarding to the guest; no provider credentials are injected |
| Management sockets | Never published |

- Keep the login URL private.
- The browser executes JavaScript returned by the guest app. VM isolation does not isolate the browser.
- This connection is separate from general egress and AI authentication. Changing egress policy alone does not close the preview.
- Each connection starts a guest relay process; this is a development preview, not a high-throughput serving path.

```go
preview, err := client.Preview(ctx, workerID, 3000, 30*time.Minute)
if err != nil {
    return err
}
defer preview.Close()
// Present preview.URL explicitly to the user; do not automatically log it.
<-preview.Done()
```

- Applications with absolute URLs or custom Origin validation may need application configuration matching the preview URL.
