# 診断の保存・出力

**日本語** | [English](../en/guides/observability.md)

- 利用可能：**未リリース（main）**。
- ローカルのJSONLへ保存。外部の監視サービスや常駐プロセスは不要です。

## 保存する情報

| 種類 | 内容 | 制限 |
|---|---|---|
| `status` | 既存statusの検査名・成否 | 接続テスト・更新照会・復号・推論を追加しない |
| `resources` | host cgroupの現在／最大メモリ、guest／hostの上限、OOM kill数 | guest内のRSSではない。取得不可は`unknown` |
| `denial` | 一般通信Proxyの拒否理由コード | 既存の最大1000行から取得。共有Proxyの履歴は選択VMだけの通信と断定できない |
| `launch` | 起動の合計時間・処理別の時間・成否 | ビルド・前面画面の待機・後の推論時間は含まない |
| `collection` | 拒否履歴を取得できなかった状態 | 取得失敗を「拒否0件」と扱わない |

- 保存しない値：資格情報、コマンド、URL、宛先、workspaceのパス、自由文のエラー。
- worker IDはSHA-256で置換。元IDを匿名化しきる保証や、root・同一UIDによる改変を防ぐ監査ログではありません。
- 現在と一世代前の2ファイルを保持。既定は各1MiB、`--max-bytes`で各1KiB〜8MiBに変更可能。古い記録は削除します。
- ディレクトリ0700、ファイル0600。不正な権限・symlink・複数のhard linkを拒否します。

## 状態・メモリ・拒否履歴を保存する

前提：対応nodeを起動済み。通常ユーザーで実行します。`my-worker`は既存ワーカー名へ置き換えます。

```bash
oaiso observe capture --directory "$HOME/.local/state/oaiso/observations" --worker my-worker
oaiso observe export --directory "$HOME/.local/state/oaiso/observations"
```

状態だけを保存する場合：

```bash
oaiso observe capture --directory "$HOME/.local/state/oaiso/observations"
```

5秒間隔で12回保存する場合：

```bash
oaiso observe capture --directory "$HOME/.local/state/oaiso/observations" \
  --worker my-worker --samples 12 --interval 5s
```

- 成功の目印：`Diagnostics saved ...`。JSONLの`kind`・`status`で結果を確認します。
- 一部の検査が失敗しても、取得できた情報を保存して終了コード1を返します。`oaiso status`で修復手順を確認してください。
- 同じcapture内の重複を抑えます。再実行時の重複・Proxyの履歴切捨て・停止中の欠損を完全に検出する配信保証はありません。
- auth ProxyのAI拒否履歴や要求内容を収集する機能ではありません。

## 起動時間を保存する

```bash
oaiso run --name measured-job --detach --keep \
  --observe-dir "$HOME/.local/state/oaiso/observations" --exec 'sleep 300'
oaiso observe export --directory "$HOME/.local/state/oaiso/observations"
```

- 保存失敗は警告を出します。起動結果・workerの所有権・後片付けの契約は変えません。
- Go API：`LaunchWithTimings`の戻り値を`cxd.SaveLaunchObservation(directory, worker.ID, timings, err != nil)`へ渡し、`cxd.ReadObservations(directory)`で読みます。
- 新規の公開配布物には未反映。検証範囲は[テスト記録](../testing.md)を参照。
