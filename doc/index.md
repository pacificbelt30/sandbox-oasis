# 御AgentISOlation（Oaiso）ドキュメント

**日本語** | [English](en/index.md)

| 項目 | 要点 |
|---|---|
| 用途 | Codex CLI・Claude CodeをKata VMで隔離実行 |
| コマンド | `oaiso` |
| 実行基盤 | containerdが管理、DragonballがVMを実行 |
| 実行ホスト | Linux x86_64 |

- CLIのクロスビルドだけでは、各OSでKataを実行できません。
- 用語・保存先・停止と再開：[Oaisoの概要](concepts.md)。

## 章から探す

| 章 | 内容 | 入口 |
|---|---|---|
| はじめに | インストール・認証の準備・初回起動 | [インストール](installation.md) → [クイックスタート](getting-started.md) |
| 利用ガイド | エージェント・作業環境・認証・通信と隔離 | [利用ガイド](guides/index.md) |
| リファレンス | CLI・設定・組み込みAPIの指定と契約 | [リファレンス](reference/index.md) |
| 管理・運用 | 日常操作・ホスト更新・VM管理・障害対応 | [日常操作](operations.md)・[配置と更新](setup.md) |
| 開発 | 仕組み・上流追従・テスト・今後の対応 | [構成](architecture.md)・[検証](testing.md) |

- v0.0.3以降のキーリング・`login`：[インストールから最初のVM起動まで](guides/first-run.md)。公開v0.0.2とは認証の準備方法が異なります。

## 初めて使う

| 読む順序 | 内容 |
|---|---|
| 1. [Installation — インストール](installation.md) | 依存ツールの取得・CLIと実行基盤の配置・動作確認 |
| 2. [クイックスタート](getting-started.md) | 利用者向け。認証・イメージを用意して最初のワーカーを起動する |
| 3. [日常の運用](operations.md) | 一覧、ログ、停止、再起動、更新、後片付け |

旧Docker環境を使っている場合は、[移行手順](migration.md)から始めてください。

## 目的から探す

| 目的 | ページ |
|---|---|
| Codex／Claude Codeを使う | [エージェント](guides/agents.md)・[Claude Code](claude-code.md) |
| ホストと製品の認証を分ける | [認証方式とプロファイル](guides/authentication.md) |
| ファイル共有・所有者・権限を確認する | [workspace](workspace.md) |
| VMの状態や残存資源を調べる | [VM管理](vm.md) |
| カスタムイメージやテンプレートを作る | [build](commands/build.md) |
| 一般通信の許可／拒否を設定する | [通信ポリシー](proxy-policy.md) |
| 認証情報の扱いを知る | [認証プロキシ](auth-proxy.md) |
| プロキシだけを使う | [単体利用](proxy-standalone.md) |
| 起動や通信の問題を調べる | [トラブルシューティング](troubleshooting.md) |
| フラグや設定名を調べる | [コマンド](commands.md)・[設定](configuration.md) |
| 製品からワーカーを操作する | [製品組み込み用 Go API](go-api.md) |
| 性能を測り、変更前後を比較する | [性能とベンチマーク](performance.md) |

## 仕組みと開発

- [アーキテクチャ](architecture.md)：各コンポーネントと起動の流れ
- [ネットワーク](network.md)：ワーカーと2種類のプロキシの接続
- [セキュリティ](security.md)：認証情報の保存方法・共有ファイルへの影響・管理権限
- [認証API](auth-proxy/endpoints.md)・[短命トークン](auth-proxy/tokens.md)
- [テストと動作確認](testing.md)：自動テスト、実Kata試験、確認済みの範囲
- [今後の対応](future-work.md)：追加するコーディングエージェントと対応条件
- [機能と安全性の改善候補](development/improvements.md)：優先順位・追加する機能・セキュリティ強化・完了条件
- [開発用ツールを準備する](development/toolchain.md)：Go公式のLinux手順と各ツールの導入リンク
- [ドキュメントを追加・修正する](development/documentation.md)：開発者向け。ページの書き方と確認コマンド

[追加のサンドボックス制御](sandbox-features.md)

## 名称と互換性

| 項目 | 内容 |
|---|---|
| 正式名称 | 御Agent**ISO**lation |
| 英字表記・読み | Oaiso／おあいそ |
| CLI | `oaiso` のみ。旧コマンドは提供しない |
| プロジェクト設定 | `oaiso.toml`。旧 `codex-dock.toml` は名前の変更が必要 |
| 既存の設定・認証情報 | 保存先は変更なし。サービス名・Go APIの名前も変更なし |
| 詳細 | [名称変更と互換性](migration.md#oaisoへの名称変更) |
