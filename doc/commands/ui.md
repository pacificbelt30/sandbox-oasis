# `oaiso ui` — TUI ダッシュボード

> **日本語** | [English](../en/commands/ui.md)
>
> [← コマンドリファレンス一覧](../commands.md)

全ワーカーをリアルタイムで監視・管理するターミナル UI を起動します。

```bash
oaiso ui
```

---

## 画面レイアウト

**配置を示す概略図。枠・幅・名前・時間は表示例。**

```text
┌──────────────────────────────────────────────────────────────┐
│ 御AgentISOlation  2 running / 3 total  3 shown  sort=name     │
│ Filter:                                                       │
├──────────────────────────────────────────────────────────────┤
│ NAME       STATUS   POLICY          TASK       │ Instance     │
│ worker-a   running  approve-for-me  テスト作成 │ Status ●     │
│ worker-b   exited   review          完了       │ Branch main  │
├──────────────────────────────────────────────────────────────┤
│ [Enter] Logs [C] Connect [/] Filter [V] Activity [?] Help    │
└──────────────────────────────────────────────────────────────┘
```

---

## キーバインド

`C`・`X`・`/`・`O`・`V`のインスタンス操作はv0.0.6以降で利用できます。

| キー | 一覧画面での動作 |
|---|---|
| `↑` / `↓` | ワーカーを選択 |
| `Enter` | 選択したワーカーの直近200行のログを取得 |
| `C` | 選択したワーカーへ新しいlogin shellで接続。`exit`でTUIへ復帰 |
| `X` | 選択したワーカーでコマンドを実行し、出力と終了コードを表示 |
| `/` | 名前または `status:` / `branch:` / `task:` / `image:` / `policy:` で絞り込み。`!` は除外 |
| `O` | 名前、状態、新旧、ブランチ、ポリシーの並び順を切り替え |
| `V` | TUI起動中に観測した起動・停止・削除・metadata変更を表示 |
| `S` | 選択したワーカーを停止。停止待ち10秒 |
| `D` | 選択したワーカーを強制削除 |
| `A` | 一覧内の実行中ワーカーをすべて停止 |
| `R` | 停止済みワーカーを再起動 |
| `E` | 選択したワーカーのProxy拒否履歴を取得 |
| `P` | Proxy設定画面を開く |
| `B` | Credential Broker設定画面を開く |
| `?` / `H` | ヘルプを開く |
| `Q` | UIを終了。ログ／拒否履歴画面では一覧へ戻る |

- 英字キー：大文字・小文字の両方を利用可。
- 削除・全停止：確認ダイアログを表示。必要な成果は先に保存。
- `C` の接続：メインAgentへattachせず別execを作成。shell終了後もAgentとワーカーは動作を継続。
- ログと拒否履歴：開いた時点で取得。継続追跡は `oaiso logs WORKER --follow` / `oaiso policy events --worker WORKER --follow`。
- 認証設定：通常のCLIと同じグローバル設定。独立プロファイルと管理シークレットを[先に設定](../guides/authentication.md)。

> **更新間隔**: コンテナ一覧は 2 秒ごとに自動更新されます。

## Proxy設定画面

```bash
oaiso proxy configure
```

- 上のコマンド：ワーカー一覧を開かず、設定だけ編集。ノード停止中でも利用可。
- 設定先：グローバル設定、または `oaiso --config PATH proxy configure` のファイル。
- 編集項目：ドメイン・CIDR・ポートの許可／拒否、空許可時の既定拒否、上流Proxy URL、ホストCAパス。

| ボタン・キー | 動作 |
|---|---|
| Validate | 入力形式を検証 |
| Test destination | 入力したHOST:PORTの名前・ポートを確認。DNS・実通信は試さない |
| Save | 設定を保存。同時編集があれば拒否 |
| Reload | 保存済み設定を読み直す |
| Back / Esc | 一覧へ戻る。単独起動時は終了 |

**Save成功時の表示**

```text
Saved. Recreate the desired proxies/workers explicitly to apply the new settings.
```

- 設定ファイルへの保存完了。次は成果を保存し、対象のProxy・ワーカーを再作成。

- APIキー・実トークン・管理シークレットの値は編集しない。
- 保存だけでは稼働中Proxyへ反映されない。[通信ガイド](../guides/networking.md)の順序で再作成。

---

## 関連ドキュメント

- [ワーカー管理コマンド](worker.md) — ps / stop / rm / logs
- [`oaiso run`](run.md) — 並列ワーカーの起動
- [ホスト管理のエージェントポリシー](../guides/agent-policies.md)
