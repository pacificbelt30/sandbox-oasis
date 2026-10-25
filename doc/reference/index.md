# リファレンス

**日本語** | [English](../en/reference/index.md)

- コマンド名・フラグ・既定値・設定キー・API契約を調べる章。
- 操作の流れから知りたい場合：[利用ガイド](../guides/index.md)。

## CLI

| 調べる対象 | リファレンス |
|---|---|
| コマンドを探す | [コマンド一覧](../commands.md) |
| 起動・エージェント・mount・承認 | [run](../commands/run.md) |
| 一覧・再起動・exec・wait・停止・削除・ログ | [ワーカー管理](../commands/worker.md) |
| イメージ・テンプレート | [build](../commands/build.md) |
| 認証Proxy | [proxy](../commands/proxy.md) |
| プロファイル・APIキー・OAuth import | [auth](../commands/auth.md) |
| ネットワーク・TUI・補完・CLI更新 | [network](../commands/network-cmd.md)・[ui](../commands/ui.md)・[completion](../commands/completion.md)・[update](../commands/update.md) |

## 設定

| 調べる対象 | リファレンス |
|---|---|
| プロジェクト設定・優先順位 | [プロジェクト設定](../configuration.md#プロジェクト設定) |
| グローバル設定・環境変数・資源プロファイル | [設定リファレンス](../configuration.md) |
| 通信の判定規則 | [通信ポリシー](../proxy-policy.md) |

## 組み込みAPI

| 調べる対象 | リファレンス |
|---|---|
| 機械可読仕様・Python・TypeScript | [ローカルAPI・SDK](../local-api-sdk.md) |
| 作成・起動・実行・停止・削除・再送 | [Go API](../go-api.md) |
| ケース別認証・資格情報版・接続世代 | [Credential Store](../credential-store.md) |
| Auth ProxyのHTTP API | [認証API](../auth-proxy/endpoints.md) |

- Go API：同じLinux実行ホストの管理サービスへ接続。
- 公開のリモート多人数サービスとしての認可・テナント管理は組み込み側で実装。
