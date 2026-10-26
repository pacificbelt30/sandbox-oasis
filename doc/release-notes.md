# Oaiso v0.0.6

## 主な変更

v0.0.6は、Codex / Claude Codeのホスト管理設定と、既存ワーカーへ安全にアクセスする操作を追加するUX更新です。

| 分野 | 改善 |
|---|---|
| エージェントポリシー | `interactive`、`approve-for-me`、`autonomous`、`review`、`private`、`restricted`の6つを追加。承認・推論・履歴などの既定値を所有者専用ファイルで管理し、list / show / clone / set / diff / audit / doctorで確認・編集 |
| ワーカーへのアクセス | `oaiso connect`とTUIの`C`キーで、メインAgentを止めずに別PTYのlogin shellへ接続。`exit`後はホストまたはTUIへ復帰 |
| 一覧と観測 | `oaiso ps`へfilter・sort・JSON／CSV・列選択・集計・snapshot比較を追加。TUIへ詳細表示、絞り込み、並び替え、コマンド実行、活動履歴を追加 |
| 端末入出力 | connect時のraw入力、初期サイズとSIGWINCH、`TERM=xterm-256color`、終了後の端末復元を追加。Esc・矢印・Ctrl-C・Ctrl-Z・Tab、ANSI装飾、UnicodeをPTYのまま転送 |
| 開発検査 | 空行・コメント・文書・テストを除く本体コード50,000行の再現可能な計測を追加。Codex設定の上流契約基準へエージェントポリシー項目を追加 |

- Codex CLI 0.163.0／Claude Code 2.1.306、Kata 4.2.0、containerd 2.4.2、BuildKit 0.34.0、runc 1.4.3の固定版はv0.0.5から変更していません。
- エージェントポリシーを指定しない場合は、対話承認を維持する`interactive`が既定です。
- 実APIキー・OAuth access／refresh／ID tokenは、引き続き認証Proxyだけで保持します。
- 保存済み認証プロファイル、暗号化形式、Local API v1に破壊的変更はありません。

## インストール

1. [前提条件とツールを準備](https://pacificbelt30.github.io/sandbox-oasis/installation/)。署名検証用のGitHub CLIが必要です。GitHubへのログインは不要です。
2. 通常ユーザーで配置計画を表示。

```bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/download/v0.0.6/install.sh | bash
```

3. 計画を確認して適用。

```bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/download/v0.0.6/install.sh | bash -s -- --apply
```

- 対象：Ubuntu 26.04 x86_64、systemd、KVM、cgroup v2、sudoが使える通常ユーザー。
- Go・Make・ソース取得は不要。uv管理Python 3.12を使用します。
- パイプ実行はHTTPS配布元のスクリプトを信頼して実行します。[スクリプト自体を実行前に署名検証する手順](https://pacificbelt30.github.io/sandbox-oasis/guides/signed-releases/)もあります。
- 適用後は再ログインし、`oaiso --version`・`oaiso runtime-check`を確認します。

## v0.0.5から更新する

| 順番 | 操作 |
|---|---|
| 1 | 作業を保存し、既存CLIで保持中のworkerとProxyを停止・削除 |
| 2 | v0.0.6のインストーラーで計画を確認し、`--apply`でCLI・node・guard・loggerを更新 |
| 3 | 再ログイン後、`oaiso --version`・`oaiso runtime-check`で確認 |
| 4 | `oaiso build --template plain --tag codex-dock:latest`を実行。独自イメージは別途確認・再ビルド |
| 5 | 必要なら`oaiso agent-policy init`と`oaiso agent-policy default approve-for-me`でホスト既定値を選択 |
| 6 | `oaiso init --agent codex`または`oaiso init --agent claude`でProxyを準備し、新しいworkerを起動 |

- ポリシーは新しく起動するworkerへ適用されます。既存のリポジトリ設定からホスト専用ポリシーを変更することはできません。
- 旧版への意図したrollbackと障害復旧：[更新と復旧](https://pacificbelt30.github.io/sandbox-oasis/setup/)。到達済みの最も新しい適用版は保持されます。

## 配布ファイル

| ファイル | 内容 |
|---|---|
| `oaiso-linux-amd64.tar.gz` | 静的CLI・guard・IO logger・`bundle.json`・ライセンス／依存notice |
| `install.sh` | v0.0.6を選択する単独インストーラー |
| `SHA256SUMS` | archive・installer・SBOMのSHA-256 |
| `sbom.cdx.json` | 実バイナリのGo依存・標準ライブラリ・固定runtime資産の一覧 |
| `provenance.json` | 4ファイルの署名・作成元・タグ・commitを検証する証跡bundle |

- Kata等の固定配布物・作業イメージ・認証情報はarchiveに含みません。
- 署名・作成元は正当なビルド経路を確認するものです。脆弱性の不存在やguest image内の安全性は保証しません。

## 検証範囲

| 検証 | 結果・範囲 |
|---|---|
| 自動試験 | build・全件race・vet・lint、コード行数検査が成功 |
| 実Kata | 使い捨てノードで新規導入、更新、更新拒否、障害復旧と受入試験が成功 |
| エージェントポリシー | 架空の設定をworkerへ渡し、Codex / Claude設定の生成、0600保存、選択ポリシーの管理情報を確認 |
| 端末 | Esc・矢印・Ctrl-C・Ctrl-Z・Tabの生バイト、91x33のresize、ANSI SGR、Unicode、`TERM=xterm-256color`を実PTYで確認 |
| 未確認 | 実アカウントの認証・推論、長期OAuth更新、人が見た物理端末の色味、実WSL、macOS／WindowsでのVM実行 |

- 実Kataでは架空の資格情報・ローカル上流を使用し、外部AIへ推論要求を送っていません。
- [検証記録と制約](https://pacificbelt30.github.io/sandbox-oasis/testing/)・[セキュリティ上の保護範囲](https://pacificbelt30.github.io/sandbox-oasis/security/)。

## English

v0.0.6 adds host-managed Codex / Claude Code defaults and safer, more capable access to existing workers.

| Area | Changes |
|---|---|
| Agent policies | Six built-in profiles, owner-private host storage, explicit precedence, structured guest configuration, and list / show / clone / set / diff / audit / doctor commands |
| Worker access | `oaiso connect` and the TUI `C` key open a separate login-shell PTY without replacing the primary agent process |
| Inventory and TUI | Filtering, sorting, JSON/CSV, selected columns, summaries, snapshots, richer details, command execution, and an in-session activity timeline |
| Terminal fidelity | Raw input, initial and SIGWINCH resize propagation, `TERM=xterm-256color`, terminal restoration, ANSI styling, Unicode, and special-key byte preservation |
| Development checks | A reproducible 50,000-line production-code gate and expanded upstream Codex configuration contracts |

- Pinned agent and runtime versions are unchanged from v0.0.5.
- The default remains the conservative `interactive` policy unless a host policy is selected explicitly.
- Stored credential profiles, encryption formats, and Local API v1 remain compatible.
- Save work and remove existing workers and proxies before updating, rebuild the desired worker image, and create new workers to apply an agent policy.
- Install and signature verification: [installation](https://pacificbelt30.github.io/sandbox-oasis/en/installation/) and [signed releases](https://pacificbelt30.github.io/sandbox-oasis/en/guides/signed-releases/).
- Disposable real-Kata setup, upgrade, recovery, policy rendering, raw special-key bytes, PTY resizing, ANSI sequences, and Unicode transport passed with synthetic credentials and local fixtures. Live accounts, physical-terminal visual rendering, long-running OAuth refresh, and real WSL remain unverified.
