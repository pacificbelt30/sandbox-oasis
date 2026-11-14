# Prepare a WSL2 execution node

[日本語](../../guides/wsl.md) | **English**

Ubuntu 24.04 detection and WSL preflight checks are **unreleased (main)**. The published v0.0.8 automatic installer supports only Ubuntu 26.04. For 24.04, [build the supporting source](../../setup.md#ソースからビルドする場合) (Japanese).

This guide targets **WSL2 on Linux x86_64 under Windows 11**. The Ubuntu version alone does not establish Kata compatibility. Installation, restart and VPN access on real WSL remain unverified; see the [verification scope](../../testing.md) (Japanese).

| Check | Requirement or recovery |
|---|---|
| Virtualization | Enable Windows/UEFI virtualization and expose nested virtualization to WSL2. Root must create a KVM VM successfully; a device file alone is insufficient |
| WSL kernel | `kvm_intel` or `kvm_amd`, `vhost_vsock`, `vhost_net`, `tun`, and XFS quotas. Ubuntu generic-kernel modules cannot replace matching WSL modules |
| systemd and cgroups | Enable systemd and cgroup v2 with `cpu memory pids` |
| Rootfs storage | Dedicated XFS mounted with `prjquota` at `/var/lib/codex-dock-containerd`. Default ext4 and `/mnt/c` cannot serve this role |
| Projects | Start with WSL's `/home`. Windows shares differ in ownership, permissions, case handling and file-watching performance |
| Memory and space | Allocate at least 8 GiB to WSL, leaving Windows sufficient memory. This example reserves 32 GiB; check free space inside WSL and on the Windows drive holding its VHD |
| Networking | Verify DNS and HTTPS from WSL, including VPN, Hyper-V firewall and corporate proxy requirements |
| Lifecycle | systemd services alone do not guarantee WSL stays alive. Stop VMs before `wsl --shutdown` or Windows restart; recheck mounts and runtime afterwards |

See Microsoft's [WSL configuration](https://learn.microsoft.com/windows/wsl/wsl-config), [systemd](https://learn.microsoft.com/windows/wsl/systemd), [filesystems](https://learn.microsoft.com/windows/wsl/filesystems) and [networking](https://learn.microsoft.com/windows/wsl/networking) documentation. Microsoft's [6.6](https://github.com/microsoft/WSL2-Linux-Kernel/blob/linux-msft-wsl-6.6.y/arch/x86/configs/config-wsl) and [6.18](https://github.com/microsoft/WSL2-Linux-Kernel/blob/linux-msft-wsl-6.18.y/arch/x86/configs/config-wsl) configurations include KVM/vhost and XFS quotas; check the actual installed kernel, modules and CPU separately.

## 1. Check Windows settings

Run in Windows PowerShell. Use the distribution name shown by `wsl --list --verbose`.

```powershell
wsl --version
wsl --list --verbose
```

- The target distribution must show `VERSION` `2`. Windows on ARM is outside the current pinned Dragonball profile.
- If WSL is old, save work before running `wsl --update` on Windows. If the CPU or Windows configuration cannot provide KVM, use another supported Linux/KVM node.
- Preserve existing `%UserProfile%\.wslconfig` settings. Check `nestedVirtualization=true` and the memory limit under `[wsl2]`; nested virtualization defaults to true. These settings apply across WSL distributions, so account for their combined use.

Preserve existing entries in WSL's `/etc/wsl.conf` and add the following, using the existing `[boot]` section if present.

```ini
[boot]
systemd=true
```

Stop and restart WSL to apply changes. `wsl --shutdown` stops **all distributions**. Save work and stop Oaiso VMs and other workloads first, then run in PowerShell:

```powershell
wsl --shutdown
wsl --distribution Ubuntu-24.04
```

## 2. Check Linux prerequisites

Unreleased (main) setup prepares `vhost_vsock`, `vhost_net` and `tun` during application and for subsequent boots. The commands below are pre-installation checks and manual recovery steps. Run inside Ubuntu on WSL. If `kmod` is missing, install it first with `sudo apt-get install -y kmod`.

```bash
uname -r
systemctl is-system-running
cat /sys/fs/cgroup/cgroup.controllers
sudo modprobe vhost_vsock
sudo modprobe vhost_net
sudo modprobe tun
ls -l /dev/kvm /dev/vhost-vsock /dev/vhost-net /dev/net/tun
```

- Confirm systemd is `running`. For `degraded`, inspect `systemctl --failed`. The cgroup controllers must include `cpu memory pids`.
- If `/dev/kvm` is missing, load only the CPU-specific module: `sudo modprobe kvm_intel` for Intel or `sudo modprobe kvm_amd` for AMD.
- If manual CPU-module loading was necessary, use `sudoedit /etc/modules-load.d/oaiso-wsl-kvm.conf` to save only the module name that worked. Preserve existing entries and select the matching CPU module rather than adding both. Recheck `/dev/kvm` after restarting.
- For `Module ... not found`, compare `uname -r` with `/lib/modules/$(uname -r)` and update WSL. A custom kernel needs its matching kernel/modules VHD configured in `.wslconfig`. Creating device names with `mknod` does not add virtualization support.
- Root setup uses the [KVM API](https://docs.kernel.org/virt/kvm/api.html) to create and immediately release an empty VM. It allocates no vCPU or guest memory and does not touch existing VMs. An actual Kata boot remains a separate check.

## 3. Prepare new dedicated XFS storage

For existing nodes, follow [backup and migration](storage-migration.md) first. This example is for **new installations only**. It creates a dedicated 32 GiB file inside WSL's Linux filesystem and uses it as XFS through a loop device. It does not format the existing `ext4.vhdx` or Windows disks.

Check space inside WSL. Also check that the Windows drive holding the distribution's VHD has at least 32 GiB plus operating headroom.

```bash
df -h /var/lib
sudo apt-get install -y xfsprogs util-linux
```

The following block stops if the storage directory exists, the target is nonempty, or the target is already mounted. After a partial failure, inspect the created file and free space; do not delete existing data to force a retry.

```bash
sudo bash -euo pipefail <<'SH'
image=/var/lib/oaiso-wsl/rootfs.xfs
target=/var/lib/codex-dock-containerd
test ! -e /var/lib/oaiso-wsl
test ! -L /var/lib/oaiso-wsl
install -d -o root -g root -m 0700 /var/lib/oaiso-wsl
test ! -e "$image"
test ! -L "$image"
test ! -L "$target"
mkdir -p "$target"
if mountpoint -q "$target" || test -n "$(find "$target" -mindepth 1 -print -quit)"; then
    echo 'Storage target is mounted or not empty; preserve it and use the migration guide.' >&2
    exit 1
fi
(umask 077; set -C; : > "$image")
fallocate -l 32G "$image"
mkfs.xfs "$image"
SH
```

Use `sudoedit /etc/fstab`, preserve existing entries, and add this line once. If `[automount] mountFsTab=false` is configured, arrange for this mount to be ready at WSL startup as well.

```fstab
/var/lib/oaiso-wsl/rootfs.xfs /var/lib/codex-dock-containerd xfs loop,prjquota,nofail 0 0
```

Mount from WSL, then confirm `FSTYPE=xfs` and `prjquota` or `pquota`.

```bash
sudo mount /var/lib/codex-dock-containerd
findmnt --mountpoint /var/lib/codex-dock-containerd -o TARGET,SOURCE,FSTYPE,OPTIONS
```

The containerd unit waits for this mount through `RequiresMountsFor`. Correct mount or quota failures before installation. Setup does not create or format the file or edit fstab automatically.

## 4. Install and verify operation

Use the [source installation](../../setup.md#ソースからビルドする場合) (Japanese) or a supporting bundle, review the plan, then apply. WSL preflight requires dedicated XFS even in plan mode. An unprivileged plan reports that root KVM probing is deferred; the root invocation checks it before applying.

After installation, run as the normal WSL user:

```bash
oaiso runtime-check
```

Once all checks show `ok`, verify [first VM startup](first-run.md). Recheck XFS mounting and `oaiso runtime-check` after restarting WSL. Keep a WSL session active while detached workers run, and save outputs against interruptions from Windows sleep or restart.

## Troubleshoot networking

- First check DNS, HTTPS and corporate proxy/CA configuration in WSL. Oaiso does not automatically select NAT or mirrored mode.
- Successful DNS resolution in WSL does not establish DNS reachability from a proxy's separate network namespace. If only proxies fail DNS, an administrator can select reachable non-loopback servers with the node's `--proxy-dns` option; see `oaiso node serve --help`. Oaiso does not automatically substitute public DNS for corporate DNS. Save work before changing node configuration, and preserve the installed unit's manifest and other arguments, including after updates.
- Inspect VPN routes with `ip route show table all`. Oaiso selects a `10.253.x.0/24` subnet without existing route overlap. If the entire range overlaps, coordinate routing with the network administrator.
- Check Windows/Hyper-V firewall and Linux forwarding rules while preserving firewall protection and unrelated rules.
- Real credentials stay in the host auth proxy. Access to a Windows proxy does not require removing all worker private-address restrictions. Use the [network policy](../../proxy-policy.md) (Japanese).
