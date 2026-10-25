# ログインと初回認証設定

**日本語** | [English](../en/commands/login.md)

**v0.0.3以降で利用できます。**

- 既定はキーリング保存です。GUIなしでは先に`oaiso auth keyring session`を開いてください。[準備と操作](../guides/keyring.md)。

## 対話でログインする

```bash
oaiso login
```

1. `codex` / `claude` を選択。
2. `subscription` / `api-key` を選択。
3. 案内に沿って認証。
4. 新しい独立プロファイルを保存し、グローバル設定へ反映。

| 認証方式 | 動作 |
|---|---|
| APIキー | 非表示入力後、公式のモデル一覧APIで接続確認。推論は送信しない |
| サブスク | 専用Kata内の固定版公式CLIでログイン。ホストCLIの導入・認証は不要 |

## エージェントと方式を指定する

```bash
oaiso login --agent codex --method subscription
oaiso login --agent claude --method subscription
oaiso login --agent claude --method api-key --profile work
```

- 上の例はいずれか1つを実行。
- `--profile` 省略時は新しい名前を生成。既存プロファイルは上書きしない。
- 表示されたURLを利用者のブラウザで開き、公式CLIの案内に従う。Codexはdevice code、ClaudeはURLと必要に応じたコード入力を使用。
- サブスクの利用可否は提供元のアカウント・組織設定による。

| フラグ | 動作 |
|---|---|
| `--agent codex\|claude` | エージェント |
| `--method api-key\|subscription` | 認証方式 |
| `--profile NAME` | 新しいプロファイル名 |
| `--key-stdin` | 明示的にstdinからAPIキー入力。agentとmethodの指定が必須 |
| `--skip-check` | APIキーを未検証のまま保存。オフラインなどで明示的に選ぶ場合のみ |
| `--encrypt` | 非表示入力のパスフレーズで直接暗号化 |
| `--keyring` | Secret Serviceで暗号化。新規loginの既定 |
| `--plaintext` | 明示的に0600平文保存。`--encrypt`・`--keyring`とは同時指定不可 |
| `--no-select` | 保存だけを行い、グローバル設定を変更しない |

## 保存と接続確認の表示

```text
Profile saved: work (encrypted, 0600).
Connection check: passed. Inference: not tested.
Selected profile: work
Settings: /home/user/.config/codex-dock/config.toml
```

- 名前・パスは例。新規loginは[キーリング方式](../guides/keyring.md)が既定。GUIなしでは`oaiso auth keyring session`内で実行します。
- 暗号化ストアは[実装仕様](../development/local-credential-store.md)を参照。
- 接続確認はモデル一覧APIへの認証。実推論や全モデルの利用権限の保証ではない。
- サブスクでは `Official CLI login: completed. Inference: not tested.` と表示。
- 通常エラーへ上流の応答本文や実キーを載せない。

## 自動で準備するもの

- 独立プロファイルと0700／0600の保存領域。
- 未設定の場合、ランダム生成した管理シークレットの0600ファイル。
- グローバル設定の `auth_profile` と必要な管理シークレット設定。
- 既存の通信制限・その他の設定・コメントは維持。
- 既存プロファイル・管理シークレット・稼働ワーカー／Proxyは自動削除・置換しない。
- 設定反映に失敗しても保存済みプロファイルは保持。再選択は `oaiso auth profile use NAME`。

## 認証用Kataの範囲

- 通常のKata起動ゲートと非root・資源制限を維持。
- 作業フォルダー・ホストHOME／認証・ユーザーテンプレート・プロジェクトhookを渡さない。
- 製品内蔵レシピの固定版公式CLIを使用。CLI版の開発用環境変数で変更しない。
- rootfsはreadonly。認証ファイルは128 MiB上限のゲストtmpfsへ保存。
- 認証用ドメインと443番へ限定した専用egress Proxyを使用。
- ログインと資格情報回収は通常consoleへ保存しないstreaming execで実行。
- 成功・取消・失敗のいずれでもVMと専用egressを削除。回収失敗はエラーとして対象IDを表示。
- 停止済み認証VMの再開を拒否。再ログインは新規VMで実行。
- 通常の作業VMには実資格情報を渡さず、認証Proxyと短命トークンを使う。

## 次の操作

```bash
oaiso init --agent claude
cd /path/to/your-project
oaiso claude
```

- Codexの場合は両方の `claude` を `codex` へ置換。
- 既存Proxyが別プロファイルを使う場合、旧ワーカーとProxyを先に終了してから初期化。
- 公式手順：[Codex認証](https://learn.chatgpt.com/docs/auth)・[Claude認証](https://code.claude.com/docs/en/authentication)。

- 既存プロファイルは変更しません。キーリングを使わない新規loginは`--encrypt`または`--plaintext`を明示指定します。
