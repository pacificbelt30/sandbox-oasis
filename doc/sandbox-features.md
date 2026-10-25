# 追加のサンドボックス制御

- 以下は明示的に選ぶ機能です。
- 一般通信の既定値：[原則拒否](proxy-policy.md)。固定AI経路は別に認可します。
- 新しいノード、プロキシ、ワーカーイメージをそろえてから有効にしてください。
- 古いノードが制限を黙って無視しないよう、ファイル制限・image digest・idle管理・ジョブには能力確認があります。

| 優先度 | 対応項目 | 実装入口 |
|---|---|---|
| P0 A1 | 実効ポリシー・説明・事前検証 | `policy show/check/explain`、Go API |
| P0 A2 | 理由コード付き拒否イベント | `policy events`、TUIのE、Go API |
| P0 A3 | 明示的なプリセット | `--policy-preset`、project config |
| P0 A4 | 診断と能力確認 | `doctor`、`capabilities`、`ProxyCapabilities` |
| P1 A5 | 個別のファイル読み書き制限 | `--deny-read` / `--deny-write` |
| P1 A6 | 実行・ファイル・背景ジョブ・PTY | `exec/files/jobs`、Go APIのsession |
| P1 A7 | 寿命・idle・保持期限 | opt-inの設定と `reap --watch` / `Reap` |
| P1 A8 | 環境固定・hook・readiness | `environment lock`、起動オプション |
| P1 A9 | 独立cloneと変更回収 | `--clone-dir`、`artifact`、files API |
| P1 A10 | 上流・パス・ヘッダーを限定した認証broker | `--credential-routes`、Go callback |
| P1 A11 | SSH/JVM・上流proxy・独自CA | compatibilityフラグとproxy設定 |

| 追加制御の対象外 | 内容 |
|---|---|
| 既定での強制 | ファイルmask・clone・自動削除 |
| 通信の追加機能 | TLS MITM・実行中ポリシーの変更 |
| 実行基盤の追加 | VM snapshot/fork・新しいOS backend・リモート多人数サービス |

## 通信の確認と設定

```bash
oaiso policy check --json
oaiso policy show --worker WORKER --json
oaiso policy explain api.example.com:443 --resolve --json
oaiso policy events --worker WORKER --follow --json
oaiso policy presets
oaiso run --policy-preset api --egress-allow-domain api.example.com -d
oaiso run --policy-preset dependencies -d
oaiso run --policy-preset offline -d
oaiso proxy configure
```

- `policy show --worker` はワーカーが選んだプロキシの実設定とIDを読みます。
- `--worker` がない場合は、新規起動用のホスト設定です。
- `explain` は接続を開きません。
- `--resolve` なしの許可は名前・ポートだけの暫定判定です。
- 指定時は全DNS応答を検証します。
- 実際の接続時にも再検証します。

- `offline` と `test` は一般通信を切り離し、固定AI経路は維持します。
- brokerを設定した場合、その固定認証経路も一般通信ポリシーとは独立して利用可能です。
- `dependencies` はnpm/Python/Goの代表的なレジストリ、`api` は明示したドメインだけを選びます。
- どのプリセットもホストの許可上限・拒否ルール・private IP遮断を緩和しません。

- 既存TUIでは **P** で設定、**E** で拒否履歴を開きます。
- `proxy configure` はノード停止中でも使えます。
- 許可・拒否の6項目、空リスト時の既定拒否、上流プロキシURL、CAファイルのパスを編集し、宛先を検査して保存できます。
- 秘密値は表示しません。
- 他の設定を保ち、同時編集は検出します。
- 複数行配列など安全に編集できない形式は保存を拒否するため、テキストエディタで編集してください。

- 保存は実行中の設定を変更しません。
- 既存ワーカーを退避して対象プロキシを明示的に再作成します。
- 詳細は[通信ポリシー](proxy-policy.md)を参照してください。

- 拒否イベントには時刻、理由コード、接続元、宛先authority、policy IDを含めます。
- URLのパス・query・ヘッダー・資格情報は記録しません。
- 一般通信プロキシは複数ワーカーで共有するため、履歴も共有です。
- 接続元IPを実行IDと同一視せず、機密性が必要なケースはプロキシも分けてください。
- プロセス内履歴は512件、CLI/APIは保持された直近1000行を読みます。
- プロキシのイベント出力は1プロセス10万件までで、上限時は `event_log_limit` を出してそれ以降のログ出力を止めます。
- プロキシ再作成後の永続監査ログではありません。

## 通信の停止と隔離

既存ワーカーの通信も止める必要がある場合は、ホストから次を実行します。

```bash
oaiso policy quarantine --worker WORKER
oaiso policy show --worker WORKER --json
```

| 操作・対象 | 隔離時の動作 |
|---|---|
| `policy revoke` | 同じ隔離操作の別名 |
| 対象ワーカー | **全実行を停止**。一般HTTP/CONNECT・固定AI通信・背景ジョブ・実行中エージェントを終了 |
| 他ワーカー・共有Proxy | 停止しない |
| workspace・管理worktree・コンテナ・ログ | 隔離操作では削除しない |
| 設定済みの保持期限・明示削除 | `reap` や削除の既存規則は適用。長期保全する成果は先に回収 |
| 設定保存・通常の `stop` | この隔離操作とは別 |

- 更新済みnodeは再起動禁止をprivateなOCI recordへ永続化してから、猶予時間を設けず停止を実行します。
- `start`、VM start、exec、背景ジョブによる再開をnodeで拒否し、node再起動時も残った停止処理を完了させます。
- 受理後の停止処理は呼び出し元が切断してもnode所有の90秒上限で続行します。
- 別クライアントの `StateDir` や同じRequestIDの再送でも解除できません。
- 対応するnodeでの運用が前提であり、古いnodeへのdowngrade前には保持ワーカーを回収してください。
- `worker-quarantine-v1` が対応能力であり、未対応nodeへの隔離要求はエラーになります。

- 成功応答は、nodeの停止応答・停止状態・隔離状態、AI仮トークン失効、Kata予約解放を確認した後だけ返します。
- 応答消失・停止失敗・資格情報管理層の障害ではエラーを返し、成功扱いにしません。
- 同じワーカーで操作を再試行してください。
- 保存済みでも停止未確認なら `quarantine_pending` がtrueとなり、宛先説明は遮断完了と断定しません。

- 再開には新しいワーカーと新しいRequestIDを使い、新しい通信設定を適用します。
- 隔離停止の解除はできません。一般通信だけの規則変更は、v0.0.4の[通信規則の即時変更](guides/live-policy.md)を使います。
- 隔離はゲストからの追加通信をVM停止で止める方式です。
- 相手側ですでに受理された処理を取り消したり、共有egress Proxyの全上流socketが閉じるまで待つ操作ではありません。
- 必要な成果物を保全してから、保持したワーカーを通常の `rm` / `RemoveRequest` で回収します。
- 管理worktreeの `rm` はそのworktreeも削除するため、先に変更を保存してください。

## ファイル・実行・回収

```bash
oaiso run --deny-read /workspace/.env --deny-write /workspace/config -d
oaiso exec --workdir /workspace --env MODE=test WORKER -- npm test
oaiso exec -it WORKER -- bash
oaiso files get WORKER src/main.go --local main.go.copy
oaiso files put WORKER input.json --local input.json
oaiso files list WORKER src
oaiso jobs start WORKER -- sh -c 'npm test'
oaiso jobs list WORKER
oaiso jobs logs WORKER JOB_ID --offset 0
oaiso jobs kill WORKER JOB_ID
oaiso artifact diff WORKER --output changes.patch
oaiso artifact commit WORKER --message 'Save staged changes'
oaiso run --clone-dir /absolute/new-copy -d
```

- ファイル制限は `/workspace/` 以下の明示したパスにOCI mask/readonlyを適用します。
- 未指定時は追加制限をかけません。
- 対象は起動時に存在する通常ファイル・ディレクトリに限り、symlinkやworkspace外への解決は拒否します。
- 将来作るファイルへの継承ルールではありません。
- パスの別名やハードリンクに対するinode単位の機密ラベルではありません。
- 複数マウントに同じ内容を露出しないでください。

- ファイルAPIはguestのPython 3で実行し、各パス要素のsymlinkを拒否します。
- 通常ファイルだけを最大64 MiBまで読み書きし、アップロードは同じディレクトリで原子的に置換します。
- ディレクトリ一覧は最大10000件です。
- 既存の実行権限やVM起動検証を迂回しません。

- PTYではstdout/stderrが一つになります。
- ストリーミング実行・セッションは切断やcancelで終了します。
- 背景ジョブは切断後も続き、状態とshim所有のログをノード再起動後に復元します。
- 出力は合計16 MiBまで保持し、以後もguest出力は排出します。
- ログAPIは1回最大1 MiB、次のoffsetと打切り情報を返します。
- 完了ジョブはワーカー削除まで残り、ノード全体で実行セッションを最大4096件保持します。

- 独立cloneはコミット済みの内容から作り、Gitオブジェクトをハードリンク共有しません。
- 既存worktree方式も維持します。
- clone先は新規の絶対パスが必要です。
- cloneは起動失敗時・ワーカー削除時も回収用に残します。
- 再送時は既存cloneを暗黙採用しません。
- 差分出力はHEADに対する追跡済み変更です。
- 未追跡ファイルはfilesで取り出し、commitは事前にstageした変更だけを記録します。

## 寿命と再現性

```bash
oaiso run --max-lifetime 2h --idle-timeout 30m --retention 24h -d
oaiso reap --dry-run
oaiso reap --watch
oaiso environment lock IMAGE --source Dockerfile --source package-lock.json
oaiso run --image IMAGE --environment-lock codex-dock.lock.json -d
oaiso run --start-hook 'npm ci' --readiness 'test -f node_modules/.package-lock.json' --readiness-timeout 2m -d
```

- 寿命・idle・保持期限は `reap --watch` またはGo APIの定期的な `Reap` 呼出しが適用します。
- CLI起動だけで監視サービスを常駐配置することはありません。
- 監視が止まれば期限の適用も止まります。
- idleは最後の管理API実行の時刻で、guestのCPU使用やファイル更新を計測した値ではありません。
- 対応ノードでは開始待ちexec・実行中exec/PTY・背景ジョブをidle回収から除外します。
- 停止直前にノード内で再確認するため、新しいexecとの競合でも停止しません。
- メイン処理は下記の`--protect-main`、管理側が把握する処理は`activity`でidle回収から保護できます。CPUやファイル更新から自動判定はしません。
- 最大寿命の明示制限は実行中でも適用されます。
- 停止はtoken失効を伴い、削除は既存の所有権確認・削除journalを通します。
- 保持期限ゼロは削除しません。

- lockはノード上のOCI image digestと指定ファイルのSHA-256を固定し、新規ファイルにだけ書きます。
- タグまたはsourceが変われば起動を拒否します。
- 依存パッケージ自体のlockfileもsourceに含めてください。
- 起動hookはguest内で認証設定後・agent開始前に順次実行します。
- probe成功後に起動完了とし、失敗や期限超過では起動を回収します。
- 既定のhook/probe期限は60秒、起動全体には既存の180秒上限もあります。
- 対応ラベルのない旧イメージではhookを拒否します。

- プロジェクトの `oaiso.toml` の `[run]` に `policy_preset`、`deny_read`、`deny_write`、`max_lifetime`、`idle_timeout`、`retention`、argv配列の `start_hooks` と `readiness`、`readiness_timeout` を指定できます。
- ホストの秘密・追加mount・runtime・プロキシURLは引き続き指定できません。

## 資格情報と互換性

- `oaiso proxy run --credential-routes /absolute/private-routes.json` は、ホストの0600 JSONを認証プロキシだけにread-onlyで渡します。
- 一般通信プロキシには資格情報を渡しません。
- 例は架空の値です。

```json
[{"Name":"service","Upstream":"https://api.example.com","Paths":["/v1"],"Methods":["GET","POST"],"Header":"X-Service-Key","Prefix":"","Provider":"openai","Secret":"HOST_SECRET"}]
```

- 完全一致の `ExactPaths` も利用できます（v0.0.4）。`Paths`は従来どおり配下を含み、両方の許可を合わせます。[設定例](guides/credential-broker.md)。
- guestは `http://AUTH_PROXY/broker/service/v1/...` に、発行済みplaceholderのBearer認証を付けて要求します。
- 固定HTTPS origin・許可パス境界・メソッド・providerを検証してから秘密を注入します。
- encoded path、WebSocket、上流redirectの追跡は拒否します。
- 秘密値はレスポンスAPIから取得できません。
- ただし許可した上流が秘密をechoする場合まで防げないため、信頼できるAPIだけを設定してください。
- 設定ファイル変更時は退避・再作成が必要です。
- 組み込みでは `StartCredentialBroker` と `CredentialRoute.Secret` callbackも使えます。
- 独立brokerのlistener・workerネットワークへの配置は呼出し側で明示します。

```bash
oaiso run --ssh-proxy --jvm-proxy --ca-bundle /absolute/extra-ca.pem -d
oaiso proxy run --upstream-proxy http://proxy.example.com:3128 --ca-bundle /absolute/extra-ca.pem
```

- SSHは `GIT_SSH_COMMAND` のCONNECT helperで一般通信ポリシーを通します。
- SSH鍵・ホストagent socketは自動共有しません。
- JVMはHTTP(S) proxy propertiesを設定します。
- CAはワーカーでsystem bundleに追加し、curl/Python/Nodeに適用します。
- JVMとkeytoolがある場合はsystem truststoreをワーカーのHOMEへコピーしてCAを追加します。
- プロキシ側のCAはsystem rootsに追加し、HTTPとWebSocket両経路でTLSを検証します。

- 多段プロキシは検証済みIPへのCONNECTを使います。
- DNS検証を上流に任せず、private IP遮断を維持します。
- 上流プロキシへのユーザー名・パスワード付きURLは受け付けません。

## Go APIと性能確認

- [Go API](go-api.md)には `Policy` / `ExplainPolicy` / `PolicyEvents`、`ExecRequest` の作業ディレクトリ・環境変数・TTY、`StartSession` / `Resize`、ファイル・ジョブ・差分回収・`Reap`を追加しました。
- 既存メソッドは維持します。
- ノードの能力とプロキシの能力は別に確認してください。

- `internal/networkpolicy` のベンチマークでは、1000ドメイン照合で毎回許可リストを走査してDNS名をIPとしてparseしていた処理を特定しました。
- コンパイル時の索引に変更し、従来のdomain/IP判定との一致を試験しています。
- 1000件での測定は変更前65.9–181 µs、48112 B・1002 allocs/回から、変更後152–153 ns、0 B・0 allocs/回になりました（同一ホスト、3回測定、判定処理のみ。
- 通信全体の速度ではありません）。
- 測定コマンドは次の通りです。

```bash
go test ./internal/networkpolicy -run '^$' -bench . -benchmem -count=3
```

- TUIログは追記描画に変更し、2000行・約4 MiBで区切ります。
- 履歴全体を毎回再生成する処理をなくし、終了時にはpollingも停止します。

## モデル認可と段階的な起動

- `oaiso proxy run --allow-model model-id`（繰返し可）、ホスト設定 `[proxy].allowed_models`、または `CaseProxyOptions.AllowedModels` で、その認証プロキシ全体のAI要求を制限します。
- 空リストでは既存の転送を変更しません。
- 設定は起動時に固定し、変更にはワーカー退避・プロキシ再作成が必要です。
- `ProxyCapabilities` は対応能力と有効状態を別々に返します。

- 制限時はPOSTのJSONを最大8 MiBまで読み、正確な `model` 文字列を照合します。
- 重複キー・大小文字違いのmodelキー・不正JSON・圧縮本文・encoded pathを拒否します。
- queryはClaude Messages/count_tokensの正確な `beta=true` だけを許可し、それ以外は拒否します。
- 検査後の本文はそのまま送信し、上流redirectは追跡しません。
- 対応経路は `/v1/responses`、`/v1/responses/compact`、`/v1/chat/completions`、`/v1/completions`、`/v1/embeddings`、`/v1/moderations`、`/chatgpt/codex/responses`、`/chatgpt/codex/responses/compact`、`/anthropic/v1/messages`、`/anthropic/v1/messages/count_tokens` です。
- その他のAI経路とWebSocketは拒否するため、HTTP対応のクライアント設定が必要です。
- モデル一覧取得やbatch・ファイルAPIもこのモードでは転送しません。

- これは要求に記載したIDの制限です。
- 上流がaliasをどの実モデルへ解決するかは管理しません。
- 認証brokerの独自経路・一般通信先・利用者が持ち込んだ別の資格情報を一括認可する仕組みではありません。
- 必須のモデル制限を持つ製品では、一般通信を無効にするか到達先を限定し、独自brokerに別のAI経路を設定しないでください。

- Go APIの `Create` は、既存 `Launch` と同じ検証済み設定で初回起動前まで作成します。
- VM・hook・agentは実行せず、生きたワーカートークンも発行しません。
- ネットワーク・snapshot・ホスト資源予約は作成するため、承認前の資源確保も禁止する契約では `Create` 自体を承認後に呼んでください。
- 採用しなかった作成済みワーカーは `Remove` で回収します。
- 最大寿命は作成時刻から数えるため、承認待ち時間も含みます。
- `Create` と `Launch` は要求IDを共有できず、同じ操作の再送は同じIDを使います。
- `Start` は隔離検証・トークン有効化・hook/readinessまで完了して成功します。
- 取消・失敗後は `Request` / `Get` と新しいcontextで照合・回収してください。

## 回収の常駐とメモリの観測

- 任意のsystemd user unitを `deploy/kata/codex-dock-reaper.service` に用意しています。
- 対応するCLIを配置後、通常オペレーターが `~/.config/systemd/user/` にコピーし、`systemctl --user daemon-reload` と `systemctl --user enable --now codex-dock-reaper.service` で有効にします。
- ログは `journalctl --user -u codex-dock-reaper.service`、状態は `systemctl --user status codex-dock-reaper.service` で確認します。
- 失敗時は5秒後に再起動します。
- ユーザーサービスをログアウト後も維持する必要がある場合は、管理者が当該ユーザーのlingerを設定します。
- 自動配置・自動有効化はしません。
- 複数ケースの製品は管理シークレットを持つ自分のクライアントから定期的に `Reap` を呼び、エラーを監視してください。

- `ResourceUsage` は検証済みで稼働中のVMについて、ホストcgroupの `memory.current`・対応カーネルの `memory.peak`・`oom_kill` を返します。
- guestのRSSではありません。
- guest/hostの予約上限は別フィールドです。
- guest値はワーカーのcgroup上限で、VM全体の `/proc/meminfo` の総RAMとは区別します。
- smallのguest 1 GiB／host 2 GiBと既定のstandardは維持します。
- 軽量用途には明示選択の `compact`（guest 512 MiB／host 768 MiB、1 CPU、512 PID）を追加しました。
- CLIは `--profile-size compact`、Go APIは `Profile: "compact"` で選びます。
- 対応nodeへ更新し、`Profiles` で値を確認してください。
- 容量の大きい依存導入やビルドの完走を保証する値ではないため、製品固有の処理・同時実行・peak・OOMを測って選択します。
- CLIの `vm inspect --json` でも同じホスト観測値を確認できます。

## 管理者定義の資源プロファイル

- v0.0.4：[接続・帯域・FD・block I/Oの上限](guides/resource-limits.md)。`ResourcePolicies(ctx)`で全プロファイル項目、`ResourceUsage.Policy`で稼働VMの契約を確認できます。ProxyのTCP数はAIの同時要求数とは別です。

- PID上限はプロファイルごとに指定できます。
- たとえば、`oaiso profile define compact-256 --base compact --pids-limit 256` は、compactのCPU・メモリ予算を維持した256 PIDのJSON配列を標準出力へ生成します。
- ファイルやnode設定は自動で変更しません。
- 既存の定義がある場合は、生成した要素をその配列に追加してください。
- `oaiso profile --node --json` は稼働中nodeの組み込み・登録済みプロファイルを取得し、`oaiso profile compact-256 --node` は選んだ上限を表示します。
- `--node` なしの一覧は従来どおり組み込み値です。

- 組み込みの `standard`・`small`・`compact` は変更しません。
- 管理者はroot所有でグループ・全体への書き込みを許可しないJSONファイルに、追加の名前とCPU・guest/hostメモリ・PID上限を登録できます。
- `node serve --resource-profiles /etc/codex-dock/resource-profiles.json` で読み込みます。
- 例は次のとおりです。

```json
[{"name":"batch","cpu_cores":2,"guest_memory_bytes":536870912,"host_memory_bytes":805306368,"pid_limit":256}]
```

- 名前は英小文字で始まる英小文字・数字・ハイフンの63文字以内です。
- 最大32定義、64KiBまでで、組み込み名の置換や重複は拒否します。
- CPUは1〜64、guestメモリは512〜2048MiB、host上限はguestより256MiB以上大きく3072MiB以下、PIDは64〜4096です。
- メモリ値はMiB単位で指定します。
- 固定したDragonball資産の範囲を広げる設定ではなく、起動時のhost/guest検証も必須です。

- Goでは `client.Profiles(ctx)` を確認して `Profile: "batch"` を指定します。
- CLIは `--profile-size batch` またはグローバル設定の `run.profile_size` を使います。
- 補完も稼働nodeの名前を取得します。
- 引数なしのパッケージ関数 `cxd.Profiles()` は組み込み値だけを返します。

- PIDが512以外の定義には、更新されたguestフックと、そのimageに対応するschema v2のguestメタデータが必要です。
- `prepare_guest.py build` は新方式の資産を生成し、`runtime-lock` はimageのSHA256と対応するメタデータを検証して `guest-policy` 資産として固定します。
- 古いguest・manifestの512動作は維持します。
- 未対応資産で512以外を登録するとnode起動時に失敗します。
- `dynamic-pid-profiles-v1` と `operator-resource-profiles-v1` で対応を確認できます。
- PID値はnodeがOCI annotationに設定し、guestの実際の上限をbootstrapで確認してから資格情報を有効にします。
- [固定Kataのhook state](https://github.com/kata-containers/kata-containers/blob/4.2.0/src/agent/rustjail/src/container.rs)を使います。

- 新しいguest imageへの切り替え・再固定前には既存VMを回収してください。
- 登録済み定義の値は予約台帳に固定されます。
- 名前の追加は既存の資産stampを変更しませんが、ファイル変更後はnodeを再起動します。
- 値の変更・削除はその定義の予約をすべて回収してから行ってください。
- 保持した予約を別予算へ読み替えることは拒否します。
- 誤って変更した場合は、元の定義を復元してから回収してください。
- 配置・サービス再起動は自動実行しません。

- 再固定時には `runtime-lock --logger /absolute/current/codex-dock-io-logger` で対応版のログ収集バイナリを明示できます。
- PATHから古いhelperを選ぶとバックグラウンドジョブの出力上限オプションに対応しない場合があるため、node bundleの対応するhelperを固定してください。

## メイン処理と活動通知によるidle回収の防止

- 利用可能：**v0.0.4**。対応するCLI・nodeが必要です。
- 既定のidle判定は維持します。必要なワーカーで保護を明示してください。

| 指定 | 保護する期間 | 通知停止・終了時 |
|---|---|---|
| `run --protect-main` | メインプロセスが実行中・一時停止中 | 終了後は通常の保持期限へ移行。対話待ちも実行中として保護 |
| `activity WORKER --ttl 2m` | nodeが確認した時刻から2分 | 更新が止まれば期限切れ。最後の活動時刻とidle設定で回収 |
| `activity WORKER --ttl 0s` | 活動通知の保護を解除 | 解除した時刻を最後の活動として記録 |
| `--max-lifetime` | 上記の保護と独立 | 実行中でも最大寿命で停止 |

- 活動通知は通常ユーザーのCLIまたはホストのGo APIから行います。管理socket・上流資格情報をVMへ渡しません。
- TTLは0〜300秒の整数。通知側は期限より短い間隔で更新し、失敗を監視してください。
- 保護・期限はnodeへ保存し、node再起動後も維持。VMの再起動では以前の活動通知を解除します。
- 停止直前にもnode内で確認。状態不明・保存失敗・確認応答の不一致は成功としません。
- 寿命・idle・保持期限を実際に適用するには`reap --watch`または定期的なGo API `Reap`が必要です。

実行例：設定済みの実行ホストから、1時間のメイン処理をidle回収から守ります。

```bash
oaiso run --name long-job --detach --keep --idle-timeout 5m --protect-main --max-lifetime 2h --exec 'sleep 3600'
oaiso reap --dry-run
```

活動通知の例：`my-worker`は既存ワーカーの名前へ置き換えます。

```bash
oaiso activity my-worker --ttl 2m
```

出力例（時刻は例示）：

```json
{"worker":"my-worker","active_until":"2026-10-24T12:02:00Z"}
```

保護の解除は別の操作です。

```bash
oaiso activity my-worker --ttl 0s
```

- Go API：`LaunchRequest.Lifecycle.ProtectMain = true`、`client.Heartbeat(ctx, workerID, 2*time.Minute)`。
- `oaiso.toml`／ホスト設定：`[run] protect_main = true`。
