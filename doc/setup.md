# 導入と更新の自動化

- 初めてインストールする場合：[取得から動作確認までのコマンド](installation.md)。
- このページ：配置オプション・更新・失敗時の復旧。

| 対象 | 方法 |
|---|---|
| Ubuntu 26.04・Linux x86_64・systemd・KVM・cgroup v2 | このページの自動配置 |
| その他のLinux | [手動配置](kata.md) |
| Windows・macOS | 実行ノードの配置対象外 |
| ホスト自体がVM | nested virtualizationも必要 |

## 初めて配置する

- **v0.0.7**では、[rootfs容量制限](guides/resource-limits.md)のため専用XFSとproject quotaが必要です。既存ext4配置は、保管・ストレージ準備・ワーカー再作成を終えてから適用してください。setupはディスクを自動変換しません。

### 配置計画を確認して適用する

```bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/latest/download/install.sh | bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/latest/download/install.sh | bash -s -- --apply
```

- GitHub Releasesのビルド済みCLI・補助プログラムを使います。Go・Make・ソース取得は不要です。
- uvとPython 3.12は、なければユーザー用領域へ準備します。
- リリースのインストーラーは、そのリリースと同じ版の配布物を取得します。
- 配布アーカイブのSHA-256、`bundle.json` の版と各ファイルのSHA-256、実CLIの版を検証してから実行します。
- SHA-256は破損・取り違えの検出用です。配布元の独立した署名検証ではありません。

| 指定 | 動作・条件 |
|---|---|
| 指定なし | リリースのバンドルを取得・検証して計画を表示 |
| `--version TAG` | 指定した版を取得 |
| `--apply` | 計画の成功後、同じCLIと選択済みPythonの絶対パスを使って `sudo` で配置 |
| `--operator USER` | 権限を与える既存の通常利用者を指定 |
| operator省略 | スクリプトを実行した通常利用者を選択 |
| rootでスクリプトを実行 | `--operator USER` が必須 |
| `--bundle PATH` | 指定ディレクトリのビルド済みCLI・補助プログラムを使用。`--version` とは併用不可 |
| `--cache PATH` / `--offline` | 計画と適用の両方へ渡す |

### ビルド済みバンドルとは

- **同じ版の実行ファイルと、検証用の一覧をまとめたディレクトリ**です。
- リリースのインストーラーが自動で取得・展開します。通常利用では `--bundle` の指定は不要です。
- ソースからビルドする場合は `make node-bundle` が `dist/node-bundle/` に作成します。
- 別ホストでビルドしたものや、前に作成したものを使うときだけ `--bundle PATH` を指定します。持ち込み先のホストにGo・Makeは不要です。
- 自分でビルドしたもの、または配布元を確認したものを使ってください。

```text
node-bundle/
├── oaiso                    CLI・管理サービス・埋め込みセットアップ処理
├── codex-dock-worker-guard  VM起動時の検証用プログラム
├── codex-dock-io-logger     入出力・ログ用プログラム
├── bundle.json             版と各ファイルのSHA-256
├── LICENSE                 製品のライセンス（リリース配布物）
└── THIRD_PARTY_NOTICES.txt  依存ライブラリのライセンス／notice（リリース配布物）
```

- 以下は[保存した `install.sh`](installation.md#版を固定して取得実行する)と、検証・展開済みバンドルを使う例です。パスは実際の保存先へ置換。

```bash
bash install.sh --bundle /path/to/node-bundle
bash install.sh --bundle /path/to/node-bundle --apply
```

- 含まないもの：Kata等の配布物、コンテナイメージ、プロジェクト、認証情報。
- オフライン導入には、バンドルに加えて[配布物のキャッシュとPythonの準備](#オフライン配置)が必要です。

### ソースからビルドする場合

**開発者・独自変更を含む版を使う場合の手順です。ビルド済み版の導入では不要です。**

1. [開発用ツールを準備](development/toolchain.md)。Goは公式Linuxアーカイブから導入し、`go version` を確認。
2. 以下でソースを取得し、配置計画を確認して適用。

```bash
git clone https://github.com/pacificbelt30/sandbox-oasis.git
cd sandbox-oasis
./scripts/install.sh
./scripts/install.sh --apply
```

- 必要なツールと公式手順：[Go・Make・Git・uv・Python](development/toolchain.md#必要なツールと公式手順)。
- Goの最低版は `go.mod` の1.26.0。公式の安定版と対応するSHA-256を使用してください。

### 個別に計画・適用する場合

```bash
make node-bundle
./dist/node-bundle/oaiso setup
```

**計画に表示する項目**

- バンドルのパスと版。
- operator。
- キャッシュのパス。
- オフライン指定。

**バンドルと利用者の選択**

- バンドル内CLI：隣接する `bundle.json` を自動探索。
- 通常利用者による計画：その利用者をoperatorに選択。
- 別のCLI／利用者：`--bundle PATH --operator USER` を明示。
- 適用：同じバンドルのCLIを使用し、付与先の `--operator` を明示。

```bash
setup_python=$(uv python find --no-project --no-config --managed-python --system --no-python-downloads 3.12)
sudo ./dist/node-bundle/oaiso setup \
  --operator "$USER" --python "$setup_python" --apply
```

- `--python` は配置処理に使うPythonの絶対パスです。通常の `install.sh --apply` では自動で渡します。
- `sudo` のPATHやHOMEにuvがなくても、選択したPythonを使用できます。直接 `setup` を実行する場合は、準備済みのuv管理Python 3.12を探します。
- 管理者権限での `setup` 自体はPythonをダウンロードしません。

### 通常利用者として認証とイメージを準備する

1. 再ログインしてグループ所属を反映。
2. 利用する認証を準備。
3. 通常利用者で `init` を実行。

```bash
oaiso init
# Claude Codeを使う場合
oaiso init --agent claude
```

- 通常の認証・`run`：`sudo` 不要。
- リリース版のProxyイメージは、配置済みの静的CLIから作成します。ソースの取得は不要です。
- 詳細：[クイックスタート](getting-started.md)。

- 新規loginのキーリング保存にはD-Bus・GNOME Keyring・`secret-tool`を使用し、v0.0.3以降のsetupで不足分を導入します。GUIなしでは[専用セッション](guides/keyring.md)を使用します。

## 自動で準備するもの

| 対象 | 内容 |
|---|---|
| OSパッケージ | 不足分を追加。既存Docker／既定containerdのパッケージは置き換えない |
| 固定配布物 | Kata 4.2.0・containerd 2.4.2・BuildKit 0.34.0・runc 1.4.3 |
| guest・固定資産 | PID hook・共有cache無効のDragonball設定・manifest |
| サービス・権限 | 専用containerd・node・BuildKit・`codex-dock` グループ |
| ホスト設定 | IPv4 forwarding・次回起動用vsock/netモジュール |

### 配布物と権限の確認

| 項目 | 確認事項 |
|---|---|
| 外部配布物 | [固定URLとSHA256](https://github.com/pacificbelt30/sandbox-oasis/blob/main/deploy/kata/assets.lock.json)で検証 |
| 自作バンドルの `bundle.json` | CLI/helperの組合せ・破損を検出。配布元の署名ではない |
| バンドルの入手元 | 信頼したソースからビルドしたものを使用 |
| 管理グループ | ホストmount・任意イメージを選択できるroot相当の権限 |
| 既存firewall | 変更・flushしない。egressを許可するforward方針は管理者が確認 |
| KVM・cgroup | KVMと、cgroup v2の `cpu memory pids` controllerが必要 |

## 更新する

**対象：この自動配置で管理している環境。**

1. アプリの保存を完了し、共有workspace・必要なHOME内データ・ホスト設定を保管。VMを停止して、必要な[rootfs checkpoint](guides/disk-checkpoint.md)と[専用DB](guides/agent-state.md#バックアップと別vmへの復元)を別々に保存します。バックアップは共有workspaceの外へ置き、対象外のデータも確認してください。
2. 旧版CLIでワーカーとProxyを停止・削除。停止しただけでは更新できません。VM削除前にバックアップを確認し、不要なジョブ・予約枠も回収します。
3. [配布インストーラー](installation.md#3-計画を確認してインストール)を再実行して最新版の計画を確認。
4. 同じインストーラーに `--apply` を付け、新しいCLI・node・補助プログラムを同時に配置。
5. 再ログイン後、通常利用者でワーカーイメージを再ビルドし、認証済みのエージェントで `init`。
6. `runtime-check`と、新規ワーカーでの動作を確認。保存したrootfs／専用DBは、互換性を確認して新しいVMへ復元します。元のVM IDは引き継ぎません。

**手順5〜6の例（既定ワーカーイメージ・Codex）**

```bash
oaiso build --template plain --tag codex-dock:latest
oaiso init --agent codex
oaiso runtime-check
```

**Claude Codeを使う場合**

```bash
oaiso build --template plain --tag codex-dock:latest
oaiso init --agent claude
oaiso runtime-check
```

- `init` だけでは、既存のワーカーイメージが必ず更新されるとは限りません。
- Proxyイメージは、必要に応じて新しい静的CLIから作成します。
- 独自イメージ・利用者テンプレート：[更新対象と再作成の注意点](operations.md#cliプロキシを更新する)。
- 実行基盤の受入試験：[Kataの確認手順](kata.md)。実推論の成功は、新規ワーカーで別途確認。

| 更新時の条件 | 動作 |
|---|---|
| 受付サービス・BuildKit | 先に停止 |
| 空であることの確認対象 | 停止済みを含む全コンテナ・task・VM予約・VM cgroup |
| containerdへ接続不能 | 「空」として扱わない |
| 残存資源あり | 旧サービスを再開し、回収方法を表示して終了 |
| 作業中のVM・worktree | 自動削除しない |
| CLI/nodeの配布版不一致 | 検証で拒否 |
| 開発用 `dev` ビルド | API・資産・共有設定の確認は実施 |
| 既存の手動配置 | 上書きを拒否。[手動更新](kata.md#固定資産の更新)で移行 |

## 更新に失敗した場合

| 対象 | 扱い |
|---|---|
| 新しい資産 | `/opt/codex-dock/releases/` の新規ディレクトリへ配置 |
| 既存の固定資産 | 書き換えない |
| 導入完了の確定 | サービス切り替えと診断の成功後 |
| 切り替え後の失敗 | 以前のCLI・設定・サービス定義・forwarding設定・サービス有効状態を復元 |
| 残る変更 | OSパッケージ追加・グループ作成／所属追加・ダウンロードキャッシュ |

### 前の成功した配置へ戻す

1. 必要なデータを保管してから、ワーカーとProxyを停止・削除。停止済みVMも残せません。
2. rollback計画を確認。
3. `--apply` で適用。

```bash
setup_python=$(uv python find --no-project --no-config --managed-python --system --no-python-downloads 3.12)
oaiso setup --rollback --python "$setup_python"
sudo oaiso setup --rollback --python "$setup_python" --apply
```

| 保存対象 | 場所・扱い |
|---|---|
| 導入状態・復旧記録 | `/var/lib/codex-dock-setup` |
| イメージ・BuildKitキャッシュ・プロジェクト | 削除しない |
| 古い／失敗したrelease | 調査・復旧用に保持 |
| 参照中の資産 | 手作業で削除しない |

## オフライン配置

**配置先で、先に以下を準備してください。**

1. uv本体と、`uv python install 3.12` によるPython。
2. 配布元とSHA-256を確認した `install.sh`、検証・展開済みのリリースバンドル。
3. 固定配布物を `assets.lock.json` の `file` 名で保存したキャッシュ。
4. 必要なOSパッケージ。ワーカーの準備にはコンテナイメージ・ビルド依存物も必要。

**準備済みファイルを使う例（パスは置換）**

```bash
bash /path/to/install.sh --bundle /path/to/node-bundle --cache /path/to/runtime-cache --offline
bash /path/to/install.sh --bundle /path/to/node-bundle --cache /path/to/runtime-cache --offline --apply
```

- 配布インストーラーの `--offline` には `--bundle PATH` が必須です。
- uv・Pythonの自動ダウンロードも禁止します。
- ソースからビルドする場合は、Goのビルド用キャッシュも別途準備。

| 項目 | 条件 |
|---|---|
| 既定キャッシュ | `/var/cache/codex-dock-setup` |
| 所有・更新権限 | ホスト管理者だけが変更できる場所を使用 |
| checksum検証 | オフラインでも省略しない |
| proxy・CA | ダウンロード用curlとaptにホストの設定を用意 |
| TLS検証 | 無効化オプションは提供しない |
