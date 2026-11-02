# 利用ガイド

**日本語** | [English](../en/guides/index.md)

- 前提：[インストール](../installation.md)を完了し、`oaiso runtime-check`で実行ホストを確認。
- 以下から、行いたい操作だけを選択。
- v0.0.3以降を初めて使う場合：[導入から初回起動](first-run.md)。

## サンドボックスとして使う

利用可能：**未リリース（main）**。AI認証を準備せず、開発・テスト用のKata VMを使います。

| やりたいこと | ガイド |
|---|---|
| VMを作成し、シェルへ接続する | [準備と作成](sandbox-ui.md#前提と起動) |
| ファイルを閲覧・転送する | [workspaceのファイル操作](sandbox-ui.md#ファイルを確認転送する) |
| Webアプリを確認する | [認証付きlocalhostプレビュー](sandbox-ui.md#webアプリを確認する) |
| キーを覚えずに操作する | [TUIの操作メニュー](../commands/ui.md#キーを覚えずに操作する) |

## エージェントを使う

| やりたいこと | ガイド |
|---|---|
| Codex／Claude Codeの起動方法を選ぶ | [エージェント](agents.md) |
| ClaudeのAPIキー／サブスクを使う | [Claude Code](../claude-code.md) |
| 一覧・ログ・停止・再起動を使う | [ワーカーの基本操作](../operations.md) |
| TUIで管理する | [TUI](../commands/ui.md) |
| 依頼を管理し、結果を選んで採用する | [タスクと結果レビュー](tasks.md) |

## 作業環境を用意する

| やりたいこと | ガイド |
|---|---|
| チームで設定を共有する | [プロジェクト設定](../configuration.md#プロジェクト設定) |
| workspace・追加mount・worktreeを使う | [ファイル共有と所有者](../workspace.md) |
| Dockerfile・テンプレート・パッケージを追加する | [イメージの作成](../commands/build.md) |
| ファイル制限・ジョブ・PTY・成果物回収を使う | [追加のサンドボックス制御](../sandbox-features.md) |

## 認証と通信を設定する

| やりたいこと | ガイド |
|---|---|
| ホストと製品の認証を分ける | [認証方式とプロファイル](authentication.md) |
| 通信先を許可・拒否する | [設定手順](networking.md)・[判定規則](../proxy-policy.md) |
| 資格情報の保持先・隔離の限界を確認する | [セキュリティ](../security.md) |
| 認証Proxyの仕組みを確認する | [認証プロキシ](../auth-proxy.md) |
| Proxyを別の構成で使う | [プロキシの単体利用](../proxy-standalone.md) |

## 次に調べる

- フラグ・設定・APIの正確な仕様：[リファレンス](../reference/index.md)。
- ホスト更新・障害対応：[管理・運用](../operations.md)。
- アプリケーションへの組み込み：[Go API](../go-api.md)。
