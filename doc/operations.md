# 日常の運用

| 目的 | このページの操作・参照先 |
|---|---|
| 初回準備 | [クイックスタート](getting-started.md) |
| ホストの配置 | [インストール](installation.md) |
| 自動配置環境の更新・復旧 | [setup](setup.md#更新する) |
| CLI・Proxy・ワーカーイメージの更新 | [更新対象と手順](#cliプロキシを更新する) |

## 状態とログを見る

```bash
oaiso ps -a
oaiso vm ps -a
oaiso vm inspect WORKER
oaiso logs WORKER --follow
oaiso ui
```

| 操作 | 確認できるもの |
|---|---|
| `ps` | ワーカーの状態 |
| `vm ps` / `vm inspect` | containerd・予約台帳・ホストプロセスを照合したVMの状態 |
| `logs --follow` | ログの追跡 |
| `ui` | TUIでの一覧・管理 |

- VM稼働の確認と、アプリケーションの正常応答の確認は別。
- 状態の意味：[VM管理](vm.md)。
- 表示との比較：[ワーカー一覧の出力例](commands/worker.md#oaiso-ps--ワーカー一覧)・[VM一覧の出力例](vm.md#一覧の出力例)。
- ログは実行したエージェント・コマンドの出力。空でも異常とは限らない。タスクの成果と終了コードも確認。

## サンドボックスの作業を確認する

利用可能：**未リリース（main・v0.0.8予定）**。対応するCLI・node・補助ツールを[準備済み](guides/sandbox-ui.md#前提と起動)のホストで、通常ユーザーが`oaiso ui`から対象VMを選びます。

| 目的 | 操作と詳細 |
|---|---|
| 長い処理を開始し、TUIを閉じてから確認する | 大文字`J`または`F2`→`Background jobs`。[ジョブの開始・ログ追従・選択停止](guides/sandbox-ui.md#バックグラウンドジョブを管理する) |
| ゲスト内のcommitも含めて変更を確認する | `W`→`Since launch`。[ファイル別レビューと成果物回収](guides/sandbox-ui.md#起動時点との比較とファイル別レビュー) |
| CPU・メモリ・容量と停止記録を確認する | `L`→`Refresh`。[観測値・警告・不明状態の読み方](guides/sandbox-ui.md#cpuメモリと停止理由) |

TUI終了ではVM・ジョブを止めません。VMの停止・削除や適用中の寿命制限ではジョブも終了するため、必要な成果物は先に回収します。

## 停止・再起動・削除する

```bash
oaiso stop WORKER
oaiso start WORKER
oaiso stop WORKER
oaiso rm WORKER
```

| 操作 | 認証・保存状態 |
|---|---|
| 停止 | 認証を失効。コンテナとsnapshotを保持 |
| 再起動 | Kataを再検証。同じID・workspace・placeholderを復元 |
| 停止済みワーカーの削除 | `rm WORKER`。必要な成果は先に保存 |
| 実行中ワーカーの削除 | `rm --force WORKER` |
| 削除失敗 | 同じコマンドで回収を再試行 |

- 保持するsnapshot：containerdのファイルシステム。VMメモリのsnapshotではない。
- `start`：元のメインプログラムを再実行。起動hookも再実行し、tmpfsは空になる。
- 一度終了した前面ワーカー：`--keep` なしなら既に削除済みのため再起動不可。
- [操作時の出力例と完了確認](getting-started.md#6-停止再起動削除する)。`Stopping...` / `Removing...` の表示だけで成功とは判断しない。

## 並列で実行する

```bash
oaiso run --agent codex --parallel 2 --worktree \
  --task "テストを追加して" --approval-mode full-auto --detach
```

| 項目 | 動作・設定 |
|---|---|
| 分離 | ワーカーごとに別VM・別worktree |
| ノードの既定上限 | 同時2台 |
| 残存資源の確認 | `vm ps -a` |
| 上限の変更 | 管理者が `node serve --max-workers` で設定 |
| CPU・メモリの要件 | [必要なホスト](kata.md#必要なホスト) |

## CLI・プロキシを更新する

| 導入方法・変更内容 | 手順 |
|---|---|
| 配布インストーラーで導入 | [setupの更新・復旧](setup.md#更新する)。CLI・node・補助プログラムを同じ版へ更新 |
| ソースから手動配置 | [Kataの固定資産・バイナリ更新](kata.md#固定資産の更新)後、下のイメージ更新を実施 |
| 認証方式・通信設定だけ変更 | CLI・node・イメージの更新は不要。新設定でProxy・ワーカーを再作成 |

### イメージを更新して再作成する

1. `oaiso ps -a` で対象を確認し、ログと成果を保存。
2. 共有Proxyを使う全ワーカーを旧版CLIで停止・削除。
3. Proxyも旧版CLIで停止・削除。
4. CLI・nodeの更新が必要な場合は、上表の手順で配置。
5. 通常ユーザーで、既定のワーカーイメージを再ビルドし、Proxyを起動。
6. `runtime-check` と新規ワーカーの動作を確認。

**手順3：Proxyを停止・削除**

```bash
oaiso proxy stop
oaiso proxy rm
```

**手順5〜6：リリース版の既定イメージを更新**

```bash
oaiso build --template plain --tag codex-dock:latest
oaiso proxy run
oaiso runtime-check
```

- リリース版のProxyイメージは、新しい静的CLIから必要に応じて作成します。ソース取得・Goは不要。
- ソースから明示的にProxyをビルドする場合は、製品のソースルートで `oaiso proxy build` を実行してから `proxy run`。[ビルドの詳細](commands/proxy.md#ビルドする)。
- `init` だけでは、既存のワーカーイメージが必ず更新されるとは限りません。

| 条件・操作 | 注意点 |
|---|---|
| 保存済みワーカー | Proxy IPが変わると再起動できないため、新規作成 |
| `proxy stop` / `proxy rm` の名前省略 | 既定の2つのProxyが対象 |
| 独自Proxy名 | それぞれ `--name NAME` で指定 |
| `proxy build` のみ実行 | 既存コンテナは更新されない |
| 独自ワーカーイメージ・テンプレート | 既定イメージに加え、実際に使うDockerfile・entrypointも更新 |
| 利用者ディレクトリのテンプレート | 同名の組み込み版より優先し、自動上書きしない。更新元とentrypointを別途確認 |
| 旧Proxy | `worker_token_auth: true` 非対応なら新CLIは拒否 |
| 新規ワーカーの起動 | 新しいイメージからProxyを再作成した後 |

### 更新対象を確認する

| 対象 | 必要な作業 |
|---|---|
| [Go API](go-api.md)の組み込み製品 | 受付を停止 → Goパッケージも同じソース版へ更新 → 再接続確認 |
| nodeの版が非対応 | `Open` が `ErrVersionMismatch` を返す |
| shim・kernel・guest imageなど | 別途[Kataの固定資産更新](kata.md#固定資産の更新)を実施 |
| `oaiso update` | Goのインストール先のCLIのみ更新。サービス・イメージは対象外 |

## ワーカー内のCodex CLIを更新する

### 版固定と互換性

| 項目 | 扱い |
|---|---|
| ホストの `codex` | 現在は主に認証ファイル作成に使用。ワーカーCLIと直接通信しない |
| ホスト／ワーカーの版番号 | 常に一致させる必要はない |
| 互換性の確認点 | ホスト認証ファイル形式・ワーカー設定キー・Proxy転送経路 |
| 組み込みイメージのCLI | 固定版。ゲスト起動時の更新確認も無効 |
| 実行中のCLI | npmで書き換えない |

### 更新手順

1. 別タグで候補イメージを作成：`CODEX_DOCK_CODEX_VERSION=<版> oaiso build --template plain --tag codex-dock:codex-<版>`。
2. 基本要件を検査：`oaiso image check codex-dock:codex-<版> --agent codex`。
3. 検証ホストで起動：`oaiso run --image codex-dock:codex-<版> --agent codex`。
4. APIキー・ChatGPT認証で、実際の対話・SSE・OAuth更新を確認。
5. 成功した場合だけ、新規ワーカーの `default_image` を候補タグへ変更。

- `image check` だけでは、認証の互換性を確認できない。
- 既存ワーカー：古いイメージのまま完了させる。
- 失敗時：以前のタグへ戻す。

### 自動追従の範囲

| 項目 | 現状・方針 |
|---|---|
| 新版検出・自動昇格 | 未実装 |
| 上流契約の変更検知 | [CIで実装済み](upstream-compatibility.md)。差分を報告し、CLI版を自動昇格しない |
| 将来の流れ | npm新版検出 → 候補タグ作成 → 実環境ゲート → 新規ワーカー向け既定タグ変更 |
| Proxyの基本中継 | `/v1/*`・`/chatgpt/*` のパス・クエリ・本文を転送 |
| 認証の処理 | 上流認証ヘッダーの置換・OAuth更新のrefresh token秘匿 |
| CLIのベースURL・認証方式変更 | 設定と接続試験の更新が必要 |

## 後片付けする

**ワーカーを削除した後に、共有Proxyとegressネットワークを停止します。**

```bash
oaiso proxy stop
oaiso proxy rm
oaiso network rm
```

- 孤立予約の確認・回収：[VM管理](vm.md#孤立した資源の回収)。
- `vm prune`：空の孤立予約が対象。
- 停止済みワーカーは削除しない。
