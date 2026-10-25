# クイックスタート

**日本語** | [English](en/getting-started.md)

| 今の状態 | 次の操作 |
|---|---|
| 実行ホストをまだ用意していない | 先に[Installation — インストール](installation.md)を実施 |
| 実行ホストは準備済み | 1〜4で最初のワーカーを起動。5〜6は任意 |
| 既存環境を更新したい | [日常の運用](operations.md) |

- このページはv0.0.3以降向けです。[インストールから一続きの手順](guides/first-run.md)も用意しています。

## 1. 実行ホストを確認する

- 実行ユーザー：operator groupに属する通常利用者。
- `run` / `proxy run`：`sudo` 不要。

```bash
oaiso runtime-check
```

**正常時の出力例（既定のKata構成）**

```text
[ok] egress-policy: Host rules are valid; private addresses stay blocked
[ok] runtime: io.containerd.kata.v2
[ok] host: linux/amd64
[ok] kvm: KVM device exists; the root node owns VM access
[ok] cgroups: cgroup v2 has cpu, memory and pids
[ok] node: pinned runtime, assets, resource profile and management socket verified
[ok] builder: BuildKit is reachable
[ok] forwarding: IPv4 forwarding enabled
```

- 全項目 `[ok]`・終了コード `0`：この手順を続行。
- `[error]`：直後の `Fix:` を確認。[診断例](troubleshooting.md#複数の前提条件をまとめて調べる)を参照し、解決後に再実行。
- 確認範囲：ホストの実行基盤。認証成功やAIからの応答は、後の手順で確認。

| 用途 | 必要なもの |
|---|---|
| VM実行 | Linux x86_64・KVM・cgroup v2・専用containerd・Kata runtime-rs・Dragonball |
| イメージ作成 | BuildKit・`buildctl` |
| ソースからのCLI作成 | Go 1.26.0以上・Make |
| 診断エラーの解決 | [トラブルシューティング](troubleshooting.md) |

- このページはCLIと実行ホストの配置後に実施。未配置なら[インストール](installation.md)へ戻る。
- リリース版の `init` は任意のディレクトリで実行できます。エージェントは自分の作業プロジェクトで起動します。

## 2. 認証方式を選ぶ

**新規利用は`login`で、ホストCLIと分けた認証を作成します。**

1. キーリングを確認。SSH・WSL・GUIなしでは`session`を開き、そのシェル内で続行。
2. 下表から利用する方式を1つ選択。

```bash
oaiso auth keyring check
# キーリングを利用できない場合：
oaiso auth keyring session
```

| 認証 | コマンド |
|---|---|
| Codex・APIキー | `oaiso login --agent codex --method api-key --profile work` |
| Codex・サブスク | `oaiso login --agent codex --method subscription --profile work` |
| Claude Code・APIキー | `oaiso login --agent claude --method api-key --profile work` |
| Claude Code・サブスク | `oaiso login --agent claude --method subscription --profile work` |

- 成功時は、キーリング＋age保存、プロファイル選択、管理シークレットの準備を自動実行。
- APIキーは非表示入力・モデル一覧APIで接続確認。サブスクは一時Kataで公式CLIのログイン手順を実行。
- 以後の`init`も同じキーリングセッション内で実行します。[パスフレーズと解錠](guides/keyring.md)。
- `auth profile set/import`や管理シークレット生成を追加実行する必要はありません。

### 既存の認証方式を使う場合

| 認証方式 | 準備 | 読み込み元 |
|---|---|---|
| 手動の独立プロファイル | [認証ガイドの1〜3](guides/authentication.md) | 選択した製品専用Storeのみ。ホスト認証へfallbackしない |
| 従来のホスト連携 | 下表の環境変数またはログインファイル | ホスト認証を[優先順位](auth-proxy.md#認証ソース)で選択 |

**従来のホスト連携を選んだ場合**

| エージェント | 利用できる認証 |
|---|---|
| Codex | `OPENAI_API_KEY`・保存済みAPIキー・`~/.codex/auth.json` |
| Claude Code | `ANTHROPIC_API_KEY`・`CLAUDE_CODE_OAUTH_TOKEN`・`~/.claude/.credentials.json`・保存済みAPIキー |

- ログインファイル：`oaiso` を実行する利用者のHOMEに配置。
- OpenAI APIキーの保存：`oaiso auth set`。
- 読み取り権限：Proxyは同じ利用者のUID/GIDで読む。
- `0600` のまま利用可。
- ワーカーへ渡すもの：短命トークン。
- 実APIキー・OAuth access/refresh tokenは渡さない。
- Claude固有の設定：[APIキー・サブスクの手順](claude-code.md)。
- 保存済みAnthropic APIキー：既存ファイルの互換読取。`auth set` はOpenAI専用なので、新規保存には独立プロファイルを使用。

### 一般通信が必要なら、初回起動前に許可する

| 用途 | 初期状態・準備 |
|---|---|
| AIへの推論だけ | 固定AI経路を利用。一般通信の許可は不要 |
| Git・npm・pipなど | 一般通信は既定拒否。[通信ガイド](guides/networking.md)で必要な宛先を許可 |
| 独立プロファイルと通信設定を併用 | 同じグローバル `[proxy]` にキーを統合。管理シークレット設定を残す |

## 3. プロキシとイメージを用意する

**通常利用者で実行します。リリース版はソースの取得不要です。**

| 登録した資格情報 | 実行するコマンド（どちらか1つ） |
|---|---|
| OpenAI | `oaiso init --agent codex` |
| Anthropic | `oaiso init --agent claude` |

- 資格情報がない側の `init` まで続けて実行しない。
- 終了時の `Ready.` はホスト・イメージ・Proxyの準備完了。実際の推論成功は次の手順で確認。

**Codex準備成功時の末尾**

```text
Ready. From your project run: oaiso codex
For host edit watchers add --watch-polling to `oaiso run` (Chokidar/Watchpack).
```

- Claudeの場合：1行目は `Ready. From your project run: oaiso claude`。
- 途中にはホスト診断・イメージ検査・必要に応じたビルドログも表示。

| `init` が行うこと | 条件・制限 |
|---|---|
| ホスト診断・認証ソースの存在確認 | 認証本文を表示しない。既存設定を書き換えない |
| ワーカーイメージの作成・互換性検査 | 不足時にビルド |
| Proxy起動 | リリース版は配置済みの静的CLIからイメージを準備 |
| 使用エージェントの選択 | `--agent` → 設定した既定値。シェル既定ならCodexを準備 |
| 認証方式が異なる既存Proxy | ワーカー稼働中は強制変更せずエラー |
| 検証対象外 | 実サービスへの認証成功・疎通・推論 |

- ソースから作った動的リンク版でProxyのソース不足が表示された場合は、そのソースのルートで `init` を再実行。リリース版はソース不要。
- 準備後：作業するプロジェクトへ移動。
- Codexを既定にする：`oaiso default codex`。
- プロジェクト限定は `--local`。

### 手動で分けて準備する場合

```bash
oaiso build --template plain --tag codex-dock:latest
oaiso proxy run
```

| Proxy | 役割・接続先 |
|---|---|
| `codex-auth-proxy` | AI APIへの認証付き通信 |
| `codex-http-proxy` | Git・npm・pipなどの一般通信。実資格情報なし |
| 管理API | ホストの `127.0.0.1:18081` に公開 |
| ワーカー向けAPI | ホストへ公開しない |

- `proxy run` がネットワークを自動作成。
- 詳細：[認証プロキシ](auth-proxy.md)。
- 既存チームプロジェクトの独自Dockerfileは、[設定を確認](configuration.md#プロジェクト設定)してプロジェクト側で `init`。リリース版はProxyイメージも配置済みCLIから準備します。

## 4. ワーカーを起動する

- 実行場所：作業するプロジェクトのディレクトリ。
- 共有先：VM内の `/workspace`。
- 共有権限・双方向反映：[ファイル共有と所有者](workspace.md)。
- 製品ソースにいる場合は、起動前に自分の作業プロジェクトへ `cd`。
- 下の起動例は、準備した認証に合うものを1つ選択。

```bash
oaiso codex
# Anthropicを準備した場合はこちら
oaiso claude
```

**シェル・単発コマンド**

- 以下はCodex認証を準備した場合の例。`oaiso run` の既定を変更済みなら、そのエージェントが起動。

```bash
oaiso run
oaiso run -- ls -la
```

| 起動方法 | 動作 |
|---|---|
| `oaiso codex` | Codexを起動 |
| `oaiso claude` | Claude Codeを起動 |
| `oaiso run` | 設定した既定値。組み込み既定値は認証済みシェル |
| `oaiso run -- COMMAND` | コマンドを1回実行 |
| Kata検証に失敗 | 起動中止。隔離・権限・資源の確認を省略しない |

- 最初のプロンプトで応答を確認。401・502などの場合は[認証・通信の診断](troubleshooting.md)。
- 前面実行を終了すると、ワーカーは既定で削除。停止・再起動を試す場合は次のバックグラウンド例を使用。

### チームで同じ環境を使う場合

- この手順は任意。個人で既定の設定を使う場合、`oaiso.toml` は不要。

1. リポジトリのルートで `oaiso project init`。
2. `oaiso.toml` をコミット。
3. 独自ツールはプロジェクトのDockerfileを `[build]` で指定。
4. クローン先でホストと認証を準備。
5. `oaiso project check` → `oaiso init`。

[プロジェクト設定例](configuration.md#プロジェクト設定)

## 5. バックグラウンドで実行する

- 例はCodex用。Claudeでは `--agent claude` に置換。
- 次のコマンドは承認確認を省略するため、共有範囲とタスクを確認してから実行。

```bash
oaiso run --agent codex --name add-tests \
  --task "ユニットテストを追加して" --approval-mode full-auto -d

oaiso ps -a
oaiso logs add-tests --follow
```

**バックグラウンド起動時の出力例（末尾）**

```text
Container started: a1b2c3d4e5f6
```

- IDは実行ごとに変化。`ps -a` で `add-tests` の状態を確認：[一覧の出力例](commands/worker.md#oaiso-ps--ワーカー一覧)。
- `running`：ワーカー実行中。タスク成功はログ・成果・終了コードで確認。
- 短いタスクは一覧を見る前に終了する場合あり。`exited` の場合もログを確認。

- ログ追跡のCtrl+C：追跡だけを終了。ワーカーは継続。
- ブランチも分ける場合：[worktreeの条件と保存先](workspace.md#git-worktree)を確認して `--worktree` などを追加。
- `--approval-mode full-auto`：エージェントの承認確認を省略。
- ファイルへの書き込み範囲：[マウントと承認設定](commands/run.md)で確認。

## 6. 停止・再起動・削除する

```bash
oaiso stop add-tests
oaiso start add-tests
oaiso stop add-tests
# 成果を保存してから削除
oaiso rm add-tests
```

**停止・再起動・削除時の出力例**

```text
$ oaiso stop add-tests
Stopping add-tests...
$ oaiso start add-tests
add-tests
$ oaiso stop add-tests
Stopping add-tests...
$ oaiso rm add-tests
Removing add-tests...
```

- `Stopping...` / `Removing...` は処理開始の表示。終了コード `0` と、続けて実行した `ps -a` で完了を確認。
- 停止後：`⏹ exited`。再起動後：`✅ running`。削除後：一覧から消える。
- すべて削除済みの `ps -a`：列見出しだけを表示。

| 操作・条件 | 結果 |
|---|---|
| 停止 | 短命トークンを失効。再起動用のワーカーを保持 |
| 再起動 | 同じワーカーID・workspaceで認証を復元 |
| 削除 | 管理対象のネットワーク・worktreeも回収。成果は先に保存 |
| 前面実行の終了 | 既定で削除。保持する場合は `--keep` |
| ワーカー終了後のProxy | 共有サービスとして動作を継続 |

- 再起動はメインプログラムの再実行。中断した処理・メモリ状態の続きから再開する機能ではない。
- `rm --force` は実行中でも削除する操作。通常の終了確認後は `stop` → `rm`。

## 次に読む

| 目的 | 参照先 |
|---|---|
| VM一覧・並列実行・更新・後片付け | [日常の運用](operations.md) |
| Dockerfile・テンプレート | [カスタムイメージ](commands/build.md) |
| 通信の許可・拒否・`--no-internet` | [通信ポリシー](proxy-policy.md) |
| 起動・認証・通信のエラー | [トラブルシューティング](troubleshooting.md) |
| Claudeの認証 | [APIキー・サブスク](claude-code.md) |
