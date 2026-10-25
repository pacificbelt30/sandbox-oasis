# プロキシ管理

**日本語** | [English](../en/commands/proxy.md)

- `oaiso proxy` は、認証用と一般通信用の2つのプロキシを管理します。
- 仕組みは[認証プロキシ](../auth-proxy.md)、更新は[運用手順](../operations.md#cliプロキシを更新する)を参照してください。

- リリース版では `init` / `proxy run` が配置済みの静的CLIから必要なイメージを作成します。通常は `proxy build` の実行・ソース取得・Goの導入は不要です。

## 起動する

```bash
oaiso proxy run
```

| フラグ | 既定値 | 内容 |
|---|---|---|
| `--name` | `codex-auth-proxy` | 認証プロキシ名 |
| `--http-name` | `codex-http-proxy` | 一般通信用プロキシ名 |
| `--port`, `-p` | `18080` | 認証用dataポート。ホストへ公開しない |
| `--admin-port` | `18081` | ホストの127.0.0.1に公開する管理ポート |
| `--http-port` | `18082` | 一般通信ポート。ホストへ公開しない |
| `--network` | `dock-net-proxy` | 共通egressネットワーク。未作成なら作成 |
| `--admin-secret` | 未設定 | 管理APIのsecret。ホストCLI側にも同じ値を設定 |
| `--admin-secret-file` | 未設定 | 所有者の0600ファイルから管理secretを読む。値指定と併用不可 |
| `--forward-default-deny` | `true` | 宛先許可なしでは一般egressを拒否 |
| `--allow-model` | 空（制限なし） | 正確な上流モデルID。繰り返し可。対応AI HTTP経路だけを許可しWebSocket拒否 |
| `--upstream-proxy` | 未指定 | 宛先を検証済みIPで接続する上流HTTP(S) Proxy |
| `--ca-bundle` | 未指定 | Proxy側の追加PEM CA |
| `--max-connections` / `--max-connections-per-source` | `0` | v0.0.4。Proxy全体／接続元IPごとのTCP数 |
| `--network-bytes-per-second` | `0` | v0.0.4。接続元IPごとの双方向byte/秒。[対象と動作](../guides/resource-limits.md) |
| `--credential-routes` | 未指定 | 認証broker用のホスト0600 JSON。Auth Proxyだけへmount |
| `--credential-store-socket` / `--case-id` | 未指定 | ケース専用Store接続。組み込み向け。独立プロファイルとの併用不可 |

- 通信ルールには `--forward-allow-domain` / `--forward-deny-domain`、`--forward-allow-cidr` / `--forward-deny-cidr`、`--forward-allow-port` / `--forward-deny-port` を使います。
- ドメインとCIDRは繰り返し、ポートは繰り返しまたはカンマ区切りで指定できます。
- 例と判定順は[通信ポリシー](../proxy-policy.md)を参照してください。

- 従来の認証ファイルは呼出元のUID/GIDと専用HOMEで読み取り専用マウントします。
- `--auth-profile NAME` の[独立プロファイル](auth.md#独立プロファイル)では、そのディレクトリだけをOAuth更新用に読み書き可能でマウントし、ホスト認証を読みません。
- 認証ソースは[認証プロキシ](../auth-proxy.md#認証ソース)に記載しています。
- 既存のlayout・通信ルール・認証API能力が対応しない場合はエラーになり、既存プロキシを自動削除しません。

## ビルドする

**開発・独自のProxyイメージをソースから作る場合の手順です。**

```bash
oaiso proxy build
oaiso proxy build --tag my-proxy:v1 -f ./docker/proxy/Dockerfile
```

| フラグ | 内容 |
|---|---|
| `--tag`, `-t` | タグ。既定は設定の `proxy_image` |
| `--dockerfile`, `-f` | Dockerfileのパス |

- BuildKitを使い、OCI出力を専用containerdへ取り込みます。
- ビルドコンテキストはカレントディレクトリです。
- Goソースが必要なため、リポジトリのルートで実行してください。
- Dockerfileは `docker/proxy/Dockerfile`、旧配置の `auth-proxy.Dockerfile` / `docker/auth-proxy.Dockerfile`、設定ディレクトリの順に探します。

## 通信設定を編集する

```bash
oaiso proxy configure
```

- 編集範囲・保存・適用：[TUIのProxy設定画面](ui.md#proxy設定画面)。
- 操作順序：[宛先設定 → 検証 → 再作成](../guides/networking.md)。

## 停止・削除する

```bash
# 名前を省略すると既定の2つが対象
oaiso proxy stop
oaiso proxy rm

# 個別に指定
oaiso proxy stop --name codex-http-proxy
oaiso proxy rm --name codex-http-proxy
```

- `proxy rm --force` は実行中でも削除します。
- 共有するワーカーの認証・通信に影響するため、先にワーカーの作業を終えてください。
- 独自名で作成した場合は `--name` で個別に操作します。

## ローカルプロセスとして起動する

開発や単体利用には、内部コマンドの `proxy serve` を使えます。

```bash
oaiso proxy serve --role auth \
  --listen 127.0.0.1:18080 --admin-listen 127.0.0.1:18081 \
  --admin-secret-file "$HOME/.config/codex-dock/proxy-admin-secret"
```

- `--role auth` は認証用、`--role egress` は一般通信用です。
- 管理secretファイルは[先に作成](auth.md#管理シークレットを作成し起動する)。値をargvへ載せない例です。
- `serve` のdataリスナー既定は `0.0.0.0:18080` なので、単体利用では上のように明示してください。
- KataワーカーのネットワークやVM隔離は、このコマンドでは用意しません。
- 詳しくは[単体利用](../proxy-standalone.md)を参照してください。

- `oaiso proxy run --allow-model model-id` は繰返し指定でき、固定AI経路の要求本文でモデルを強制します。
- [対応経路と制約](../sandbox-features.md#モデル認可と段階的な起動)を確認してください。
