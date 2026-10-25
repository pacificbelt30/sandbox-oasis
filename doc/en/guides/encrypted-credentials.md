# Encrypt stored credentials

[日本語](../../guides/encrypted-credentials.md) | **English**

- **Available in v0.0.3 or later.**
- New `login` profiles use the [keyring](keyring.md) by default. Existing profiles and low-level `auth profile set/import` keep their existing behavior.
- Supports API keys and subscription credentials. The Auth Proxy remains; work VMs receive no real credentials.

## Choose a backend

| Backend | Use case | Requirements |
|---|---|---|
| Passphrase | WSL, Linux VMs, SSH and headless hosts | A long passphrase, at least 12 bytes |
| Keyring (new-login default) | Desktop, SSH, WSL and VMs | Secret Service, user D-Bus, `secret-tool`; [setup and headless use](keyring.md) |

- age is embedded: no additional age executable, service, database, Vault or container; storage/unlocking are local.
- Use an existing desktop service or an explicitly opened private session; failure never switches backends. Keyring storage protection depends on its OS configuration.
- Existing keyring unlock dialogs may appear. Unanswered operations fail after 15 seconds.
- [`secret-tool` manual](https://manpages.debian.org/trixie/libsecret-tools/secret-tool.1.en.html); Ubuntu packages it as `libsecret-tools`.

## Create an encrypted login

```bash
oaiso login --agent codex --method subscription --profile work --encrypt
```

- Enter and confirm a hidden passphrase.
- Choose `--method api-key` for an API key; `--agent claude` for Claude Code.
- Subscription login runs in temporary Kata; collected credentials are encrypted directly.
- Existing profiles are never overwritten; choose a different name.

Use `--keyring` instead of `--encrypt` for an existing OS keyring:

```bash
oaiso login --agent claude --method api-key --profile claude-work --keyring
```

## Start, inspect and lock

```bash
oaiso init --agent codex
oaiso status
oaiso auth profile show work
oaiso auth profile lock work
```

| Action | Behavior |
|---|---|
| Proxy startup/restart | Unlock on host; transfer the data key through container stdin |
| Additional tasks with a running proxy | Check wrapped-key fingerprint and connection generation; reuse without another prompt |
| `status`, `profile show/list` | Show storage metadata without decryption |
| `status --check-connection` | Explicitly unlock and check API keys through models APIs; no inference |
| `profile lock` | Stop affected workers and authentication proxies; retain work and ciphertext |

- `show` reporting `locked` means that command has not decrypted credentials. Inspect the separate status proxy check for a running proxy.
- Lock interrupts work; save results first.
- Stopping the proxy or rebooting also discards its process-local key.
- OS keyring locking does not erase an already-unlocked proxy key; use `profile lock` separately. That command does not lock the OS keyring itself.
- Old proxy images need rebuilding; drain workers before recreating proxies.

## Migrate plaintext

1. Save work and stop affected workers.
2. Stop the authentication proxy.
3. Explicitly migrate:

```bash
oaiso auth profile encrypt work
```

- Save ciphertext, read it back and verify before removing `work.json`.
- Migration rotates connection generation; create new workers instead of reauthorizing old ones.
- If interrupted with both records present, normal reads fail closed. Repeat the same command/passphrase to compare credentials and complete migration.
- Different content is never silently removed.
- Deleted disk blocks and old backups are not securely erased.

## Migrate the older encrypted format

```bash
oaiso auth profile show work
oaiso auth profile lock work
oaiso auth profile migrate-age work
oaiso init --agent codex
```

- `Format: legacy-v1` identifies the old format; new records show `Format: age`.
- Save work first. Stop/remove old proxies, then migrate using the original passphrase or keyring.
- Verify age persistence/read-back and rotate connection generation; create new workers.
- Retain `work.vault.v1-backup` as 0600 ciphertext, never automatically loaded.
- Retry the same command after interruption; a different backup is never overwritten.
- Retrying an already migrated record verifies it without rotating generations again.
- Keyring migration also retains the old keyring item for backup recovery; operators can remove retired items after verification.
- New proxy startup/refresh on the old format is refused, without plaintext/host credential fallback.
- Host `setup --rollback` does not downgrade credential formats. Prefer reauthentication when recovering an older version; do not resume old workers.

## Change a passphrase

```bash
oaiso auth profile lock work
oaiso auth profile passwd work
```

- Unlock with the old passphrase, then enter/confirm the new one.
- Remove stopped profile proxies, then protect the same age private identity/authentication key using the new passphrase.
- Refuse changes while profile workers/proxies are running; unavailable for keyring-backed profiles.
- Run `oaiso init` with the new passphrase to restart the proxy.

## Noninteractive unlocking

```bash
oaiso --passphrase-fd 3 init --agent codex 3< /private/runtime/passphrase-input
```

- This demonstrates FD wiring. Supply a private secret source or temporary pipe, never a passphrase file in the project or alongside ciphertext.
- Descriptor >= 3, maximum 4096 bytes, one trailing newline removed. Separate it from API-key stdin.
- No passphrase arguments/environment variables.
- For `passwd`, provide old input using `--passphrase-fd` and separate new input using `--new-passphrase-fd`.

## Format and limits

| Item | Specification |
|---|---|
| Storage | `~/.config/codex-dock/profiles/NAME/NAME.vault`; 0700 directories, 0600 files |
| Container | JSON manifest containing standard age ciphertext/protected-key data; the whole `.vault` is not direct age CLI input |
| Cipher | age v1.3.2 X25519 recipient; HMAC-SHA256 also prevents public-key-only substitution |
| Key protection | age scrypt, `logN=17` (approximately 128 MiB), only when unlocking |
| OS keyring | age private identity/authentication-key bundle in existing Secret Service |
| Secret location | Unlocked host command/proxy memory; only placeholders in work VMs |
| Main protection | Reading inactive files, disks and backups |
| Outside protection | Host root/same-UID process access, live memory, swap and outer VM snapshots |

- WSL uses private Linux storage. Reject unverified DrvFS/NFS/FUSE filesystems. Kata separately requires KVM.
- Lost passphrases/keyring keys cannot be recovered; reauthenticate into a new profile.
- Local-only storage cannot fully detect rollback of a VM snapshot containing both keys and ciphertext.
- [Detailed specification](../development/local-credential-store.md), [verification and remaining gaps](../../testing.md).
