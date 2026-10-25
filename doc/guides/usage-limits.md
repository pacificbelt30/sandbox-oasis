# 実行ごとの利用上限

**日本語** | [English](../en/guides/usage-limits.md)

- 利用可能：**未リリース（main）**。対応するCLI・node・認証Proxyを使用します。
- API・サブスク認証とも、上流資格情報を持つ認証Proxyが制限します。VMへ管理権限や台帳を渡しません。
- 未指定では従来の動作を維持します。上限付き実行を古いProxyへ渡すと、制限を省略せず失敗します。

## 制限できる値

| CLI | 制御 | 正確さ・条件 |
|---|---|---|
| `--max-requests` | 上流へ送る要求の最大数 | 送信前に記録。上流エラーでも要求数を減らさない。モデル一覧・token数確認・brokerも含む |
| `--max-concurrent` | 同時に処理する要求数 | HTTP/SSEの終了まで数える。0は無制限。ただしtoken上限だけを指定した場合の既定は1 |
| `--max-transfer-bytes` | 要求・応答本文の合計byte数 | 上流／VMへ渡す前に合算。ヘッダーは含めない。超過するstreamは途中で終了 |
| `--max-observed-tokens` | 観測済みtoken合計による新規要求の停止 | 合計が上限以上なら次のAI要求を拒否。**事前払いの上限・厳密な金額上限ではない** |

- 各値は0で無制限。設定した上限は同じ実行内で変更できません。
- 対象は認証Proxyを通るAI・brokerです。一般HTTP／CONNECTの通信量には適用しません。
- 最後の推論や、既に始まった並行要求でtoken上限を超えることがあります。失効・切断で課金済み処理を取り消せるとは保証しません。
- tokenの金額換算は行いません。サブスクの残量や提供元の課金額も取得しません。
- 利用上限付き実行のWebSocketは拒否します。Codexは既存のHTTP/SSE設定へ切り替えます。Claude CodeはHTTP/SSEを使用します。
- token上限は対応する固定AI経路だけで利用できます。モデル一覧・token数確認は推論token数へ加算せず、要求数と本文byte数へ含めます。

## CLIで使う

前提：実行ホストで独立プロファイルの`oaiso login`と`oaiso init`を完了。通常ユーザーで実行します。

- 独立loginでは管理シークレットも自動設定します。
- 旧認証モードでadmin secretがないProxyは従来どおり利用上限なしです。上限を使う場合は独立profileへ切り替えるか、`proxy.admin_secret_file`を設定し、ワーカーを回収してProxyを作り直してください。

```bash
oaiso run --name limited-job --detach --keep \
  --max-requests 100 --max-concurrent 2 \
  --max-transfer-bytes 10485760 --max-observed-tokens 50000 \
  --exec 'sleep 3600'
oaiso usage limited-job --json
```

要求をまだ送っていない場合の出力例：

```json
{"policy":{"max_requests":100,"max_concurrent":2,"max_bytes":10485760,"max_tokens":50000},"requests":0,"active":0,"bytes":0,"tokens":0,"unmetered_requests":0,"indeterminate":false}
```

| 結果 | 意味・次の操作 |
|---|---|
| HTTP 429／`usage_limit` | 要求数・同時数・本文量・観測tokenの上限。同時数だけの超過なら処理終了後に再試行 |
| HTTP 503／`usage_state_unavailable` | 保存失敗、または使用量を確定できない実行。予算を自動で初期化しない |
| `unmetered_requests` | 完全なtoken使用量を取得できなかった要求数。0 tokenとは区別 |
| `indeterminate: true` | 処理中の強制終了などで残量が不明。その実行の追加AI要求を拒否 |

- token上限指定時にusage欠落・途中終了・未対応形式を検出した場合も、追加AI要求を拒否します。
- token上限を指定しない場合、token使用量が欠けても要求数・同時数・本文量の制限は継続します。
- 通常の停止・再起動、トークン再発行・復元ではカウンターを戻しません。
- 不明な実行を続けたい場合は成果を回収し、新しいワーカー名／実行IDで新規実行します。台帳削除で既存実行を復元しません。

## Go API・設定

| 指定先 | 項目 |
|---|---|
| Go API | `LaunchRequest.UsagePolicy = &identity.UsagePolicy{MaxRequests: 100, MaxConcurrent: 2, MaxBytes: 10485760, MaxTokens: 50000}` |
| 使用量の照会 | `client.Usage(ctx, workerID)`。認証情報は返さない |
| 外部Store用Proxy | `CaseProxyOptions.UsageStateDir`に既存workspace外の所有者専用ディレクトリを指定 |
| ホスト設定・`oaiso.toml` | `[run] max_requests`、`max_concurrent`、`max_transfer_bytes`、`max_observed_tokens` |
| 単独のProxy | `proxy serve --usage-state-dir PATH`。別のadmin listenerとadmin secretが必要 |

- CLIが作る台帳は`~/.local/state/codex-dock/usage/<Proxy名のhash>/`。ディレクトリ0700、ファイル0600。
- 保存するのはhash化した実行ID・上限・カウンター・不明状態だけです。APIキー・OAuth・要求内容は保存しません。
- Proxyごとに台帳を排他利用。起動時に壊れた形式・不正な権限・symlinkを拒否します。
- nodeは台帳とworkspace／追加共有の重なりを拒否。既存VMの共有先へ後から台帳を作ることも拒否します。
- 保持は最大10,000実行・8MiB。満杯や保存失敗は新たな上限付き実行を拒否します。整理は全実行を止め、必要な記録を退避し、新規実行へ切り替えて行います。
- host root・同じUIDからの台帳改変を防ぐ仕組みではありません。既存の[ホストの信頼範囲](../security.md)を維持します。
