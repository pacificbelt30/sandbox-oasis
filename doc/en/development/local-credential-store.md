# Local encrypted credential store specification

[日本語](../../development/local-credential-store.md) | **English**

- Specification for v0.0.3 or later.
- [Usage](../guides/encrypted-credentials.md), [Store contract](../credential-store.md).

## Defaults and dependencies

| Item | Contract |
|---|---|
| Default | New login profiles use Secret Service; existing profiles and low-level set/import remain unchanged; explicit `--encrypt`/`--plaintext` alternatives |
| Location | Private product profiles on the control host, outside workspace/clone/additional shares |
| Encrypted data | API keys, OAuth access/refresh/ID tokens, credential versions and connection generations |
| Implementation | Embed `filippo.io/age` v1.3.2 in the Go binary |
| External dependencies | Passphrase mode needs no network, external age CLI, additional daemon or database |
| Proxy admin authority | Separate 0600 file; outside this store and never shared with work VMs |
| Go | Minimum 1.26.0; development/CI toolchain 1.27.3. Binary-distribution users need no Go |

- [Official age source](https://github.com/FiloSottile/age/tree/v1.3.2), [format specification](https://age-encryption.org/v1), [Go API](https://pkg.go.dev/filippo.io/age@v1.3.2).

## Format and cryptography

| Element | Contract |
|---|---|
| File | `NAME/NAME.vault`, a single atomically updated JSON manifest |
| Manifest version | Oaiso schema 2, separate from the standard age format version |
| Public metadata | Profile name, unlock backend, key ID, public age recipient and protected key file |
| Credential ciphertext | Standard binary age stream, base64 encoded in the manifest, encrypted to an X25519 recipient |
| Secret bundle | Product-specific age private identity plus an independent random 32-byte authentication key |
| Store authentication | HMAC-SHA256 binds the manifest and credential ciphertext |
| Complete reads | Consume the age stream through EOF, validating the final chunk and size limit before use |

- Public-key age encryption does not authenticate the sender. The separate authentication key prevents someone knowing only the recipient from replacing stored credentials undetected; protect it together with the private identity.
- age implements credential/key-bundle encryption. No custom cipher, age recipient or age-format parser is implemented.
- The whole `.vault` file is a JSON manifest, not direct input to the age CLI. Interoperability tests extract its age ciphertext and decrypt it with the official CLI.
- Keeping one file avoids multi-file transactions during ordinary OAuth refresh.

## Key protection

| Method | Contract |
|---|---|
| Passphrase | Encrypt the secret bundle using an age scrypt recipient; hidden TTY or dedicated FD |
| New scrypt cost | `logN=17`, `r=8`, `p=1`, approximately 128 MiB workspace; explicit small-control-host budget |
| Accepted maximum | `SetMaxWorkFactor(18)` prevents unbounded file-supplied KDF work |
| Input limits | New passphrases 12–4096 bytes; explicit descriptor >= 3, maximum 4096 bytes |
| Keyring | New-login default; store/read-back the bundle through an existing service or an explicit private session |
| Keyring requirements | Host `secret-tool` and user D-Bus; 15-second operation timeout |
| Everyday use | Unlock on proxy startup; no repeated prompt while the same proxy is running |
| Refresh cost | Reuse the unlocked identity and encrypt to its public recipient; no scrypt per inference/refresh |

- age CLI defaults to `logN=18`; Oaiso explicitly uses 17 for its 128 MiB budget and accepts reads up to 18. [Implementation](https://github.com/FiloSottile/age/blob/v1.3.2/scrypt.go).
- No automatic unlocking through a plaintext key alongside ciphertext.
- OS keyring encryption/locking depend on OS configuration. Unavailable backends never switch to passphrases, plaintext or host credentials.
- No direct backend-conversion command; reauthenticate into a new profile using the other backend.

## Proxy and VM boundary

1. Unlock the identity/authentication-key bundle in host CLI memory.
2. Transfer it to the existing Auth Proxy through a bounded, length-prefixed private stdin stream.
3. Drop the long-running host command's unlocked references after proxy readiness.
4. Give work VMs only short-lived, proxy-scoped placeholders.

| Boundary | Contract |
|---|---|
| argv/environment/configuration/container metadata/ordinary logs | No secret bundle or real credentials |
| Stdin protocol | Maximum 1024 bytes; reject partial input, disconnect and excessive lengths |
| Proxy reuse | Match profile, protected-key fingerprint, connection generation and protocol |
| Older proxy protocol | Drain workers and explicitly remove/recreate the authentication proxy |
| Work VMs | No ciphertext, identity, authentication key, credential socket or control-host HOME |
| Login-only Kata | Separate temporary memory-only collection using the pinned official CLI |

## Persistence and locking

| Operation | Contract |
|---|---|
| OAuth refresh | Cross-process lock and atomic expected-version CAS; retain connection generation |
| Durable save | Ciphertext temporary file, fsync, atomic rename and directory fsync |
| Failure/conflict | Never acknowledge success; propagate to existing proxy revocation behavior |
| Product lock | Stop workers/proxies belonging to this private store; retain work/ciphertext |
| OS keyring lock | Does not erase an already-unlocked proxy; run `auth profile lock` separately |
| Process close | Clear owned key/authentication-key byte slices and drop the age identity reference |

- Public `credentialstore.Store` is unchanged.
- Go cannot guarantee zeroing every temporary secret copy; bound lifetime, including process termination.

## Explicit migration

| Source | Operation |
|---|---|
| Plaintext `.json` | `auth profile encrypt NAME`; verify age persistence/read-back before deleting plaintext |
| Encrypted schema 1 | `auth profile migrate-age NAME`; unlock using its original backend and explicitly convert |
| Ordinary schema-1 writes/new proxy startup | Refuse; no automatic conversion or credential fallback |
| Migration generation | Rotate credential/connection versions; old workers cannot be reauthorized |
| Recovery backup | Retain `NAME.vault.v1-backup` with mode 0600; ciphertext only, never loaded automatically |
| Conflicting backup | Refuse overwrite; preserve recovery data |
| Retry/interruption | Validate using the selected backend and resume; an already-migrated profile does not rotate again |
| Coexisting plaintext/ciphertext | Fail closed until explicit migration compares content and completes |
| Passphrase change | Reprotect the same identity/authentication key; retain connection generation |

- Argon2id/XChaCha20-Poly1305 remain only in the read-only schema-1 decoder, never new writes.
- Keyring migration retains the old keyring item for encrypted-backup recovery; operators can remove retired backups/items after verification.
- Backups still decrypt with old passphrases/keys. Deleted blocks and earlier backups are not securely erased.

## Protection and verification

- Primary target: reading inactive credential files, disks and backups.
- Outside this protection: control-host root/same-UID process access, live memory, swap and outer VM snapshots.
- Local-only storage cannot fully detect a snapshot rolling back keys, ciphertext and generation state together.
- WSL uses private Linux storage; reject unverified DrvFS/NFS/FUSE filesystems.
- [Results, measurements and remaining checks](../../testing.md).

## Keyring sessions

- [User guide](../guides/keyring.md): existing desktop service discovery and private headless sessions.
- The CLI owns foreground D-Bus and GNOME Keyring processes; normal exit, SIGINT, SIGTERM and SIGHUP cancel and reap them.
- Keys stay in the separate private keyring files and proxy memory; no passphrase file or global PAM modification.
- Share validation rejects keyring storage and filesystem D-Bus socket paths.
