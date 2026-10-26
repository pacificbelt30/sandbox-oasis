# ワーカー管理コマンド — ps / start / exec / wait / stop / rm / logs

> **日本語** | [English](../en/commands/worker.md)
>
> [← コマンドリファレンス一覧](../commands.md)

---

## `oaiso ps` — ワーカー一覧

実行中のコンテナを一覧表示します。

```bash
oaiso ps [OPTIONS]
```

| オプション | 省略形 | デフォルト | 説明 |
|---|---|---|---|
| `--all` | `-a` | `false` | 停止済みのコンテナも含めて表示 |
| `--filter` | `-f` | | 文字列または `field:value` で絞り込み |
| `--sort` | | `name` | `name` / `status` / `newest` / `oldest` / `branch` / `policy` |
| `--format` | | `table` | `table` / `json` / `csv` |
| `--wide` | `-w` | `false` | 全列を省略せず表示 |
| `--columns` | | | 表示列をカンマ区切りで選択 |
| `--summary` | | `false` | 状態・イメージ・ポリシー別の集計を表示 |
| `--save-snapshot` | | | 現在の状態を0600のJSONへ保存 |
| `--compare-snapshot` | | | 保存状態との差分だけを表示 |

**`oaiso ps -a` の出力例（名前・経過時間・タスク・イメージは説明用）：**

```text
NAME               STATUS      UPTIME  BRANCH  TASK            IMAGE              POLICY
add-tests          ✅ running  5m      -       Add unit tests  codex-dock:latest  approve-for-me
codex-calm-beacon  ⏹ exited   2m      main    (interactive)   codex-dock:latest  interactive
```

- `✅ running`：実行中。タスク成功の意味ではない。
- `⏹ exited`：終了済み。`ps -a` で表示し、ログと終了コードを確認。
- `BRANCH` の `-`：worktree用のブランチ情報なし。
- `UPTIME`：現在の実装は作成からの経過時間。終了済みでも増え、再起動でリセットしない。
- ワーカーがない場合：列見出しだけを表示。

```bash
oaiso ps -a --filter 'status:running !policy:restricted' --sort newest --wide
oaiso ps -a --format json --pretty
oaiso ps -a --save-snapshot ~/.local/state/oaiso/instances.json
oaiso ps -a --compare-snapshot ~/.local/state/oaiso/instances.json
```

## `oaiso connect` — 対話shellで接続

```bash
oaiso connect worker-name
oaiso connect --start --shell zsh worker-name
```

- メインAgentへattachせず、新しいPTY execを作ります。
- `exit`するとホストへ戻り、Agentとワーカーは継続します。
- 停止済みワーカーは既定では拒否し、`--start` のときだけ先に起動します。
- `--workdir` と反復可能な `--env KEY=value` を指定できます。

---

## `oaiso start` — ワーカーの再起動

```bash
oaiso start NAME_OR_ID
```

- 停止済みワーカーを同じID・workspaceで再起動します。
- Kataの起動検証をやり直し、保存済みplaceholderを復元してから処理を解放します。
- プロキシIPが変わった場合は再起動を拒否するため、新しいワーカーを作成してください。
- Kata VM の台帳を指定して復旧する場合は、従来の `oaiso vm start` も使えます。

## `oaiso exec` — 既存ワーカーでの実行

```bash
oaiso exec --timeout 30s worker-name -- npm test
oaiso exec --json worker-name -- sh -c 'echo ok; echo warning >&2'
oaiso exec -i worker-name -- cat < input.txt
```

**2つ目のJSON実行例の出力**

```json
{"exit_code":0,"stdout":"ok\n","stderr":"warning\n"}
```

- `stderr` に文字列があっても、この例の終了コードは `0`。成功・失敗は `exit_code` で判断。

- 標準出力と標準エラーは別々に転送され、`oaiso exec` はゲストの終了コードをシェルへ返します。
- タイムアウトは124、Ctrl+Cによる取消は130です。
- `--json` は `exit_code`、`stdout`、`stderr`、失敗時の `error` を返します。
- メモリ使用を抑えるため、JSON出力は各ストリーム8 MiBまでです。

## `oaiso wait` — 終了待ち

```bash
exit_code=$(oaiso wait --timeout 5m worker-name)
oaiso wait --json worker-name
```

**JSON形式で、正常終了したワーカーを待った場合**

```json
{"exit_code":0}
```

- ワーカーの終了コードを表示します。
- `exit_code: 0`：ワーカーのメインプログラムが正常終了。
- `wait` 自体は、正常に待機できれば終了コード `0`。ワーカーの成功・失敗は表示された値で判断。
- 待機タイムアウトは124、Ctrl+Cは130です。

## `oaiso stop` — コンテナの停止

実行中のコンテナを停止します。

```bash
oaiso stop [NAME|ID...] [OPTIONS]
```

| オプション | 省略形 | デフォルト | 説明 |
|---|---|---|---|
| `--all` | `-a` | `false` | 実行中の全コンテナを停止 |
| `--timeout` | `-t` | `10` | 強制停止までの待機時間（秒） |

**使用例：**

```bash
# 特定のコンテナを停止
oaiso stop codex-brave-atlas

# 複数のコンテナを停止
oaiso stop codex-brave-atlas codex-calm-beacon

# 全コンテナを停止
oaiso stop --all
```

> コンテナ停止時に Auth Proxy の短命トークンも自動的に失効します。
> 詳細は [トークンライフサイクル](../auth-proxy/tokens.md) を参照してください。

---

## `oaiso rm` — コンテナの削除

停止済みのコンテナを削除します。

```bash
oaiso rm [NAME|ID...] [OPTIONS]
```

| オプション | 省略形 | デフォルト | 説明 |
|---|---|---|---|
| `--force` | `-f` | `false` | 実行中のコンテナも強制削除 |

**使用例：**

```bash
# 停止済みコンテナを削除
oaiso rm codex-brave-atlas

# 実行中のコンテナを強制削除
oaiso rm --force codex-brave-atlas
```

---

## `oaiso logs` — ログの表示

コンテナのログを表示します。

```bash
oaiso logs NAME|ID [OPTIONS]
```

| オプション | 省略形 | デフォルト | 説明 |
|---|---|---|---|
| `--tail` | `-n` | `100` | 末尾から表示する行数 |
| `--follow` | `-f` | `false` | ログをリアルタイムで追跡する |

**使用例：**

```bash
# 直近 100 行を表示
oaiso logs codex-brave-atlas

# リアルタイムでログを追跡
oaiso logs codex-brave-atlas --follow

# 直近 50 行を表示
oaiso logs codex-brave-atlas --tail 50
```

---

## 関連ドキュメント

- [TUI ダッシュボード](ui.md) — コンテナの一元管理
- [`oaiso run`](run.md) — コンテナの起動
- [クイックスタート](../getting-started.md) — 後片付けの手順

VMの実プロセス・cgroupと台帳の照合は[VM管理](../vm.md)を参照してください。
