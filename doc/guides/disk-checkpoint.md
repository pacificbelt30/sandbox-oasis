# disk checkpoint

**日本語** | [English](../en/guides/disk-checkpoint.md)

- 利用可能：**未リリース（main）**。対応するCLI・node・Proxyが必要です。
- 停止済みKata VMのroot filesystemを保存し、**同じnode上の新しいVM**へ復元します。
- メモリ上の処理、接続、実行位置を保存する機能ではありません。

## 保存するもの・除外するもの

| 対象 | 扱い |
|---|---|
| root filesystemの通常ファイル | インストール済みツール、`/opt`などのデータを保存。削除済みファイルも復元後は存在しない |
| ファイルの基本属性 | 権限、数値UID／GID、更新日時、symlinkを保持。hard linkは独立したファイルへ変換 |
| 読み取り禁止にしたpath | 下のdiskに元データがあっても保存しない。別のpathへのhard linkも除外 |
| `/workspace`・追加の共有mount | **内容を保存しない**。復元時に新しい共有元を指定。ホスト側のファイルを巻き戻さない |
| `/home`・`/root` | 内容を保存しない。認証情報・履歴・キャッシュ・ユーザー単位の依存も除外。ホーム直下の空ディレクトリと所有権だけ保持 |
| `/run`・`/tmp`・`/var/tmp`・`/dev`・`/proc`・`/sys` | 内容を保存しない。tmpfs、起動時の管理ファイル、カーネル情報も除外 |
| `/var/log`・hosts／hostname／resolv.conf | 保存しない。新しいVMの実行時に必要な設定を作る |
| 環境変数・起動引数・認証・通信規則・利用上限・jobs | コピーしない。新しい実行の設定と認証を使用 |
| 拡張属性・ACL・file capability・特殊ファイル | 保存しない。完全なblock diskの複製ではない |

- 除外ファイルへのhard linkも除外します。symlinkを含む除外パスはrootfs内の実際の宛先も除外します。
- `--exclude`で任意の秘密ファイル・ディレクトリを追加できます。既定の除外は解除できません。
- **任意の場所に利用者が書いた秘密を自動判別する機能はありません。** 保存前に追加除外を指定してください。
- 認証Proxyの実APIキー・OAuth・Store・台帳は対象VMのdiskにありません。Proxy・認証専用VM・隔離停止済みVMは保存対象にできません。

## 保存する

前提：通常ユーザーでlogin・init済み。`source-job`は自分の保持中のワーカー名です。

1. アプリの保存・DBの停止などを完了します。複数ファイルの整合性はアプリ側で確保してください。
2. ワーカーを停止します。保存操作は稼働中・一時停止中のVMを自動停止しません。
3. checkpointを作成します。

```bash
oaiso stop source-job
oaiso checkpoint create source-job --exclude /opt/app/private.env
```

出力例（主要項目のみ。ID・容量は例）：

```json
{"format":"oaiso-disk-v1","id":"64桁のcheckpoint ID","image":"docker.io/library/oaiso-checkpoint:...","digest":"sha256:...","bytes":734003200,"entries":24000,"excluded":["/home","/workspace","..."]}
```

- 出力された`id`を控えます。保存元は保持し、保存中はそのVMの開始・停止・削除を拒否します。他のVMの管理は継続できます。
- SIGKILLや強制停止後は、アプリの処理途中のデータが残り得ます。停止確認はアプリの正常な保存完了を保証しません。
- 完成前の失敗・容量超過・取消ではcheckpointを公開しません。公開と応答の間の失敗では完成している可能性があります。再試行前に`list`で確認します。

## 一覧・確認・復元

```bash
oaiso checkpoint list
```

次のコマンドはBashで実行。問い合わせ後に`checkpoint_id`へ保存済みIDを入力します。

```bash
read -r -p 'Checkpoint ID: ' checkpoint_id
oaiso checkpoint inspect "$checkpoint_id"
oaiso run --checkpoint "$checkpoint_id" --name restored-job \
  --project /home/alice/new-project --detach --keep --exec 'sleep 300'
```

- `/home/alice/new-project`は存在する自分のディレクトリへ置き換えます。元のworkspaceの内容は自動転送しません。
- 追加mount・認証profile・モデル制限・通信規則・利用上限も、新しい実行に必要な値を指定します。
- 保存時のagent・依存はdisk上の版で復元します。agentやアプリの新版との互換性は別途確認してください。
- 新しいVM ID・ネットワーク・認証プレースホルダーを発行し、通常と同じホスト／guest検証後に処理を開始します。
- `--image`・位置引数のイメージ・`--template`・`--environment-lock`との併用は拒否します。Kata専用です。
- 既存のワーカーを上書きしたり、古い利用上限を復元して残量を払い戻したりする機能ではありません。復元は別の実行です。

## 容量・削除・互換性

| 項目 | 条件 |
|---|---|
| 1件のアーカイブ | 既定4GiB。`create --max-bytes`で1MiB〜4GiBへ縮小。最大500,000 entries |
| node内の合計 | 公開済みアーカイブの論理サイズで16GiB、最大1,024件。作成・削除は1操作ずつ |
| 実際の空き容量 | 上記はホストdisk全体のquotaではない。元のイメージ、展開先snapshot、containerdの一時contentにも空きが必要 |
| 保存先 | 専用containerd namespaceのローカルOCIイメージ。registryへ自動送信しない |
| 取消・障害 | 未公開contentはlease解放後にcontainerd GCの回収対象。削除直後の物理容量回復は保証しない |
| node再起動 | 完成済みcheckpointは保持。保存中の処理は再開せず、新たに作成する |
| 復元互換性 | `oaiso-disk-v1`、Linux amd64、現nodeと同じ標準entrypoint。現在のKata・guard・隔離検証を適用 |
| 対象範囲 | 同一nodeの新規VM。別nodeへの転送、RAM snapshot、実行中streamの再開は提供しない |

使わなくなったcheckpointの削除：

```bash
read -r -p 'Checkpoint ID: ' checkpoint_id
oaiso checkpoint rm "$checkpoint_id"
```

- 稼働中・停止中を問わず、そのイメージを参照するワーカーが残っている場合は削除を拒否します。必要な成果を回収し、そのワーカーを削除してから再実行します。
- 失敗時は`checkpoint inspect`・`list`を確認。起動失敗は`oaiso status`・`runtime-check`で調べます。必要なライブラリやアプリ設定を除外すると、復元したアプリが動かないことがあります。
- 管理socket・同一ホストのroot/operatorは信頼対象です。暗号化バックアップや改ざん耐性のある保管庫ではありません。

## Go API

| 操作 | API |
|---|---|
| 保存 | `client.Checkpoint(ctx, workerID, cxd.CheckpointOptions{Exclude: []string{"/opt/app/private.env"}})` |
| 一覧／確認 | `client.Checkpoints(ctx)`／`client.InspectCheckpoint(ctx, checkpointID)` |
| 復元 | `client.RestoreCheckpoint(ctx, checkpointID, newLaunchRequest)`。新しい`RequestID`・workspace・認証identityを指定 |
| 削除 | `client.RemoveCheckpoint(ctx, checkpointID)` |
| 機能照会 | `client.Capabilities().Features`の`disk-checkpoint-v1` |

- 旧nodeでは制限を省略せず非対応エラーにします。
- 保存の応答消失は結果不明です。保存元ID・作成時刻を一覧で照合してから再実行してください。

## 保存期限・容量を確認して回収する

- 利用可能：**未リリース（main）**。
- nodeの合計上限は従来どおり16GiB。`--keep-bytes`は回収後の目標容量です。将来の作成を指定容量で止める設定ではありません。
- 容量は論理的なtarのバイト数。containerdの共有content・展開先・ホスト全体の使用量とは異なります。

### 残すcheckpointを保護する

IDは`oaiso checkpoint list`で確認します。

```bash
read -r -p 'Checkpoint ID to protect: ' checkpoint_id
```

```bash
oaiso checkpoint pin "$checkpoint_id"
oaiso checkpoint inspect "$checkpoint_id"
```

- `pinned: true`なら保護済みです。
- 保護の解除：`oaiso checkpoint pin "$checkpoint_id" --unpin`。
- 保護は自動回収用です。IDを明示した`checkpoint rm`は禁止しません。

### 削除予定を確認する

例：7日より古いものを回収し、保存済みアーカイブを合計8GiB以下へ減らす場合。

```bash
oaiso checkpoint prune --max-age 168h --keep-bytes 8589934592
```

| 出力 | 意味 |
|---|---|
| `delete` | 削除予定のID。古いものから選ぶ |
| `protected` | pin済み、または保持中ワーカーが同じイメージdigestを使用 |
| `remaining_bytes` | 削除後の見込み容量 |
| `target_met` | 容量目標を達成できるか。保護対象が多い場合はfalse |
| `id` | この削除計画のID |

### 確認した計画を適用する

```bash
read -r -p 'Checkpoint retention plan ID: ' retention_plan_id
```

```bash
oaiso checkpoint prune --max-age 168h --keep-bytes 8589934592 \
  --apply --plan-id "$retention_plan_id"
oaiso checkpoint list
```

- `--apply`なしでは削除しません。定期回収はこのCLIを外部のスケジューラー等から明示的に実行します。
- 適用前に計画を作り直します。新しい保存・pin・ワーカー参照・期限到達で計画が変わった場合は拒否します。
- 途中失敗時は一部が削除済みの場合があります。`list`を確認し、新しい計画を確認してから再実行してください。
- 取得失敗を「未使用」と判断せず、回収を中止します。共有contentへの一括pruneは行いません。
- Go API：`RetainCheckpoints(ctx, cxd.CheckpointRetentionOptions{...})`、`PinCheckpoint(ctx, id, true)`。適用時は`Apply: true`と`ExpectedPlan`を指定します。
