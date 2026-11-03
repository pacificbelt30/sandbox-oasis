# 容量を制限したworkspaceと復元点を使う

**日本語** | [English](../en/guides/managed-workspaces.md)

利用可能：**未リリース（main）**。ホストの元ファイルを共有せず、専用ディスク上で現在の作業内容を試せます。停止したVMは、rootfsとworkspaceを組にした復元点へ保存できます。

## 前提と保存範囲

- 管理者が[quota対応の専用ストレージ](storage-migration.md)を準備し、CLI・node・guard・loggerを同じソース版バンドルで配置します。古いnodeでは利用できません。
- 操作はノード管理グループの通常ユーザーで行います。管理ソケットに接続できるユーザーは信頼されたホスト管理者です。
- [TUIの起動とイメージ準備](sandbox-ui.md)を先に行います。既存のホスト共有・独立Gitコピーも引き続き使えます。

| 方式 | `/workspace`の内容 | 容量・削除 |
|---|---|---|
| Share host project | 元のホストフォルダー | ホストの容量。書き込みは元データに反映 |
| Independent Git copy | 確認したコミットの独立コピー | ホストの容量。VM削除後もコピーは残る |
| Managed current files (bounded) | 確認した現在の通常ファイル | 固定容量・inode上限の専用ディスク。VM削除後も残る |
| Existing managed workspace | 使用中でない管理workspace | 同じディスクを再利用。所有UID/GIDを維持 |

ホストはゲストが使ったファイルシステムをマウント・解析しません。ゲストへ有限のext4ブロックディスクを渡します。workspaceの上限はrootfs quotaとは別です。追加mountは別途ホストのデータを公開するため、通常の共有と同様に確認してください。`Project`は管理workspaceでもホスト・プロジェクト設定の解決に使います。

## 現在のファイルから作成する

1. `oaiso ui`の`N`で作成画面を開きます。
2. `Workspace source`を`Managed current files (bounded)`にします。容量とinode上限を指定します。`New copy directory`は不要です。
3. `Review`／`Ctrl-R`で、コピー対象・各SHA-256・除外理由・所有UID/GID・容量を確認します。`↑↓`／`PgUp`／`PgDn`でスクロールできます。
4. `Tab`で`Confirm`へ移動し、承認します。`Created sandbox`が出たら一覧の`C`で接続します。キャンセルではディスクを作りません。

| コピー対象 | 扱い |
|---|---|
| 未コミット・未追跡・ignored・Git管理外の通常ファイル | 現在の内容をコピー。Gitは実行しない |
| `.git`・リンク・特殊ファイル | コピーしない。hardlinkも除外 |
| `.env*`・`.ssh`・`.aws`・`.azure`・`.codex`・`.claude`・`.config`・認証JSON・秘密鍵など | 既知のパスを除外。任意ファイル内の秘密は検出できない |
| `node_modules`・`.venv`・`venv`・`__pycache__`・`.cache`・`lost+found` | 除外。必要な依存はVM内で再構築 |
| 64MiB超のファイル | 除外理由を表示 |
| 空ディレクトリ・拡張属性・所有者・特殊な権限 | 引き継がない。ファイルは0644、実行ビットがあれば0755。所有者はVMに合わせる |
| レビュー後に変更された対象ファイル | hash・サイズの不一致で取り込みを拒否。作成済みディスクは一覧へ残す |

- コピーは最大8192ファイル、合計はディスク容量の半分か512MiBの小さい方です。inodeに対してディレクトリや管理領域の余裕も必要です。
- 容量は64MiB刻みの64MiB〜4GiB、既定1GiB。inodeは1024〜65536の2のべき乗、既定16384です。管理workspace・snapshotの予約合計は16GiB、最大128件です。
- ホストの空きを先に確保します。空き不足なら作成を拒否します。VM内で使い切ると`ENOSPC`になります。元の共有フォルダーや追加mountの容量はこのディスクで制限しません。
- 所有者は新規コピーで`current`・`codex`・`dir`・数値`UID:GID`を解決し、レビューに表示します。rootは拒否します。既存ディスク・復元コピーでは保存した数値所有者を使います。

## 使用量・再利用・削除

| 操作 | 意味 |
|---|---|
| 一覧の`L` | 固定容量・inode上限・保持VMを表示。稼働中はゲスト観測の使用量も表示 |
| 一覧の`M` | workspaceとsnapshotの状態、予約容量、VM leaseを一覧表示 |
| 使用中でないworkspaceを選択→`Use in new VM…` | 同じディスクを新規VMへ接続。停止VMが保持している間は再接続不可 |
| VMを`D`で削除 | VMのleaseを解放。workspaceのデータは残す |
| `M`で対象→`Delete permanently…` | 確認後にディスクを永久削除。VM・復元点の参照があれば拒否 |

`L`の使用量はゲストからの観測であり、ホストの予約容量の証明ではありません。停止中の使用量は不明と表示し、固定予約は継続します。VMの保持期限による削除でもworkspaceは残ります。

名前付き環境には方式・容量・inode上限を保存できます。workspace ID・復元点・所有者・コピー計画は保存せず、読み込み後に再選択・再レビューします。

## 変更を回収する

管理workspaceの`W`は、最初の取り込みmanifestに対する通常ファイルの追加・変更・削除を表示します。復元コピーでも比較基準は最初の取り込みです。秘密用パス・cache・リンクは比較しません。1ファイル64MiB・合計512MiB・8192ファイルを超える比較はエラーです。

`Export selected files…`で選んだファイルだけを新しい0600のtarへ保存できます。管理workspaceではGitを必要とせず、`head`は`workspace:取り込みmanifest ID`、`changes.patch`は空です。上限・レビュー後の変更拒否は[成果物の回収](artifacts.md)と同じです。個別ダウンロードは`F`でも使えます。ホストの元ファイルへ自動適用しません。

## 停止VMを保存・復元する

1. 元VMを`S`で停止します。停止したVMを選び、`K`／`Save recovery point…`を開きます。
2. 名前を付け、管理workspaceがある場合は`Include managed workspace`を選びます。追加のrootfs除外パスも指定できます。
3. `Review`で保存範囲を確認して承認します。保存中は元VMの起動・削除を拒否します。
4. 一覧の`Z`で`ready`の復元点を選び、`Restore as new VM…`から新しい名前・通信・資源設定をレビューして作成します。

| 保存するもの | 保存しないもの・復元の動作 |
|---|---|
| フィルター済みrootfs checkpoint | `/home`・`/root`・`/run`・一時領域・ログ・すべてのbind mountなどは除外。詳細は[disk checkpoint](disk-checkpoint.md) |
| 選択した管理workspaceのディスク全体 | workspace内で追加した秘密も含む。既知パスの除外は初回コピー時のみ |
| 保存時の所有者・資源プロファイル名 | 元の実行ID・RAM・ホスト共有の内容は復元しない。認証・通信・起動検証は新しいVMで再設定 |

- 復元は新しいVMと新しいworkspaceを作ります。元VM・元workspace・元ホストファイルを巻き戻しません。rootfsのみの点では、ホスト共有の現在の内容を通常の作成レビューで選びます。
- workspaceのsnapshotは直接VMへ接続せず、SHA-256を検査してコピーします。ホストでゲストのext4を開きません。
- nodeが途中で再起動した点は`failed`として残します。`empty`・`restoring`・`failed`を再起動可能な完成品として扱いません。残った資源を一覧で確認してから削除・再作成します。
- 復元点の削除は関連rootfsとsnapshotを削除します。保持VMがrootfsを使用していれば拒否します。復元で作ったworkspaceは別資源として残ります。
- 復元点は同じノード内の復元機能です。ホスト故障に備えた外部バックアップや、RAMを含むVM snapshotではありません。

## CLIで同じ操作をする

通常ユーザーで、コピー元のディレクトリから実行します。まず読み取り専用の計画を確認します。

```bash
oaiso workspace plan . --bytes 1073741824
```

出力の`Manifest.files`と`Skipped`を確認し、`Manifest.id`を入力します。

```bash
read -r -p 'Reviewed copy plan ID: ' workspace_plan_id
```

```bash
oaiso workspace create . --name experiment --bytes 1073741824 \
  --inodes 16384 --plan-id "$workspace_plan_id"
oaiso workspace list
```

出力の`id`を入力します。状態が`ready`でleaseが空であることを確認します。

```bash
read -r -p 'Managed workspace ID: ' workspace_id
```

```bash
oaiso run --managed-workspace "$workspace_id" --runtime kata \
  --agent shell --no-ai --no-internet --image codex-dock:latest
```

`--managed-workspace`はCLI専用で、ホスト・プロジェクト設定へ保存しません。既存worktree／cloneや`/workspace`と重なる追加mountは併用できません。CLIではVMの`--user UID:GID`をディスクの所有者に合わせてください。ゲストのデータをホストで開かずにパスの実体を検査できないため、管理workspaceではパス別の`deny_read`／`deny_write`を拒否します。全体の`--read-only`か従来の共有／コピー方式を使ってください。

保存・一覧・削除のコマンドは`oaiso recovery-point --help`、workspaceの再利用・snapshotコピー・削除は`oaiso workspace --help`で確認できます。通信断・起動失敗は、再試行前に一覧の名前・ID・lease・不完全な状態を確認してください。
