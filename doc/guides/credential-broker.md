# 認証brokerの設定

**日本語** | [English](../en/guides/credential-broker.md)

- 固定したHTTPS宛先へ、許可したpath・methodでだけ秘密を注入します。
- VMは発行済みプレースホルダーでbrokerを呼び出します。実資格情報は認証Proxyが保持します。

| 機能 | 利用可能な版 |
|---|---|
| 固定HTTPS宛先、path配下、methodの制限 | v0.0.2以降 |
| 設定テンプレート・CLI・TUI | v0.0.4 |
| 完全一致path、CLIのmethod指定 | v0.0.4 |

## 制限の範囲を選ぶ

| 指定 | 許可する範囲 |
|---|---|
| `--exact-path /v1/read` | `/v1/read`だけ。子パス・`/v1/read-more`は拒否 |
| `--path /v1/read` | `/v1/read`と`/v1/read/...`。`/v1/read-more`は拒否 |
| 両方を指定 | どちらかのpathに一致すれば許可 |
| path指定なし | 保存・起動を拒否 |
| `--method GET` | GETだけ。省略時はGET・HEAD |
| `--method POST` | POSTだけ。CONNECT・TRACE等は設定不可 |

- methodは繰返し、または `--method GET,HEAD` で指定します。
- `--exact-path`は繰返し指定できます。
- 完全一致はURLのpath部分です。query・bodyの内容は検査しません。
- 曖昧なエンコード、dot segment、重複slash、末尾slash、制御文字などの曖昧なpathは拒否します。
- リダイレクトは追従せず、WebSocketは拒否します。VMのHostやmethod上書きヘッダーは上流へ渡しません。
- 上流APIがquery・bodyで転送先や操作を受け取る場合、そのAPI自体の権限設計も確認してください。

## 完全一致の読取り経路を作る

通常ユーザーのBashで実行します。`https://api.example.com`と`/v1/read`は利用先に置き換えてください。秘密は画面・履歴・コマンド引数へ出しません。

```bash
read -r -s -p 'Broker secret: ' broker_secret
printf '\n'
printf '%s' "$broker_secret" | oaiso broker set service \
  --template bearer-readonly --origin https://api.example.com \
  --exact-path /v1/read --method GET --secret-stdin
unset broker_secret
oaiso broker check
```

出力例（経路が1件の場合）：

```json
[{"name":"service","upstream":"https://api.example.com","paths":null,"exact_paths":["/v1/read"],"methods":["GET"],"header":"Authorization","prefix":"Bearer ","secret_set":true}]
```

- 終了0と `secret_set: true` が設定保存・検査の成功の目印です。
- `paths: null` は配下の許可がないことを表します。完全一致の指定だけが有効です。
- DNS、接続、提供元の認証・推論は実行しません。

### 配下も許可する場合

上で保存した`service`を、GET・HEADで配下も許可する設定に変更します。保存済みの秘密は維持します。

```bash
oaiso broker set service --origin https://api.example.com --path /v1/read
oaiso broker check
```

### POSTだけの別経路を作る場合

別の秘密をstdinから入力します。`/v1/write`だけを許可し、子パスは許可しません。

```bash
read -r -s -p 'Broker secret: ' broker_secret
printf '\n'
printf '%s' "$broker_secret" | oaiso broker set service-write \
  --origin https://api.example.com --exact-path /v1/write \
  --method POST --secret-stdin
unset broker_secret
oaiso broker check
```

## TUIで編集する

```bash
oaiso broker templates
oaiso broker configure
```

| 項目 | 内容 |
|---|---|
| Template | `bearer-readonly`はAuthorization、`api-key-readonly`はX-Api-Key。どちらも初期methodはGET・HEAD |
| Path prefixes | 指定pathと配下を許可 |
| Exact paths | 指定pathだけを許可 |
| Methods | 必要なmethodだけを指定 |
| New secret | 空欄なら保存済みの秘密を維持。保存済みの値は表示しない |

1. 名前・HTTPS宛先・pathを入力します。
2. テンプレートを適用し、必要ならMethodsを変更します。
3. Validate→秘密を入力→Saveの順に進めます。

- 既存TUIの`B`キーからも開けます。node停止中は`oaiso broker configure`を使います。
- `Load route`で指定名を読み込みます。同時編集を検出したら、再読み込みして編集し直してください。

## Proxyへ適用する

ワーカーを回収し、既存Proxyを明示的に削除してから新規作成します。保存だけでは稼働中の規則は変わりません。

```bash
oaiso proxy run --credential-routes "$HOME/.config/codex-dock/broker/routes.json"
```

ホスト設定に保存する場合は、`~/.config/codex-dock/config.toml`の既存`[proxy]`へ追記します。

```toml
[proxy]
credential_routes = "/home/alice/.config/codex-dock/broker/routes.json"
```

- VMは認証Proxyの `/broker/service/v1/read` へ、発行済みプレースホルダーのBearer認証を付けて要求します。
- 既存 `Paths` は従来どおり配下も許可します。完全一致へ狭める場合は `ExactPaths`へ移し、重なる `Paths`を削除してください。
- Go APIは `CredentialRoute.ExactPaths`、JSON設定は `ExactPaths`、`broker check`の出力は `exact_paths` です。
- `--file PATH`で別ファイルを編集・検査できます。Proxyにも同じ絶対パスを指定します。

## 保存と保護の範囲

| 項目 | 内容 |
|---|---|
| 保存先 | `~/.config/codex-dock/broker/routes.json` |
| 編集支援の権限・上限 | ディレクトリ0700、ファイル0600。最大1MiB／128経路 |
| 秘密の形式 | このファイル内では平文。エージェント用ageプロファイルとは別 |
| VMへの共有 | nodeはbrokerファイルと共有mountの重なりを拒否。ファイルはworkspace外へ置く |
| 認証・失効 | 生存する発行済みtokenが必要。失効時は通信中のbroker streamも取り消す |
| モデル制限付きtoken | 任意APIのモデル認可を検証できないため、broker経路を拒否 |
| 上流 | 信頼する固定宛先だけを指定。上流が秘密を応答へ返す場合まで防げない |

- host root・同一UIDは信頼対象です。
- [一般CONNECTとの違い・迂回防止](../proxy-policy.md#pathmethodまで限定する場合)を確認してください。一般通信と固定AI経路の権限は、それぞれ別に設定します。
