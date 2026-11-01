# タスクを実行し、結果を選んで採用する

**日本語** | [English](../en/guides/tasks.md)

利用可能：**未リリース（main）**。

依頼をキューへ保存し、専用worktreeのKata VMで実行します。結果は停止後に確認し、ファイル／hunkを選んで新しいブランチへ採用できます。

## 前提と保存先

- [初回起動](first-run.md)を完了した通常ユーザーで実行します。Gitリポジトリには少なくとも1つcommitが必要です。
- 台帳・依頼・レビューはホストの`~/.config/codex-dock/tasks/`へ所有者限定で保存します。`task --store DIR`で共有プロジェクト外の専用ディレクトリを指定できます。
- 各試行は作成時のcommitから新しいdetached worktreeを作成します。プロジェクトの未コミット変更は取り込みません。再試行も同じ開始commitを使います。
- worktreeのGitメタデータは既存の[worktree共有](../workspace.md)と同じです。別リポジトリのコピーやGitメタデータの隔離にはなりません。
- 認証・承認・イメージ・ネットワークの既定値は通常の`run`を利用します。タスク機能が承認を自動で緩めることはありません。非対話実行に必要な許可は[ホストのAgent Policy](agent-policies.md)で選びます。

## TUIで操作する

```bash
oaiso task board
```

`oaiso ui`のインスタンス一覧では`T`で開きます。

| キー | タスク一覧での操作 |
|---|---|
| `N` | タイトル、プロジェクト、Agent、依頼、任意のイメージ・Policy・テストコマンドを入力し、キューへ保存 |
| `R` | 選択した依頼を実行。失敗・取消済みなら新しい試行を開始 |
| `S` | 確認後にAgent VMとテストVMを停止 |
| `C` | 実行中VMへ別shellで接続。`exit`で復帰 |
| `T` | 設定したテストコマンドを、同じworktreeを共有する別のKata VMで実行 |
| `V` | 停止済みの結果を固定して差分レビューを開く |
| `G` | 実際のVM状態と同期 |
| `Tab` | 一覧と詳細のフォーカスを切り替え |
| `Q` / `Esc` | 一覧から戻る。入力画面の`Esc`は保存せず戻る |

- 実行中・状態不明の試行は画面を開いている間、3秒ごとに同期します。キューは`R`で明示的に実行し、TUI終了後の自動実行はありません。開始済みのVMは継続します。
- `review`はAgentプロセスの終了コード0を意味します。成果物の正しさやテスト合格を意味しません。
- nodeに接続できない場合は同期を失敗として表示し、VMが消えたと判断して再実行しません。
- 終了を同期すると、通常の停止処理でトークンとKata予約枠を解放します。VMの保存領域とworktreeは保持し、自動削除しません。
- TUIを閉じている間に終了した試行は、次の`sync`で予約枠を解放します。

## 結果を確認する

1. Agentとテストの終了後に`V`を押します。
2. ファイルまたはhunkへ移動し、`Space`で選択します。`A`はレビュー対象をすべて選択します。`Tab`で差分へ移り、スクロールできます。
3. `P`で採用先の既存ブランチと、新しく作る結果ブランチを指定します。
4. commit・開始commit・ファイル数をプレビューし、`Confirm`で採用します。`Cancel`または`Esc`で中止できます。

採用は新しいブランチとcommitだけを作成します。現在のcheckout・index・未コミット変更を保持し、pushは行いません。レビュー後にworktreeまたは採用先ブランチが変化した場合、再レビューが必要です。適用が競合した場合も停止し、自動マージしません。

- Agentがcommitした変更と未追跡ファイルも開始commitとの差分へ含めます。ignoredファイルは対象外です。
- 新規・削除・バイナリ・mode変更はファイル単位です。symlink・submoduleの変更は拒否します。
- `.env*`、`.ssh`、`.codex`、認証JSON、秘密鍵など既知の資格情報パスを除外します。任意のソース中に書かれた秘密の検出を保証しません。採用前に内容を確認してください。
- テスト結果は実行開始時の結果全体に対応します。開始後に差分が変わった場合は`stale`と表示します。選択した一部のhunkだけの合格を保証しません。
- 1レビューは最大512ファイル・差分32MiBです。台帳は1MiB、最大512タスク・各16試行です。保持したworktreeとVMは容量を使用します。

## CLIで同じ操作を行う

次の例は、現在のGitリポジトリで架空のshellタスクを実行します。ホストには`jq`が必要です。実行に使う認証設定は通常の`run`と同じです。

```bash
TASK_ID=$(oaiso task create --title '結果確認' --project "$PWD" --agent shell \
  --prompt 'printf "sample\n" > result.txt' --test-command 'test -s result.txt' | jq -r .id)
oaiso task run "$TASK_ID"
oaiso task sync "$TASK_ID"
```

`sync`の`state`が`review`になった後にテストを開始します。

```bash
oaiso task test "$TASK_ID"
oaiso task sync "$TASK_ID"
```

`attempts`の最新の`test_state`が`passed`になった後、レビューを保存します。採用先が`main`の例です。結果ブランチ`oaiso/result-example`は未作成である必要があります。

```bash
REVIEW_ID=$(oaiso task review "$TASK_ID" | jq -r .id)
mkdir -m 700 -p "$HOME/oaiso-approval"
oaiso task plan "$TASK_ID" --review "$REVIEW_ID" --target main \
  --branch oaiso/result-example --plan-file "$HOME/oaiso-approval/plan.json"
```

表示されたcommitと差分を確認してから採用します。

```bash
oaiso task accept "$TASK_ID" --plan-file "$HOME/oaiso-approval/plan.json" --yes
git show oaiso/result-example
```

- `plan`は既定で全レビュー対象を選択します。`--selection-file`へ所有者限定JSONを渡すと、`{"file.go":["hunk ID"]}`で一部を選択できます。`[]`はファイル全体です。
- 長い／非公開の依頼には`create --prompt-file FILE`を使用します。UTF-8、最大32KiB、所有者限定ファイルが必要です。
- `run --headless --agent codex/claude --task-file FILE`も利用できます。Codexは`exec --json`、Claudeは`--print`を使います。通常の対話起動は変わりません。
- `task list`・`show ID`は保存した台帳を表示します。最新状態は`sync ID`で取得します。
- 不要なVMは`oaiso rm --force WORKER_NAME`で削除します。台帳の`attempts[].workspace`は手動確認用に保持されます。worktreeを削除する場合は、その試行のVMを削除してから`git worktree remove --force PATH`を実行します。

- 採用ブランチ名は英数字と`/`・`_`・`-`・`.`、180バイト以内です。Gitで不正な名前は拒否します。
- 暗号化プロファイルの`--passphrase-fd`は子CLIへFDで渡します。起動ごとに新しい入力が必要なため、TUIで繰り返し起動する場合はキーリングプロファイルを利用してください。
