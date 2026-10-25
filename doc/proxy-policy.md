# プロキシの通信ポリシーと拡張

- ワーカーの一般通信は認証情報を持たないegress proxyが扱う。
- OpenAI/Anthropicの固定認証経路とは別で、下記ルールによってAI APIの固定経路を許可・拒否するものではない。
- ワーカーからの直接通信や他ワーカーへの通信は専用bridge・namespace・nftablesで遮断する。

## 設定

`~/.config/codex-dock/config.toml`:

```toml
[proxy]
forward_default_deny = true
forward_allow_domains = ["github.com", "npmjs.org", "pypi.org", "pythonhosted.org"]
forward_deny_domains = ["blocked.github.com"]
forward_allow_ports = [80, 443]
forward_deny_ports = [25]
# IP単体、IPv4/IPv6のCIDRを指定可能。DNSが返すIPにも適用する。
forward_allow_cidrs = []
forward_deny_cidrs = []
```

- `proxy run`には同名の単数形フラグがある。
- ドメイン・CIDRは繰り返し指定、ポートは繰り返しまたはカンマ区切りで指定する。
- 明示したフラグは対応する設定ファイル項目を上書きする。

```bash
oaiso proxy run \
  --forward-allow-domain github.com \
  --forward-deny-domain blocked.github.com \
  --forward-allow-port 80,443 \
  --forward-deny-cidr 203.0.113.0/24
```

### 許可・拒否の判定

| 条件 | 結果 |
|---|---|
| 拒否ルールに一致 | 拒否優先。許可リストで打ち消せない |
| 複数種類の許可を指定 | ドメイン・接続IP・ポートをすべて満たす必要あり |
| `forward_default_deny` 省略 | `true`（CLI：`--forward-default-deny`） |
| ドメイン・CIDRの許可が両方空 | 一般通信を拒否。ポートだけでは宛先を許可しない |
| `forward_default_deny = false` を明示 | 空の許可リストは追加制限なし。全種類が空でも公開宛て通信を許可する従来動作 |
| 固定AI経路 | 一般egressとは独立した認証で制御 |

### ドメインの書き方

| 指定・条件 | 意味 |
|---|---|
| `example.com` | その名前と全サブドメイン |
| `=api.example.com` | その名前だけに完全一致 |
| 許可・拒否 | どちらにも上記形式を利用可 |
| 大文字・小文字 | 区別しない |
| `*.example.com`・URL | 指定しない |
| 国際化ドメイン | ASCIIのPunycode表記を使用 |

### IP・DNS・転送先の検査

| 検査対象 | 動作 |
|---|---|
| private・loopback・link-local・metadata・CGNAT | 常に拒否。CIDR許可では解除できない |
| ドメイン・ポート | DNS問い合わせ前に検証 |
| DNS応答 | 拒否IPが一つでもあれば接続しない |
| 接続時 | 確認済みIPへ直接接続。名前を再解決しない |
| HTTPリダイレクト | 移動先も同じ規則で検査 |
| CONNECT | 要求された接続先のホスト・ポート・接続IPを検査 |

### 検査の範囲と上流Proxy

- CONNECTのTLS内URL・SNI：復号・検査しない。
- ドメイン拒否：要求された名前への制限。同じサービスへのIP直指定・別名まで一括拒否する保証はない。
- 厳密な制限：ドメイン許可リストに、必要なCIDR・ポート制限を組み合わせる。
- 一般egress Proxy：環境変数の `HTTP_PROXY` 等へ接続を委譲しない。
- 上流Proxyの指定：`[proxy].upstream_proxy` または `proxy run --upstream-proxy URL`。
- 接続先を検査したIPでCONNECTし、管理対象の宛先ポリシーを維持。独自CAは `[proxy].ca_bundle` または `--ca-bundle PATH`。
- TLS内のURL・SNIを検査する機能は、この指定でも追加されない。

### path・methodまで限定する場合

| 検査 | 一般CONNECT | 固定origin broker |
|---|---|---|
| 接続先 | CONNECTの宛先host・port・解決IP | 設定したHTTPS origin |
| TLS内のHTTP Host・path・method | 検査しない | 上流Hostを固定し、許可path・methodでだけ転送 |
| path指定 | なし | 配下を含む `Paths`、完全一致の `ExactPaths` |
| query・body | 検査しない | 内容を検査しない。上流API自身の権限も必要 |

- brokerの完全一致pathとCLIのmethod指定は**未リリース（main）**です。[設定・具体例](guides/credential-broker.md)を参照してください。
- SNIの照合だけでは、暗号化されたHTTP Hostやpathを制限できません。
- brokerだけを通したい場合は、一般egressを止めるか、そのoriginへの一般HTTP/CONNECTを許可しないでください。
- 固定AI経路の認可は独立しています。brokerの規則で、他の経路の権限が変わることはありません。
- 任意のTLS通信を復号する機能は追加していません。

## 更新と既存環境からの移行

- 未設定時の既定値を既定拒否へ変更しました。
- 更新後はホスト側の許可ドメイン・CIDRを設定して新しいワーカーを作成してください。
- 従来の公開宛て許可が必要なら `forward_default_deny = false` を明示します。
- 既存proxy・ワーカー・CONNECTは自動変更しません。
- 設定ファイルの読み込み失敗・構文エラー・不正な既定拒否の値はCLIの起動エラーになります。

- プロキシでのポリシー拒否はHTTP/CONNECTとも403で返します。
- ドメイン・ポートをDNS問い合わせ前に、解決されたIPをTCP接続前に検査します。
- 直接通信の隔離は名前解決の失敗に依存せず、経路・転送制御とnftablesで維持します。

- 起動時に規則を検証する。設定ファイルの自動再読込は行わない。
- 未知のロール、誤ったドメイン/CIDR/ポートは起動エラー。
- 未リリースmainでは、[確認・承認付きの即時変更](guides/live-policy.md)を利用可能。既存接続を閉じ、規則の世代と完了状態を確認する。
- 通常のワーカーは共有egress proxyを使用する。
- `oaiso run --egress-allow-domain api.example.com` を指定したワーカーは、ポリシーの識別子を持つ専用egress proxyに接続する。
- 異なる許可リストのワーカーはproxyを共有しない。
- 同じ許可リストのワーカーは共有できる。
- プロジェクトの `oaiso.toml` では `[run] egress_allow_domains = ["api.example.com"]` と書ける。
- ホストのグローバル許可ドメインがある場合、ワーカーの指定はその範囲内に限る。
- `--no-internet` は一般通信を止める。

- 設定ファイルの変更は新しいproxy・ワーカーへ適用する。既存Proxyを変更する場合は、明示的な`policy plan/apply`を使う。
- 設定編集だけでは既存のCONNECTや通信中のストリームを打ち切らない。
- 緊急時は `oaiso policy quarantine --worker <ID>` で対象ワーカーを停止・隔離する。
- 一般通信だけでなくAI通信と全ジョブも終了し、再起動は禁止される。
- workspace・worktree・ログは保持する。
- 隔離停止したワーカーから再開する場合は、新しいワーカー・要求IDを使う。
- 隔離の詳細と失敗時の扱いは[追加制御](sandbox-features.md#通信の停止と隔離)を参照。
- 不要になったポリシーproxyは、利用ワーカーを停止した後に `oaiso proxy rm --name <proxy名> --force` で回収できる。

- TUIの `Default deny` も未設定時は有効になる。
- CLIでの新規ワーカー作成時に既定拒否で宛先が未許可なら、一般egressへ接続しない。
- Go APIもゼロ値で既定拒否となる。
- 宛先の許可なしに公開先への通信を許可するには `Options.EgressPolicy.AllowPublicDestinations: true` を明示する。
- ホスト側に許可が一つもない場合、ワーカー側から許可ドメインを追加して緩めることはできない。
- Go APIのホストポリシーは `LaunchRequest.EgressAllowDomains` を省略しても管理proxyで強制する。
- `=api.example.com` のホスト規則をワーカーが `api.example.com` に広げることも拒否する。
- 設定内容は不変の要求条件に含まれ、同じRequestIDへ異なるポリシーを送ると競合する。

- `proxy run`は既存egressコンテナのポリシー識別子と要求した設定を照合し、古い版や設定の不一致をエラーにする。
- 既存コンテナへ設定を反映したことにはせず、稼働中プロキシも自動で削除しない。
- 設定ファイルの編集だけでは稼働中プロキシは変化しない。

- 既定のproxyイメージはCLIのビルド版とprotocolラベルを照合する。リリース版では、必要なイメージを配置済みの静的CLIからBuildKitで作成する。ソース取得・Goは不要。
- 独自の `proxy_image` は自動で上書きせず、互換性が不明なら明示的な再ビルドを要求する。

1. 稼働ワーカーの成果を保存し、停止・削除する。
2. `oaiso proxy stop --name codex-http-proxy`、`oaiso proxy rm --name codex-http-proxy` で一般通信用proxyを回収する。
3. `oaiso proxy run` で新しい設定の一般通信用proxyを作成する。
4. ワーカーを新規作成する。

- 上は通信設定だけを変更する場合。CLI・イメージの更新を伴う場合は、認証用も含めて[更新手順](operations.md#cliプロキシを更新する)を実施。

- KataワーカーはproxyのIPを固定しており、IP変更時は保存済みワーカーの再起動を拒否する。

- 改造版proxyではトークンと既存認証protocolを維持する。
- auth proxyも更新する場合は、同じ停止期間に[更新手順](operations.md#cliプロキシを更新する)を適用する。

## コードの境界

| モジュール | 責任 |
|---|---|
| `internal/networkpolicy` | HTTP/実行エンジン/認証から独立した宛先判定、設定検証、DNS応答検証とIP固定dialer |
| `authproxy/forward.go` | 一般通信のHTTP/CONNECT処理 |
| `authproxy/transport.go` | ポリシーとHTTP transportの接続、HTTPリダイレクトの再検証 |
| `authproxy/openai.go`, `anthropic.go` | 各プロバイダーの認証注入とOAuth更新 |
| `authproxy/reverse.go`, `websocket.go` | 共通のHTTP/SSE転送とWebSocket処理 |
| `authproxy/tokens.go`, `admin.go`, `restore.go` | placeholderの発行・失効・復元と管理API |
| `authproxy/config.go`, `proxy.go` | 設定とサービスの組み立て・起動停止 |
| `authproxy/service.go`, `remote.go` | ワーカー管理側との境界。HTTP上の管理protocolを保てば外部実装へ交換可能 |
| `cmd/proxy_policy.go` | CLI/configの読み込みと既存コンテナの設定照合 |

- 独自ルールを加える場合は`networkpolicy.Policy`の`CheckDestination`と`CheckAddress`を実装し、`authproxy.Config.EgressPolicy`へ注入する。
- 設定由来の基本ポリシーとANDで合成するため、追加実装がprivate IP遮断等を緩めることはない。
- 拒否には`networkpolicy.ErrDenied`を返す。
- 実装は並行呼び出しに対応させ、存続する接続との不整合を避けるためポリシーを稼働中に変更しない。

- ポリシーはワーカー固有のinternal networkへ接続したproxyの選択で強制する。
- 未検証のHTTPヘッダーや申告されたワーカー名を認可根拠にしない。

## 動作確認

- 宛先判定・DNS・redirect・CONNECTは自動テスト、VMを含めた通信分離は実Kata受入試験で確認しています。
- 詳細は[テストと動作確認](testing.md)を参照してください。

[TUI・プリセット・検査・拒否イベント](sandbox-features.md#通信の確認と設定)
