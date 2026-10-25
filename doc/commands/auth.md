# `oaiso auth` — 認証管理

> **日本語** | [English](../en/commands/auth.md)
>
> [← コマンドリファレンス一覧](../commands.md)

| やりたいこと | コマンド・設定 |
|---|---|
| 製品専用のAPIキーを保存 | `auth profile set NAME --provider openai\|anthropic` |
| OAuthの認証情報を取り込む | `auth profile import NAME --credentials-stdin --format codex\|claude\|credentials` |
| 秘密を表示せず確認 | `auth profile show NAME` / `auth profile list` |
| 利用するプロファイルを選ぶ | `--auth-profile NAME` またはグローバル `auth_profile` |
| ホスト認証を従来どおり使う | プロファイル未選択で [`auth show`](#auth-show--認証状態の確認) / [`auth set`](#auth-set--api-キーの保存) |

## キーリング

- `oaiso auth keyring check`：秘密を表示せず保存・取得・削除を確認。
- `oaiso auth keyring session [-- COMMAND...]`：GUIなしの専用セッション。
- [依存・解錠・終了時の扱い](../guides/keyring.md)。

## 独立プロファイル

### 動作と保存先

| 項目 | 仕様 |
|---|---|
| 認証の分離 | ホストCLIのログイン・認証環境変数を自動流用しない |
| 存在しないプロファイル／権限不備 | エラー。ホスト認証へfallbackしない |
| 保存ファイルの例 | `~/.config/codex-dock/profiles/work/work.json` |
| ディレクトリ | root・プロファイルともに `0700` |
| JSON・lockファイル | `0600`。平文保存 |
| 保存rootの変更 | `--auth-profile-dir` またはグローバル `auth_profile_dir` |
| WSL | Linux側の私有領域を使用。共有・同期フォルダーを避ける |
| 入力の制限 | キーを引数へ書かない。非対話入力は明示したstdinのみ |

### APIキーを登録する

```bash
oaiso auth profile set work --provider openai  # TTYで非表示入力
oaiso auth profile set claude-work --provider anthropic
oaiso auth profile show work
oaiso auth profile list
```

- 対話入力：TTYで非表示入力。
- 非対話入力：`--key-stdin` を付け、安全な入力元からstdinへ渡す。
- このコマンドは保存のみ。v0.0.3以降の[login](login.md)では登録時の実API接続確認も行います。
- 成功時：`Profile created (plaintext, 0600).`。[プロファイル確認の出力例](../guides/authentication.md#3-管理シークレットとプロファイルを設定する)。

### OAuthを取り込む

```bash
oaiso auth profile import work-oauth --format codex --credentials-stdin < /private/product-login/auth.json
oaiso auth profile import claude-oauth --format claude --credentials-stdin < /private/product-login/.credentials.json
```

| `--format` | 受け付ける形式 |
|---|---|
| `codex` | 公式CLIの `tokens` と `last_refresh` |
| `claude` | `claudeAiOauth` |
| `credentials`（既定） | `credentialstore.Credentials` のJSON。両providerを含められる |

- `--credentials-stdin`：必須。
- 元ファイル：自動探索・自動削除しない。
- このコマンドは既存JSONの明示import用。開発版では[login](login.md)で専用Kataから認証できます。
- 成功時：`Profile imported (plaintext, 0600).`。確認表示はproviderの `oauth` を参照。

### 管理シークレットを作成し、起動する

- 管理secret：資格情報とは別の `0600` ファイルに保存。
- 起動引数：secretの値を載せず、ファイルのパスを渡す。
- 下の生成例：既存ファイルを上書きしない。既存の管理シークレットを使う場合は生成を省略。
- 実行場所：ホストの任意のディレクトリ。ワーカー内では実行しない。

```bash
python3 - <<'PY'
import os, pathlib, secrets
path = pathlib.Path.home() / '.config/codex-dock/proxy-admin-secret'
path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), 'w') as f:
    f.write(secrets.token_hex(32) + '\n')
PY
```

- 生成成功時：出力なし。次のコマンドで権限と保存先だけを確認。

```bash
stat -c '%a %n' "$HOME/.config/codex-dock/proxy-admin-secret"
```

```text
600 /home/user/.config/codex-dock/proxy-admin-secret
```

- `/home/user` は自分のHOMEに変わる。secretの本文は表示不要。

### 日常操作で使うプロファイルを固定する

**設定先：`~/.config/codex-dock/config.toml`（信頼済みグローバル設定）。**

- なければ作成。既存ファイルには以下のキーを統合し、他の設定を残す。
- `auth_profile`：`[run]` / `[proxy]` など、最初のセクションより前に置く。
- 既存の `[proxy]` がある場合は、その中へ `admin_secret_file` を追加。セクションを重複させない。
- `work` と `/home/user/...`：実際のプロファイル名と絶対パスへ置換。TOML内の `$HOME` や `~` の展開を前提にしない。

```toml
auth_profile = "work"
[proxy]
admin_secret_file = "/home/user/.config/codex-dock/proxy-admin-secret"
```

| コマンド | 選択方法・表示内容 |
|---|---|
| `init` / `run` / `start` / TUI | 上のグローバル設定を共有 |
| `oaiso run` | `--auth-profile` などの明示フラグも利用可 |
| `oaiso codex` / `oaiso claude` | 引数をエージェントCLIへ渡すため、プロファイルはグローバル設定で選択 |
| `auth show` | 選択したプロファイルのモードだけを表示 |

- プロジェクトの `oaiso.toml` に認証設定を置かない。

**設定後、通常ユーザーで1つだけ実行（リリース版は任意のディレクトリで実行可能）**

| 保存した資格情報 | 初回準備 |
|---|---|
| OpenAI | `oaiso init --agent codex` |
| Anthropic | `oaiso init --agent claude` |

- その後、作業プロジェクトへ移動して `oaiso codex` / `oaiso claude`。
- 一時的な明示指定には `oaiso --auth-profile work run --agent codex --proxy-admin-secret-file "$HOME/.config/codex-dock/proxy-admin-secret"`。この指定だけでは後続の `stop` / `start` / TUIへ引き継がれないため、日常利用は上のグローバル設定を使う。

### 更新・切り替え・共有の制限

| 操作・境界 | 扱い |
|---|---|
| 作成・import | 新規作成のみ。既存プロファイルを上書きしない |
| 通常のOAuth更新 | 同じStoreへCAS保存。接続世代を維持 |
| 再ログイン | 新しいプロファイル名へ取り込む |
| 既存ワーカー | 別プロファイル・別接続世代へ再認可しない |
| Auth Proxy | 選択した保存ディレクトリだけを更新用に読み書き可能でmount |
| ワーカー・egress | 資格情報の保存領域を渡さない |
| workspace・追加mount | 保存先と管理シークレットを含めない |

**再ログイン後の切り替え手順**

1. 新しい名前でプロファイルを作成・import。
2. 旧ワーカーを停止・削除。
3. 旧設定のまま `oaiso proxy stop` → `oaiso proxy rm`。
4. グローバル設定の `auth_profile` を新しい名前へ変更。
5. 対象エージェントの `init` を通常ユーザーで実行し、Proxyを再作成。
6. 作業プロジェクトで新規ワーカーを起動。

- 以下の従来コマンド：プロファイル未選択時に利用。
- 選択中の `auth set` / `auth rotate`：拒否。

## `auth show` — 認証状態の確認

```bash
oaiso auth show
```

- 表示対象：現在の認証ソース。
- 非表示：実APIキー・トークン。
- プロファイル未選択時：従来モードのOpenAI情報だけを表示。Anthropic認証の確認にはならない。
- 表示は設定メタデータ。稼働中Proxyの認証方式や、実APIでの認証成功を保証しない。
- 従来モードでOpenAI OAuthとAPIキーが共存する場合、`auth show` の表示順と実Proxyの選択順は異なる。[実際の優先順位](../auth-proxy.md#認証ソース)を確認。

### 出力例：APIキー

```
Auth source: OPENAI_API_KEY env
Key configured: true
```

### 出力例：OAuth

```
Auth source: ~/.codex/auth.json (OAuth/ChatGPT subscription)
Key configured: true
```

### 出力例：未設定

```
Auth source: none
Key configured: false
```

## `auth set` — API キーの保存

```bash
export OPENAI_API_KEY=sk-...
oaiso auth set
```

| 項目 | 内容 |
|---|---|
| 読み込み元 | `OPENAI_API_KEY` 環境変数 |
| 保存先 | `~/.config/codex-dock/apikey` |
| パーミッション | `0600` |
| 利用条件 | 独立プロファイル未選択 |

## `auth rotate` — トークンのローテーション

```bash
oaiso auth rotate
```

| 目的 | 使う操作 |
|---|---|
| ローカルの更新マーカー作成 | `auth rotate`。稼働中Proxyのトークンは即時失効しない |
| 即時失効 | ワーカー停止または `/admin/revoke` |
| Proxy全体の更新 | [運用手順](../operations.md#cliプロキシを更新する) |

## 認証ファイルの場所

| ファイル | 場所 | 作成元 |
|---|---|---|
| 製品専用プロファイル | `~/.config/codex-dock/profiles/NAME/NAME.json` | `auth profile set` / `import` |
| 従来のAPIキー | `~/.config/codex-dock/apikey` | `auth set` |
| ホストOAuth | `~/.codex/auth.json` | Codex CLIの `codex login` |

[認証ファイルの形式](../configuration.md#認証ファイルの場所)

## 関連ドキュメント

| 確認したいこと | 参照先 |
|---|---|
| 認証ソースの優先順位・資格情報保護 | [Auth Proxy](../auth-proxy.md) |
| 設定値・ファイル形式 | [設定リファレンス](../configuration.md) |
| 初回の準備 | [クイックスタート](../getting-started.md) |

## 暗号化して保存する（v0.0.3以降）

- [方式・操作・出力例](../guides/encrypted-credentials.md)。
- 新規：`oaiso login --encrypt`／`--keyring`。
- 既存の平文を移行：`oaiso auth profile encrypt NAME`。
- 旧暗号化形式からageへ：`oaiso auth profile migrate-age NAME`。
- パスフレーズ変更：`oaiso auth profile passwd NAME`。
- 実行とProxyを停止してロック：`oaiso auth profile lock NAME`。
- 確認時は復号せず、秘密を表示しません。
