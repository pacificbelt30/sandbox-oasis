# Kata実行ホストの準備

- ワーカーは **containerd → Kata runtime-rs → Dragonball** で起動する。
- CLIはroot管理サービスのUnixソケットを使い、管理サービスが公式containerd Go SDKで専用namespace `codex-dock` を操作する。
- 既存のCLI/TUI、認証プロキシ、イメージ・テンプレート、worktreeの操作は維持する。

```text
Oaiso CLI / TUI
  └─ /run/codex-dock-node.sock (root:codex-dock, 0660)
       ├─ containerd (専用socket / namespace / snapshot)
       │    ├─ Kata runtime-rs + 組み込みDragonball → ワーカーVM
       │    └─ runc → auth / egress プロキシ
       ├─ root所有のVM予約台帳・資源検証
       └─ 専用bridge / network namespace / nftables
buildctl → 独立BuildKit → OCI archive → 同じcontainerd namespace
```

- Goの互換DTOとHTTPクライアントにはDockerライブラリを利用するが、接続先は本製品の管理サービスである。
- `DOCKER_HOST`・Docker context・Docker daemonの探索や自動fallbackはしない。
- 接続先を変える場合は `CODEX_DOCK_KATA_NODE_SOCKET` を指定する。

- Ubuntu 24.04／26.04（24.04は未リリース（main））で新しく配置する場合は、[自動セットアップ](setup.md)も使えます。
- 以下は手動配置の手順です。

- v0.0.7では、[専用XFSとproject quota](guides/resource-limits.md)も必要です。

## 必要なホスト

- Linux x86_64、KVM、cgroup v2、`cpu memory pids` controller、pidfdと`cgroup.kill`、iproute2・nftables・runc、containerd 2.xの設定v3が必要。
- v0.0.7の固定配布物はKata 4.3.0 / containerd 2.4.2 / BuildKit 0.34.0。
- KataのDragonball用kernelには公式配布物上でexperimentalの名称が付く。
- 更新時は資産を検証して受入試験を再実行する。

- 既定のstandardではワーカー1台につきguestのCPU 1・メモリ2 GiB・PID 512、ホスト側のVM全体にCPU 1・メモリ3 GiB・swap 0・PID 512を設定する。
- 既定の同時予約は2台。
- ホストにはVM合計とcontainerd・BuildKit・プロキシ用の余裕が必要で、2台構成は8 GiB以上を目安にする。
- `node serve --max-workers` は1〜64。

- 管理サービスとBuildKitの利用者は、ホストmountとイメージの選択ができる信頼済み管理者であり、root相当の権限を持つ。
- 専用グループ `codex-dock` を使う。
- 通常CLIへroot/CAP_SYS_PTRACEを付与せず、管理socket・状態・ランタイム資産をguestへ渡さない。

## 1. 配布物を検証して準備する

- まずリポジトリを取得します。
- 以降の配置例は、このルートから実行してください。

```bash
git clone https://github.com/pacificbelt30/sandbox-oasis.git
cd sandbox-oasis
```

- 公式リリースの配布物とSHA256を確認し、新しい作業ディレクトリへ展開する。
- Go・Make・Git・uv・Cコンパイラー：[開発用ツールの準備と公式手順](development/toolchain.md)。
- 追加ツール：zstd（[公式配布](https://github.com/facebook/zstd/releases)）、debugfsを含むe2fsprogs（[公式配布・導入案内](https://e2fsprogs.sourceforge.net/)）。
- 実行基盤の公式手順：[Kata・containerd・BuildKit・runc](installation.md#ツールの公式手順)。
- ホストPythonは[インストール手順](installation.md#2-ツールを準備する)と同じuv管理の3.12を使う。

```bash
curl -fL -o kata-static-4.3.0-amd64.tar.zst \
  https://github.com/kata-containers/kata-containers/releases/download/4.3.0/kata-static-4.3.0-amd64.tar.zst
echo '79a77ff015c887da3067110c664c1f9f33fb38a3630dbe88daf4098a63d054d0  kata-static-4.3.0-amd64.tar.zst' | sha256sum -c -
mkdir kata-staging
tar --zstd -xf kata-static-4.3.0-amd64.tar.zst -C kata-staging \
  ./opt/kata/runtime-rs/bin/containerd-shim-kata-v2 \
  ./opt/kata/share/kata-containers/vmlinux-6.18.56-208-dragonball-experimental \
  ./opt/kata/share/kata-containers/kata-ubuntu-resolute.image \
  ./opt/kata/share/defaults/kata-containers/runtime-rs/configuration-dragonball.toml
gcc -static -Os -Wall -Wextra -Werror deploy/kata/pids_hook.c -o pids-hook
uv python install 3.12
host_python=$(uv python find --no-project --no-config --managed-python --system --no-python-downloads 3.12)
"$host_python" deploy/kata/prepare_guest.py build \
  --source-image kata-staging/opt/kata/share/kata-containers/kata-ubuntu-resolute.image \
  --hook-binary pids-hook --output guest-assets
"$host_python" deploy/kata/prepare_guest.py verify guest-assets/assets.lock.json
"$host_python" deploy/kata/configure.py \
  --source kata-staging/opt/kata/share/defaults/kata-containers/runtime-rs/configuration-dragonball.toml \
  --output kata.toml
make build
```

- `prepare_guest.py` は固定した元imageのSHA256と構造を確認し、新しいコピーへPID hookを入れる。
- 元imageは書き換えない。
- `configure.py` はguest seccomp、annotation無効化、hook、sandbox cgroup、静的資源割当を明示する。
- guest VMはworkload 2 GiB + overhead 512 MiBで起動する。
- 共有ファイルの `virtio_fs_cache` は `none` に固定し、`virtio_fs_extra_args` は `open,no_writeback_cache` を指定する。
- `auto` / `always` ではホストの上書きをVMが古い内容として読むため、現行ノードはその構成を拒否する。
- 既存環境はワーカーを停止・削除し、設定の再生成とmanifestの再作成を行う。
- CLIは管理サービスが共有cacheの要件を検証できるかも確認するため、nodeも同じ版へ更新する。

## 2. ホストへ配置する

- 配置先が既存の場合は、[固定資産の更新](#固定資産の更新)を参照してください。
- 以下は新規配置の例です。
- `make build` にはGo 1.26.0以上とMakeが必要です。

```bash
sudo groupadd --system codex-dock
sudo usermod -aG codex-dock "$USER"
sudo cp -a kata-staging/opt/kata /opt/kata
sudo cp -a guest-assets /opt/codex-dock-kata
sudo chown -R root:root /opt/kata /opt/codex-dock-kata
sudo chmod -R go-w /opt/kata /opt/codex-dock-kata
sudo install -d -m 0755 /etc/codex-dock /opt/codex-dock /usr/local/libexec/codex-dock
sudo install -m 0755 oaiso codex-dock-io-logger /usr/local/bin/
sudo install -m 0755 codex-dock-worker-guard /opt/codex-dock/worker-guard
sudo install -m 0755 deploy/kata/prepare-node.sh /usr/local/libexec/codex-dock/
sudo install -m 0644 kata.toml /etc/codex-dock/kata.toml
sudo install -m 0644 deploy/kata/containerd.toml deploy/kata/buildkit.toml /etc/codex-dock/
sudo install -m 0644 deploy/kata/codex-dock-containerd.service \
  deploy/kata/codex-dock-node.service deploy/kata/codex-dock-buildkit.service /etc/systemd/system/
```

- containerdとruncは公式配布物または対応ディストリビューションから配置する。
- サービス例のcontainerdパスは `/usr/bin/containerd`。
- BuildKitの `buildctl` と `buildkitd` も公式配布物を検証して `/usr/local/bin` へ配置する。
- BuildKit 0.34.0のlinux-amd64 tar.gzのSHA256は `14813b1fb2a40082114e6d386c6fc5e7dd5298176ce7ee1a18bc33e73e109ac3`。
- `buildkit.toml` の `grpc.gid` を `getent group codex-dock` の数値GIDへ設定する。

## 3. ネットワークとサービスを有効にする

- プロキシのegress NATにはホストのIPv4 forwardingが必要。
- 既存firewallのforward方針も管理者が確認する。
- 本製品は自身のnftables tableだけを作り、他サービスのtableを削除・flushしない。
- ネットワークは10.253.0.0/16から/24を予約し、既存アドレスとの重複を避ける。
- VPNなどの経路と競合するホストでは割当を確認する。

```bash
sudo sysctl -w net.ipv4.ip_forward=1
sudo systemctl daemon-reload
sudo systemctl enable --now codex-dock-containerd
sudo /usr/local/bin/oaiso runtime-lock
sudo systemctl enable --now codex-dock-node codex-dock-buildkit
# 新しいグループが反映されたログインセッションで実行する。
oaiso runtime-check
```

- `runtime-lock` は既存manifestを上書きせず、Kata config・shim・kernel・guest image・bootstrap・IO logger・containerd本体と設定・ip/nft/sysctl/runcを固定する。
- 署名の代わりではないため、公式配布物の検証とroot所有・書き換え不可の配置を先に行う。
- 更新はworkerのdrain、サービス停止、資産検証、新manifestの順で行う。

- プロキシnamespaceから使えるDNSが必要。
- systemd-resolvedの上流設定があれば利用し、それ以外はhost resolv.confを参照する。
- hostのloopback専用resolverは別namespaceから使えないため、`node serve --proxy-dns ADDRESS` で到達可能な上流を指定する。
- workerは静的hostsと127.0.0.1のDNSを使い、外部名解決はegressプロキシに任せる。

- ビルド用のHTTP_PROXY / HTTPS_PROXY / NO_PROXY等はBuildKitのbuild argsへ渡す。
- `CODEX_PROXY_CERT` がある場合は `proxy_ca` secretとして渡し、組み込みDockerfileはそのRUN中だけ信頼する。
- CAをimageへコピーせずTLS検証を維持する。
- BuildKit daemonのregistry通信にも必要なproxy/CA設定をサービス側で用意する。

## 配置後の操作と保存先

認証を用意し、[クイックスタート](getting-started.md)で最初のワーカーを起動してください。

- 通常の起動・停止・再起動・更新は[日常の運用](operations.md)を参照してください。
- 認証ファイルの権限と短命トークンの検証は[認証プロキシ](auth-proxy.md)、イメージは[build](commands/build.md)で説明します。

- 管理サービスの状態は `/var/lib/codex-dock-node`、VM予約は `allocations`、実行仕様・network・ログは `engine` 配下。
- containerdは `/var/lib/codex-dock-containerd`、runtime stateは `/run/codex-dock-containerd`。
- 実行時のguest kernel・host PID・cgroupは[VM管理](vm.md)で照合する。
- 管理サービス再起動でVMを再作成しない。
- ログプロセスはshimが所有し、stdinの接続維持も担当する。

## Dragonball 4.2 の回収対応

- 組み込みVMMのshim自身がsandbox cgroupに入るため、終了時に自身のcgroupを削除するとEBUSYになる。
- guestのユーザーコードを開始する前に、固定したshimとログプロセスのinode・worker ID・所属・資源上限を確認し、同じ資源上限内の `supervisor` leafへ移す。
- 管理サービスの再起動による再接続時と、停止の終了待ちを始める前にも確認する。Kataは終了通知を返す前にcgroupを削除することがあるため、task終了後の移動だけでは遅い。
- 資源上限の外へは移さない。

- network namespaceを保持したまま再起動すると、残ったtcfilter設定と衝突する場合がある。
- そのため停止済みworker側のnamespace/vethだけを所有確認して再生成し、同じbridge・IP・proxy接続を維持する。
- 複数workerで使うproxy側namespaceは保持する。

## 固定資産の更新

1. 作業中ワーカーの成果を保存し、旧版で停止・削除する。
2. ノードとBuildKit、専用containerdを停止する。
3. 新しい配布物を別の作業ディレクトリで検証する。

    - shim・kernel・guest image・guard・IO logger・設定を対応する版へそろえる。

4. root所有・一般利用者が書き換えできない状態で配置する。
5. 新しいmanifestを作成し、管理サービスがそのmanifestを参照するようにする。

    - 既存manifestはruntime-lockで上書きできないため、別パスで作成して配置する。

6. 専用containerd、ノード、BuildKitを起動し、runtime-checkと受入試験を実行する。

- manifestは配布元の署名やchecksum検証の代わりにはなりません。
- CLI・プロキシだけの更新は[運用手順](operations.md#cliプロキシを更新する)、旧Docker環境からは[移行手順](migration.md)を参照してください。

## 動作確認

配置後は通常利用者で次を実行します。

```bash
oaiso runtime-check
make kata-e2e
```

確認項目と実機・実AIの検証結果は[テストと動作確認](testing.md)にまとめています。
