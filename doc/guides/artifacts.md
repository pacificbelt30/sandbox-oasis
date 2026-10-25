# 成果物をまとめて回収する

**日本語** | [English](../en/guides/artifacts.md)

- 利用可能：**未リリース（main）**。
- 対象：起動中のワーカー。workspaceはHEADのあるGitリポジトリで、Gitメタデータを利用できる必要があります。ホスト側で `oaiso` を操作します。
- 指定したファイル、指定範囲のGit差分、内容一覧をtarに保存します。

## 保存される内容

| 内容 | 範囲 |
|---|---|
| ファイル | `--include`で選んだ通常ファイル。未追跡ファイルも指定可 |
| `changes.patch` | 選んだパスだけの、HEADからの変更。Gitの外部diff・textconvは実行しない |
| `manifest.json` | ワーカーID、イメージID、runtime、Git HEAD、パス、容量、SHA-256。`--job`で選んだジョブのID・実行中かどうか・終了コード |
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
