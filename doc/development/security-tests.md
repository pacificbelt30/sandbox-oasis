# 認証・通信・障害の境界を自動検査する

**日本語** | [English](../en/development/security-tests.md)

- 追加のfuzz・負荷・停止競合の検査：未リリース（main）。既存のCAS・回収試験も再利用します。
- 実キー・実アカウント・外部AIへの推論を使いません。架空値とローカルfixtureで確認します。

## 検査すること

| 境界 | 入力・障害 | 守る動作 |
|---|---|---|
| 認証 | 未発行token、壊れた認証header | 上流接続・資格情報注入を始めない |
| モデル指定 | JSONの重複・大小文字違い・余分なJSON | 許可モデルだけを使用。転送本文を書き換えて整合性を崩さない |
| brokerのURL・path | 別origin、`..`、二重slash、percent encoding、禁止method | 固定origin・許可path・methodから広げない |
| 通信先 | IPv4/IPv6、metadata、混在DNS回答、不正authority | 拒否した宛先・アドレスへdialしない |
| 管理JSON | 重複key、Unicodeによる同一fieldの別表記、末尾JSON、未知field、サイズ・深さ超過 | 認証・Storeの変更前に拒否 |
| token期限 | 負数・過大値・TLSの上限超過 | 不正な期限で既存の権限を置換しない。HTTPのTTL省略は設定済み既定値を使用 |
| 保存失敗 | `ENOSPC`を注入、破損した監査head、OAuth保存失敗 | 利用枠や新しい資格情報を発行しない。復旧後に勝手に利用枠を初期化しない |
| 並行・長い通信 | 96本のSSE、失効と新規要求 | 既存streamを閉じ、新しい上流処理を始めない |
| 停止と競合 | 発行と停止を競合、停止済みProxyへの発行・復元 | 停止後に権限を残さない。期限監視goroutineを終了 |
| Store更新 | 既存の別process CAS・更新失敗試験 | 競合を成功扱いせず、未保存の更新を採用しない |
| 回収・取消 | 既存の停止失敗・削除journal・応答消失・再送試験 | 停止未確認を完了扱いにしない。回収の再試行情報を残す |

- `ENOSPC`は保存境界への障害注入です。共有ホストのdiskを埋める試験ではありません。
- TLS／許可規則／Store／停止の全組合せを網羅したという意味ではありません。

## 実行する

リポジトリのルートで、[開発用Go](toolchain.md)を準備して実行します。

通常の回帰・race検査：

```bash
make test
```

5系統のfuzz探索：

```bash
make security-fuzz
```

- 各targetは5秒・2 worker。seed準備とコンパイルの時間は別です。
- 固定seedは通常の`make test`でも実行します。探索時のrace検査は、通常のrace付き全件試験と分けています。
- CIは通常のrace試験後に同じfuzzを実行します。
- 失敗時は最小の再現入力を確認し、架空値だけを残して回帰試験へ追加します。生ログ・資格情報・大量の探索cacheはGitへ追加しません。

## 実Kataで確認する

- [使い捨てノードの手順](../testing.md#新規導入更新復旧を検証する)を使用します。
- TLS通信・失効：`--acceptance-run '^TestKataWorkerMTLS$'`。
- Go API・ケース分離・保存したProxy監査：`--acceptance-run '^TestKataGoAPI$'`。
- 認証保存：`TestKataEncryptedProfile`／`TestKataKeyringProfile`。
- 署名のCIはGitHub／Sigstoreへ通信します。上流CLIの通常契約検査はオフライン、AIの実認証・推論とは区別します。
- 実行結果・失敗からの修正・未確認範囲：[テストと動作確認](../testing.md)。
