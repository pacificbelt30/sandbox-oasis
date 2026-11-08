# 成果物をまとめて回収する

**日本語** | [English](../en/guides/artifacts.md)

- 利用可能：**v0.0.4**。
- 対象：起動中のワーカー。通常のworkspaceはHEADのあるGitリポジトリで、Gitメタデータを利用できる必要があります。
- 未リリース（main）：[管理workspace](managed-workspaces.md)ではGit不要です。`head`は`workspace:取り込みmanifest ID`、`changes.patch`は空です。選択内容のhash・上限・レビュー後の変更拒否は同じです。ホスト側で `oaiso` を操作します。
- 指定したファイル、指定範囲のGit差分、内容一覧をtarに保存します。

## 保存される内容

| 内容 | 範囲 |
|---|---|
| ファイル | `--include`で選んだ通常ファイル。未追跡ファイルも指定可 |
| `changes.patch` | 選んだパスだけの変更。既定は現在のHEAD、`--since-start`なら起動時点のcommitが基準。Gitの外部diff・textconvは実行しない |
| `manifest.json` | ワーカーID、イメージID、runtime、Git HEAD、パス、容量、SHA-256。`--since-start`では比較基準の`base`も含む。`--job`で選んだジョブのID・実行中かどうか・終了コード |
| 上限 | 256ファイル・32ジョブ。ファイルと差分の合計32MiB。パスはUTF-8で94バイトまで。指定情報の合計64KiBまで |
| ホスト側の上限 | VMの出力を信用せず、計画は1MiB・tarは指定容量＋1MiBで中止 |
| 保存しないもの | ディレクトリ全体、symlink、環境変数、既知のパスにある認証設定、実行ログ |

- `.git`・`.ssh`・`.aws`・`.codex`・`.claude`・`.env*`・認証JSON・秘密鍵などの既知のパスは拒否します。
- 任意のファイルに書かれた秘密を自動で見つける機能ではありません。保存する内容は利用者が選びます。
- 共有workspaceへのホスト側の変更も含まれます。ワーカー削除前に回収してください。

## 1. 内容と容量を確認する

例：`dev-worker`の`src/main.go`と`result.json`を回収する場合。

```bash
oaiso artifact plan dev-worker \
  --include src/main.go \
  --include result.json
```

出力の確認項目：

| 項目 | 確認内容 |
|---|---|
| `files` | 選んだパス・容量・内容のhash |
| `patch_bytes`・`total_bytes` | 差分と合計の容量 |
| `id` | この内容を保存するときのplan ID |

## 2. 確認した内容を保存する

前の出力の`id`を入力します。

```bash
read -r -p 'Artifact plan ID: ' artifact_plan_id
```

```bash
oaiso artifact bundle dev-worker \
  --include src/main.go \
  --include result.json \
  --plan-id "$artifact_plan_id" \
  --output ./dev-worker-artifacts.tar
tar -tf ./dev-worker-artifacts.tar
```

```text
manifest.json
changes.patch
files/result.json
files/src/main.go
```

- ジョブの状態・終了コードも保存する場合は、確認と保存の両方に`--job JOB_ID`を追加します。ログ・引数・環境変数は含めません。`running: true`の終了コードは未確定です。
- 新しいファイルを0600で作成します。既存ファイルは上書きしません。
- 内容・差分が変わった場合は保存を拒否します。手順1から再確認してください。
- 失敗した未完成の出力ファイルは削除します。自動展開・自動commitは行いません。

## Go API

| 操作 | メソッド |
|---|---|
| 確認 | `client.PlanArtifacts(ctx, workerID, cxd.ArtifactOptions{Paths: paths})` |
| 保存 | `client.ExportArtifacts(ctx, workerID, options, writer)`。`ExpectedPlan`に確認済みIDを指定 |

- Python・TypeScriptでは、個別ファイルの回収に[ファイル操作SDK](../local-api-sdk.md#ファイルジョブcheckpointを操作する)を使えます。tar出力はCLI／Go APIを使います。

- Go APIのwriterには、エラー時に途中まで出力されている場合があります。一時ファイルで受け、成功した場合だけ採用してください。

## 起動時点からの変更を回収する

利用可能：**未リリース（main）**。Git workspaceの新規VMでは、ユーザー処理を始める前に記録したcommitを比較基準にできます。ゲスト内でcommit済みの変更もpatchへ含めます。指定しなければ従来どおり現在のHEADと比較します。

対応する開発版のCLI・node・補助ツールを[準備](sandbox-ui.md#前提と起動)し、Git workspaceを持つ稼働中の`dev-worker`を対象にホストで実行します。VM名とファイル名は自分の対象へ置き換え、確認と出力の両方に`--since-start`を指定します。

```bash
oaiso artifact plan dev-worker --include src/main.go --since-start
```

出力の`id`を読み取り、次の入力へ貼り付けます。`base`が起動時点のcommit、`head`が確認時のHEADです。

```bash
read -r -p 'Artifact plan ID: ' artifact_plan_id
```

```bash
oaiso artifact bundle dev-worker --include src/main.go --since-start \
  --plan-id "$artifact_plan_id" --output ./dev-worker-since-start.tar
```

- 停止・再起動でも比較基準を保持します。既存VMに現在のHEADを後付けで採用しません。基準がない場合やGit objectが消えた場合は失敗として返します。
- 管理workspaceは初回importハッシュ比較を使い、このGit専用フラグは受け付けません。
- TUIでは[`W`→`Since launch`→`Browse changes…`](sandbox-ui.md#起動時点との比較とファイル別レビュー)からファイルを選び、同じ比較基準でレビューして保存できます。
- Go APIは`ArtifactOptions.SinceLaunch = true`を指定します。ファイル・差分・HEAD・比較基準が変われば、新しいレビューが必要です。

| 失敗・表示 | 次に確認すること |
|---|---|
| `launch Git baseline unavailable` | 起動時点を記録できた新規VMか確認。既存VMでは現在HEADによるレビューを明示的に選ぶか、対応版で新規VMを作成 |
| Git比較に失敗・基準objectがない | 基準commitをゲスト内で参照できるか確認。参照できない場合は比較方式を選び直して再レビュー |
| レビュー後の変更で出力を拒否 | ファイル・比較方式・`--job`をそろえてplanを取り直し、新しいIDと未使用の保存先で再実行 |
