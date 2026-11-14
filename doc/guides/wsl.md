# WSL2で実行ノードを準備する

**日本語** | [English](../en/guides/wsl.md)

Ubuntu 24.04の自動判定とWSL向け事前確認は**未リリース（main）**です。公開版v0.0.8の自動配置はUbuntu 26.04のみ対象です。24.04では対応するソースから[ビルド](../setup.md#ソースからビルドする場合)します。

対象はWindows 11上の**WSL2・Linux x86_64**です。Ubuntuの版だけではKataの実行可否は決まりません。実WSLでの導入・再起動・VPN接続は未検証です。[検証範囲](../testing.md)と、次の条件を確認してください。

| 確認対象 | 必要条件・対処 |
|---|---|
| 仮想化 | Windows/UEFIで仮想化を有効化し、WSL2へnested virtualizationを公開。`/dev/kvm`の存在に加え、rootでVM作成が成功すること |
| WSLカーネル | `kvm_intel`または`kvm_amd`、`vhost_vsock`、`vhost_net`、`tun`、XFS/project quota。Ubuntuのgenericカーネル用モジュールでは代用不可 |
| systemd・cgroup | systemdを有効化し、cgroup v2の`cpu memory pids`を使用 |
| rootfs保存先 | `/var/lib/codex-dock-containerd`に専用XFSを`prjquota`付きでmount。既定のext4や`/mnt/c`は使用不可 |
| プロジェクト | まずWSLの`/home`配下を使用。Windows共有領域は所有者・権限・大文字小文字・監視性能の差に注意 |
| メモリ・容量 | WSLに8 GiB以上を割り当て、Windowsにも余裕を残す。以下の例は32 GiBを事前確保。WSL内とWindows側の両方に空きが必要 |
| 通信 | WSL内から名前解決とHTTPSが成功すること。VPN・Hyper-V firewall・企業proxyの条件も確認 |
| 停止・再起動 | systemdサービスだけでWSLの常駐は保証されない。`wsl --shutdown`・Windows再起動の前にVMを停止し、再開後にmountとruntimeを再確認 |

Microsoftの[WSL設定](https://learn.microsoft.com/windows/wsl/wsl-config)、[systemd](https://learn.microsoft.com/windows/wsl/systemd)、[ファイルシステム](https://learn.microsoft.com/windows/wsl/filesystems)、[通信](https://learn.microsoft.com/windows/wsl/networking)を参照してください。Microsoftの[6.6系](https://github.com/microsoft/WSL2-Linux-Kernel/blob/linux-msft-wsl-6.6.y/arch/x86/configs/config-wsl)・[6.18系](https://github.com/microsoft/WSL2-Linux-Kernel/blob/linux-msft-wsl-6.18.y/arch/x86/configs/config-wsl)の設定にはKVM/vhostとXFS quotaがありますが、手元のカーネル・モジュール・CPUでの利用可否は別に確認します。

## 1. Windows側を確認する

WindowsのPowerShellで実行します。ディストリビューション名は`wsl --list --verbose`の表示に合わせてください。

```powershell
wsl --version
wsl --list --verbose
```

- 対象ディストリビューションの`VERSION`が`2`であることを確認します。Windows on ARMは現在の固定Dragonballプロファイルの対象外です。
- WSLが古い場合は、作業を保存してからWindows側で`wsl --update`を実行します。CPUやWindowsの制約でKVMが使えない場合は、対応する別のLinux/KVMノードを使用します。
- `%UserProfile%\.wslconfig`を編集する場合は、既存設定を保ち、`[wsl2]`の`nestedVirtualization=true`とメモリ上限を確認します。nested virtualizationの既定値はtrueです。WSL全体に適用されるため、他のディストリビューションの使用量も見積もってください。

WSL内の`/etc/wsl.conf`は既存項目を保ち、次を設定します。`[boot]`がある場合はそこへ追加します。

```ini
[boot]
systemd=true
```

設定の反映にはWSLの停止と再起動が必要です。`wsl --shutdown`は**全ディストリビューション**を停止します。稼働中のOaiso VMや他の作業を保存・停止した後だけ、PowerShellで実行します。

```powershell
wsl --shutdown
wsl --distribution Ubuntu-24.04
```

## 2. WSL内の必須機能を確認する

未リリース（main）のsetupは、適用時と次回起動用に`vhost_vsock`・`vhost_net`・`tun`を準備します。以下は導入前の確認・手動復旧用です。WSL内のUbuntuのシェルで実行します。`kmod`がなければ、先に`sudo apt-get install -y kmod`で導入します。

```bash
uname -r
systemctl is-system-running
cat /sys/fs/cgroup/cgroup.controllers
sudo modprobe vhost_vsock
sudo modprobe vhost_net
sudo modprobe tun
ls -l /dev/kvm /dev/vhost-vsock /dev/vhost-net /dev/net/tun
```

- systemdは`running`を確認します。`degraded`の場合は`systemctl --failed`で失敗したunitを調べます。cgroupには`cpu memory pids`が必要です。
- `/dev/kvm`がなければ、CPUに合う方だけを実行します。Intelは`sudo modprobe kvm_intel`、AMDは`sudo modprobe kvm_amd`です。
- 手動のCPUモジュール読込が必要だった場合は、`sudoedit /etc/modules-load.d/oaiso-wsl-kvm.conf`で実際に成功したモジュール名だけを保存します。既存の項目は保ち、IntelとAMDの両方を追加しないでください。再起動後も`/dev/kvm`を確認します。
- `Module ... not found`の場合は`uname -r`と`/lib/modules/$(uname -r)`を照合し、WSLを更新します。カスタムカーネルなら、同じビルドのkernel/modules VHDを`.wslconfig`へ指定します。`mknod`でデバイス名だけを作っても仮想化機能は追加されません。
- setupのroot実行時には[KVM API](https://docs.kernel.org/virt/kvm/api.html)で空のVMを作成し、すぐに解放します。vCPU・guestメモリは確保せず、既存VMには触れません。成功しても、実際のKata起動の確認は別に必要です。

## 3. 新規の専用XFSを準備する

既存ノードの移行は[保管・復元手順](storage-migration.md)を先に実施します。以下は**新規導入だけ**の例です。WSLのLinuxファイルシステム内に専用の32 GiBファイルを作り、loopデバイス経由でXFSとして使います。既存の`ext4.vhdx`やWindowsのディスクをformatしません。

WSL内で空きを確認します。Windows側でも、ディストリビューションのVHDが置かれたドライブに32 GiB以上と運用上の余裕があることを確認してください。

```bash
df -h /var/lib
sudo apt-get install -y xfsprogs util-linux
```

次のブロックは、保存用ディレクトリが存在する場合、対象ディレクトリが空でない場合、既にmount済みの場合に停止します。途中失敗時は、作成したファイルと空き容量を確認し、既存データを消して再実行しないでください。

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

`sudoedit /etc/fstab`で既存行を保ち、次の行を一度だけ追加します。`[automount] mountFsTab=false`を設定している場合は、WSL起動時にもこのmountが準備されるよう運用を調整してください。

```fstab
/var/lib/oaiso-wsl/rootfs.xfs /var/lib/codex-dock-containerd xfs loop,prjquota,nofail 0 0
```

WSL内でmountし、`FSTYPE=xfs`と`prjquota`または`pquota`を確認します。

```bash
sudo mount /var/lib/codex-dock-containerd
findmnt --mountpoint /var/lib/codex-dock-containerd -o TARGET,SOURCE,FSTYPE,OPTIONS
```

containerdのunitは`RequiresMountsFor`でこのmountを待ちます。XFSやquotaの確認に失敗する場合は配置へ進まず、mountを修正します。setupはファイル作成・format・fstab編集を自動実行しません。

## 4. 配置と実動作を確認する

[ソースからの配置](../setup.md#ソースからビルドする場合)または対応版のバンドルで、計画を確認して適用します。WSL向け事前確認では、計画時にも専用XFSが必要です。通常ユーザーの計画ではrootでのKVM検査を保留した旨を表示し、rootの適用前に検査します。

配置後はWSL内の通常ユーザーで実行します。

```bash
oaiso runtime-check
```

すべて`ok`になった後、[初回VM起動](first-run.md)を確認します。WSLを再起動した後も、XFSのmountと`oaiso runtime-check`を再確認してください。detachedワーカーの実行中もWSLのセッションを維持し、Windowsの休止・再起動による中断に備えて成果物を保存します。

## 通信で失敗する場合

- まずWSL自身のDNS・HTTPS・企業proxy/CAを確認します。NATとmirroredのどちらかをOaisoが自動選択することはありません。
- WSL自身で名前解決できても、Proxyの別network namespaceから同じDNSへ届くとは限りません。Proxyだけが名前解決に失敗する場合は、管理者が`oaiso node serve --help`の`--proxy-dns`で到達可能な非loopback DNSを指定します。企業DNSの代わりに公開DNSへ自動切替することはありません。ノード設定を変更する前に作業を保存し、既存unitのmanifest・他の引数と更新後の設定も照合してください。
- `ip route show table all`でVPNの経路を確認します。Oaisoは既存経路と重ならない`10.253.x.0/24`を選びます。全範囲が重なる場合は、ネットワーク管理者と経路を調整してください。
- Windows/Hyper-V firewallとLinuxのforwarding規則を確認します。一括無効化やfirewall全消去で解決しないでください。
- 認証Proxyの実資格情報はホスト側だけに置きます。Windowsのproxyへ接続するために、ワーカーのprivate-address制限を全解除する必要はありません。[通信設定](../proxy-policy.md)を使用します。
