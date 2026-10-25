# Manage credential keys with a keyring

[日本語](../../guides/keyring.md) | **English**

- **Available in v0.0.3 or later.**
- New `oaiso login` profiles use Secret Service by default. Existing profiles keep their storage backend.
- API keys and OAuth tokens remain age-encrypted in `.vault`. The keyring holds the age private identity and authentication key.
- Work VMs receive no keyring, D-Bus socket or real credentials.

## Choose a session

| Environment | Action |
|---|---|
| Available desktop keyring | Run `oaiso login` normally; an unlocked keyring needs no additional input |
| SSH, WSL or headless VM | Open `oaiso auth keyring session` |
| Without a keyring | Choose `login --encrypt` for a product passphrase |
| Explicit plaintext | Choose `login --plaintext`; 0700 directories and 0600 files |

- No GUI, TPM or USB key is required.
- An unavailable keyring fails closed. A disposable store/read/delete probe runs before provider login or API-key collection.

## 1. Install dependencies

v0.0.3+ `oaiso setup` installs missing D-Bus, GNOME Keyring and `secret-tool` dependencies. Offline setup requires these packages in advance. Manual Ubuntu installation:

```bash
sudo apt-get update
sudo apt-get install --no-install-recommends dbus-daemon gnome-keyring libsecret-tools
```

| Component | Purpose and official documentation |
|---|---|
| D-Bus | User-process communication; [official session runner](https://dbus.freedesktop.org/doc/dbus-run-session.1.html) |
| GNOME Keyring | Key storage/unlocking; [project](https://gitlab.gnome.org/GNOME/gnome-keyring), [daemon manual](https://manpages.debian.org/unstable/gnome-keyring/gnome-keyring-daemon.1.en.html) |
| `secret-tool` | Secret Service store/lookup/delete; [official implementation](https://github.com/GNOME/libsecret/blob/main/tool/secret-tool.c) |

The full GNOME desktop is unnecessary; distribution packages can still pull GUI-related libraries.

## 2. Desktop use

```bash
oaiso auth keyring check
oaiso login --agent codex --method subscription --profile work
oaiso init --agent codex
```

```text
Keyring: ready (store/read/delete verified; no provider request).
```

- `check` uses a random disposable item; existing secrets are never displayed.
- Without `DBUS_SESSION_BUS_ADDRESS`, Oaiso discovers the owned `bus` socket in the user's private 0700 runtime directory.
- An existing PAM configuration may unlock the keyring at OS login. Oaiso never receives the OS password.
- Oaiso does not edit PAM. Passwordless logins, including SSH public-key authentication, may need a separate unlock. [PAM documentation](https://wiki.gnome.org/Projects/GnomeKeyring/Pam/Manual).

## 3. Headless use

```bash
oaiso auth keyring session
```

1. Enter and confirm a private passphrase on first use; enter it once subsequently.
2. Oaiso starts private D-Bus and GNOME Keyring processes and unlocks storage.
3. Use ordinary commands inside the opened shell:

```bash
oaiso auth keyring check
oaiso login --agent claude --method api-key --profile claude-work
oaiso init --agent claude
oaiso claude
exit
```

- The 12–4096-byte passphrase is independent of the OS login password.
- Encrypted storage lives at `~/.config/codex-dock/keyring/keyrings/`, separate from the desktop keyring.
- CLI processes in the session share the unlocked service.
- Exiting stops the private keyring and D-Bus; encrypted storage remains.
- Concurrent private sessions using the same storage are rejected.
- An already-running Auth Proxy retains its own in-memory key. Run `oaiso auth profile lock NAME` before leaving if the proxy must also lock.
- Later unlocking of these profiles must also run inside `keyring session`.

To run one command:

```bash
oaiso auth keyring session -- oaiso auth keyring check
```

Explicit noninteractive input uses global `--passphrase-fd`. Never store the passphrase in argv, environment or a plaintext file.

## Existing profiles

- Existing plaintext/passphrase profiles remain usable.
- To change backend, log in to a new profile name with the intended keyring available.
- No automatic keyring copying or conversion of existing passphrase profiles.
- [age format and profile locking](encrypted-credentials.md), [verification and limitations](../../testing.md).
