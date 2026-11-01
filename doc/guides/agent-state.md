# CodexのSQLite状態を保持する

**日本語** | [English](../en/guides/agent-state.md)

利用可能：**v0.0.7**。対応するCLI・node・worker-guardへ更新後、新規作成した通常のKataワーカーが対象です。既存ワーカーの保存先やデータを自動移行しません。

通常のHOMEを使いながら、CodexのSQLite DBだけをワーカー専用ext4ディスクへ保存します。共有workspaceの整合性設定は維持します。HOME内の設定・認証用プレースホルダー・会話ファイルの場所は変えません。

## 保持される期間

| 操作・条件 | 保存内容 |
|---|---|
| 同じワーカーの停止・再起動 | SQLite DBと通常HOMEを保持 |
| nodeプロセスの再起動 | 保持。実行用メタデータが失われた場合は同じディスクから再生成 |
| ワーカー削除・通常runの終了時削除 | 専用ディスクも削除。残したい場合は`--keep`を指定 |
| 別ワーカーの作成 | 別の空ディスク。名前が同じでも履歴を引き継がない |
| `--read-only-rootfs` | 専用ディスクを作らず、従来どおり一時HOME。VM停止で状態を失う |
| 認証専用VM・runc | 専用ディスクの対象外 |
| rootfs checkpoint | 専用ディスクの内容を含めない。[保存対象](disk-checkpoint.md)を確認 |

## 対話操作と再起動

前提：通常ユーザーとしてホストでlogin・init済み。新しいワーカー名`codex-work`を使います。この例は、接続用のワーカーを明示的に保持します。

1. ホストでワーカーを作り、shellへ接続します。

```bash
oaiso run --agent shell --name codex-work --detach --keep --exec 'sleep infinity'
oaiso connect codex-work
```

2. VMのshellでCodexを実行します。

```bash
codex
```

3. Codexを終了した後、VMのshellで`exit`してホストへ戻ります。接続を閉じてもワーカーは稼働しています。

4. ホストで停止し、同じワーカーを再起動して接続します。

```bash
oaiso stop codex-work
oaiso connect --start codex-work
```

5. VMのshellで会話を再開します。

```bash
codex resume --last
```

フォルダーの信頼確認が表示されたら、対象を確認して操作します。`codex exec`で作った非対話の履歴を選ぶ場合は、固定版Codexでは`codex resume --last --include-non-interactive`を使います。

不要になったら、VMのshellを抜けてホストで削除します。HOMEと専用DBディスクも削除されます。

```bash
oaiso stop codex-work
oaiso rm codex-work
```

## 容量と設定

| 設定 | 意味 |
|---|---|
| 既定・`auto` | 新しい通常Kataワーカーごとに512MiBを確保 |
| `--agent-state-size 1GiB` | 作成時容量を変更。整数の64MiB〜8GiB |
| `--agent-state-size off` | 自分で保存先を管理する場合に無効化 |
| `[run].agent_state_size` | ホスト設定の既定値。明示CLIフラグを優先 |
| Go／ローカルAPIの`LaunchRequest.AgentStateSize` | 同じ指定。既存の空値は既定動作を維持 |

通常ユーザーのホスト設定例：

```toml
[run]
agent_state_size = "1GiB"
```

容量は作成時に固定されます。既存ディスクを自動拡張・縮小・再フォーマットしません。`64MiB`など容量の数値指定は一時HOMEやruncと併用できません。

- DBの既定保存先は`/var/lib/oaiso-agent-state/codex`です。`CODEX_SQLITE_HOME`を既に指定したイメージではその値を保持します。
- 明示したCodexの`sqlite_home`設定は環境変数より優先します。[OpenAI Docs](https://learn.chatgpt.com/docs/config-file/environment-variables)の仕様に従います。FUSE上の保存先を明示した場合のmmap制約は残ります。
- 専用ディスクは所有UIDだけが使えるマウントで、容量分のホスト領域を作成時に確保します。保存暗号化を追加する機能ではありません。

## 失敗した場合

| 状況 | 次の操作 |
|---|---|
| ディスク確保時の容量不足 | ホストの`df -h`と`oaiso ps -a`で確認。不要な自分のワーカーだけを削除するか、新規作成時の容量を減らす |
| VM内のDBディスクが満杯 | 不要な自分のファイルを整理。DBを勝手に削除する自動処理はない。SQLiteのエラーを処理してから再試行 |
| ディスク欠損・差し替え・権限不一致 | 起動を拒否し、データを保持。node管理者が状態・証跡を確認。自動で空ディスクに置き換えない |
| 実行用メタデータの競合・回収失敗 | 削除情報を保持。管理者が内容と所有権を確認してから再試行 |
| 追加マウントが専用DBのパスと競合 | `/var/lib/oaiso-agent-state`と重ならない保存先へ変更。自分でDBを管理する構成なら`--agent-state-size off`を指定 |
| 旧node・旧worker-guard | 対応する組へ更新・再検証したnodeで新規ワーカーを作る |

実Kataの確認条件と未確認事項は[検証記録](../testing.md)へまとめています。

## バックアップと別VMへの復元

利用可能：**v0.0.7**。通常ユーザーとしてホストで実行します。保存元・復元先の専用ディスク容量と数値UID/GIDが同じであることが必要です。以下は既定の512MiBを使う`codex-work`の例です。別の容量なら復元先にも同じ`--agent-state-size`を指定します。

1. アプリを保存し、VMを停止してバックアップします。共有workspaceの外に置いてください。

```bash
install -d -m 0700 "$HOME/oaiso-backup"
oaiso stop codex-work &&
  oaiso agent-state export codex-work --output "$HOME/oaiso-backup/codex-state.backup"
```

2. 新しいワーカーを**起動せず**作成します。`/home/alice/restored-project`は復元したworkspaceへ置き換えます。必要な認証profile・ポリシー・共有も新たに指定します。

```bash
oaiso run --name restored-codex --project /home/alice/restored-project   --agent-state-size 512MiB --create-only --keep --exec 'sleep infinity' &&
  oaiso agent-state restore restored-codex --input "$HOME/oaiso-backup/codex-state.backup" &&
  oaiso start restored-codex &&
  oaiso connect restored-codex
```

- `--create-only`はKata専用。実行前のVMでもネットワーク・snapshot・ディスクなどの資源を保持します。不要になったら`oaiso rm restored-codex`で回収します。
- rootfsも引き継ぐ場合は、作成時に`--checkpoint ID`を追加します。DBとrootfsは同じ停止時点で別々に保存します。[移行手順](storage-migration.md)。
- 稼働中・一時停止中の保存、使用済み・復元済みVMへの復元、UID/GID・容量不一致、長さやhashの不一致を拒否します。復元中のnode停止後は永続化した復元情報から再開します。
- 復元中は、未起動の保存先ディスクに加え、同容量の一時ディスクを確保します。nodeの状態領域にも追加の空きが必要です。
- バックアップは0600で、既存ファイルを上書きしません。ディスク全体を保存するため、削除済み領域や会話履歴などの秘密が含まれる可能性があります。暗号化はしません。
- CodexのSQLite専用領域だけが対象です。HOME内の会話ファイル・設定・認証、workspace、Proxy、トークンは含めません。明示設定でSQLiteの保存先を専用領域外へ変えた場合、そのDBは含みません。
- ゲストのext4はホストでmount・解析せず、新しいKata内だけで使います。ハッシュは作成者の信頼性を保証しません。信頼できる自分のバックアップだけを取り込んでください。
- Go API：`Create(ctx, request)`→`RestoreAgentState(ctx, workerID, path)`→`Start(ctx, workerID)`。書き出しは`ExportAgentState(ctx, workerID, path)`。
