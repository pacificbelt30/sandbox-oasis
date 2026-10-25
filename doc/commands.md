# コマンドリファレンス

> **日本語** | [English](en/commands.md)

- oaiso の全コマンドリファレンスです。
- 各コマンドの詳細は以下のページを参照してください。

---

## コマンド一覧

| コマンド | 説明 | 詳細 |
|---|---|---|
| [`oaiso api`](local-api-sdk.md) | 組み込み用ローカルAPI（v0.0.4） | `stdio`・`schema` |
| [`oaiso claude`](claude-code.md) | 公式Claude Codeの起動 | ネイティブ引数・APIキー／サブスク |
| [`oaiso codex`](commands/run.md#codex-cli-を直接起動する) | Codex CLIの引数をそのまま渡して起動 | カレントディレクトリを共有・直接exec |
| [`oaiso run`](commands/run.md) | サンドボックスコンテナの起動 | オプション・承認モード・並列実行・パッケージ管理 |
| [`oaiso completion`](commands/completion.md) | Tab 補完スクリプトの生成 | Bash・Zsh・Fish・PowerShell |
| [`oaiso profile`](commands/run.md#資源プロファイル) | 資源プロファイルの表示・定義JSON生成 | `--node`・`--json`・`define --pids-limit` |
| [`oaiso default`](configuration.md#プロジェクト設定) | `oaiso run` の既定エージェントを表示・変更 | グローバル設定または `--local` |
| `oaiso project init/check` | プロジェクト設定の作成と検証 | [設定リファレンス](configuration.md#プロジェクト設定) |
| [`oaiso setup`](setup.md) | ホスト導入・更新・復旧 | 計画表示 / --apply / --rollback |
| [`oaiso login`](commands/login.md) | 独立プロファイルで認証（開発版） | API・サブスク、認証専用Kata、初回設定 |
| [`oaiso status`](commands/status.md) | 準備状態を一括確認（開発版） | `--json`・明示的な接続／更新チェック |
| [`oaiso init`](getting-started.md) | 初回起動の準備 | 診断・認証確認・イメージ検査・プロキシ起動 |
| [`oaiso runtime-check`](troubleshooting.md#複数の前提条件をまとめて調べる) | 実行環境の診断 | doctor別名・--json・修正手順 |
| [`oaiso image check`](commands/build.md#起動前に互換性を調べる) | イメージの事前検査 | platform・必要ツール・entrypoint |
| [`oaiso vm`](vm.md) | Kata VMの照合と管理 | ps / inspect / start / stop / rm / prune |
| [`oaiso start`](commands/worker.md) | 停止済みワーカーの再起動 | 同じID・認証の復元 |
| [`oaiso exec`](commands/worker.md#oaiso-exec--既存ワーカーでの実行) | 既存ワーカーでコマンドを実行 | `--timeout`・`-i`・`--json` |
| [`oaiso wait`](commands/worker.md#oaiso-wait--終了待ち) | ワーカーの終了を待つ | 終了コード・`--timeout`・`--json` |
| [`oaiso proxy`](commands/proxy.md) | Auth Proxy の管理 | build / run / serve / stop / rm |
| [`oaiso ps`](commands/worker.md#oaiso-ps--ワーカー一覧) | ワーカー一覧 | 実行中コンテナの表示 |
| [`oaiso stop`](commands/worker.md#oaiso-stop--コンテナの停止) | コンテナの停止 | 単体・全停止 |
| [`oaiso rm`](commands/worker.md#oaiso-rm--コンテナの削除) | コンテナの削除 | 停止済み・強制削除 |
| [`oaiso logs`](commands/worker.md#oaiso-logs--ログの表示) | ログの表示 | tail・follow |
| [`oaiso auth`](commands/auth.md) | 認証管理 | profile set/import/show/list、従来のshow/set/rotate |
| [`oaiso network`](commands/network-cmd.md) | ネットワーク管理 | create / rm / status |
| [`oaiso build`](commands/build.md) | サンドボックスイメージのビルド | Dockerfile の自動検出 |
| [`oaiso ui`](commands/ui.md) | TUI ダッシュボード | キーバインド一覧 |
| [`oaiso upgrade`](commands/upgrade.md) | 配布版ホスト更新の計画と適用（開発版） | `--version`・`--apply`、イメージ更新の案内 |
| [`oaiso update`](commands/update.md) | Goで配置したCLIの更新 | リリース版は[配布インストーラー](setup.md#更新する)で更新 |
| [`oaiso policy`](sandbox-features.md#通信の確認と設定) | 実効通信規則・拒否理由・隔離 | show / check / explain / events / presets / quarantine（revoke別名） |
| [`oaiso capabilities`](sandbox-features.md) | nodeの検証済み対応機能 | JSON出力 |
| [`oaiso files`](sandbox-features.md#ファイル実行回収) | ワークスペース内のファイル操作 | get / put / list |
| [`oaiso jobs`](sandbox-features.md#ファイル実行回収) | ワーカー内の背景ジョブ | start / list / logs / kill |
| [`oaiso artifact`](sandbox-features.md#ファイル実行回収) | Git差分・commitによる成果回収 | diff / commit |
| [`oaiso environment`](sandbox-features.md) | イメージ・環境の固定 | lock |
| [`oaiso reap`](sandbox-features.md#回収の常駐とメモリの観測) | 寿命・idle・保持期限の処理 | `--dry-run` / `--watch`。自動起動はしない |
| [`oaiso node`](kata.md) | ホスト管理サービス | serve。管理者向け |
| [`oaiso runtime-lock`](kata.md) | 検証済み固定資産のmanifest作成 | 管理者向け。配布元の署名検証の代わりではない |
| `oaiso help [COMMAND]` | ヘルプを表示 | 通常は `COMMAND --help` も利用可 |

---

## グローバルオプション

- 通常のサブコマンド用です。
- `codex` / `claude` は引数を公式CLIへ渡すため、Oaiso側の設定はグローバル設定ファイルで指定してください。

| オプション | 省略形 | デフォルト | 説明 |
|---|---|---|---|
| `--verbose` | `-v` | `false` | 詳細なログを出力する |
| `--debug` | | `false` | デバッグログを出力する |
| `--config` | | `~/.config/codex-dock/config.toml` | 設定ファイルのパス |
| `--auth-profile` | | 未選択 | 独立プロファイル名。ホスト認証へfallbackしない |
| `--auth-profile-dir` | | `~/.config/codex-dock/profiles` | プロファイル保存root |
| `--help` | `-h` | | ヘルプ。`codex` / `claude` では公式CLI側へ渡る |
| `--version` | | | ルートの `oaiso --version` で製品の版を表示 |

---

## 関連ドキュメント

- [クイックスタート](getting-started.md) — よく使うコマンドパターン
- [設定リファレンス](configuration.md) — コマンドフラグのデフォルト値を変更する
- [Auth Proxy のみを使う](proxy-standalone.md) — `oaiso run` を使わずに Codex CLI を設定する

ホスト管理者用の `node serve` と `runtime-lock` は[Kataの配置](kata.md)を参照してください。

- 廃止済みの `firewall`：[移行先](commands/firewall.md)。現行の実行可能コマンドには含みません。

## 作業を支援する追加コマンド

利用可能：**v0.0.4**。

| コマンド | 使い方 |
|---|---|
| `artifact plan/bundle` | [選んだ成果物の確認・回収](guides/artifacts.md) |
| `preview WORKER PORT` | [認証付きlocalhostプレビュー](guides/preview.md) |
| `policy propose/plan/apply` | [通信許可の申請・Proxy単位の即時変更](guides/live-policy.md) |
| `checkpoint pin/prune` | [保存期限・容量の確認と保護](guides/disk-checkpoint.md#保存期限容量を確認して回収する) |
