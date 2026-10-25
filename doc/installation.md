# Installation — インストール

**日本語** | [English](en/installation.md)

| 今の状態 | 手順 |
|---|---|
| 新しいUbuntu 26.04ホスト | このページの1〜4 |
| 実行ホストは準備済み | [認証・初回起動](getting-started.md#2-認証方式を選ぶ) |
| インストール済み環境を更新 | [更新手順](setup.md#更新する) |
| その他のLinux | [手動配置](kata.md) |

## インストールする版

- この手順では署名付きのビルド済み配布版（v0.0.4）を導入します。
- 一続きの操作例：[インストールから初回起動まで](guides/first-run.md)。
- このページの`releases/latest`は公開版を導入する手順です。

## 1. 前提条件

| 項目 | 必要条件 |
|---|---|
| OS・CPU | Ubuntu 26.04・Linux x86_64 |
| ホスト機能 | systemd・KVM（`/dev/kvm`）・cgroup v2の `cpu memory pids` |
| 実行アカウント | `sudo` が使える通常ユーザー |
| 事前ツール | Bash・curl・CA証明書・GitHub CLI（署名検証用）。Go・Make・Gitは不要 |
| ホストPython | uv管理の3.12。uv・Pythonがなければユーザー用領域へ自動準備 |
| メモリ目安 | 既定の2ワーカー構成は8 GiB以上。ビルド用の余裕も確保 |
| 通信 | GitHub Releases・uv/Python・固定ランタイム配布物・OSパッケージ・コンテナイメージの取得先に到達可能 |
| WSL／VM | 上記要件に加え、nested virtualizationが必要 |

- operator groupにはホストのroot相当の管理権限を付与。信頼する利用者だけを指定。

## 2. ツールを準備する

**通常ユーザーのシェルで実行。curl・CA証明書と、署名検証用のGitHub CLIを準備します。**

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

- [GitHub CLI公式のAPT導入手順](https://github.com/cli/cli/blob/trunk/docs/install_linux.md#debian)に沿って公式配布元を登録します。既に対応版が使える場合は省略できます。
- 成功の目印：最後のコマンドが終了コード0。`gh auth login`は不要です。

- uv本体：[Astral公式インストーラー](https://docs.astral.sh/uv/getting-started/installation/)で `~/.local/bin` へ準備。既に使えるuvがあれば利用します。
- Python：[uv管理の3.12](https://docs.astral.sh/uv/guides/install-python/)を使用。OSのPythonは変更しません。
- `test -c /dev/kvm` でKVM、`cat /sys/fs/cgroup/cgroup.controllers` で `cpu memory pids` を確認。
- KVMがない場合：[ホスト要件](kata.md#必要なホスト)を満たしてから続行。

## 3. 計画を確認してインストール

**任意のディレクトリで実行。リポジトリのcloneは不要です。**

```bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/latest/download/install.sh | bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/latest/download/install.sh | bash -s -- --apply
```

**計画の出力例（抜粋）**

```text
Plan: verified Ubuntu 26.04 amd64 node; Kata 4.2.0 / Dragonball / containerd 2.4.2 / BuildKit 0.34.0 / runc 1.4.3
Bundle: /tmp/oaiso-release-<id>/bundle (version v0.0.4); operator: user; cache: /var/cache/codex-dock-setup; offline: no
```

- 版・パス・利用者名は例。実際の環境とリリースにより変わります。
- 計画だけの実行：最後に `Plan only. Re-run the installer with --apply to install this release.` と表示。
- 適用成功：最後に `Node installed. Log in again, then run oaiso init.` と表示。再ログイン後、次の手順で確認。
- 計画表示まででもuv・Python・配布物を取得します。管理サービスなどの配置は `--apply` 時です。

| コマンド・指定 | 動作 |
|---|---|
| 指定なし | 同じリリースのCLI・補助プログラムを取得・検証し、配置計画を表示 |
| `--version v0.0.4` | 指定版を取得 |
| `--apply` | 計画成功後に `sudo` で配置。選択済みPythonの絶対パスも渡す |
| operator省略 | スクリプトを実行した通常ユーザーを登録 |
| `--operator USER` | 別の既存通常ユーザーを登録。rootから実行する場合は必須 |

| 配置対象 | 主な場所・名前 |
|---|---|
| CLI | `/usr/local/bin/oaiso` |
| Kata・containerd・BuildKit・runc | `/opt/codex-dock/releases/` の管理資産 |
| 管理サービス | `codex-dock-node`・`codex-dock-containerd`・`codex-dock-buildkit` |
| operator group | `codex-dock` |
| Bash・Zsh・Fish補完 | `/usr/local/share/` 以下 |

- 既存Docker／既定containerdのパッケージは置き換えない。
- ネットワーク・検証・失敗時の復旧：[自動セットアップの詳細](setup.md)。

### 版を固定して取得・実行する

**v0.0.4を指定する例。保存したスクリプトを確認してから、同じファイルで計画・適用します。**

```bash
curl -fsSL -o install.sh https://github.com/pacificbelt30/sandbox-oasis/releases/download/v0.0.4/install.sh
less install.sh
bash install.sh --version v0.0.4
bash install.sh --version v0.0.4 --apply
```

## 4. 再ログインして確認

1. ログアウトし、登録した通常ユーザーで再ログイン。
2. 下のコマンドを実行。

```bash
command -v oaiso
oaiso --version
id -nG
oaiso runtime-check
```

**CLI・グループの出力例**

```text
$ command -v oaiso
/usr/local/bin/oaiso
$ oaiso --version
oaiso version <version>
$ id -nG
user sudo codex-dock
```

- `$` は入力を表す記号。コピーして実行する場合は上の `bash` ブロックを使用。
- 版・利用者名・他のグループは環境依存。`codex-dock` への所属を確認。
- `runtime-check`：[正常時の全項目と読み方](getting-started.md#1-実行ホストを確認する)。

| 確認対象 | 成功条件 |
|---|---|
| CLI | `oaiso` のパスと版が表示される |
| グループ | `id -nG` に `codex-dock` が含まれる |
| 実行基盤 | `runtime-check` が成功する |

## 次は認証と初回起動

1. [キーリングを解錠し、loginで認証を登録](guides/first-run.md#3-キーリングを解錠する)。既存設定を使う場合は[認証ガイド](guides/authentication.md)を参照。
2. `oaiso init`。Claude Codeの場合は `oaiso init --agent claude`。
3. 作業するプロジェクトで `oaiso codex` または `oaiso claude`。

- 通常の `init`・`run`：`sudo` 不要。
- 実APIキー・OAuthトークン：Auth Proxyで保持。作業VMには短命トークンだけを渡す。
- 一般通信：既定拒否。Git・パッケージ取得が必要なら[宛先を許可](proxy-policy.md)。

## ほかの導入方法

| 方法 | 範囲・参照先 |
|---|---|
| ソースからビルド | [開発者向けのビルド手順](setup.md#ソースからビルドする場合)。Go・Make・Git・uvが必要 |
| 手動で実行ホストを配置 | [Kata実行ホストの準備](kata.md) |
| 別の場所でビルドした実行ファイル一式を使用 | [ビルド済みバンドルとは](setup.md#ビルド済みバンドルとは)。`--bundle PATH`。ホストのGo・Makeは不要 |
| オフライン導入 | [オフライン配置](setup.md#オフライン配置) |
| CLI・補助ツール・補完をビルド・配置 | `make build` / `sudo make install`。Kata・管理サービスの配置は含まない |
| 更新・ロールバック | [更新と復旧](setup.md#更新する) |

## ツールの公式手順

- ビルド済み版の導入には **Go・Make・Gitは不要**。ソースからのビルド時は[開発用ツール](development/toolchain.md)を準備。
- 本製品の配置には上のインストーラーを使用。実行基盤は本製品の固定版・設定で配置します。

| ツール | 配置方法・用途 | 公式の導入手順・配布元 |
|---|---|---|
| Bash | ホストのシェル。Ubuntu標準のものを使用 | [GNU Bashの導入](https://www.gnu.org/software/bash/manual/html_node/Installing-Bash.html) |
| curl | ホストでインストーラー・配布物を取得 | [公式ダウンロード案内](https://curl.se/download.html) |
| GitHub CLI | 配布物の署名・作成元を確認 | [公式Linux手順](https://github.com/cli/cli/blob/trunk/docs/install_linux.md) |
| uv | 未導入ならインストーラーが自動準備 | [uvのインストール](https://docs.astral.sh/uv/getting-started/installation/) |
| Python | uv管理の3.12を自動準備。配置・更新処理に使用 | [uvによるPythonの導入](https://docs.astral.sh/uv/guides/install-python/) |
| Kata Containers | VMによるワーカー隔離。インストーラーが配置 | [インストールガイド](https://github.com/kata-containers/kata-containers/blob/main/docs/install/README.md) |
| containerd | VM・コンテナの管理。インストーラーが配置 | [Getting started](https://github.com/containerd/containerd/blob/main/docs/getting-started.md) |
| BuildKit | ワーカー・Proxyのイメージ作成。インストーラーが配置 | [Quick start](https://github.com/moby/buildkit#quick-start) |
| runc | Proxyコンテナの実行。インストーラーが配置 | [公式配布物の導入](https://github.com/opencontainers/runc#releases) |

- Goが必要になるのは、製品自体やGo APIを使う呼び出し側をソースからビルドする環境です。[Go公式のLinux手順](https://go.dev/doc/install)に沿った[コマンド例](development/toolchain.md#2-goを公式linux手順で導入する)を掲載しています。

## 配布物の検証

- インストーラーは署名・作成元・対象タグ・SHA-256を確認してから配置します。
- [スクリプト自体の実行前検証・SBOM・旧版への巻戻し防止](guides/signed-releases.md)。
