# インストールから最初のVM起動まで

**日本語** | [English](../en/guides/first-run.md)

- このページはv0.0.4以降の手順です。`oaiso --version`で導入した版を確認してください。
- 以下はUbuntu 26.04・x86_64・systemd・KVM・cgroup v2があるホスト向けです。[前提条件](../installation.md#1-前提条件)。

## 1. ビルド済み版をインストールする

通常ユーザーで計画を表示し、内容を確認して適用します。先に[curl・CA証明書・GitHub CLIを準備](../installation.md#2-ツールを準備する)してください。GitHub CLIは署名検証に使います。

```bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/latest/download/install.sh | bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/latest/download/install.sh | bash -s -- --apply
```

- Go・Make・ソース取得は不要。uv・Pythonはスクリプトが準備します。
- 不足するD-Bus・GNOME Keyring・`secret-tool`もsetupが導入します。
- 版を固定する場合：[v0.0.4を指定して導入](../installation.md#版を固定して取得実行する)。
- mainをソースから導入する場合：[開発ツール](../development/toolchain.md)と[ソースからの配置手順](../setup.md)を参照してください。

## 2. 再ログインして基盤を確認する

ログアウト・再ログイン後、通常ユーザーで実行します。

```bash
oaiso --version
id -nG
oaiso runtime-check
```

- `id -nG`に`codex-dock`が含まれること。
- `runtime-check`が終了コード0・全項目`[ok]`になること。
- 認証や通常の起動には`sudo`を付けません。

## 3. キーリングを解錠する

| 環境 | 操作 |
|---|---|
| デスクトップのキーリングが解錠済み | 次の`check`へ進む |
| SSH・WSL・GUIなし | 下の専用セッションを開き、以後の操作をそのシェル内で行う |

```bash
oaiso auth keyring session
```

- 初回は専用パスフレーズを2回、次回は1回入力。パスフレーズ自体は保存しません。
- 認証・初回起動が終わるまで、このシェルを閉じないでください。

```bash
oaiso auth keyring check
```

```text
Keyring: ready (store/read/delete verified; no provider request).
```

## 4. 利用するエージェントでログインする

以下のうち、使うものを1つ実行します。

| エージェント・認証方式 | コマンド |
|---|---|
| Codex・APIキー | `oaiso login --agent codex --method api-key --profile work` |
| Codex・サブスク | `oaiso login --agent codex --method subscription --profile work` |
| Claude Code・APIキー | `oaiso login --agent claude --method api-key --profile work` |
| Claude Code・サブスク | `oaiso login --agent claude --method subscription --profile work` |

- APIキーは非表示入力。サブスクは一時Kataで公式CLIのログイン手順を実行。
- APIキーはモデル一覧APIで接続確認します。実推論の成功とは別です。
- 成功すると、暗号化保存・利用プロファイルの選択・管理シークレットの準備を自動実行。
- **ここでは手動の`auth profile set/import`や管理シークレット生成を追加実行しません。**
- 既存の`work`は上書きしません。名前が重なる場合は別名を指定します。

```text
Profile saved: work (encrypted, 0600).
Selected profile: work
Settings: /home/user/.config/codex-dock/config.toml
```

## 5. イメージと認証プロキシを準備する

ログインした側だけを実行します。

**Codexの場合**

```bash
oaiso init --agent codex
```

**Claude Codeの場合**

```bash
oaiso init --agent claude
```

- 初回は既定のワーカーイメージを取得・ビルドするため、通信と時間が必要です。
- `Ready.`はイメージとプロキシの準備完了を表します。AIからの応答確認は次の手順です。
- 企業プロキシ等が必要な環境では、[BuildKit daemonとビルド時の通信設定](../kata.md#3-ネットワークとサービスを有効にする)も準備してください。

## 6. プロジェクトで実行する

```bash
cd /path/to/your-project
```

| エージェント | VM内の起動確認（推論なし） | 通常利用 |
|---|---|---|
| Codex | `oaiso codex --version` | `oaiso codex` |
| Claude Code | `oaiso claude --version` | `oaiso claude` |

- 起動確認では、出力の末尾に`codex-cli 0.163.0`または`2.1.306 (Claude Code)`が表示され、終了コード0になることを確認します。
- `/path/to/your-project`は実際の作業先へ置換。現在のディレクトリをVMへ共有します。
- Git・npm・pipへの一般通信は既定拒否。[必要な宛先を許可](networking.md)してください。
- 終了後のVM管理：[日常の操作](../operations.md)。

## 確認できる範囲

| 確認 | 意味 |
|---|---|
| `runtime-check` | 実行基盤が準備できている |
| `keyring check` | 鍵の保存・取得ができる |
| `login`成功 | 資格情報を保存し、初期設定できた |
| `init`の`Ready.` | イメージと認証プロキシが準備できた |
| `codex/claude --version` | VM内で固定CLIを起動・終了できた。推論は未確認 |
| エージェントから実際の応答 | そのアカウント・モデルで推論できた |

- 自動検証では架空の資格情報を使用します。実アカウントのログイン・推論成功は、利用者の認証・プラン・モデル権限・通信条件に依存します。
- [実施済みの通し試験と未確認事項](../testing.md)。
