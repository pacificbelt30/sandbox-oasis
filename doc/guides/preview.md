# Webアプリをブラウザで確認する

**日本語** | [English](../en/guides/preview.md)

- 利用可能：**v0.0.4**。
- 対象：起動中のワーカー内のHTTP／WebSocketアプリ。
- ホストの`127.0.0.1`で待ち受けます。外部公開用の機能ではありません。

## 起動する

前提：`dev-worker`でWebアプリがポート3000を使って起動済み。アプリはVMの`127.0.0.1`から接続できる必要があります。

```bash
oaiso preview dev-worker 3000 --lifetime 30m
```

出力例：ポートとtokenは実行ごとに変わります。

```text
http://127.0.0.1:49152/__oaiso_login?token=...
```

1. 出力したURLを同じホストのブラウザで開きます。
2. 認証後、アプリの`/`へ移動します。
3. 終了するときはCLIで`Ctrl-C`を押します。

## 接続の条件

| 項目 | 動作 |
|---|---|
| 認証 | 実行ごとのランダムtoken。初回URLからHttpOnly・SameSite=StrictのCookieを設定 |
| 有効期間 | 既定30分。最大1時間。CLI終了・期限切れで接続を閉じる |
| 接続先 | 指定したVMのloopback・指定した1ポートだけ |
| Host・Origin | 発行したlocalhostのURLと異なる値は拒否 |
| 同時数 | 最大8要求・TCP接続は16まで。HTTP本文は8MiBまで |
| ワーカー停止・隔離 | 新規要求は拒否。既存接続も状態確認後に閉じる。確認間隔は1秒 |
| 認証情報 | プレビュー用CookieはVMへ転送しない。実APIキー・OAuthも渡さない |
| 管理socket | 公開しない。既存の検証付きExecを通して接続する |

- URLはプレビューへ入るための秘密です。共有しないでください。
- ブラウザではVM内のアプリが返すJavaScriptなどが動きます。隔離されるのはワーカーであり、ブラウザの実行環境ではありません。
- 一般通信規則・AI認証の失効とは別の接続です。一般通信の規則変更だけではプレビューを終了しません。
- 接続ごとにVM内で転送処理を起動するため、大量の通信を扱う配信用途には向きません。

## Go API

```go
preview, err := client.Preview(ctx, workerID, 3000, 30*time.Minute)
if err != nil {
    return err
}
defer preview.Close()
// preview.URLを利用者へ明示して開く。ログへ自動保存しない。
<-preview.Done()
```

- 絶対URLや独自のOrigin検査を使うアプリでは、プレビューのURLに合わせたアプリ側の設定が必要な場合があります。
