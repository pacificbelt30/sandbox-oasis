# From installation to the first VM

[日本語](../../guides/first-run.md) | **English**

- This page covers v0.0.4 or later. Check `oaiso --version` first.
- Requires Ubuntu 26.04, x86_64, systemd, KVM and cgroup v2. [Requirements](../installation.md#1-requirements).

## 1. Install the prebuilt release

Run as a normal user, review the plan, then apply it. First [prepare curl, CA certificates and GitHub CLI](../installation.md#2-prepare-tools). GitHub CLI verifies release signatures.

```bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/latest/download/install.sh | bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/latest/download/install.sh | bash -s -- --apply
```

- No Go, Make or source checkout is required. The script prepares uv/Python.
- Setup also installs missing D-Bus, GNOME Keyring and `secret-tool` dependencies.
- To pin the release, use the [v0.0.4 installation example](../installation.md#download-and-review-a-pinned-release).
- For main source builds, see the [toolchain guide](../development/toolchain.md) and [source installation](../../setup.md).

## 2. Log in again and check the host

```bash
oaiso --version
id -nG
oaiso runtime-check
```

- Log out/in first; group membership must include `codex-dock`.
- Continue only when runtime checks pass with exit code 0.
- Authentication and ordinary startup run without sudo.

## 3. Unlock the keyring

An unlocked desktop keyring can be used directly. For SSH, WSL or a headless VM:

```bash
oaiso auth keyring session
```

- Enter the private passphrase twice on first use, once subsequently.
- Run the remaining steps inside this shell and keep it open.

```bash
oaiso auth keyring check
```

```text
Keyring: ready (store/read/delete verified; no provider request).
```

## 4. Log in to one agent

| Agent and method | Command |
|---|---|
| Codex, API key | `oaiso login --agent codex --method api-key --profile work` |
| Codex, subscription | `oaiso login --agent codex --method subscription --profile work` |
| Claude Code, API key | `oaiso login --agent claude --method api-key --profile work` |
| Claude Code, subscription | `oaiso login --agent claude --method subscription --profile work` |

- API keys use hidden input and a models-list authentication check; inference is separate.
- Subscription login runs the official CLI inside temporary Kata.
- Successful login encrypts storage, selects the profile and prepares the admin secret.
- **Do not additionally run manual `auth profile set/import` or admin-secret creation for this flow.**
- Existing profiles are never overwritten; choose another name when necessary.

```text
Profile saved: work (encrypted, 0600).
Selected profile: work
Settings: /home/user/.config/codex-dock/config.toml
```

## 5. Prepare the image and proxy

Run only the matching agent:

**For Codex**

```bash
oaiso init --agent codex
```

**For Claude Code**

```bash
oaiso init --agent claude
```

- An uncached first run downloads/builds the default worker image and needs network access and time.
- `Ready.` means images and proxy are prepared; it does not confirm a live AI response.
- Proxy-dependent environments also need [BuildKit service and build network configuration](../../kata.md#3-ネットワークとサービスを有効にする).

## 6. Run in your project

```bash
cd /path/to/your-project
```

| Agent | Check startup in the VM (no inference) | Normal use |
|---|---|---|
| Codex | `oaiso codex --version` | `oaiso codex` |
| Claude Code | `oaiso claude --version` | `oaiso claude` |

- The startup check ends with `codex-cli 0.163.0` or `2.1.306 (Claude Code)` and exit code 0.
- Replace the example path. The current directory is shared with the VM.
- General Git/npm/pip traffic is denied by default; [allow required destinations](networking.md).
- [Daily operations](../../operations.md).

## What each check proves

| Check | Meaning |
|---|---|
| `runtime-check` | Execution infrastructure is ready |
| `keyring check` | Secret storage/read-back works |
| Successful `login` | Credentials and initial settings were saved |
| `init` prints `Ready.` | Image and proxy preparation succeeded |
| `codex/claude --version` | The pinned CLI started and exited inside the VM; inference remains untested |
| Actual agent response | The account/model can perform inference |

Automated verification uses invalid credentials. Real login and inference depend on the account, plan, model permissions and network. See [verification and remaining checks](../../testing.md).
