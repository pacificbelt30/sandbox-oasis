# `oaiso upgrade` — 配布版の更新

**日本語** | [English](../en/commands/upgrade.md)

- **v0.0.3以降で利用できます。**
- Goの追加は不要です。既存の配布インストーラーを使い、ホスト全体の更新手順を案内します。

## 更新計画を確認する

**最新版を調べて計画を表示**

```bash
oaiso upgrade
```

**版を固定して計画を表示**

```bash
oaiso upgrade --version v0.0.4
```

- 先に[GitHub CLIを準備](../installation.md#2-ツールを準備する)します。GitHubへのログインは不要です。
- `install.sh`・`SHA256SUMS`・`provenance.json`を取得し、インストーラーの署名・作成元・タグ・ハッシュを検証してから計画を表示します。
- インストーラーが残りの配布物を取得・検証します。[署名の確認範囲](../guides/signed-releases.md)。
- この段階ではサービス・製品バイナリの配置を変更しません。必要なuv・Pythonの利用者領域への準備と、配布物のダウンロードは行います。

```text
Installed CLI: dev
Target release: v0.0.4

Host update: CLI, node, helpers and pinned runtime assets.
Images: rebuild the default worker after the host update; prepare proxies with the new CLI.
Custom images/templates: review separately.
Before applying: save work, then stop/remove retained workers and proxies with the installed CLI.
...
Plan only. Apply the same release with: oaiso upgrade --version v0.0.4 --apply
```

- 上は出力例です。実際の版・計画は配置状態で変わります。
- 暗号化プロファイルの互換性を検査し、対応しない配布物の適用を拒否します。保存先を変更する場合は`--auth-profile-dir`で指定してください。

## 適用する

1. 計画と[リリース情報](../release-notes.md)を確認。
2. 作業を保存し、既存CLIでワーカー・Proxyを停止して削除。
3. 計画に表示された版を指定して適用。

```bash
oaiso upgrade --version vX.Y.Z --apply
```

- `vX.Y.Z`は確認した配布版に置き換えます。
- 通常ユーザーで実行します。必要な管理操作はインストーラーが`sudo`で実施します。
- 実行中・保持中の資源や不整合があると、既存インストーラーの検査が適用を止めます。
- 失敗時の復旧：[setup](../setup.md)。

## 更新する範囲と順番

| 順番 | 対象 | 操作 |
|---|---|---|
| 1 | CLI・node・guard・IO logger・固定runtime資産 | `upgrade --apply`の配布インストーラー |
| 2 | デフォルト作業イメージ | 更新後のCLIで`oaiso build --template plain --tag codex-dock:latest` |
| 3 | 認証Proxy | 更新後のCLIで`oaiso init --agent codex`または`--agent claude` |
| 4 | 独自イメージ・テンプレート | 差分を確認して個別にビルド |
| 5 | 動作 | 新しいワーカーで接続・推論を確認 |

- イメージを実行中の古いCLIから自動ビルドしません。手順2以降は更新後のCLIで実施します。
- `oaiso update`は[Goで導入したCLIだけ](update.md)を更新する別のコマンドです。
