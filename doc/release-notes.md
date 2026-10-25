# Oaiso v0.0.5

## 主な変更

v0.0.5は、v0.0.4の実行・認証境界を強化するセキュリティ更新です。

| 分野 | 改善 |
|---|---|
| Kataの共有mount | mount元を受入時の正規化済み絶対pathへ固定。guestが書き込める別の共有領域内にmount元を置く構成を拒否し、停止中にpathや親directoryがsymlinkへ差し替えられた場合は再起動を拒否 |
| OAuth更新 | OpenAIの更新要求を64 KiB、issuer応答を1 MiBへ制限。request bodyと上流応答にtimeoutを設け、token失効時は待機中の読取り・上流通信を取消。OpenAI／Anthropicの更新待ちを分離し、credential保存だけを直列化 |
| worker出力 | primary processの保持ログを16 MiBへ制限。上限後も出力をdrainしてguestを停止させず、node再起動後も上限を引き継ぐ。旧形式のworkerは管理受付前に停止・ログ制限を適用 |
| 回帰試験 | mount差替え、過大・低速OAuth応答、失効中の取消、出力上限とnode再起動を自動試験へ追加。実Kataで導入・全受入・更新・障害復旧を再確認 |
| 開発作業 | Go／Dockerのbuild cache、使い捨てイメージ、一時bundleを所有範囲で回収し、証跡と再試験用cacheを区別する手順を追加 |

- Codex CLI 0.163.0／Claude Code 2.1.306、Kata 4.2.0、containerd 2.4.2、BuildKit 0.34.0、runc 1.4.3の固定版はv0.0.4から変更していません。
- 実APIキー・OAuth access／refresh／ID tokenは、引き続き認証Proxyだけで保持します。
- mount元を別のguest-writable mount内へ入れる構成は使用できません。独立したhost pathへ移し、workerを作り直してください。
- v0.0.4以前の稼働中workerを残したまま更新はできません。作業を保存し、workerとProxyを停止・削除してから更新してください。

## インストール

1. [前提条件とツールを準備](https://pacificbelt30.github.io/sandbox-oasis/installation/)。署名検証用のGitHub CLIが必要です。GitHubへのログインは不要です。
2. 通常ユーザーで配置計画を表示。

```bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/download/v0.0.5/install.sh | bash
```

3. 計画を確認して適用。

```bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/download/v0.0.5/install.sh | bash -s -- --apply
```

- 対象：Ubuntu 26.04 x86_64、systemd、KVM、cgroup v2、sudoが使える通常ユーザー。
- Go・Make・ソース取得は不要。uv管理Python 3.12を使用します。
- パイプ実行はHTTPS配布元のスクリプトを信頼して実行します。[スクリプト自体を実行前に署名検証する手順](https://pacificbelt30.github.io/sandbox-oasis/guides/signed-releases/)もあります。
- 適用後は再ログインし、`oaiso --version`・`oaiso runtime-check`を確認します。

## v0.0.4から更新する

| 順番 | 操作 |
|---|---|
| 1 | 作業を保存し、既存CLIで保持中のworkerとProxyを停止・削除 |
| 2 | v0.0.5のインストーラーで計画を確認し、`--apply`でCLI・node・guard・loggerを更新 |
| 3 | 再ログイン後、`oaiso --version`・`oaiso runtime-check`で確認 |
| 4 | `oaiso build --template plain --tag codex-dock:latest`を実行。独自イメージは別途確認・再ビルド |
| 5 | `oaiso init --agent codex`または`oaiso init --agent claude`で新Proxyを準備し、新しいworkerを起動 |

- 保存済み認証プロファイル、暗号化形式、Local API v1に破壊的変更はありません。
- 旧版への意図したrollbackと障害復旧：[更新と復旧](https://pacificbelt30.github.io/sandbox-oasis/setup/)。到達済みの最も新しい適用版は保持されます。

## 配布ファイル

| ファイル | 内容 |
|---|---|
| `oaiso-linux-amd64.tar.gz` | 静的CLI・guard・IO logger・`bundle.json`・ライセンス／依存notice |
| `install.sh` | v0.0.5を選択する単独インストーラー |
| `SHA256SUMS` | archive・installer・SBOMのSHA-256 |
| `sbom.cdx.json` | 実バイナリのGo依存・標準ライブラリ・固定runtime資産の一覧 |
| `provenance.json` | 4ファイルの署名・作成元・タグ・commitを検証する証跡bundle |

- Kata等の固定配布物・作業イメージ・認証情報はarchiveに含みません。
- 署名・作成元は正当なビルド経路を確認するものです。脆弱性の不存在やguest image内の安全性は保証しません。

## 検証範囲

| 検証 | 結果・範囲 |
|---|---|
| 自動試験 | build・全件race・vet・lint、Docker build、Local API／SDK契約が成功 |
| セキュリティ回帰 | mount差替え・nested mount、OAuth body／応答上限・timeout・失効取消、出力上限・再起動移行を確認 |
| 実Kata | VM・認証・TTY・共有・通信・暗号化・実キーリング・Go APIの8スイートが成功 |
| 導入・復旧 | 使い捨てノードで新規導入、v0.0.5相当への更新、更新拒否、意図的なreadiness失敗からのrollbackを確認 |
| 未確認 | 実アカウントの認証・推論、長期OAuth更新、実WSL、macOS／WindowsでのVM実行 |

- 実Kataでは架空の資格情報・ローカル上流を使用し、外部AIへ推論要求を送っていません。
- [検証記録と制約](https://pacificbelt30.github.io/sandbox-oasis/testing/)・[セキュリティ上の保護範囲](https://pacificbelt30.github.io/sandbox-oasis/security/)。

## English

v0.0.5 is a security-hardening update for the v0.0.4 runtime and authentication boundaries.

| Area | Changes |
|---|---|
| Kata mounts | Persist canonical host source paths, reject bind sources nested under another guest-writable share, and fail closed when a stopped worker's source or parent is replaced by a symlink |
| OAuth refresh | Bound OpenAI request bodies to 64 KiB and issuer responses to 1 MiB; apply read/upstream timeouts; cancel blocked reads and upstream calls when the worker token is revoked; separate provider refresh queues |
| Worker output | Retain at most 16 MiB from the primary process while continuing to drain guest output; preserve the limit across node restarts and migrate legacy workers before accepting management traffic |
| Validation | Added regressions for path replacement, oversized/slow OAuth traffic, revocation cancellation and output retention; repeated disposable real-Kata setup, acceptance, upgrade and recovery |

- Pinned agent and runtime versions are unchanged from v0.0.4.
- Save work and remove existing workers and proxies before updating. Stored credential profiles and Local API v1 remain compatible.
- Install and signature verification: [installation](https://pacificbelt30.github.io/sandbox-oasis/en/installation/) and [signed releases](https://pacificbelt30.github.io/sandbox-oasis/en/guides/signed-releases/).
- Real-Kata validation used synthetic credentials and local upstream fixtures; live accounts, long-running OAuth refresh and real WSL remain unverified.
