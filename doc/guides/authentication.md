# 認証方式とプロファイル

**日本語** | [English](../en/guides/authentication.md)

- 前提：[インストール](../installation.md)を完了。
- 設定場所：信頼済みホスト側。WSL／VMをホストにする場合も同様。

- v0.0.3以降の新規導入は[インストール→キーリング→login→初回起動](first-run.md)へ。`login`がプロファイル選択と管理シークレットを自動準備します。
- 以下は`set/import`で手動登録する場合の手順です。`login`の手順と重ねて実行しないでください。

## 1. 認証の分離方法を選ぶ

| 方式 | 資格情報の保持先 | 選ぶ場合 |
|---|---|---|
| 独立プロファイル（ホストと分ける場合の推奨） | 製品専用の0700ディレクトリ・0600ファイル | ホストCLIのログインと製品の認証を分ける |
| 従来のホスト連携 | ホストの認証ファイル・認証環境変数 | 既存のホスト認証を利用する |
| 組み込み製品のStore | 組み込み側が提供する保存先 | ケースごとの認証を[Go API](../credential-store.md)で管理する |

- 独立プロファイル選択時：ホスト認証へfallbackしない。
- 以下の手動`set/import`例は0600平文保存。v0.0.3以降の新規`login`は[キーリング＋age](keyring.md)が既定です。
- プロジェクトの `oaiso.toml`：資格情報・認証プロファイル設定を置かない。

## 2. エージェントと認証方式を選ぶ

| エージェント | APIキー | サブスク |
|---|---|---|
| Codex CLI | OpenAI APIキーを[登録](../commands/auth.md#apiキーを登録する) | Codex形式のOAuthを[import](../commands/auth.md#oauthを取り込む) |
| Claude Code | Anthropic APIキーを[登録](../commands/auth.md#apiキーを登録する) | Claude形式のOAuthを[import](../commands/auth.md#oauthを取り込む)。setup-tokenは[Claudeの手順](../claude-code.md) |

**独立プロファイルは、以下から1つだけ実行。例では名前を `work` に統一します。**

| 認証 | 実行するコマンド |
|---|---|
| OpenAI APIキー | `oaiso auth profile set work --provider openai` |
| Anthropic APIキー | `oaiso auth profile set work --provider anthropic` |
| Codex OAuth | `oaiso auth profile import work --format codex --credentials-stdin < /private/login/auth.json` |
| Claude OAuth | `oaiso auth profile import work --format claude --credentials-stdin < /private/login/.credentials.json` |

**登録成功時の出力**

| 操作 | 表示 |
|---|---|
| APIキー登録 | `Profile created (plaintext, 0600).` |
| OAuth import | `Profile imported (plaintext, 0600).` |

- APIキー入力時：`API key:` と表示。入力内容は画面に出ない。
- この表示は保存成功。キーの有効性やサブスク利用可否の検証結果ではない。

- `/private/login/...`：事前に公式CLIで認証したJSONの実際のパスへ置換。
- OAuth import：コピー時点でホストCLIの保存先と分離。その後のログイン変更は自動同期しない。
- `auth profile set`：TTYから非表示入力。引数へ実キーを書かない。
- `auth profile import`：明示したstdinから入力。元ファイルは自動探索・削除しない。
- 従来のホスト連携：[認証ソースの準備](../getting-started.md#2-認証方式を選ぶ)。

## 3. 管理シークレットとプロファイルを設定する

1. [管理シークレットを作成](../commands/auth.md#管理シークレットを作成し起動する)。資格情報とは別の0600ファイルに保存。
2. [グローバル設定でプロファイルを選択](../commands/auth.md#日常操作で使うプロファイルを固定する)。
3. 設定の `auth_profile` は、上で作成した `work` に合わせる。`/home/user/...` は実際の絶対パスへ置換。
4. 認証方式を確認。

```bash
oaiso auth show
```

**OpenAI APIキーを選択した場合の出力**

```text
Profile: work
Storage: plaintext (0600)
OpenAI: api-key
Anthropic: disabled
```

| 登録した認証 | 確認する行 |
|---|---|
| OpenAI APIキー | `OpenAI: api-key` |
| Codex OAuth | `OpenAI: oauth` |
| Anthropic APIキー | `Anthropic: api-key` |
| Claude OAuth | `Anthropic: oauth` |
| 未登録のprovider | `disabled`。そのproviderでの起動はできない |

- 独立プロファイルの期待値：`Profile: work` と、選択したproviderの `api-key` または `oauth`。
- プロファイルを作成しただけでは選択されない。設定前の `auth show` は従来モードのOpenAI情報を表示。
- 専用Kataでログインする場合は[loginとAPI接続確認](../commands/login.md)を参照してください。

## 4. 初回起動へ進む

1. 必要なら[一般通信の宛先を設定](networking.md)。
2. [クイックスタートのイメージ・Proxy準備](../getting-started.md#3-プロキシとイメージを用意する)へ戻る。
3. `work` にOpenAIを登録した場合はCodex、Anthropicを登録した場合はClaudeを準備・起動。

- `init` は認証ソースの存在を確認する。実サービスでの認証成功は、エージェントの最初の要求で別途確認。

## 更新と切り替え

| 操作 | 扱い |
|---|---|
| 通常のOAuth更新 | 同じStoreへCAS保存。接続世代を維持 |
| 再ログイン・別アカウントへの切り替え | 新規プロファイルへ取り込み、[ワーカーとProxyを切り替える](../commands/auth.md#更新切り替え共有の制限) |
| ワーカーへ渡す資格情報 | 短命トークンのみ。実APIキー・access/refresh/ID tokenは返さない |
| 認証専用Kata・自動login・API接続確認 | [v0.0.3で実装](../commands/login.md)。実アカウントでの確認は別途必要 |
| ローカル暗号化・鍵backend | [v0.0.3以降でage・キーリング対応](encrypted-credentials.md)。外部Vaultは組み込み側のStoreで接続 |

- 保持先・権限・制限：[セキュリティ](../security.md)。
- フラグと保存先：[認証コマンドリファレンス](../commands/auth.md)。
