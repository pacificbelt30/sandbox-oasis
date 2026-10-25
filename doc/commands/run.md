# `oaiso run` — サンドボックスコンテナの起動

> **日本語** | [English](../en/commands/run.md)
>
> [← コマンドリファレンス一覧](../commands.md)

| やりたいこと | 起動方法 |
|---|---|
| Codexを直接起動 | `oaiso codex` |
| Claude Codeを直接起動 | `oaiso claude` |
| イメージ・worktree・mountを指定 | `oaiso run --agent codex` / `--agent claude` |
| 認証済みシェル | `oaiso run`（組み込み既定値） |
| コマンドを1回実行 | `oaiso run -- COMMAND...` |

| 項目 | 既定動作・設定先 |
|---|---|
| 実行基盤 | Linux x86_64・containerd・Dragonball。`--runtime kata` |
| 通信 | 専用bridge・namespace・nftablesで直接通信を遮断。必要なProxyだけ接続 |
| 既定エージェント | `oaiso default codex`。プロジェクト限定は `--local`、確認は `oaiso default` |
| イメージ | 不足時は自動ビルド。テンプレートのタグ、または既定Dockerfileを使用 |
| 再ビルド | [auto_build](../configuration.md#auto_build)で設定 |
| プロジェクト設定 | カレントの `oaiso.toml` の `[run]` / `[build]`。`--project DIR` は `DIR` の設定を使用 |

- 初回準備：[導入](../getting-started.md)。
- ネットワーク：[通信の仕組み](../network.md)。
- 独自イメージ：[build](build.md)。
- 設定の詳細：[プロジェクト設定](../configuration.md#プロジェクト設定)。

## Codex CLI を直接起動する

```bash
oaiso codex
oaiso codex "このリポジトリを調べて"
oaiso codex exec --json "変更点を要約して"
```

- 引数は `codex` にそのまま渡します。
- カレントディレクトリを `/workspace` にマウントし、ワーカー内でCodexを直接 `exec` します。
- 承認モードなどのワーカー既定値は設定ファイルの `[run]` で指定できます。
- イメージ、worktree、追加マウント、detachなどのコンテナ指定には `oaiso run --agent codex` を使います。
- 新規VMの起動時間は通常のホスト上の `codex` より長くなります。

```bash
oaiso run [OPTIONS] [IMAGE [COMMAND...]]
oaiso run [OPTIONS] -- COMMAND...  # 既定イメージでコマンドを実行
```

- イメージとコマンドの位置は `docker run IMAGE COMMAND` と同じです。
- フラグはイメージの前に置きます。
- `--image` または `--template` を指定した場合は位置引数をコマンドとして扱います。
- コマンドは `--exec` と同等ですが、全体を1つの文字列にクォートする必要はありません。

```bash
oaiso run -d --name worker codex-dock:latest
oaiso run codex-dock:latest npm test -- --watch=false
oaiso run -- npm test -- --watch=false
```

---

## 資源プロファイル

| 組み込み名 | CPU | guestメモリ | ホスト予約・上限 | guest／host PID上限 |
|---|---|---|---|---|
| `standard`（既定） | 1 | 2 GiB | 3 GiB | 512 |
| `small` | 1 | 1 GiB | 2 GiB | 512 |
| `compact` | 1 | 512 MiB | 768 MiB | 512 |

```bash
oaiso profile --node --json
oaiso run --profile-size small --agent codex
```

- `profile` だけではCLI組み込み値、`--node` では実際のnodeと管理者定義を照会。
- 予約・上限は実測メモリ使用量ではない。`compact` は大きな依存導入・ビルドに不足する場合あり。
- PID上限の変更：[管理者定義](../sandbox-features.md#管理者定義の資源プロファイル)。`profile define` はJSON生成だけで、nodeへの登録ではない。
- [実測値](../sandbox-features.md#回収の常駐とメモリの観測)を確認して選択。

## オプション一覧

- 認証をホストと分けるには `oaiso --auth-profile NAME run --proxy-admin-secret-file /private/admin-secret ...`、または信頼済みグローバル設定を使います。
- [独立プロファイルの作成と起動](auth.md#独立プロファイル)を参照してください。

| オプション | 省略形 | デフォルト | 説明 |
|---|---|---|---|
| `--runtime` | | `kata` | ワーカーの隔離方式。`runc` は明示的な互換モード |
| `--kata-runtime` | | `io.containerd.kata.v2` | 専用containerdのKata登録名 |
| `--image` | `-i` | `codex-dock:latest` | サンドボックスに使用するOCIイメージ。位置引数でも指定可能 |
| `--template` | `-T` | | サンドボックスイメージテンプレート（`plain`, `pwn` 等）。指定したテンプレートのタグに解決される。`--image` を明示指定した場合はそちらが優先。存在しなければ自動ビルドされる |
| `--pkg` | `-p` | | 追加パッケージ（繰り返し指定可）`apt:<pkg>`, `pip:<pkg>`, `npm:<pkg>` |
| `--pkg-file` | | | パッケージ定義ファイルのパス (`packages.dock`) |
| `--project` | | `.`（カレント） | `/workspace` にマウントするプロジェクトディレクトリ |
| `--worktree` | `-w` | `false` | git worktree を使ってコンテナを分離する |
| `--branch` | `-b` | | チェックアウトするブランチ名（`--worktree` が必要） |
| `--new-branch` | `-B` | `false` | 新規ブランチを作成する（`--worktree` と `--branch` が必要） |
| `--name` | `-n` | 自動生成 | コンテナ名（省略すると `codex-<形容詞>-<名詞>` 形式で自動生成） |
| `--agent` | | `shell`（設定で変更可） | 起動するエージェント（[詳細](#--agent--エージェントの選択)） |
| `--task` | `-t` | | エージェントに渡す初期タスクプロンプト |
| `--exec` | `-e` | | 認証済みシェルの代わりに実行するコマンド（[詳細](#--exec--コマンドの実行)） |
| `--approval-mode` | | `suggest` | 承認モード（[詳細](#--approval-mode--承認モードの指定)） |
| `--full-auto` | | `false` | **非推奨**: `--approval-mode full-auto` を使用してください |
| `--model` | `-m` | | エージェントに渡すモデル名 |
| `--read-only` | | `false` | プロジェクトディレクトリを読み取り専用でマウント |
| `--read-only-rootfs` | | `false` | イメージの rootfs を読み取り専用にし、HOME と一時ファイルには tmpfs を使用 |
| `--profile-size` | | `standard` | Kata VM の資源プロファイル（`standard` / `small` / `compact`） |
| `--no-git-metadata` | | `false` | linked worktree の共通 Git metadata を自動共有しない |
| `--mount` | | | 追加のホストディレクトリをマウント（繰り返し指定可）`<host-path>:<container-path>[:ro\|rw]`（[詳細](#--mount--任意ディレクトリの追加マウント)） |
| `--no-internet` | | `false` | 一般egressプロキシを接続せず、AI API経路だけを維持 |
| `--keep` | | `false` | フォアグラウンド実行の終了時にコンテナと専用ネットワークを削除しない（既定では削除し、ネットワークの蓄積を防ぐ） |
| `--force-recreate-proxy` | | `false` | ホストとAuth Proxyの認証状態が異なる場合、稼働中ワーカーがあってもAuth Proxyを再作成する（既存ワーカーの認証は失効） |
| `--token-ttl` | | `3600` | Auth Proxy トークンの有効期限（秒） |
| `--agents-md` | | | `AGENTS.md` ファイルのパス |
| `--detach` | `-d` | `false` | バックグラウンドで実行（ログを表示しない）。従来の `-D` も利用可能 |
| `--parallel` | `-P` | `1` | 並列ワーカー数 |
| `--shell` | `-s` | `false` | `entrypoint.sh` をバイパスした素の（未認証の）シェルを起動（デバッグ用） |
| `--shell-type` | | `$SHELL` から自動判定、それ以外は `bash` | 対話シェルの種類（[詳細](#--shell-type--シェルの種類の指定)） |
| `--user` | | `current` | コンテナ内の実行ユーザ（[詳細](#--user--コンテナ実行ユーザの指定)） |

### 通信・隔離・実行準備のオプション

| フラグ | 既定値 | 用途・条件 |
|---|---|---|
| `--policy-preset` | 未指定 | `offline` / `test` / `dependencies` / `api`。ホストの通信許可を拡張しない |
| `--egress-allow-domain` | 未指定 | ワーカー別の許可を狭める。繰り返し可 |
| `--deny-read` / `--deny-write` | 空 | 既存の `/workspace` 以下をmask／readonly。繰り返し可 |
| `--watch-polling` | `false` | Chokidar／Watchpackの1秒ポーリング |
| `--ssh-proxy` / `--jvm-proxy` | `false` | SSH CONNECT／JVMのProxy設定 |
| `--ca-bundle` | 未指定 | ワーカーに渡す追加PEM CAのホストパス |
| `--clone-dir` | 未指定 | 新しい絶対パスへ独立clone。並列起動と併用不可 |
| `--environment-lock` | 未指定 | lockファイルとimage digestを照合 |
| `--image-id` | 未指定 | `sha256:...` のimage digestを要求 |
| `--start-hook` | 空 | 起動前のguest shellコマンド。繰り返し可 |
| `--readiness` | 未指定 | 起動準備完了のguest shell probe |
| `--readiness-timeout` | 0（実効60秒） | hook・probeの期限 |
| `--max-lifetime` / `--idle-timeout` / `--retention` | `0` | 寿命・idle・終了後保持。`30m` / `2h` など。`reap` の実行が必要 |
| `--protect-main` | `false` | 未リリースmain。メイン処理の実行中はidle回収しない。最大寿命は適用 |
| `--max-requests` / `--max-concurrent` | `0` | 未リリースmain。実行ごとの要求数／同時数を認証Proxyで制限 |
| `--max-transfer-bytes` / `--max-observed-tokens` | `0` | 未リリースmain。本文合計量／観測tokenで制限。[正確さと条件](../guides/usage-limits.md) |
| `--proxy-admin-url` | `http://127.0.0.1:18081` | ホスト管理API |
| `--proxy-container-url` | `http://codex-auth-proxy:18080` | ワーカー向け固定AI経路 |
| `--http-proxy-url` | `http://codex-http-proxy:18082` | ワーカー向け一般egress |
| `--proxy-admin-secret` | 未指定 | 管理secretの直接指定。日常利用ではファイル指定を優先 |
| `--proxy-admin-secret-file` | 未指定 | 管理secretの所有者0600ファイル |

- 詳細・制限：[追加のサンドボックス制御](../sandbox-features.md)。
- idleはguest CPUやファイル更新を監視しません。メイン処理には`--protect-main`、管理側の活動通知には`oaiso activity`を指定してください。

---

## Docker CLI との対応

- `run -d`、`run IMAGE COMMAND`、`ps -a`、`logs -f`、`start`、`stop`、`rm -f`、`build -t` はDockerと同じ形で使えます。
- 従来の `run -d PATH`（プロジェクト指定）は `run --project PATH` に置き換えてください。
- 従来の `-D` はバックグラウンド実行として引き続き使えます。
- ただし `run -p` は追加パッケージ、`run -i` はイメージ、`run -t` はエージェントのタスク、`-v` は詳細ログです。
- ホストポートの公開には対応していません。
- 追加マウントは `--mount HOST:CONTAINER[:ro|rw]` を使います。
- ワーカーは既定で現在のプロジェクトを `/workspace` に共有し、前面実行の終了時に削除されます（保持する場合は `--keep`）。

---

## `--agent` — エージェントの選択

- `--agent` フラグでサンドボックス内に起動する AI エージェントを選びます。
- コンテナ内では `DOCK_AGENT` 環境変数として渡され、`entrypoint.sh` が分岐します。

| 値 | 動作 | 注入される認証情報 |
|---|---|---|
| _（省略）_ | 設定した既定値を使う（組み込み既定値は認証済みシェル） | 選ばれたエージェントに応じる |
| `shell` | 認証設定済みの対話シェルを起動（`codex` / `claude` 両方が PATH 上にある） | Codex と Anthropic の両方（利用可能なもの） |
| `codex` | OpenAI Codex CLI を起動 | `CODEX_AUTH_PROXY_URL`, `CODEX_TOKEN`, `OPENAI_BASE_URL` ほか |
| `claude` | Anthropic Claude Code を起動 | `ANTHROPIC_BASE_URL`(`…/anthropic`), `ANTHROPIC_API_KEY`（プレースホルダ） |

- `--agent claude` は Auth Proxy が Anthropic 認証情報を持たない場合、起動前にエラーで停止します
  （Proxy の `/admin/mode` を参照）。
- ホストと Auth Proxy の認証状態が異なる場合、稼働中ワーカーがなければ Auth Proxy を自動再作成します。
  稼働中ワーカーがある場合は、それらの認証を失効させないようエラーで停止します。意図的に再作成する場合は
  `--force-recreate-proxy` を指定し、プロキシIPが変わった既存ワーカーは新規作成してください。
- `--shell` は `entrypoint.sh` を完全にバイパスして**認証なし**の素のシェルを起動します（デバッグ用）。
  認証済みシェルが欲しい場合は `--agent shell` を指定してください。

- 設定した既定エージェントは、`oaiso run IMAGE COMMAND`、`oaiso run -- COMMAND`、`--exec` のコマンド実行時には適用しません。
- 明示した `--agent codex` / `--agent claude` はコマンドより優先します。

```bash
oaiso run --agent codex  --approval-mode full-auto
oaiso run --agent claude --approval-mode full-auto --task "Refactor the auth module"
oaiso run --agent shell  # 認証済みシェル（codex / claude 両方利用可）
```

---

## `--exec` — コマンドの実行

- `--exec` フラグで、対話シェルの代わりにコンテナ内で1回だけ実行するコマンドを指定できます。
- コンテナ内では `DOCK_EXEC_CMD` 環境変数として渡され、`entrypoint.sh` の「エージェント未指定」 分岐（認証設定済みシェルを起動する分岐）でのみ参照されます。

- `--agent codex` / `--agent claude` を指定した場合は**エージェントが優先され**、`--exec` は無視されます。
- `--agent shell` ではコマンドを実行します。
- `--shell`（`entrypoint.sh` を完全にバイパスする素のデバッグシェル）と併用するとエラーになります
  （`--shell` では `entrypoint.sh` 自体が実行されないため `--exec` が効きません）。
- コマンドは `--shell-type` で選んだシェル（既定は bash）の `-c` 経由で実行されます。
- コマンドラインの末尾に置く `-- command...`（上記参照）とは同時に指定できません。
  どちらを使っても構いませんが、複数語のコマンドは後者の方が1つの文字列にクォートしなくて済む分手軽です。

```bash
# 認証設定済みの環境でコマンドを1つ実行して終了
oaiso run --exec "ls -la"

# 同じことを、コマンド全体をクォートせずに
oaiso run -- ls -la

# バックグラウンドでコマンドを実行
oaiso run --exec "npm test" --detach

# --agent を指定すると --exec は無視され、エージェントが起動する
oaiso run --agent codex --exec "ls -la"   # → Codex CLI が起動する
```

---

## `--shell-type` — シェルの種類の指定

- `--shell-type` で、サンドボックス内の対話シェルに使うバイナリを選べます。
- 対象は「エージェント未指定時の認証済みシェル」と「`--shell` デバッグモード」の両方です （コンテナ内では `DOCK_SHELL` 環境変数として渡され、`entrypoint.sh` が参照します）。

| 値 | 動作 |
|---|---|
| `bash` | bash を起動 |
| `zsh` | zsh を起動。対象イメージに zsh がインストールされている必要がある |

| 条件 | 選ばれるシェル |
|---|---|
| フラグ・`default_shell_type`・`run.shell_type` がすべて未指定 | ホストの `$SHELL` のベース名で判定 |
| `$SHELL` が `zsh` | `zsh` |
| その他・未設定・認識できないシェル | `bash` |
| `--shell` デバッグモードでフラグ未指定 | 自動判定せず常に `bash` |
| `--shell --shell-type zsh` | 明示した `zsh` |

**デバッグモードの制限**

- `--shell` はコンテナの `Entrypoint` をシェルバイナリへ置換。
- `entrypoint.sh` をバイパスするため、同スクリプトによるfallbackは使えない。
- 指定したシェルがイメージにない場合：実行エンジンがコンテナ起動を拒否。

- 組み込みの `plain` テンプレートには zsh がインストール済みで、`pwn` は `plain` を継承するため （`FROM codex-dock:latest`）、どちらのテンプレートでも `--shell-type zsh`（や `$SHELL=zsh` による自動判定）がそのまま使えます。
- 自作テンプレートで zsh を使う場合は、そのテンプレートの Dockerfile 側で zsh をインストールしてください。
- インストールされていない場合でも、`--shell` 以外（エージェント未指定時のシェルや `--exec`） では `entrypoint.sh` が起動時にバイナリの有無を確認し、無ければ警告を出して `bash` に フォールバックするため、コンテナが起動に失敗することはありません。

> エージェント（`--agent codex`/`claude`）を Ctrl+Z で一時停止してシェルに戻る機構
> （`launch_interactive`）は、bash の `PROMPT_COMMAND` を使った実装のため、
> `--shell-type` の値に関わらず内部的には常に bash で動作します。`--shell-type` が
> 影響するのはエージェント未指定時の対話シェルと `--shell` デバッグモードのみです。

```bash
# zsh で対話シェルを起動
oaiso run --shell-type zsh

# zsh の素のデバッグシェル（entrypoint.sh をバイパス）
oaiso run --shell --shell-type zsh
```

- 設定ファイル（`~/.config/codex-dock/config.toml`）の `default_shell_type`（グローバル）/ `run.shell_type`（`run` のみ）でデフォルト値を変更できます。
- 詳細は [設定リファレンス](../configuration.md#default_shell_type) を参照してください。

---

## `--approval-mode` — 承認モードの指定

- `--approval-mode` フラグでエージェントがアクションを実行する際の承認動作を制御します。
- 外側の隔離は選択したワーカーランタイムが担当します。
- 値は両エージェントに対応する フラグへマッピングされます。

| 値 | Codex CLI フラグ | Claude Code フラグ | 動作 |
|---|---|---|---|
| `suggest`（デフォルト） | 承認フラグを追加しない | 承認フラグを追加しない | エージェント自身の既定設定に従う |
| `auto-edit` | `--ask-for-approval on-request` | `--permission-mode acceptEdits` | Codexはモデル判断で承認要求、Claudeは編集を自動許可 |
| `full-auto` | `--ask-for-approval never` | `--dangerously-skip-permissions` | 承認を一切求めない |
| `danger` | `--dangerously-bypass-approvals-and-sandbox` | `--dangerously-skip-permissions` | エージェントの承認とCodexの内側sandboxを省略。KataのVM隔離・ホスト制約は維持 |

- Kata検証済みワーカーでは、Codexの内側sandboxを `danger-full-access` に設定します。
- VM内の入れ子namespaceとseccompの不整合を避けるためです。
- 承認モードは独立した設定です。
- 非対話execではエージェント自身が承認動作を選ぶ場合もあります。
- 共有workspaceや追加mountは、指定した権限で変更できます。
- [認証情報の扱い・共有ファイルへの影響・制限](../security.md)を参照してください。

```bash
# エージェント既定の承認設定
oaiso run --agent codex --task "テストを書いて"

# Codexはモデル判断で承認要求、Claudeは編集を自動許可
oaiso run --agent codex --approval-mode auto-edit --task "リファクタリング"

# 完全自動実行（承認なし）
oaiso run --agent codex --approval-mode full-auto --task "バグ修正"

# エージェントの承認・内側sandboxを無効化
oaiso run --agent codex --approval-mode danger --task "ビルドスクリプトを実行"
```

---

## `--user` — コンテナ実行ユーザの指定

- `--user` フラグを使うと、コンテナ内のプロセスを任意の uid:gid で起動できます。
- ホストのファイルシステムとの権限整合性を確保するために使用します。

| 値 | 動作 |
|---|---|
| `current` (省略時のデフォルト) | `oaiso` コマンドを実行しているユーザの uid:gid を自動取得 |
| `codex` | 従来のデフォルト挙動。コンテナ内 `codex` ユーザ（`1001:1001`）で実行 |
| `""` | イメージのデフォルトユーザを使用 |
| `dir` | `--project` で指定したディレクトリ所有者の uid:gid を自動取得 |
| `uid` または `uid:gid` | 明示的に指定（例: `1000`, `1000:1000`） |

> **注意**: カスタムユーザを指定した場合、そのユーザがコンテナの `/etc/passwd` に存在しない
> ことがあります。`oaiso` は `HOME=/var/tmp/codex-home` を自動注入します。認証ファイルや
> Codex CLI 設定はその `HOME` 配下に作成され、`/workspace` 配下に補助ディレクトリは作成しません。

---

## `--mount` — 任意ディレクトリの追加マウント

- `--mount` フラグで、`/workspace`（`--project`）以外の任意のホストディレクトリを コンテナへバインドマウントできます。
- 繰り返し指定することで複数のディレクトリを マウントできます。

```
--mount <host-path>:<container-path>[:ro|rw]
```

- `<host-path>`: ホスト側のディレクトリ（相対パスは実行時のカレントディレクトリから解決）
- `<container-path>`: コンテナ内の絶対パス。
- `/workspace` は `--project` 専用のため指定不可
- `:ro` / `:rw`: 読み取り専用 / 読み書き可能（省略時は `rw`）
- 同じ `<container-path>` を複数回指定するとエラーになる

```bash
# ホストの共有データディレクトリを読み取り専用でマウント
oaiso run --mount /home/user/datasets:/data:ro

# 複数のディレクトリをマウント（読み書き・読み取り専用を混在）
oaiso run --mount ./cache:/cache --mount /opt/models:/models:ro
```

> 追加マウントされたディレクトリはプロジェクトディレクトリ本体（`/workspace`）とは
> 独立して読み取り専用を指定できます。`--read-only` は `/workspace` のみに適用されます。

- `--read-only-rootfs` はイメージの rootfs に適用され、`/workspace` と追加 mount の読み書き設定は変えません。
- 起動時のパッケージ追加は使えないため、必要なパッケージはイメージに入れてください。
- HOME と `/tmp`・`/var/tmp` は一時的な tmpfs になり、ワーカー削除後には残りません。
- `--no-git-metadata` を指定した linked worktree では共通 Git metadata をマウントしません。
- VM 内で Git を使う場合は私有の metadata と mount を別途用意してください。

- `oaiso profile --json` はプロファイルごとの CPU、guest RAM、ホスト側上限、PID 上限を出力します。
- `small` は 1 CPU・guest 1 GiB・ホスト上限 2 GiB、`standard` は 1 CPU・guest 2 GiB・ホスト上限 3 GiB です。
- `--profile-size small` は Kata のみで使えます。
- 小型の実際のメモリ使用量・起動性能はホストで測定してください。

- 設定ファイル（`~/.config/codex-dock/config.toml`）の `[run]` セクションでも `mounts` として既定値を指定できます（`--mount` を指定した場合はそちらが優先）。
- 詳細は [`doc/configuration.md`](../configuration.md#runmounts) を参照してください。

```toml
[run]
mounts = ["/home/user/datasets:/data:ro", "./cache:/cache"]
```

---

## パッケージ記述形式

`--pkg` または `packages.dock` ファイルで使用できるパッケージ形式：

```
apt:libssl-dev          # apt でインストール
pip:requests            # pip でインストール
npm:lodash              # npm でインストール
libssl-dev              # プレフィックスなし → apt 扱い（デフォルト）
```

`packages.dock` ファイルの例：

```
# コメントは # で始める
apt:libssl-dev
apt:postgresql-client
pip:requests
pip:numpy
npm:typescript
```

---

## 並列ワーカー

`--parallel N` を指定すると、N 個のコンテナが同時に起動します。

```bash
oaiso run --parallel 2 --worktree --branch myfeature
```

- ノードの既定上限は同時2台です。
- 自動的に以下のブランチが作成されます：

- `myfeature-1`（worker 1）
- `myfeature-2`（worker 2）

`--branch` を指定しない場合は `worker-1`, `worker-2` が使用されます。

---

## 使用例

```bash
# 基本的な実行（カレントディレクトリをマウント）
oaiso run

# タスクを指定して完全自動実行
oaiso run --agent codex --task "ユニットテストを書いて" --approval-mode full-auto

# git worktree を使って feature ブランチで作業
oaiso run --worktree --branch feature-auth --new-branch

# 並列ワーカー2つを起動（各ワーカーに別ブランチ）
oaiso run --parallel 2 --worktree

# 追加パッケージをインストールして実行
oaiso run --pkg "apt:libssl-dev" --pkg "pip:requests" --pkg "npm:lodash"

# packages.dock ファイルを使用
oaiso run --pkg-file ./packages.dock

# バックグラウンドで完全自動実行
oaiso run --agent codex --task "リファクタリング" --approval-mode full-auto --detach

# 読み取り専用・インターネットなしでセキュアに実行
oaiso run --agent codex --read-only --no-internet --task "コードレビュー"

# 特定のOCIイメージを使用
oaiso run --image my-custom-codex:v2

# テンプレートイメージを使用（未ビルドなら自動ビルド）
oaiso run --image codex-dock:pwn

# --template でテンプレート名から直接指定（--image codex-dock:pwn と同じ）
oaiso run --template pwn

# ~/.config/codex-dock/templates/ に置いた自作テンプレートも同様に使える
oaiso run --template mytool

# カスタムモデルを指定
oaiso run --model "o3"

# エージェントを使わずコマンドを1つ実行して終了
oaiso run --exec "ls -la"

# デフォルトで、コマンド実行ユーザと同じ uid:gid でコンテナを起動
oaiso run

# 従来どおり codex(1001:1001) ユーザで実行
oaiso run --user codex

# 明示的にコマンド実行ユーザを指定
oaiso run --user current

# プロジェクトディレクトリの所有者と同じ uid:gid でコンテナを起動
oaiso run --user dir --project /srv/myapp

# uid:gid を明示指定
oaiso run --user 1000:1000
```

---

## 関連ドキュメント

- [クイックスタート](../getting-started.md) — 初回実行の手順
- [アーキテクチャ概要](../architecture.md) — 起動シーケンスの詳細
- [Auth Proxy 技術仕様](../auth-proxy.md) — 認証の仕組み
- [ネットワーク仕様](../network.md) — 専用ワーカーネットワークと2種類のプロキシの構成
- [設定リファレンス](../configuration.md) — config.toml の設定項目

- `--observe-dir PATH`: 未リリースmain。起動時間を秘密を除いた形式で保存。[診断の保存](../guides/observability.md)。
