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
| `I` | 全情報をスクロールできる詳細画面（未リリース・main） |
| `F2` / `Space` | 操作メニュー。矢印で選び、`Enter`か各操作のキーで実行（未リリース・main） |
| `Tab` | Actionsボタンへ移動。再度`Tab`で一覧へ戻る（未リリース・main） |
| `Esc` | 一覧の絞り込みを解除。入力中なら編集前へ戻す（未リリース・main） |
| `↑` / `↓` | ワーカーを選択 |
| `Enter` | 選択したワーカーの直近200行のログを取得 |
| `C` | 選択したワーカーへ新しいlogin shellで接続。`exit`でTUIへ復帰 |
| `X` | 選択したワーカーでコマンドを実行し、出力と終了コードを表示 |
| `/` | 名前または `status:` / `branch:` / `task:` / `image:` / `policy:` で絞り込み。`!` は除外 |
| `O` | 名前、状態、新旧、ブランチ、ポリシーの並び順を切り替え |
| `V` | TUI起動中に観測した起動・停止・削除・metadata変更を表示 |
| `N` | [サンドボックスを作成](../guides/sandbox-ui.md)（未リリース・main） |
| `F` | workspaceファイルの閲覧・新規転送（未リリース・main） |
| `U` | 認証付きWebプレビューを管理（未リリース・main） |
| `T` | [タスクと結果レビュー](../guides/tasks.md)を開く（未リリース・main） |
| `S` | 選択したワーカーを停止。停止待ち10秒 |
| `D` | 選択したワーカーを強制削除 |
| `A` | 一覧内の実行中ワーカーをすべて停止 |
| `R` | 停止済みワーカーを再起動 |
| `E` | 選択したワーカーのProxy拒否履歴を取得 |
| `P` | Proxy設定画面を開く |
| `B` | Credential Broker設定画面を開く |
| `G` | ホストのAgent Policyを選択・複製・編集・レビュー（未リリース・main） |
| `?` / `H` | ヘルプを開く |
| `Q` | UIを終了。ログ／拒否履歴画面では一覧へ戻る |

- 英字キー：大文字・小文字の両方を利用可。
- 削除・全停止：確認ダイアログを表示。必要な成果は先に保存。
- `C` の接続：メインAgentへattachせず別execを作成。shell終了後もAgentとワーカーは動作を継続。
- ログと拒否履歴：開いた時点で取得。継続追跡は `oaiso logs WORKER --follow` / `oaiso policy events --worker WORKER --follow`。
- AI routesを使う場合の認証設定：通常のCLIと同じグローバル設定。独立プロファイルと管理シークレットを[先に設定](../guides/authentication.md)。

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

## rootfs容量を確認する

利用可能：**v0.0.7**。詳細欄へホストで観測した書き込み可能rootfsの使用量と上限（byte・inode）を表示します。image・共有workspace・専用DBの総使用量ではありません。照会できない場合は取得不可として`oaiso runtime-check`を案内します。[専用ストレージ](../guides/storage-migration.md#使用量を調べる)。

## キーを覚えずに操作する

未リリース（main）：画面下の`Actions F2`ボタンをクリックするか、`F2`・`Space`を押して操作を選べます。`↑↓`・`Enter`、または表示されたキーを使います。停止中は接続・ファイル・プレビューを出さず、開始を案内します。削除は従来どおり確認を挟みます。

- 110桁未満では詳細欄を畳み、名前・状態・稼働時間を優先します。広げると詳細欄が戻ります。`I`では画面幅に関係なく全情報を確認できます。
- 操作メニューの`Esc`は一覧へ戻ります。一覧の`Esc`はフィルターを解除します。
- メニュー表示中に選択対象が変わった場合、対象を選び直してから操作します。
