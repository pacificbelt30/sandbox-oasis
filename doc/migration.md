# 旧版からの更新・移行

- 現行構成は **containerd → Kata runtime-rs → Dragonball** です。

## Oaisoへの名称変更

- 正式名称は **御AgentISOlation**、英字表記は **Oaiso**、CLIは **`oaiso`** です。

- 提供するコマンドは `oaiso` のみ。旧 `cxd` / `codex-dock` の互換コマンド・補完は生成しません。
- シェルスクリプト・CI・ショートカット内の旧コマンド名を `oaiso` に置換してください。
- GoでCLIだけを配置する場合は `go install github.com/pacificbelt30/codex-dock/cmd/oaiso@latest` を使用します。
- [配布インストーラー](installation.md)でCLI・node・補助プログラムをまとめて配置・更新できます。

## プロジェクト設定を oaiso.toml に変更する

- 新しいファイル名は小文字の **`oaiso.toml`**。設定の書式は同じです。
- 旧 `codex-dock.toml` があるプロジェクトでは、起動・ビルド・設定変更の前に名前を変更してください。
- プロジェクト設定を使っていなかった場合：ファイルの作成も移行も不要です。

**Gitで管理していて、新しい名前のファイルがまだない場合**

```bash
git mv codex-dock.toml oaiso.toml
oaiso project check
```

| 状態 | 必要な操作 |
|---|---|
| Gitで管理していない | `mv -n -- codex-dock.toml oaiso.toml` → `oaiso project check` |
| 両方の名前のファイルがある | 内容を比較・統合し、旧名のファイルを取り除いてから `project check`。移動で上書きしない |
| `obsolete project config` と表示される | 旧名のファイルが残っている。表示されたパスを確認し、上の手順で変更 |

- `read_only`・`no_internet`・ファイルの拒否設定なども含めて移してください。
- 旧名の設定は自動で読み込みません。制限が失われないよう、旧名が残っていれば処理を停止します。
- `project init` / `default --local` も、旧名の設定を残したまま新しいファイルを作成しません。
- CLIとGo APIのプロジェクト設定は、どちらも同じ名前と検証方法を使います。

## 変更しない保存先・サービス名

| 対象 | 更新後も使う場所・名前 |
|---|---|
| 利用者ごとの設定・認証プロファイル | `~/.config/codex-dock/` |
| 従来のホスト認証で更新したOAuth情報 | `~/.local/state/codex-dock/oauth/` |
| 追加パッケージの一覧 | `packages.dock` |
| 管理サービス・グループ・イメージ・環境変数 | 既存の名前を使用 |
| Goライブラリ・リポジトリURL | Go moduleと `pkg/cxd` のimport、リポジトリURLは変更なし |

- 設定や認証情報をコピーし直したり、名前の変更だけを理由に再ログインしたりする必要はありません。
- 導入済みノードの更新：[通常の更新手順](setup.md)。作業中のワーカーや保存領域を、名前の変更だけを理由に削除しないでください。
- 新しい `oaiso` の配置先に別のファイルがある場合、セットアップは上書きせず停止します。

## 旧コマンドを削除する

| 旧版の導入方法 | 更新時の旧コマンドの扱い |
|---|---|
| 自動セットアップ | 導入記録と旧版の内容が一致するコマンド・補完だけを回収。失敗時は復元 |
| `make install` | 既存 `oaiso` と内容が同じ旧コマンドと、その未変更の補完を回収 |
| Go・手動コピー・独自配置 | 配置元を自動判定しない。`command -v cxd` / `command -v codex-dock` で場所を確認し、自分で導入した旧実行ファイル・補完を削除 |

- 無関係な同名ファイル・内容を変更した旧ファイルは自動削除しません。
- 旧版の内部バイナリは、更新失敗時に以前の状態へ戻すために保持。通常のCLIとして再配置しません。

## 引き継げるもの

| 既存のファイル・資産 | 必要な作業 |
|---|---|
| プロジェクト、Gitリポジトリ | 同じパスを指定して利用できる |
| Dockerfile、テンプレート、`packages.dock` | BuildKitで再ビルドする |
| 利用者ごとの設定、ホスト認証ファイル | 現行設定を確認して再利用する。プロジェクト設定だけは上の手順で名前を変更 |
| 旧コンテナID、snapshot、ネットワーク | 自動移植しない。新しいワーカーを作成する |
| 旧QEMU/Docker用manifest、VM予約 | 採用しない。現行資産を検証して作成する |

## 移行する

1. 旧版のCLIで、必要なログと成果を保存する。
2. 旧ワーカーと旧プロキシを停止・削除する。

    - 旧版が管理していたworktreeやネットワークも確認する。

3. [Kata実行ホストの準備](kata.md)に従い、専用containerd・Kata・Dragonball・BuildKit・管理サービスを配置する。
4. 通常利用者で `oaiso runtime-check` を実行する。
5. [クイックスタート](getting-started.md)に沿ってプロキシとイメージを再ビルドし、新しいワーカーを作成する。

- 旧イメージを残す必要がある場合は、移行元で `docker save` したarchiveを専用containerdの `codex-dock` namespaceへimportする方法もあります。
- 通常はDockerfileからの再ビルドを使ってください。

## 接続先と互換モード

- CLIは `/run/codex-dock-node.sock` へ接続します。
- 変更する場合は `CODEX_DOCK_KATA_NODE_SOCKET` に絶対パスを指定します。
- `DOCKER_HOST` やDocker contextでは接続先を変更できません。

- `--runtime runc` は同じLinux/containerdノード上の互換モードです。
- 独立カーネルによる隔離は提供しません。
- Kataが利用できない場合も、自動では切り替えません。

認証プロキシだけを更新する場合は、[日常の運用](operations.md#cliプロキシを更新する)を参照してください。
