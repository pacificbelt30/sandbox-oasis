# Installation

[日本語](../installation.md) | **English**

| Current state | Next step |
|---|---|
| New Ubuntu 26.04 host | Follow steps 1–4 below |
| Execution host already prepared | [Choose credentials and start a worker](getting-started.md#2-prepare-credentials) |
| Updating an installed node | [Update procedure](../setup.md#更新する) (Japanese) |
| Other Linux distributions | [Manual installation](../kata.md) (Japanese) |

## Installed version

- v0.0.3 includes `login`, keyring and age storage in the prebuilt distribution.
- For the complete flow, follow [installation through first startup](guides/first-run.md).
- The `releases/latest` commands below install the published release.

## 1. Requirements

| Item | Requirement |
|---|---|
| OS and CPU | Ubuntu 26.04, Linux x86_64 |
| Host features | systemd, KVM (`/dev/kvm`), cgroup v2 with `cpu memory pids` |
| Account | A normal user with `sudo` access |
| Prerequisites | Bash, curl, CA certificates and GitHub CLI for signature verification; no Go, Make or Git required |
| Host Python | uv-managed Python 3.12; missing uv/Python is prepared in user-owned directories |
| Memory guidance | At least 8 GiB for the default two-worker configuration; allow extra capacity for builds |
| Network | Access to GitHub Releases, uv/Python, pinned runtime archives, OS packages and container images |
| WSL or a VM host | All requirements above, plus nested virtualization |

- Operator group membership grants root-equivalent host management access. Choose trusted users only.

## 2. Prepare tools

**Run as a normal user to install curl, CA certificates and GitHub CLI for signature verification.**

```bash
bash <<'SH'
set -euo pipefail
sudo apt-get update
sudo apt-get install -y curl ca-certificates
sudo install -d -m 0755 /etc/apt/keyrings
oaiso_gh_key=$(mktemp)
trap 'rm -f -- "$oaiso_gh_key"' EXIT
curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o "$oaiso_gh_key"
sudo install -m 0644 "$oaiso_gh_key" /etc/apt/keyrings/githubcli-archive-keyring.gpg
printf 'deb [arch=%s signed-by=/etc/apt/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main\n' "$(dpkg --print-architecture)" | sudo tee /etc/apt/sources.list.d/github-cli.list >/dev/null
sudo apt-get update
sudo apt-get install -y gh
gh attestation verify --help >/dev/null
SH
```

- Uses the [official GitHub CLI APT repository](https://github.com/cli/cli/blob/trunk/docs/install_linux.md#debian). Skip this block if compatible tools are already installed.
- Success: the final command exits with code 0. `gh auth login` is not required.

- The installer uses [Astral’s official installer](https://docs.astral.sh/uv/getting-started/installation/) to prepare uv in `~/.local/bin` if needed.
- Host Python is [uv-managed 3.12](https://docs.astral.sh/uv/guides/install-python/); the OS Python is retained.
- Check KVM with `test -c /dev/kvm` and controllers with `cat /sys/fs/cgroup/cgroup.controllers`.

## 3. Review the plan and install

**Run from any directory; no source checkout is required.**

```bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/latest/download/install.sh | bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/latest/download/install.sh | bash -s -- --apply
```

**Example plan output (excerpt)**

```text
Plan: verified Ubuntu 26.04 amd64 node; Kata 4.2.0 / Dragonball / containerd 2.4.2 / BuildKit 0.34.0 / runc 1.4.3
Bundle: /tmp/oaiso-release-<id>/bundle (version v0.0.3); operator: user; cache: /var/cache/codex-dock-setup; offline: no
```

- Versions, paths and account names are examples and vary by installation and release.
- Plan only ends with `Plan only. Re-run the installer with --apply to install this release.`
- Successful application ends with `Node installed. Log in again, then run oaiso init.` Log in again before checking below.
- Planning can download uv, Python and release assets. Host services are installed only with `--apply`.

| Command or option | Behavior |
|---|---|
| No options | Download and verify the matching release binaries, then display the plan |
| `--version v0.0.3` | Download a specific release |
| `--apply` | Apply through `sudo` after the plan succeeds, forwarding the selected Python’s absolute path |
| No operator option | Register the invoking normal user |
| `--operator USER` | Register another existing normal user; required when invoking the script as root |

| Installed component | Main location or name |
|---|---|
| CLI | `/usr/local/bin/oaiso` |
| Kata, containerd, BuildKit and runc | Managed assets under `/opt/codex-dock/releases/` |
| Services | `codex-dock-node`, `codex-dock-containerd`, `codex-dock-buildkit` |
| Operator group | `codex-dock` |
| Bash, Zsh and Fish completion | Under `/usr/local/share/` |

- Existing Docker and default containerd packages are retained.
- Network setup, verification and recovery: [setup details](../setup.md) (Japanese).

### Download and review a pinned release

**Example for v0.0.3. Inspect the saved script, then use the same file for planning and installation.**

```bash
curl -fsSL -o install.sh https://github.com/pacificbelt30/sandbox-oasis/releases/download/v0.0.3/install.sh
less install.sh
bash install.sh --version v0.0.3
bash install.sh --version v0.0.3 --apply
```

## 4. Log in again and verify

1. Log out, then log in again as the registered normal user.
2. Run:

```bash
command -v oaiso
oaiso --version
id -nG
oaiso runtime-check
```

**Example CLI and group output**

```text
$ command -v oaiso
/usr/local/bin/oaiso
$ oaiso --version
oaiso version <version>
$ id -nG
user sudo codex-dock
```

- `$` marks an input line; copy executable commands from the `bash` block above.
- Versions, account names and other groups vary. Check for membership in `codex-dock`.
- `runtime-check`: see [all successful checks and their meaning](getting-started.md#1-check-the-host).

| Check | Expected result |
|---|---|
| CLI | Command path and version are displayed |
| Group | `id -nG` includes `codex-dock` |
| Runtime | `runtime-check` succeeds |

## Next: credentials and first worker

1. [Unlock the keyring and log in](guides/first-run.md#3-unlock-the-keyring). For existing settings, see [Authentication](guides/authentication.md).
2. Run `oaiso init`; use `oaiso init --agent claude` for Claude Code.
3. From your project, run `oaiso codex` or `oaiso claude`.

- Normal `init` and `run` operations do not require `sudo`.
- Real API keys and OAuth tokens stay in the Auth Proxy; work VMs receive short-lived placeholders.
- General egress defaults to denial. [Allow destinations](../proxy-policy.md) (Japanese) when Git or package downloads are needed.

## What `--bundle` means

- A directory containing the prebuilt `oaiso` CLI, worker guard, I/O logger and `bundle.json` (version and SHA-256 checksums).
- The release installer downloads and extracts it into a temporary directory; normally you do not specify the option. `make node-bundle` creates `dist/node-bundle/` only when building from source.
- Use `--bundle PATH` to use an extracted release bundle or bring one from another host. The destination host needs uv/Python, but not Go/Make.
- It excludes Kata archives, container images, projects and credentials. Offline installation also needs prepared archives and Python.

- Use a saved `install.sh` and a verified extracted bundle. See [download and bundle examples](../setup.md#ビルド済みバンドルとは) (Japanese), including offline prerequisites.

## Other installation paths

| Method | Scope and reference |
|---|---|
| Build from source | [Developer build procedure](../setup.md#ソースからビルドする場合) (Japanese); requires Go, Make, Git and uv |
| Manual host installation | [Kata host setup](../kata.md) (Japanese) |
| Prebuilt CLI/helper directory | [Bundle contents](../setup.md#ビルド済みバンドルとは) (Japanese); `--bundle PATH` skips Go/Make on this host |
| Offline installation | [Offline setup](../setup.md#オフライン配置) (Japanese) |
| Build and install CLI, helpers and completion | `make build` / `sudo make install`; excludes Kata and host services |
| Updates and rollback | [Update and recovery](../setup.md#更新する) (Japanese) |

## Official tool installation instructions

- Prebuilt installations **do not require Go, Make or Git**. Prepare [development tools](development/toolchain.md) only for source builds.
- Use the installer above to deploy Oaiso with its pinned runtime versions and settings.

| Tool | Installation or purpose | Official instructions or downloads |
|---|---|---|
| Bash | Host shell; use Ubuntu's supplied shell | [GNU Bash installation](https://www.gnu.org/software/bash/manual/html_node/Installing-Bash.html) |
| curl | Download the installer and release assets | [Official download guide](https://curl.se/download.html) |
| GitHub CLI | Verify signatures and provenance | [Official Linux installation](https://github.com/cli/cli/blob/trunk/docs/install_linux.md) |
| uv | Prepared automatically when missing | [uv installation](https://docs.astral.sh/uv/getting-started/installation/) |
| Python | Managed 3.12, prepared through uv for setup and updates | [Install Python with uv](https://docs.astral.sh/uv/guides/install-python/) |
| Kata Containers | Worker VM isolation; deployed by the installer | [Installation guides](https://github.com/kata-containers/kata-containers/blob/main/docs/install/README.md) |
| containerd | VM/container management; deployed by the installer | [Getting started](https://github.com/containerd/containerd/blob/main/docs/getting-started.md) |
| BuildKit | Worker/proxy image builds; deployed by the installer | [Quick start](https://github.com/moby/buildkit#quick-start) |
| runc | Proxy containers; deployed by the installer | [Releases](https://github.com/opencontainers/runc#releases) |

- Go is needed in environments that build Oaiso or a product embedding its Go API. See the [official Go Linux procedure](https://go.dev/doc/install) and our [command examples](development/toolchain.md#2-install-go-using-the-official-linux-procedure).

## Signed distributions (Unreleased main)

- [Signatures, provenance, SBOM and rollback protection](guides/signed-releases.md). The next signed distribution also requires GitHub CLI; published v0.0.3 is unchanged.
