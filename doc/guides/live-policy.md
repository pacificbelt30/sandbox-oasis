# 通信許可を確認して即時変更する

**日本語** | [English](../en/guides/live-policy.md)

- 利用可能：**未リリース（main）**。
- 対象：この機能を含む版で作成した一般通信用Proxy。
- 実行場所：ホスト。既存nodeの管理権限が必要です。
- **Proxyを共有する全ワーカーが対象です。既存の一般HTTP・CONNECT接続はすべて切れます。**

## 変更される範囲

| 対象 | 動作 |
|---|---|
| 一般HTTP・CONNECT | 古い接続の終了を確認し、新しい規則で再接続 |
| HTTP内のSSE・CONNECT内のWebSocket | 上と同じ。streamも終了 |
| AI認証Proxy | 対象外。固定AI経路の認証・モデル制限は継続 |
| ワーカー・ジョブ・プレビュー | 実行を継続。通信を必要とする処理は接続切断で失敗する場合あり |
| private・metadata・DNS検査 | 維持。変更で無効化できない |
| ホストの設定ファイル | 書き換えない。変更するのは選んだProxyの規則 |
| 規則の保存 | Proxy内の0600ファイル。プロセス再起動後も復元。Proxy削除で消える |

## 1. 許可候補を作る

`dev-worker`が利用するProxyへ、完全一致の`api.example.com:443`を許可したい場合：

```bash
oaiso policy propose api.example.com:443 \
  --worker dev-worker --output ./policy-proposal.json
cat ./policy-proposal.json
```

直近の保存済み拒否イベントから作る場合は、代わりに次を実行します。

```bash
oaiso policy propose --worker dev-worker --output ./policy-proposal.json
cat ./policy-proposal.json
```

- どちらか一方を実行します。出力ファイルは上書きしません。
- ここでは通信を許可しません。候補ファイルが申請に相当します。
- ドメインは完全一致で追加。既存の拒否・ポート制限は解除しません。
- IP許可、拒否の解除、ポート許可範囲の拡大は候補の自動作成では行いません。必要なら`Rules`全体を手動で確認・編集します。
- 拒否履歴は共有Proxy単位です。別ワーカーの拒否も含むため、申請元の本人確認には使えません。
- DNSによるIPの検査は実際の接続時に行います。候補作成だけで接続成功を保証しません。

## 2. 変更内容と対象を確認する

```bash
oaiso policy plan --worker dev-worker --file ./policy-proposal.json
```

| 出力 | 確認内容 |
|---|---|
| `before` | 現在の規則・世代・適用状態 |
| `proposed` | 変更後の規則全体 |
| `workers` | 現在Proxyを共有しているワーカーID |
| `connections_closed` | このProxyの既存HTTP・CONNECT接続を切ること |
| `id` | 確認した変更計画のID |

- CIDR・ドメイン・拒否規則の組合せがあるため、許可範囲の拡大・縮小を機械的に保証する表示ではありません。前後の規則全体を確認してください。
- 承認はnode権限を持つ呼び出し元が行います。plan IDは変更内容・対象・世代の照合に使います。
- ワーカー一覧は確認時点の情報です。適用はProxy全体に行い、その間に接続したワーカーにも及びます。

## 3. 確認した計画を適用する

前の出力の`id`を入力します。

```bash
read -r -p 'Policy plan ID: ' policy_plan_id
```

```bash
oaiso policy apply --worker dev-worker \
  --file ./policy-proposal.json --plan-id "$policy_plan_id"
oaiso policy show --worker dev-worker --json
```

- 送信済みのデータや上流で開始した処理は取り消せません。
- 成功条件：終了コード0、応答の`ready: true`、世代の増加。
- 計画や世代が古い場合は拒否します。再度手順2から確認してください。
- 保存や切断確認に失敗した場合は、完了と表示しません。途中で変更済みの可能性があるため、再送前に`show --json`を確認します。
- `update_pending: true`の間は新しい一般通信を拒否します。同じ変更を完了する場合は、元の候補ファイルで新しい計画を確認して再適用できます。別の古い候補は拒否します。
- 壊れた保存規則を読み込んだ場合、起動引数の古い許可へ戻さずProxyの起動を失敗させます。

## 既存環境とGo API

| 条件・操作 | 対応 |
|---|---|
| 古いProxy | 即時変更を拒否。ワーカーの成果物を回収し、ワーカーとProxyを停止・再作成して機能を有効にする |
| 変更済みProxyへの新規割当 | 元の起動規則と違う場合は拒否。新しい規則用のProxyを選ぶ |
| 管理経路 | nodeのExec → Proxy内の0600 Unix socket。管理用TCPポートは追加しない |
| 監査 | `permission_change`の意図・結果をProxy内に保存。規則やtokenの値は記録しない |
| Goの候補作成 | `client.ProposePolicy(ctx, workerID, destination)` |
| Goの確認 | `client.PlanPolicyChange(ctx, workerID, cxd.EgressPolicy{...})` |
| Goの適用 | `client.ApplyPolicyChange(ctx, workerID, policy, plan.ID)` |

- 設定ファイルを編集するだけでは既存Proxyへ反映しません。
- 作成時のRequestID・要求内容は変えません。同じ作成要求を再送しても、即時変更した規則は元に戻りません。実際の規則は`policy show`で確認します。
- ワーカー全体を止める緊急操作は、従来どおり`oaiso policy quarantine --worker dev-worker`です。解除・再起動はできません。
