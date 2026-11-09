# 性能とベンチマーク

**日本語** | [English](en/performance.md)

- Kataの資産検証、公開Go APIのExec、認証プロキシの処理を別々に計測します。
- [計測結果と実機での回帰確認](testing.md#2026-10-21の性能計測)に、変更前後の値、測定条件、確認済みの範囲を記録しています。

## 高速化の仕組み

### 一覧更新とcheckpoint台帳

- 対象：**v0.0.8収録前のmain**。2026-11-03に測定。
- TUIの一覧更新は1要求ずつ実行し、更新中の連打を次の1要求へまとめます。nodeへの一覧・容量取得は合計3秒を上限とし、容量取得は最大4並列です。容量が取れない行も残して確認不能と表示します。
- checkpoint一覧は、復元点の参照台帳を要求内で1回読みます。各checkpointの所有権・metadata・digestは従来どおり検査し、次の要求では台帳も読み直します。要求をまたぐcacheはありません。

| 条件 | 内容 |
|---|---|
| 比較元 | `659feea9`を基点に、同じbenchmarkを追加した変更前の保存バイナリ |
| 環境 | Linux amd64、Go 1.27.3、GOMAXPROCS=4、warm page cache。他のbuild・試験は停止 |
| 標本 | 各条件3回、1回3操作。変更前→後、後→前、前→後の順で測定した中央値 |
| checkpoint | 実際のprivate参照ファイルとlocal content store。OCI metadataを読む一覧処理で、VM・containerd socket通信・保存本体は含まない |
| TUI | 容量API1回の待ちを1msに固定したfixture。実node・画面描画の速度ではない |

| 処理 | 変更前 | 変更後 | 時間短縮 |
|---|---:|---:|---:|
| checkpoint一覧：8件 | 5.72ms | 0.78ms | 86.3% |
| checkpoint一覧：32件 | 76.00ms | 3.05ms | 96.0% |
| checkpoint一覧：128件 | 1150.83ms | 10.46ms | 99.1% |
| TUIの容量収集：4台 | 4.43ms | 1.20ms | 73.0% |
| TUIの容量収集：20台 | 22.19ms | 5.59ms | 74.8% |

- 128件の一覧は変更前1146.11〜1156.07ms、変更後10.44〜10.52ms。割当量は431.28MiBから2.68MiB／操作へ減りました。RSSの測定値ではありません。
- TUIの20台収集は変更前22.13〜22.21ms、変更後5.54〜5.61ms。最大4個の作業goroutineを使い、割当量は10040から11330bytes／操作へ増えます。
- 製品全体・VM起動・AI応答の改善率を示す値ではありません。証跡：`.kata-verification/product-quality-20261103/`。

### Execの重複検証をまとめる

- 利用可能：**v0.0.4**。
- 1回のExecで重複していた資産検証を、runtime・ホスト検証と同じ要求にまとめます。
- 要求ごとに全資産を読み直し、SHA-256・所有者・パス・権限・設定を確認します。更新時刻を使ったキャッシュはありません。
- ワーカー設定、ホストのプロセス・cgroup、guestのUID・権限・資源制限も確認してからコマンドを実行します。
- `verify-runtime-v1` に未対応のnodeでは、従来の検証手順を使います。対応nodeの検証失敗を旧手順で再試行しません。
- ロック待ちの後で資産を検証し、同じ要求内の監督プロセス確認へ引き継ぎます。所有権・PID・資源上限は引き続き照合します。
- 停止・削除から単独で呼ぶ監督プロセス確認は、その都度独立して資産を検証します。
- 起動時も同じ要求内の重複だけを解消します。VM開始前と開始後の資産検証はそれぞれ行います。
- 資源予約・起動時のbootstrap・認証の有効化順序は従来どおりです。

### 資産検証とProxy認証

- 資産検証は、ファイルごとの所有者・パス・実行権限・SHA-256の確認を、1回の検証につき最大 `min(4, GOMAXPROCS)` 並列で行います。
- 毎回すべての資産を読み、設定との結び付けも確認します。
- [セキュリティ境界](security.md)の検査を維持したまま、空いているCPUを利用します。
- 資産検証の並列化だけでは総ハッシュ量は変わりません。Execの重複解消では、同じ要求内での読直しを減らします。
- CPU飽和時のthroughputや、ディスク読み出しが支配的な環境での改善率は別途計測します。

- 認証プロキシは、トークン値から発行記録を直接検索します。
- ワーカー数に比例する全件走査を避け、発行・復元・失効と同じロック内で索引を更新します。
- 期限・プロバイダー・モデル制限、既存streamの取消は[認証プロキシ](auth-proxy.md)の説明どおりに適用します。

- これらの高速化は実装側で有効になります。
- 専用の有効化フラグはありません。
- 導入は[CLI・管理サービス・プロキシの更新手順](operations.md#cliプロキシを更新する)に従い、Go APIを使う製品もnodeと同じソース版へ更新します。

## 第二回：転送前検査の比較

- 対象：**v0.0.8収録前のmain**、2026-11-03。比較元は第一回完了時の保存ソースで、公開版同士の比較ではありません。
- 同じLinux amd64／Go 1.27.3／GOMAXPROCS=4。各条件300msのbenchmarkを3回、変更前→後、後→前、前→後で測定した中央値です。他のbuild・試験と実Kataノードは停止しました。
- 既存保存先の条件だけ、guest呼出しの待ちを1msに固定したfixtureを使います。新規保存はローカル一時ディレクトリで32KiBの書込・fsync・公開・削除を行います。ネットワークや実VM転送の速度ではありません。

| 条件 | 変更前 | 変更後 | 変化 |
|---|---:|---:|---|
| 既存保存先の拒否（1ms待ちfixture） | 1.201ms | 0.0106ms | 99.1%短縮。guest呼出しと一時保存を省く |
| 新規32KiBファイルの保存 | 0.0416ms | 0.0429ms | 3.1%増、約1.3µsの追加検査 |

- 新規保存の範囲は、変更前0.0412〜0.0420ms、変更後0.0425〜0.0432ms。割当量は693→704bytes／操作です。既存保存先は712→333bytes／操作でした。
- どちらも公開時の排他的な検査と親directoryの保持を維持します。事前検査の後に作られた保存先も上書きしません。
- 通常CLI8条件も各版3回を交互に測り、出力がバイト単位で一致することを確認。条件ごとの中央値は変更前12.99〜13.91ms、変更後12.68〜13.76msでした。小標本の基準値で、一般的なCLI高速化率は主張しません。
- 証跡：`.kata-verification/product-quality-round2-20261103/transfer-benchmark-results.json`と`cli-performance.json`。[回帰確認の範囲](testing.md#第二回の互換性回帰確認2026-11-03)。

## 2026-11-03の実VM点検

- 対象：**v0.0.8収録前のmain**、試験版`quality-a`。現在版だけの基準値で、変更前後の高速化率ではありません。
- Core i5-8500／4 vCPU、Linux amd64、Go 1.27.3、clientのGOMAXPROCS=4。固定Kata 4.3.0／Dragonball、containerd 2.4.2、BuildKit 0.34.0。
- compact：guest 512MiB、host予約768MiB、CPU 1。専用node、準備済みイメージ、warm page cache、一般egress・Git共有なし、架空の資格情報。測定中に他のbuild・試験は実行しませんでした。
- 通常の資産・host／guest検証とbootstrapを含みます。Proxy作成、イメージ取得・import、削除、AI推論は計測外です。Execは`/bin/true`の終了確認までです。

| 対象 | 標本 | 中央値 | 範囲 |
|---|---:|---:|---:|
| 単独起動 | 3回 | 8.484秒 | 8.173〜8.762秒 |
| 2台同時起動：両方完了まで | 3組 | 15.021秒 | 14.757〜15.063秒 |
| 単独Exec | 7回 | 1.145秒 | 1.103〜1.253秒 |
| 2台同時Exec：両方完了まで | 5組 | 1.914秒 | 1.830〜2.031秒 |

- 最後に観測した2VMの`memory.peak`は158.27／157.32MiB、OOM killは両方0。全起動標本のpeakを採取した値ではありません。
- [全機能の受入範囲](testing.md#全機能の品質確認2026-11-03)。証跡：`.kata-verification/product-quality-20261103/native-performance-results.json`。

### 認証・通信規則の単体計測

- 同じGo・CPUでGOMAXPROCS=4、各条件300ms×3回の中央値です。VM計測が終わってから、他のbuild・試験を止めて測りました。ネットワーク・DNS・上流APIは含みません。
- 認証成功は発行記録1件で929.3ns、4096件で1110ns／要求（どちらも656bytes・8割当て）。未発行トークンの拒否は、1〜4096件の条件で128.7〜143.1ns／要求でした。
- 宛先規則10件で144.7ns、1000件で152.2ns、IP規則は22.77ns／操作。いずれも割当て0です。現在版の基準値で、高速化の比較ではありません。
- 証跡：`.kata-verification/product-quality-20261103/proxy-policy-performance.json`。

## 2026-10-25の実測

- 対象：**v0.0.4**。同じ要求内の全資産検証を、Execでは3回から1回へまとめました。
- 変更前のnode CPU profileは、CPU時間の94.0%がSHA-256計算でした。

| 条件 | 内容 |
|---|---|
| ホスト | Linux amd64、Core i5-8500、4 vCPU、KVM |
| ビルド | Go 1.27.3、client・nodeともGOMAXPROCS=4、同じ測定用BuildVersion |
| 実行基盤 | Kata 4.2.0／Dragonball、containerd 2.4.2、BuildKit 0.34.0 |
| 資源 | compact：guest 512 MiB／host予約768 MiB／CPU 1／PID 512。収容上限2台 |
| 比較 | 同じnode・固定資産・workerイメージで、変更前→変更後→変更後→変更前 |
| 標本数 | 各版：単独起動6回、2台同時起動6組、Exec14回、2台同時Exec10組 |
| 実行条件 | warm page cache、準備済みイメージ、一般egress・Git共有・エージェントなし。架空の資格情報のみ |
| 計測外 | clientのOpen、Proxy作成、イメージ作成・import、削除、AI推論 |

- 変更前は `d9c0419e` を土台に直前のP1作業を含むソースの保存版です。公開版同士の比較ではありません。
- 旧新版とも同じCPU profile採取処理を有効にし、別のビルド・試験を並行実行していません。

| 対象 | 変更前の中央値 | 変更後の中央値 | 時間短縮 |
|---|---:|---:|---:|
| 単独起動 | 7.740秒 | 7.103秒 | 8.2% |
| 2台同時起動：2台完了まで | 13.721秒 | 12.744秒 | 7.1% |
| 公開Go APIのExec：`/bin/true` | 2.475秒 | 1.161秒 | **53.1%** |
| 2台同時Exec：両方の完了まで | 3.329秒 | 1.888秒 | **43.3%** |

- Execの範囲：変更前2.362〜3.229秒、変更後1.094〜1.361秒。
- 2台同時Execの範囲：変更前3.197〜3.456秒、変更後1.830〜2.407秒。
- 単独起動の範囲：変更前7.638〜8.080秒、変更後6.904〜7.238秒。
- 工程別の中央値：Execの全資産・ホスト確認0.706秒、guest確認0.203秒。単独起動のVM開始APIは3.670→3.667秒でした。
- VM開始APIにはnode側の待ちも含みます。起動の追加改善と収容台数の増加は、引き続き検討対象です。

### CPU・メモリ・I/Oの費用

| 観測対象 | 変更前 | 変更後 |
|---|---:|---:|
| node CPU時間／Exec | 3.062 CPU秒 | 1.064 CPU秒（65.3%減） |
| nodeの論理読取量／Exec | 1.197 GiB | 0.400 GiB（66.6%減） |
| node＋2VMのCPU時間／Exec | 3.256 CPU秒 | 1.336 CPU秒（59.0%減） |
| 2VMだけのCPU時間／Exec | 0.194 CPU秒 | 0.272 CPU秒 |
| nodeのCPU待ち：Exec14回合計 | 0.883秒 | 0.808秒 |
| Exec区間終了時のnode RSS | 36.79 MiB | 38.84 MiB |
| 2VMの観測メモリ合計 | 313.31 MiB | 312.80 MiB |
| VMのOOM kill | 0 | 0 |

- CPU・読取量はExec区間の合計を回数で割った値、RSS・観測メモリは2区間の終了時の平均です。
- CPU待ちはOSがCPUへ割り当てるまでの待ち時間です。`schedstat`で各区間の開始・終了に存在したスレッドを集計しました。
- VM側のCPU時間とnode RSSは増えました。計測対象のCPU合計は減り、VMの資源予約は変えていません。
- VM単体の観測メモリは155.09〜157.45 MiB、観測peakの最大は157.83 MiBでした。起動瞬間のpeakを全VMで測った値ではありません。
- Exec中のnodeの物理ディスク読取は両版とも0 byteでした。論理読取量の減少は、cold時のディスク高速化率を示しません。
- 起動区間のnode CPU／I/O記録には、標本間の削除も含みます。起動だけの値としては比較していません。
- node CPUと当該VM以外のホスト処理は、CPU合計に含めていません。
- 実AIの応答、cold起動、他CPU・資源プロファイル、3台以上は未計測です。
- [実Kataでの回帰確認](testing.md#2026-10-25の高速化と回帰確認)も参照してください。

## 計測対象を選ぶ

| Benchmark | 計測に含むもの | 計測外のもの |
|---|---|---|
| `BenchmarkWorkerRequest` | 認証ヘッダー解析、発行記録の検索、期限・経路の確認、request contextの生成・解放 | ネットワーク、上流APIへの通信 |
| `BenchmarkLoadPinnedManifest` | 実資産の読込・checksum・所有権・パス・設定の照合 | VM起動、guest内の処理 |
| `BenchmarkKataExec` | Execごとの資産・ホスト・guestの検証、`/bin/true` の実行と終了確認 | VM作成、clientのOpen、warmup、AI推論 |
| `BenchmarkKataLaunch` | 新規ワーカーのLaunch全体と工程別時間。最初のExecは別の値として表示 | clientのOpen、Proxy作成、イメージ作成・import、削除、AI推論 |
| `BenchmarkCheckpointCatalog` | private復元点ファイル・OCI metadataを読む一覧処理 | VM、containerd socket、checkpoint作成 |
| `BenchmarkDashboardQuotaCollection` | 1ms待ちの容量取得fixtureを4台・20台分収集 | 実nodeの測定、TUI描画 |

- Proxyのbenchmarkは1・32・256・4,096件の発行記録で、認証成功と未発行トークンの拒否を測ります。
- Execのbenchmarkに表示されるallocation値はクライアント側の値です。

## 起動のどこに時間がかかるか調べる

- Go APIの `client.LaunchWithTimings(ctx, req)` は、`Worker`、`LaunchTimings`、`error` を返します。
- 通常の `Launch` と同じリクエスト識別・検証・認証・後片付けを使い、計測の有無はリクエストのfingerprintに入りません。
- 通常の `Launch` は計測記録を作りません。

```go
worker, timing, err := client.LaunchWithTimings(ctx, req)
// 失敗時も既存の契約どおり worker.ID を保持して回収する。
for _, phase := range timing.Phases {
    fmt.Printf("%s: %s\n", phase.Name, phase.Duration)
}
```

- 各工程は重複しない経過時間で、合計が `timing.Duration` になります。
- JSONの時間単位はnsです。
- preflight、ネットワーク作成、容量予約、コンテナ作成、作成条件の検証、VM起動、host/guest検証、トークン有効化、bootstrap解放、readiness、要求の完了確認を区別します。
- `vm_start` はnodeの開始API全体であり、純粋なVMM起動時間ではありません。
- `host_guest_verification` は資産再検証・host検証・guest probeを含みます。
- 初回以外の同じRequestIDへの再送では、既存ワーカーの照合など実行した工程だけを返します。
- 失敗時は回収・要求の中断確認も含めた部分計測を返します。

- レポートには固定の工程名と時間だけを記録し、パス・引数・リクエスト値・トークン・エラー本文は含めません。
- 保存や送信は呼出側が選択します。
- 工程名は診断用であり、版ごとに追加・変更され得ます。
- 起動完了・認証の判定には従来どおりAPIの結果を使ってください。

## Execのどこに時間がかかるか調べる

- 利用可能：**v0.0.4**。
- `ExecWithTimings(ctx, workerID, request)` は、終了コード・`ExecTimings`・errorを返します。
- stdout／stderr、stdin、終了コード、取消、失敗の意味は通常の `Exec` と同じです。
- 工程名と時間だけを記録し、引数・パス・出力・資格情報・エラー本文は記録しません。
- 並列実行では呼出しごとに別の計測記録を作ります。

```go
code, timing, err := client.ExecWithTimings(ctx, worker.ID, cxd.ExecRequest{
    Argv: []string{"/bin/true"},
})
fmt.Printf("exit=%d ok=%t\n", code, err == nil)
for _, phase := range timing.Phases {
    fmt.Printf("%s: %s\n", phase.Name, phase.Duration)
}
```

| 工程 | 含むもの |
|---|---|
| `request_validation` | 引数・作業ディレクトリ・環境変数の検証 |
| `exec_lookup` | 対象の照会、稼働・管理対象・隔離状態の確認 |
| `exec_runtime` | runtime登録・資源プロファイル・cgroup版の確認 |
| `exec_configuration` | 設定・mount・ネットワーク・資源上限の照合 |
| `exec_host` | 全資産とホストの検証、ノード情報の照合 |
| `exec_guest` | guest probeと制限の照合 |
| `exec_create`・`exec_attach` | コマンド作成とstreamへの接続 |
| `exec_io`・`exec_result` | 実行中の入出力・待ちと終了結果の照会 |

- `exec_verification` は検証入口の短い処理です。
- 各工程は重複せず、合計は `timing.Duration` になります。失敗時はそこまでの時間を返します。
- `exec_io` はコマンドの実行時間を含みます。AIの応答待ちもここへ入り得ます。
- CPU時間・メモリ・I/O量はこのAPIに含みません。ノード側のprofileやcgroupと合わせて調べます。

## 再計測する

- ソースのルートで、[開発用Go環境](testing.md#コードを確認する)を使って実行します。
- 次の `CODEX_DOCK_BENCH_*` はbenchmarkだけが読む入力です。

### Proxy認証

Kataや実APIへの接続は不要です。

```bash
go test ./internal/authproxy -run '^$' \
  -bench '^BenchmarkWorkerRequest$' -benchmem -count=5 -benchtime=500ms
```

### 一覧と容量収集

Linux上で実行します。Kata・実資格情報は不要で、一時ディレクトリにfixtureを作ります。各版で同じコマンドを使い、測定中は他のbuild・試験を止めてください。

```bash
GOMAXPROCS=4 go test ./internal/engine -run '^$' \
  -bench '^BenchmarkCheckpointCatalog$' -benchmem -count=3 -benchtime=3x
GOMAXPROCS=4 go test ./internal/ui -run '^$' \
  -bench '^BenchmarkDashboardQuotaCollection$' -benchmem -count=3 -benchtime=3x
```

### 固定資産の検証

- Linux上で、実際に固定したmanifestのパスを指定します。
- ファイルへの書き込みは行いません。
- root所有のmanifestには、node運用者の読み取り権限が必要です。
- 信頼済みソースから実行してください。

```bash
CODEX_DOCK_BENCH_MANIFEST=/etc/codex-dock/kata-assets.json \
  go test ./internal/kataguard -run '^$' \
    -bench '^BenchmarkLoadPinnedManifest$' -benchmem -benchtime=3x -count=5
```

### 実KataのExec

- 準備済みの検証ホストで、通常の手順でアイドル状態の管理Kataワーカーを起動し、その完全なIDを指定します。
- benchmarkは既存VMで `/bin/true` を実行します。
- ワーカーの起動と、測定後の停止・削除は呼出側が行います。
- `FULL_WORKER_ID` を実際のIDに置き換え、`-ldflags` の `dev` は実行中nodeのBuildVersionと一致させてください。

```bash
CODEX_DOCK_KATA_NODE_SOCKET=/run/codex-dock-node.sock \
CODEX_DOCK_BENCH_WORKER=FULL_WORKER_ID \
  go test -ldflags '-X github.com/pacificbelt30/codex-dock/internal/buildinfo.Version=dev' \
    ./pkg/cxd -run '^$' -bench '^BenchmarkKataExec$' -benchmem -benchtime=3x -count=5
```

- manifestのパスやワーカーIDを省略すると、対応するbenchmarkはskipします。
- skipを計測成功として扱わないでください。

### 実Kataの新規起動

- 他の利用者・起動処理と共有しない検証用nodeで実行します。
- workerとProxyのイメージは事前に用意してください。
- benchmark自身が無効なAPIキーだけを返すCredentialStoreと専用ケースProxyを作り、各標本で新しいワーカーを起動・削除します。
- 実アカウントやAIへの通信は使いません。
- 一般egressは無効、Gitメタデータ共有なし、エージェントなしのidleワーカーを使います。

```bash
CODEX_DOCK_KATA_NODE_SOCKET=/run/codex-dock-node.sock \
CODEX_DOCK_BENCH_LAUNCH=1 CODEX_DOCK_BENCH_PROFILE=compact \
CODEX_DOCK_KATA_TEST_IMAGE=codex-dock:latest \
CODEX_DOCK_KATA_PROXY_IMAGE=codex-dock-proxy:latest \
GOMAXPROCS=4 \
  go test -ldflags '-X github.com/pacificbelt30/codex-dock/internal/buildinfo.Version=dev' \
    ./pkg/cxd -run '^$' -bench '^BenchmarkKataLaunch$' -benchtime=3x -count=5 -timeout=20m -v
```

- `dev` はnodeのBuildVersionに合わせます。
- `CODEX_DOCK_BENCH_LAUNCH=1` を省略するとskipします。
- `launch_ms/op` と各工程の `*_ms/op` は起動時間、`first_exec_ms/op` は起動後の最初の `/bin/true` の実行・終了確認です。
- 詳細な各標本は `launch_timing` のJSONに出ます。
- Go benchmarkは本計測の前に1回の校正起動も行います。
- 既定の `compact` 以外も `CODEX_DOCK_BENCH_PROFILE` で選べます。
- 準備済みイメージ・温まったpage cacheの結果を、初回イメージ展開や実タスクの開始時間と混同しないでください。

### 起動・Exec・2台同時実行をまとめて測る

- 利用可能：**v0.0.4**。
- 2台以上の空き容量がある専用nodeで実行します。共有サービスは使わないでください。
- 前節と同じ固定worker／Proxyイメージを事前に用意します。実資格情報・AI推論は使いません。
- 順次起動3回、2台同時起動3組、Exec7回、2台同時Exec5組を測ります。
- 試験自身が作成したワーカー・Proxyを回収します。

```bash
OAISO_KATA_PERFORMANCE=1 GOMAXPROCS=4 \
CODEX_DOCK_KATA_NODE_SOCKET=/run/codex-dock-node.sock \
CODEX_DOCK_KATA_TEST_IMAGE=codex-dock:latest \
CODEX_DOCK_KATA_PROXY_IMAGE=codex-dock-proxy:latest \
  go test -ldflags '-X github.com/pacificbelt30/codex-dock/internal/buildinfo.Version=dev' \
    ./pkg/cxd -run '^TestKataPerformance$' -count=1 -timeout=20m -v
```

- `dev` はnodeのBuildVersionに合わせます。clientの `GOMAXPROCS` はnode側の設定を変えません。
- `perf_sample` が工程別時間、`perf_resources` が予約値・観測メモリ・OOM回数です。
- wall timeにイメージ作成・import、Proxy作成、削除は含めません。
- 温まったpage cacheでの準備済みイメージ起動です。初回導入・cold起動・AI推論の計測とは分けます。
- CPU・ディスクI/O・schedulerの待ちは、別途ノード運用者が採取します。この試験だけでは収集しません。

## 変更前後を比較する

### 次の高速化候補の判断

- [新規起動の実測](testing.md#新規kata起動の工程別計測)ではVM開始が主要な待ち時間でした。
- 次の候補は未使用VMの事前起動です。
- 現時点ではWarm pool・メモリsnapshot・VM forkは実装していません。
- 既存の `Create` は停止状態での作成であり、VMの起動は `Start` で行います。

- Warm poolの採用には、事前起動中もbootstrapを閉じ、認証を有効にせず、割当時に資産・host/guest・ポリシーを再検証する仕組みが必要です。
- 現行のworkspace、mount、UID、ネットワーク、image、モデル権限、実行identityの不変条件を引き継ぎます。
- 異なる要求への使い回しや、使用済みVMの再投入は行いません。
- 待機VMも容量予約に数え、期限切れ・取消・node再起動時の所有権確認と回収、同じVMの二重割当防止まで確認してから有効化します。
- 通常起動と同条件で割当から最初のExecまでを比較し、検証・補充・予約メモリの費用を含めて採用を判断します。
- VM開始時間をそのまま改善率として扱わないでください。

### 比較条件

- 同じGo版・CPU条件・資産・ワーカープロファイルで比較し、計測中は他のビルドや試験を止めます。
- nodeとclientには同じ測定用BuildVersionを指定します。
- 各組で旧版・新版の順序を入れ替え、複数標本の中央値を比較してください。
- page cacheが温まった状態と、初回のディスク読み出しを含む状態も区別します。

- 性能計測後には[回帰確認](testing.md)を行います。
- runtime変更は `make kata-e2e`、認証変更は失効・期限切れ・置換・restore・Proxy停止による接続取消も確認します。
- 試験用VM・コンテナ・ネットワークを回収してから、検証用サービスや管理記録を削除します。
