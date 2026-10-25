# 認証brokerの設定

**日本語** | [English](../en/guides/credential-broker.md)

- 設定支援：**未リリース（main）**。broker本体はv0.0.2以降。
- 固定したHTTPS宛先へ、許可したpath・methodでだけ秘密を注入します。
- [通信と認証の境界](../proxy-policy.md)は維持します。VMにはProxy用プレースホルダーだけを渡します。

## 設定を作る

通常ユーザーで実行。`https://api.example.com`と`/v1/read`は利用先に置き換えます。

| テンプレート | 注入するヘッダー | 許可method |
|---|---|---|
| `bearer-readonly` | `Authorization: Bearer …` | GET、HEAD |
| `api-key-readonly` | `X-Api-Key: …` | GET、HEAD |

```bash
oaiso broker templates
oaiso broker configure
```

TUIでは名前・HTTPS宛先・許可pathを入力し、テンプレートを適用→Validate→秘密を入力→Saveの順に進めます。

- 既存TUIの`B`キーからも開けます。node停止中は`oaiso broker configure`を使います。
- `Load route`で指定名の設定を読みます。保存済みの秘密は表示しません。秘密を空欄にすると維持し、入力すると置換します。
- pathは指定値とその配下が対象です。methodは必要なものだけに絞ってください。
- 同時編集を検出した場合は保存を拒否します。再読み込みして編集し直してください。

CLIで作る場合（Bash。入力を画面と履歴に残さない）：

```bash
read -r -s -p 'Broker secret: ' broker_secret
printf '\n'
printf '%s' "$broker_secret" | oaiso broker set service \
  --template bearer-readonly --origin https://api.example.com \
  --path /v1/read --secret-stdin
unset broker_secret
```

## 検証してProxyへ適用する

```bash
oaiso broker check
```

- 成功：終了0。宛先・path・method・`secret_set: true`を出力し、秘密値は出しません。
- 検査対象：所有者・ファイル権限・JSON・固定HTTPS宛先・path・method・ヘッダー・秘密の存在。
- DNS、接続、提供元の認証・推論は実行しません。
- 保存先：`~/.config/codex-dock/broker/routes.json`。ディレクトリ0700、ファイル0600、最大1MiB／128経路。**brokerの秘密はこのファイル内の平文**です。エージェント用ageプロファイルとは別です。

ワーカーを回収し、既存Proxyを明示的に削除してから新規作成します。稼働中のProxyやVMへ設定を自動反映しません。

```bash
oaiso proxy run --credential-routes "$HOME/.config/codex-dock/broker/routes.json"
```

ホスト設定に保存する場合（`~/.config/codex-dock/config.toml`の既存`[proxy]`へ追記）：

```toml
[proxy]
credential_routes = "/home/alice/.config/codex-dock/broker/routes.json"
```

- `--file PATH`で別ファイルを編集・検査できます。Proxyの引数／ホスト設定にも同じ絶対パスを指定します。
- nodeはbrokerファイルとVM共有の重なりを両方向で拒否します。ファイルはworkspace外に置いてください。
- host root・同一UIDは信頼対象。提供先が受け取った秘密を応答本文へ返すことまでは防げません。信頼する固定宛先だけを設定します。
