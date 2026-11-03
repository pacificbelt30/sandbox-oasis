# 御AgentISOlation（Oaiso）

**開発・テストを、ワーカーごとのKata VMで隔離して実行。Codex CLI・Claude Codeにも対応。**

| 項目 | 内容 |
|---|---|
| 正式名称 | 御Agent**ISO**lation |
| 英字表記・読み | Oaiso／おあいそ |
| コマンド | `oaiso` |
| 実行ホスト | Linux x86_64・KVM・containerd・Kata runtime-rs・Dragonball |
| イメージ作成 | BuildKit |

[日本語ドキュメント](doc/index.md) · [English documentation](doc/en/index.md)

- AI認証なしで使う：[サンドボックスTUI](doc/guides/sandbox-ui.md)（未リリース・main）。
- Codex／Claude Codeを使う：[インストールから初回起動まで](doc/guides/first-run.md)。

## Installation — インストール

- 対象：**Ubuntu 26.04・x86_64・systemd・KVM・cgroup v2**。
- 実行者：`sudo` が使える通常ユーザー。管理操作はスクリプトが昇格。
- 配布：GitHub Releasesのビルド済み版。Go・Make・ソース取得は不要。
- 事前準備：[curl・CA証明書・GitHub CLIを導入](doc/installation.md#2-ツールを準備する)。GitHub CLIは配布物の署名検証に使用。GitHubへのログインは不要。
- ホストPython：[uv公式手順](https://docs.astral.sh/uv/getting-started/installation/)でuvを準備し、[uv管理のPython 3.12](https://docs.astral.sh/uv/guides/install-python/)を使用。未導入なら自動準備。
- ほかのLinux：[手動配置](doc/kata.md)。WSL／VM：上記要件とnested virtualizationが必要。

**1. 配置計画を確認し、インストール**

```bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/latest/download/install.sh | bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/latest/download/install.sh | bash -s -- --apply
```

- スクリプト自体も実行前に検証する場合：[署名を確認して導入](doc/guides/signed-releases.md#スクリプトも実行前に確認する)。

**2. ログアウト・再ログイン後、確認**

```bash
oaiso --version
oaiso runtime-check
```

**3. 利用目的を選ぶ**

| 目的 | 次の手順 |
|---|---|
| AI認証なしのサンドボックス | [開発版の準備・イメージ作成・TUI操作](doc/guides/sandbox-ui.md)。未リリース（main）のCLI・node・補助ツールを同じ版で準備 |
| Codex／Claude Code | [キーリング解錠・login・初回起動](doc/guides/first-run.md#3-キーリングを解錠する) |

- 詳細・配置先・更新：[Installation](doc/installation.md)。[必要なツールの公式手順](doc/installation.md#ツールの公式手順)も掲載。
- `--bundle`：[ビルド済みの実行ファイル一式を使う場合](doc/setup.md#ビルド済みバンドルとは)の指定。通常は不要。
- `make install` はCLI・補助ツール・補完の配置のみ。実行ホストの準備には上の手順を使用。

## できること

| 目的 | 機能・参照先 |
|---|---|
| 認証なしで検証する | [TUIでVM作成・独立コピー・成果物回収](doc/guides/sandbox-ui.md)。既定はAI認証なし・一般通信なし（未リリース・main） |
| エージェントを動かす | Codex CLI・[Claude Code](doc/claude-code.md)。ClaudeはAPIキー／サブスクに対応 |
| 作業を隔離する | ワーカーごとに独立したカーネル。CPU・メモリ・プロセス数を制限 |
| 認証情報を分ける | 実資格情報は認証Proxyで保持。ワーカーには短命トークンを渡す |
| 通信を制限する | 直接の外部・ホスト・ワーカー間通信を遮断。一般通信はドメイン・IP・ポートで制御 |
| 並列で開発する | VMとGit worktreeを分けて実行 |
| 実行環境を拡張する | Dockerfile・テンプレート・追加パッケージ |
| 状態を管理する | CLI／TUIで一覧・ログ・停止・再起動・削除 |
| 製品に組み込む | 同じホストの製品から[Go API](doc/go-api.md)で操作 |

## 最初に使う

| 順序 | 担当 | 実施内容 |
|---|---|---|
| 1 | 管理者 | [インストール](doc/installation.md)。Ubuntu 26.04対応ホストでは配布インストーラーで計画、`--apply` で適用 |
| 2 | 利用者 | [クイックスタート](doc/getting-started.md)で利用目的を選び、イメージを用意。AIを使う場合は認証も準備 |
| 3 | 利用者 | プロジェクトでワーカーを起動 |

### AI認証なしのサンドボックス

利用可能：**未リリース（main）**。同じ版のCLI・node・補助ツールと互換イメージを[先に準備](doc/guides/sandbox-ui.md#前提と起動)し、通常ユーザーで作業プロジェクトから実行します。

```bash
oaiso ui
```

- `Actions F2`から操作を選択。`N`で作成、`C`で接続、`F`でファイル、`U`でWebプレビュー、`I`で詳細。
- TUI終了後もVMは残ります。不要になったVMは停止・削除します。
- [共有ファイルへの影響・転送制限・プレビューの終了条件](doc/guides/sandbox-ui.md)を確認してください。

### Codex／Claude Code

**先に認証を設定（いずれか1つ）**

| 認証方式 | 準備するもの |
|---|---|
| 独立プロファイル | [キーリング解錠・login](doc/guides/first-run.md#3-キーリングを解錠する)。選択と管理シークレット設定も自動化 |
| 従来のホスト連携 | [エージェント別の認証ソース](doc/getting-started.md#2-認証方式を選ぶ)を準備 |

- 以下はどちらか1つ。`/path/to/your-project` は実際の作業プロジェクトへ置換。
- Git・npm・pipなどを使う場合は、先に[一般通信の宛先](doc/guides/networking.md)も許可。

**Codex：初期設定 → 作業先で起動**

```bash
oaiso init --agent codex
cd /path/to/your-project
oaiso codex
```

同じプロジェクトで、対話起動の代わりに依頼を渡す場合：

```bash
oaiso codex "このリポジトリを調べて"
```

**Claude Code：初期設定 → 作業先で起動**

```bash
oaiso init --agent claude
cd /path/to/your-project
oaiso claude
```

**起動時の要点**

- リリース版はソースを取得せずに `init` できます。
- 通常操作：管理グループ（`codex-dock`）に所属する通常ユーザーで実行。
- `oaiso run` の組み込み既定値：認証済みシェル。
- Codexを既定にする：`oaiso default codex`。
- プロジェクト限定は `--local`。
- 現在の既定値を確認：`oaiso default`。
- `oaiso codex`：現在のディレクトリを `/workspace` に共有し、Codexを直接起動。
- コンテナの詳細指定：`oaiso run --agent codex`。
- Kata検証失敗時：起動中止。
- runcへ自動切り替えしない。
- Tab補完：導入スクリプト／`make install` がBash・Zsh・Fish用を配置。
- [手動設定](doc/commands/completion.md)も可。

## よく使う操作

通常ユーザーで実行します。`WORKER`は自分が管理する対象の名前またはIDへ置換し、必要な操作だけを選びます。

| 目的 | コマンド・操作 |
|---|---|
| 操作を一覧から選ぶ | `oaiso ui`。開発版mainでは`Actions F2`メニューを利用可 |
| ワーカー一覧 | `oaiso ps -a` |
| VMの台帳・containerd・ホストプロセスを照合 | `oaiso vm ps -a` |
| ログを追う | `oaiso logs WORKER --follow`。`Ctrl-C`で終了 |
| 停止して保持 | `oaiso stop WORKER` |
| 再起動 | `oaiso start WORKER` |
| 削除 | 必要な成果を保存し、停止後に`oaiso rm WORKER` |

- 前面実行は終了時に削除。保持する場合は`run --keep`を指定します。
- 並列実行やworktree：[ワーカー起動](doc/commands/run.md)・[ファイル共有](doc/workspace.md)。

## プロジェクトごとの設定（任意）

- **`oaiso.toml` がなくても起動できます。** 指定がない項目は、利用者ごとの設定と既定値を使います。
- プロジェクトごとにイメージ・エージェント・モデルを変える場合や、チームで同じ設定を使う場合に作成します。
- ファイル名は小文字の **`oaiso.toml`**。作業するリポジトリの一番上に置きます。

```bash
oaiso project init       # oaiso.tomlを作成。既存ファイルは上書きしない
oaiso project check      # 設定の書式・指定内容を確認
```

Gitを使うチームで共有する場合は、同じリポジトリで追加します。

```bash
git add oaiso.toml
```

- ホストの準備は各利用者が[クイックスタート](doc/getting-started.md)で確認します。AIを使う場合は認証も設定します。
- 旧 `codex-dock.toml` を使っていた場合：[ファイル名を変更する手順](doc/migration.md#プロジェクト設定を-oaisotoml-に変更する)。

| 設定先 | 書く内容 |
|---|---|
| `oaiso.toml` | イメージ・ビルド元・モデルなど。独自環境はプロジェクトのDockerfileを `[build]` で指定 |
| `packages.dock` | チームで共有する追加パッケージ |
| `~/.config/codex-dock/config.toml` | 利用者ごとの設定。使う認証プロファイル・Proxy・VM接続先・追加で共有するホストのフォルダー |

- APIキー・ログイントークン：`oaiso.toml` やGitへ保存せず、[認証プロファイル](doc/guides/authentication.md)で管理。
- 利用者ごとの設定・認証情報は、既存の保存先をそのまま使えます。再登録は不要です。

[設定例と優先順位](doc/configuration.md#プロジェクト設定)

## 読みたいことから探す

| 目的 | ドキュメント |
|---|---|
| はじめに：インストールから初回起動 | [Installation](doc/installation.md) → [クイックスタート](doc/getting-started.md) |
| 利用ガイド：エージェント・作業環境・認証・通信 | [利用ガイド](doc/guides/index.md) |
| リファレンス：CLI・設定・組み込みAPI | [リファレンス](doc/reference/index.md) |
| 管理・運用：日常操作・更新・障害対応 | [日常操作](doc/operations.md)・[ホスト更新](doc/setup.md)・[トラブルシューティング](doc/troubleshooting.md) |
| 開発：構成・上流追従・検証 | [アーキテクチャ](doc/architecture.md)・[上流互換性](doc/upstream-compatibility.md)・[テスト](doc/testing.md) |

[全体の章立て](doc/index.md#章から探す) · [English chapters](doc/en/index.md#browse-chapters)

## 開発

- ビルド済み版の利用にGoは不要。ソースからビルドする場合はGo 1.26.0以上・Makeなどを準備。
- [開発用ツールの準備](doc/development/toolchain.md)：[Go公式のLinux手順](https://go.dev/doc/install)と各ツールの公式手順。
- `make build`：CLI・ワーカー起動用guard・IO loggerを作成。
- ホストへの配置：[Kataの手順](doc/kata.md)。
- ドキュメントを修正する場合：[ページの追加・修正手順](doc/development/documentation.md)。

```bash
make build
make test
make vet
```

## 安全に使うための注意点

| 確認すること | 動作・注意点 |
|---|---|
| APIキー・ログイントークン | 認証Proxyで保持。作業VMには短命トークンだけを渡す |
| 認証情報の保存 | 新規loginは[キーリング＋age](doc/guides/keyring.md)が既定。既存方式は維持 |
| プロジェクトの共有 | 既定では読み書き可能。エージェントの変更・削除はホストの共有ファイルにも反映される |
| 外部通信 | AI用の接続先とは別に、Git・パッケージ取得先などを許可する。一般通信は既定で拒否 |
| 管理グループ `codex-dock` | メンバーはホストのroot相当の操作が可能。信頼する利用者だけを登録 |
| `--runtime runc` | 明示して選ぶ互換モード。ワーカーごとのVM隔離は使われない |

- 詳しい動作：[認証情報とファイルの保護・制限](doc/security.md)。
- 旧版からの変更：[更新・移行手順](doc/migration.md)。
