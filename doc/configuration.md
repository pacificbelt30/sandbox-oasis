# 設定リファレンス

**日本語** | [English](en/configuration.md)

- 利用者ごとの設定（グローバル設定）は `~/.config/codex-dock/config.toml` に記述します。
- 別ファイルを使う場合は `oaiso --config PATH COMMAND` と指定します。
- ソース配置では `make install-config` で例を配置できます。
- 既存ファイルは上書きしません。

## プロジェクト設定

- **`oaiso.toml` は任意です。なくても既定の設定で起動できます。**
- プロジェクトごとに設定を変える場合や、チームで同じ設定を使う場合に作成します。
- ファイル名は小文字の `oaiso.toml`。このファイルを使わない場合、`project init` / `project check` は不要です。
- リポジトリ直下に `oaiso.toml` を置くと、そのディレクトリからの `oaiso init`、`oaiso run`、`oaiso codex`、`oaiso build` が読み込みます。
- `run --project DIR` は `DIR/oaiso.toml` を使います。
- 親ディレクトリの設定は自動探索しないため、リポジトリのルートで実行してください。

```bash
oaiso project init
oaiso project check
git add oaiso.toml
```

**作成・確認時の出力例**

```text
$ oaiso project init
/home/user/my-project/oaiso.toml
$ oaiso project check
Project config: /home/user/my-project/oaiso.toml
build.template = plain
run.image = codex-dock:latest
```

- パスは実行したディレクトリに変わります。`project check` は設定の確認だけで、VM起動やAIへの接続は行いません。
- 旧 `codex-dock.toml` がある場合は、[名前を変更](migration.md#プロジェクト設定を-oaisotoml-に変更する)してから実行してください。旧名の設定を無視して起動することはありません。

- `oaiso run` で起動するエージェントの既定値は CLI からも設定できます。
- `oaiso default` は現在有効な値と設定元を表示します。

```bash
oaiso default codex          # グローバル設定
oaiso default claude --local # このプロジェクトだけ
oaiso default shell          # 認証済みシェルへ戻す
```

- `--local` はこのディレクトリの `oaiso.toml` を作成または更新します。
- 共有したい場合はコミットしてください。
- 明示した `--agent`、`--exec`、`IMAGE COMMAND`、`-- COMMAND` は既定値より優先されます。
- `oaiso codex` は設定に関係なく Codex を起動します。

- 生成する最小構成は次のとおりです。
- 組み込み `plain` イメージを使うため、クローン先では `oaiso init` が不足するイメージをビルドできます。

```toml
[run]
image = "codex-dock:latest"
approval_mode = "suggest"

[build]
template = "plain"
```

### ホスト管理のエージェントポリシー

Codex / Claude固有の既定値は、リポジトリと分離した `~/.config/codex-dock/agent-policies/config.toml` で管理できます。

```toml
[run]
agent_policy = "approve-for-me"
```

一時的な選択は `oaiso run --agent-policy NAME`、別の保存先はグローバルな `--agent-policy-file PATH` を使います。ポリシーの作成・監査・全項目は[ホスト管理のエージェントポリシー](guides/agent-policies.md)を参照してください。`oaiso.toml` からホスト専用項目は変更できません。

- 独自環境はプロジェクト内のDockerfileを指定します。
- ビルドコンテキストはリポジトリのルートです。
- `FROM codex-dock:latest` を使う場合、クローン先で不足するベースイメージも先に作ります。

```toml
[run]
image = "my-project:dev"
model = "選択するモデル"
watch_polling = true

[build]
dockerfile = "Dockerfile.codex-dock"
tag = "my-project:dev"
```

- `packages.dock` は従来どおり自動で読み込みます。
- 依存パッケージやベースイメージを固定したい場合は、プロジェクトのDockerfileやパッケージ指定にもバージョンを記述してください。
- プロジェクトでテンプレートを指定する場合、組み込み版またはその未変更コピーだけを使えます。
- 各利用者の編集済みテンプレートに依存する構成は、プロジェクトのDockerfileへ移してください。

### プロジェクトで指定できる項目

| 分類 | キー・指定可能な値 |
|---|---|
| イメージ・テンプレート | `run.image`、`run.template` |
| エージェント・モデル | `run.agent`（`shell` / `codex` / `claude`）、`run.model` |
| 承認 | `run.approval_mode`（`suggest` / `auto-edit`） |
| 資源 | `run.profile_size`（組み込み `standard` / `small` / `compact`） |
| ファイル制限 | `run.read_only`、`run.read_only_rootfs`、`run.no_git_metadata`、`run.deny_read`、`run.deny_write` |
| 通信制限 | `run.no_internet`、`run.egress_allow_domains`、`run.policy_preset` |
| 寿命・保持 | `run.max_lifetime`、`run.idle_timeout`、`run.retention` |
| 起動処理 | `run.start_hooks`、`run.readiness`、`run.readiness_timeout` |
| 監視 | `run.watch_polling` |
| ビルド | `build.dockerfile`、`build.template`、`build.tag` |

| 検証条件 | 扱い |
|---|---|
| `read_only` / `no_internet` | `true` のみ指定可。ホストの制限を緩めない |
| `run.image` / `build.tag` | 同じ値を指定 |
| 認証・Proxy・runtime・token TTL・追加mount | プロジェクトには書けない。グローバル設定／明示CLIフラグで管理 |
| 不明なキー・不一致 | `project check` がエラー |

### 起動処理の書式と優先順位

| 設定先 | `start_hooks` | `readiness` |
|---|---|---|
| グローバル `config.toml` | 文字列配列。例：`["npm ci"]` | shell文字列。例：`"test -d node_modules"` |
| プロジェクト `oaiso.toml` | argvの配列。例：`[["npm", "ci"]]` | argv配列。例：`["test", "-d", "node_modules"]` |

- プロジェクト例はguest内で実行。`init` / `run` 前に、cloneした設定のコマンドと共有範囲を確認。
- ファイル拒否リスト：ホスト設定へ追加して適用。
- 最大寿命・idle：明示CLI指定がない場合、ホスト値より短い正の値だけを採用。保持期限は正のプロジェクト指定を採用。
- 実行・回収条件：[寿命と再現性](sandbox-features.md#寿命と再現性)。

- 不足するイメージは `init` / `run` 時にビルドします。
- 既存イメージの再ビルド判定はDockerfileの更新日時を見るため、`COPY` するファイルや依存関係だけを変えた場合は `oaiso build` を実行してください。

- 製品から使う場合は[Go API](go-api.md)の `PrepareProject` で同じプロジェクトファイルを検証し、必要なイメージを作成できます。
- Go API はグローバル設定を自動で読み込まないため、ホスト設定と認可は製品側で適用してください。
- `egress_allow_domains` は `LaunchRequest.EgressAllowDomains` に反映されます。

## 最小の設定例

```toml
default_image = "codex-dock:latest"
proxy_image = "codex-dock-proxy:latest"

[run]
runtime = "kata"
user = "current"
approval_mode = "suggest"
token_ttl = 3600

[proxy]
container_url = "http://codex-auth-proxy:18080"
admin_url = "http://127.0.0.1:18081"
forward_allow_domains = ["github.com", "registry.npmjs.org"]
forward_allow_ports = [80, 443]
```

認証情報はこの例に埋め込まず、[認証ソース](auth-proxy.md#認証ソース)を使ってください。

- この例は従来のホスト連携用。独立プロファイルは、最初のセクションより前へ `auth_profile` を追加し、同じ `[proxy]` 内へ `admin_secret_file` を統合。
- [認証設定の作成手順](commands/auth.md#日常操作で使うプロファイルを固定する)。パスの例は実際の絶対パスへ置換。

## 優先順位

- 通常の優先順位は **明示したCLIフラグ > プロジェクトの `oaiso.toml` > グローバルの `config.toml` > 組み込み既定値** です。
- プロジェクトの `read_only` と `no_internet` は制限を追加する場合に限ります。
- `run`・`build` 専用の設定がある場合は、グローバル設定より優先します。
- 環境変数は[下記の対応項目](#環境変数)を参照してください。
- すべての設定に `CODEX_DOCK_` を付ければ上書きできるわけではありません。

## グローバル設定

| キー | 既定値 | 用途 |
|---|---|---|
| `auth_profile` | 未選択 | 独立認証プロファイル。`--auth-profile` |
| `auth_profile_dir` | `~/.config/codex-dock/profiles` | プロファイル保存root。`--auth-profile-dir` |
| `default_image` | `codex-dock:latest` | ワーカーのOCIイメージ |
| `proxy_image` | `codex-dock-proxy:latest` | 2種類のプロキシのOCIイメージ |
| `default_template` | 未設定 | `run`・`build` のテンプレート |
| `default_token_ttl` | `3600` | 短命トークンのTTL、秒 |
| `auto_build` | `true` | ファイルとして存在するテンプレートの更新時に再ビルド |
| `default_shell_type` | 自動判定 | `bash` または `zsh` |
| `verbose` / `debug` | `false` | CLIの詳細ログ |

### `auto_build`

- テンプレートのDockerfile更新日時と、イメージの作成日時を比較します。
- Dockerfileが新しい場合は `run` 時に再ビルドします。
- バイナリに埋め込まれたままのテンプレートには比較するファイルがないため、この判定を行いません。
- イメージが存在しない場合の自動ビルドは、この設定を無効にしても行います。

### `default_shell_type`

- 明示しない場合はホストの `$SHELL` を見て、`zsh` ならzsh、それ以外はbashを選びます。
- デバッグ用 `--shell` は、明示しない限りbashです。
- 詳しくは[run](commands/run.md)を参照してください。

## `[run]`

| キー | 既定値・引継ぎ元 | 対応フラグ |
|---|---|---|
| `runtime` | `kata` | `--runtime` |
| `kata_runtime` | `io.containerd.kata.v2` | `--kata-runtime` |
| `image` | `default_image` | `--image` |
| `template` | `default_template` | `--template` |
| `agent` | `shell` | `--agent` |
| `token_ttl` | `default_token_ttl` | `--token-ttl` |
| `approval_mode` | `suggest` | `--approval-mode` |
| `user` | `current` | `--user` |
| `shell_type` | `default_shell_type` | `--shell-type` |
| `auto_build` | `auto_build` | 設定ファイルで指定 |
| `mounts` | 空配列 | `--mount` |
| `watch_polling` | `false` | `--watch-polling` |
| `model` | 未設定 | `--model` |
| `read_only` | `false` | `--read-only` |
| `read_only_rootfs` | `false` | `--read-only-rootfs` |
| `agent_state_size` | `auto`（512MiB） | `--agent-state-size`。[状態保存](guides/agent-state.md)、未リリース（main） |
| `no_git_metadata` | `false` | `--no-git-metadata` |
| `profile_size` | `standard` | `--profile-size` |
| `no_internet` | `false` | `--no-internet` |
| `egress_allow_domains` | 空配列 | `--egress-allow-domain` |
| `policy_preset` | 未指定 | `--policy-preset` |
| `deny_read` / `deny_write` | 空配列 | `--deny-read` / `--deny-write` |
| `max_lifetime` / `idle_timeout` / `retention` | `"0s"` | 同名ハイフン形式。例：`"30m"` / `"2h"`。`reap` が必要 |
| `protect_main` | `false` | v0.0.4。`--protect-main`。メイン処理をidle回収から保護 |
| `max_requests` / `max_concurrent` | `0` | v0.0.4。`--max-requests` / `--max-concurrent` |
| `max_transfer_bytes` / `max_observed_tokens` | `0` | v0.0.4。`--max-transfer-bytes` / `--max-observed-tokens`。[上限の条件](guides/usage-limits.md) |
| `environment_lock` | 未指定 | `--environment-lock` |
| `image_id` | 未指定 | `--image-id` |
| `start_hooks` | 空配列 | `--start-hook` |
| `readiness` | 未指定 | `--readiness` |
| `readiness_timeout` | `"0s"`（実効60秒） | `--readiness-timeout` |
| `ssh_proxy` / `jvm_proxy` | `false` | `--ssh-proxy` / `--jvm-proxy` |
| `ca_bundle` | 未指定 | `--ca-bundle`。ワーカーに渡す追加PEM CA |

- 上表は信頼済みグローバル設定。プロジェクトに置けるキーは[許可一覧](#プロジェクトで指定できる項目)に限定。
- 日付・期間ではなくGo duration形式を使用。`1d` ではなく `24h`。

- 明示した `--image` はテンプレートによるイメージ選択より優先します。
- `runtime = "runc"` は同じcontainerdノードの互換モードで、VM隔離はありません。

### `run.mounts`

```toml
[run]
mounts = ["/home/user/datasets:/data:ro", "./cache:/cache:rw"]
```

- 相対パスはコマンド実行時のディレクトリから解決します。
- `--mount` を指定した場合、設定ファイルの配列は追加せず置き換えます。
- `--read-only` はworkspaceの指定であり、追加mountの権限は個別に指定します。

- `network_name` は旧設定キーで、現行のnetworkコマンドは `dock-net-proxy` を使います。
- プロキシの接続先は `proxy run --network` で指定します。

## `[build]`

| キー | 引継ぎ元 | 対応フラグ |
|---|---|---|
| `template` | `default_template` | `build --template` |
| `tag` | `codex-dock:latest`。テンプレート指定時はそのタグ | `build --tag` |

Dockerfile、テンプレート、カスタムイメージの例は[build](commands/build.md)を参照してください。

## `[proxy]`

- 独立認証の選択：グローバルの `auth_profile` / `auth_profile_dir`。
- 選択中：指定したStoreだけを使用し、ホスト認証へfallbackしない。
- 未選択：従来のホスト連携。
- プロジェクト設定での指定：不可。
- [作成と起動](commands/auth.md#独立プロファイル)を参照。

| キー | 既定値 | 対応箇所 |
|---|---|---|
| `max_connections` / `max_connections_per_source` | `0` | v0.0.4。`proxy run --max-connections` / `--max-connections-per-source` |
| `network_bytes_per_second` | `0` | v0.0.4。`--network-bytes-per-second`。[対象と動作](guides/resource-limits.md) |
| `container_url` | `http://codex-auth-proxy:18080` | `run --proxy-container-url` |
| `admin_url` | `http://127.0.0.1:18081` | `run --proxy-admin-url`、ワーカー管理 |
| `admin_secret` | 未設定 | 管理クライアントの認証。サーバー起動には `proxy run --admin-secret` も指定する |
| `admin_secret_file` | 未設定 | 所有者の0600ファイル。`proxy run --admin-secret-file` / `run --proxy-admin-secret-file`、init・ワーカー管理・TUIで使用 |
| `forward_default_deny` | `true` | `proxy run --forward-default-deny`。許可ドメイン・CIDRの両方が空なら一般通信を拒否 |
| `forward_allow_domains` / `forward_deny_domains` | 空配列 | `proxy run --forward-allow-domain` / `--forward-deny-domain` |
| `forward_allow_cidrs` / `forward_deny_cidrs` | 空配列 | `--forward-allow-cidr` / `--forward-deny-cidr` |
| `forward_allow_ports` / `forward_deny_ports` | 空配列 | `--forward-allow-port` / `--forward-deny-port` |
| `allowed_models` | 空配列（制限なし） | `proxy run --allow-model`。正確な上流モデルID。制限時はWebSocket拒否 |
| `upstream_proxy` | 未指定 | `proxy run --upstream-proxy`。明示した上流HTTP(S) Proxy |
| `ca_bundle` | 未指定 | `proxy run --ca-bundle`。Proxy側の追加PEM CA |

- `run.ca_bundle`：ワーカー側。`proxy.ca_bundle`：Proxy側。BuildKit daemonのregistry用CAは管理者が別途設定。

- `admin_secret_file` と値指定（設定・環境変数を含む）が両方有効なら拒否します。
- 明示したrunのsecretフラグは設定値より優先します。
- 管理ファイル・profile保存先はworkspaceや追加mountへ含めないでください。
- 管理secretの変更はworkerを退避した後、Proxyを明示的に再作成して行います。

- `container_url` はワーカーが使うURL、`admin_url` はホストCLIが使うURLです。
- 一般通信用のURLは `run --http-proxy-url` で指定します。
- 旧 `firewall.proxy_container_url` は互換用に読み込みますが、新規設定には `[proxy]` を使ってください。

- 通信ルールは一般egressプロキシにだけ適用します。
- 拒否を優先し、空でない許可リストはすべて満たす必要があります。
- `forward_default_deny` は省略時も `true` で、空の宛先許可リストを拒否します。
- 公開宛て通信を許可する従来動作は `false`（CLIでは `--forward-default-deny=false`）の明示指定で選びます。
- ドメインは `example.com` ならサブドメイン込み、`=example.com` なら完全一致です。
- private IP等の遮断は、許可ルールでは解除できません。
- 変更はプロキシの再作成後に反映します。
- [通信ポリシー](proxy-policy.md)を参照してください。

## 環境変数

| 変数 | 用途 |
|---|---|
| `CODEX_DOCK_RUNTIME` | `run.runtime` の上書き |
| `CODEX_DOCK_KATA_RUNTIME` | `run.kata_runtime` の上書き |
| `CODEX_DOCK_PROXY_ADMIN_SECRET` | ホストCLIの管理secret。明示したCLIフラグを優先 |
| `CODEX_DOCK_KATA_NODE_SOCKET` | CLIが接続する管理Unixソケットの絶対パス |
| `CODEX_DOCK_STATE_DIR` | ワーカーの回収・再試行記録の保存先。安全な絶対パスを指定 |
| `XDG_STATE_HOME` | 専用保存先未指定時の状態ルート |
| `BUILDKIT_HOST` | ビルド先。既定は `unix:///run/codex-dock-buildkit/buildkitd.sock` |
| `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` / `ALL_PROXY` と小文字版 | BuildKitへのbuild args |
| `CODEX_PROXY_CERT` | ビルド中に使うCAのパス。`proxy_ca` secretで渡す |
| `OPENAI_API_KEY` / `ANTHROPIC_API_KEY` | ホスト認証プロキシの実APIキー |
| `CLAUDE_CODE_OAUTH_TOKEN` | ホスト認証プロキシ用のサブスクaccess token。`claude setup-token` で発行 |
| `CODEX_DOCK_CODEX_VERSION` / `CODEX_DOCK_CLAUDE_VERSION` | ビルド時の固定CLI版の上書き |

- 実行方式の環境変数は、明示したフラグより下、設定ファイルより上の優先順位です。
- `DOCKER_HOST` とDocker contextは利用しません。

- Claude Codeのサブスク認証にはホストの `CLAUDE_CODE_OAUTH_TOKEN` も使えます。
- ビルド用の版指定は `CODEX_DOCK_CLAUDE_VERSION` です。
- [Claude Code](claude-code.md)に認証の優先順位と更新手順をまとめています。

## ホスト管理サービスの設定

利用者のconfigと、root管理サービスの設定は別です。

- 以下の `/etc/codex-dock/` は手動配置の例。自動配置では `/opt/codex-dock/releases/` 内の管理設定を参照。
- 実際の起動引数・設定先は `systemctl cat codex-dock-node codex-dock-containerd codex-dock-buildkit` で確認。

| 対象 | 設定場所・方法 |
|---|---|
| ノード | `oaiso node serve --help`。socket、state、cgroup、DNS、同時実行上限 |
| 専用containerd | `/etc/codex-dock/containerd.toml` |
| Kata | `/etc/codex-dock/kata.toml` |
| 固定資産manifest | `/etc/codex-dock/kata-assets.json` |
| BuildKit | `/etc/codex-dock/buildkit.toml` |

- `[kata].node_socket` はKata検証クライアントの設定です。
- 標準と異なるsocketを使う場合は、実行管理用の `CODEX_DOCK_KATA_NODE_SOCKET` も同じパスにそろえてください。
- 配置・更新は[Kata実行ホストの準備](kata.md)に従います。

## 認証ファイルの場所

| ファイル | 内容 |
|---|---|
| `~/.config/codex-dock/apikey` | OpenAI APIキー。`auth set` で保存 |
| `~/.codex/auth.json` | Codexの既存ログイン情報 |
| `~/.config/codex-dock/anthropic-apikey` | Anthropic APIキー |
| `~/.claude/.credentials.json` | Claude Codeの既存ログイン情報 |

- 本文を公開ログやイメージに含めないでください。
- 読込優先順位とマウントは[認証プロキシ](auth-proxy.md#認証ソース)を参照してください。

- ホストの `[proxy].allowed_models` は正確な上流モデルIDの許可リストです。
- 空は従来動作を維持し、設定時は未対応AI経路とWebSocketを拒否します。
- `proxy run --allow-model` の明示指定が優先します。
- プロキシ再作成と[対応経路の確認](sandbox-features.md#モデル認可と段階的な起動)が必要です。

## ノード管理者の資源プロファイル

- [FD・block I/Oの追加プロファイル設定](guides/resource-limits.md)：v0.0.4。組み込みプロファイルの既定値は維持します。

- `run.profile_size` は組み込みまたはnodeに登録された名前を選びます。
- 追加の予算は、rootのnode管理者が `node serve --resource-profiles /etc/codex-dock/resource-profiles.json` で登録します。
- 利用者のTOMLで上限を新規登録する仕組みではありません。
- [許容範囲・guest資産・予約の固定](sandbox-features.md#管理者定義の資源プロファイル)を参照してください。

- `oaiso profile define compact-256 --pids-limit 256` で登録用JSONを生成でき、`oaiso profile --node --json` で実際に有効なPID上限を確認できます。
- PIDはhost・guestの両方で制限します。

- `proxy.credential_routes`: v0.0.4。[認証broker設定ファイル](guides/credential-broker.md)の絶対パス。ホスト設定だけで指定します。
