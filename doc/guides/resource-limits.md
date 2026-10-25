# 接続・帯域・FD・I/Oの上限

**日本語** | [English](../en/guides/resource-limits.md)

- 利用可能：**未リリース（main）**。対応するCLI・node・guard・Proxyが必要です。
- 追加の制限は任意です。未指定では従来の動作を維持します。
- 既存のCPU・メモリ・PID、要求本文のサイズ、[AIの実行別利用上限](usage-limits.md)とは別の設定です。

## 何を制限するか

| 対象 | 単位・設定先 | 上限到達時 |
|---|---|---|
| TCP接続数 | Proxy全体、接続元IPごと | 新規接続を閉じる。既存のHTTP/SSE/WS/CONNECTは維持 |
| 通信帯域 | Proxyごと・接続元IPごとの双方向合計byte/秒 | 待たせて転送。初期burstは32 KiB。継続時間による切断は追加しない |
| file descriptor（FD） | 管理者定義プロファイルの、guest内の各プロセス | soft/hardを同じ値へ固定。追加openは`EMFILE`。Exec・子プロセスも同じ上限 |
| block I/O | 管理者定義プロファイルの、VM全体のホストcgroup・デバイスごと | `io.max`でread/writeのbyte/秒・IOPSを制限。上限ではI/Oを待たせる |

- 別のadmin listenerは制限対象外です。旧来の単一listener構成では、管理接続も同じ枠へ含まれます。
- 接続数にはidle接続とTLS接続も含みます。HTTP/2の1接続内の要求数は数えません。AI要求の同時数には既存の`--max-concurrent`を併用します。
- 帯域はworker側socketのbyte数です。認証ProxyではTLS handshake・record、一般通信ではCONNECT内のデータも含みます。IP/TCPヘッダー・再送・上流側の別hopは数えません。
- kernelの送受信bufferがあるため、NICでの瞬間的な通信量を厳密に制限する仕組みではありません。
- Auth Proxyと一般egress Proxyの枠は別々です。2つを合算したVM全体の帯域上限ではありません。
- `--max-transfer-bytes`はAI／broker本文の**累積量**で、超過streamを終了します。今回の帯域設定はbyte/秒を調整し、累積量を制限しません。
- 既存のtoken・証明書期限、失効、明示した最大寿命・idle回収やクライアント自身のtimeoutは引き続き適用します。

## Proxyの接続数・帯域を設定する

前提：通常ユーザーで独立loginと初期設定を完了。既存Proxyを変更する場合は、利用中ワーカーの作業を終えて回収し、[更新手順](../operations.md#cliプロキシを更新する)で明示的に作り直します。

```bash
oaiso proxy run --max-connections 512 --max-connections-per-source 32 \
  --network-bytes-per-second 1048576
```

| CLI（`proxy run`／`proxy serve`） | ホスト設定`[proxy]` | 値 |
|---|---|---|
| `--max-connections` | `max_connections` | 0＝無制限、1〜65536 |
| `--max-connections-per-source` | `max_connections_per_source` | 0＝無制限、1〜65536。全体上限を超える値は不可 |
| `--network-bytes-per-second` | `network_bytes_per_second` | 0＝無制限、1024〜1099511627776 |

ホストの`~/.config/codex-dock/config.toml`で設定する場合：

```toml
[proxy]
max_connections = 512
max_connections_per_source = 32
network_bytes_per_second = 1048576
```

- CLIフラグを優先します。ホスト設定は自動起動・宛先別Proxyにも使用します。プロジェクトの`oaiso.toml`からは指定できません。
- 既存Proxyと設定が違う場合は、黙って旧設定を使わずエラーにします。設定変更だけで稼働中の接続を切りません。
- 接続元はsocketのIPです。`X-Forwarded-For`などの値を信用しません。管理下のKataは個別ネットワーク上のVMのIPで区別します。
- 外部NAT経由などで同じIPを共有すると枠も共有します。認証済みの実行IDによる区分ではありません。認可には別途tokenと証明書を使用します。
- 接続元の管理表は最大4096件。帯域待ちが残る枠は再接続で初期化しません。Proxy再作成では帯域枠が初期化されます。永続的な利用量台帳ではありません。
- 超過時はTCP接続を閉じるため、HTTP 429を返せるとは限りません。既存接続が終了すると接続枠が空きます。

## FD上限を定義する

通常ユーザーで、作業用ディレクトリからJSONを生成します。既存のプロファイルファイルがある場合は、配列へ要素を追加し、他の定義を残します。

```bash
oaiso profile define limited --base compact --pids-limit 512 --nofile 1024 \
  > resource-profiles.json
```

- `--nofile`：0＝従来のruntime既定値、128〜1048576。
- プロファイルはJSON生成だけでは有効になりません。次の管理者登録を行います。
- 起動・再起動では、OCI設定とguardの実測値を照合してからユーザー処理・認証を有効にします。
- プロセスごとの上限です。VM全体の合計FD数や、ホストのKata runtime／ProxyのFD数を制限する値ではありません。

## block I/Oも制限する場合

- 管理者がホストの対象デバイスの`MAJOR:MINOR`を確認します。例の`8:0`は実機の値に置き換えてください。
- controllerの`io`をnodeのcgroup subtreeまで委譲する必要があります。指定した制限が使えない場合は、制限なしで続行せず起動を拒否します。

```bash
oaiso profile define limited-io --base compact --pids-limit 512 --nofile 1024 \
  --io-limit '8:0 rbps=67108864 wbps=8388608 riops=4096 wiops=1024' \
  > resource-profiles.json
```

| 指定 | 意味 |
|---|---|
| `rbps` / `wbps` | 読み／書きbyte/秒。正の値は最大2^40 |
| `riops` / `wiops` | 読み／書きI/O回数/秒。正の値は最大2^30 |
| 省略、`max` | その項目は無制限。1デバイスにつき最低1つの制限が必要 |
| `--io-limit`の繰返し | 最大16デバイス。重複デバイス・重複項目・不明項目は拒否 |

- 対象は、そのホストcgroupへ計上されるblock I/Oです。cached read、tmpfs、別デバイス、別サービスによるI/Oまで一括では制限しません。
- device mapper・writeback・共有ファイルの帰属で結果が変わります。対象デバイスと`io.stat`、実ワークロードで効果を確認します。
- kernelは一時的なburstを許します。厳密な1秒窓の最大値や、他VMへの最低帯域保証ではありません。
- 仕様：[Linux cgroup v2のI/O controller](https://www.kernel.org/doc/html/latest/admin-guide/cgroup-v2.html#io-interface-files)、[OCIのprocess rlimits](https://github.com/opencontainers/runtime-spec/blob/main/config.md#posix-process)。

## 管理者が登録して確認する

1. 対象の既存VM・予約を回収し、既存の定義を保持してJSONを配置します。

```bash
sudo install -d -o root -g root -m 0755 /etc/codex-dock
sudo install -o root -g root -m 0600 resource-profiles.json \
  /etc/codex-dock/resource-profiles.json
sudo systemctl cat codex-dock-node
sudo systemctl edit codex-dock-node
```

2. editorで、元の`ExecStart`の引数を保ち、末尾へ`--resource-profiles /etc/codex-dock/resource-profiles.json`を追加します。上書きには空の`ExecStart=`が必要です。

```ini
[Service]
ExecStart=
ExecStart=<確認した元のコマンド> --resource-profiles /etc/codex-dock/resource-profiles.json
```

3. 保存後、再起動して通常ユーザーで確認します。配布版の更新後は、上書きした実行ファイル・manifestの参照先も再確認してください。

```bash
sudo systemctl daemon-reload
sudo systemctl restart codex-dock-node
for attempt in $(seq 1 60); do
  if oaiso profile --node --json; then break; fi
  sleep 1
done
oaiso profile --node --json
```

FDのみの例を登録した場合、通常ユーザーでプロジェクトのディレクトリから実行：

```bash
oaiso run --name limited-job --profile-size limited --detach --keep --exec 'sleep 600'
oaiso exec limited-job -- sh -c 'ulimit -Sn; ulimit -Hn'
oaiso vm inspect limited-job --json
```

I/O用の例を登録した場合は、代わりにこちらを実行：

```bash
oaiso run --name limited-job --profile-size limited-io --detach --keep --exec 'sleep 600'
oaiso exec limited-job -- sh -c 'ulimit -Sn; ulimit -Hn'
oaiso vm inspect limited-job --json
```

- どちらもFD上限の`1024`が2行表示されます。
- `vm inspect`の`io.max`は設定、`io.stat`はホストの観測です。表示されるだけで全ストレージへの効果を保証するものではありません。
- 終了後は`oaiso rm -f limited-job`で自分のVMを回収します。
- 定義の変更・削除は予約を回収してから行います。保持中の定義の読み替えは拒否します。[既存の登録条件](../sandbox-features.md#管理者定義の資源プロファイル)。

## Go API

| API | 指定・照会 |
|---|---|
| ケースの認証Proxy | `CaseProxyOptions.NetworkResources = cxd.ProxyResourceLimits{MaxConnections: 512, MaxConnectionsPerSource: 32, BytesPerSecond: 1048576}` |
| 管理下の一般egress | `Options.EgressNetworkResources`に同じ型を指定。`EgressPolicy`に明示した宛先規則が必要 |
| プロファイルの全項目 | `client.ResourcePolicies(ctx)`。`FileDescriptorLimit`と`BlockIO`も返す |
| 稼働VMの契約 | `usage, err := client.ResourceUsage(ctx, id)`の`usage.Policy`（errorも確認）。従来の`Profile`はCPU・メモリ・PIDのまま |
| 起動時の選択 | 既存の`LaunchRequest.Profile`を使用 |

- [試験・未確認範囲](../testing.md)。ローカルのrootや管理者からの変更を防ぐ仕組みではありません。
