# `oaiso status` — 準備状態をまとめて確認する

**日本語** | [English](../en/commands/status.md)

- **v0.0.3以降で利用できます。**
- 保存済み・接続確認済み・推論確認済みを区別し、秘密は表示しません。

```bash
oaiso status
oaiso status --json
```

| 確認するもの | 既定の動作 |
|---|---|
| CLIの版 | 実行中の版を表示 |
| 選択プロファイル | 保存状態を表示。暗号化プロファイルは復号しない |
| 実行基盤 | ローカルnode・Kata・ビルド環境の検査 |
| 認証Proxy | 管理認証・短命トークン強制・プロファイル一致を確認 |
| ワーカー | 保持中のワーカー／セッション数を表示 |
| 更新 | 未確認。GitHubへ問い合わせない |
| 接続・推論 | 未確認。提供元のAPIへ問い合わせない |

```text
[ok] version: v0.0.3
[ok] profile: work (encrypted (passphrase, age), 0600); saved, locked for this command
[unchecked] connection: Credentials were not decrypted; connection and inference not tested
...
[ok] proxy: worker-token enforcement is active; inference not tested
[ok] workers: 0 retained workers/sessions
[unchecked] updates: No external release request was made.
```

- 上は一部の出力例です。実際の状態・エラーに応じて修正手順も表示します。
- `locked for this command`は、この検査では復号していないという意味です。稼働中Proxyの停止を意味しません。
- 基盤や選択プロファイルの検査に失敗すると終了コード1。`unchecked`だけなら、それを失敗扱いにはしません。

## 外部へ明示的に問い合わせる

```bash
oaiso status --check-connection
oaiso status --check-updates
```

| フラグ | 問い合わせ先・制限 |
|---|---|
| `--check-connection` | 保存済みAPIキーでOpenAI／Anthropicのモデル一覧APIを確認。暗号化なら解錠が必要 |
| `--check-updates` | GitHubの最新安定版を取得し、インストール版と並べて表示 |

- APIチェックは推論を実行せず、推論権限や全モデルの利用可否を保証しません。
- OAuthの自動更新・実ログイン確認は行いません。実エージェントで別に確認してください。
- [ログイン](login.md)、[暗号化保存](../guides/encrypted-credentials.md)、[更新](upgrade.md)。
