# `oaiso completion` — Tab 補完

> **日本語** | [English](../en/commands/completion.md)

- `oaiso` は Bash・Zsh・Fish・PowerShell 向けの補完スクリプトを生成できます。
- [配布インストーラー](../installation.md)の `--apply` でBash・Zsh・Fish用の補完も標準のシステムディレクトリへ配置します。
- ソースから導入する場合は `scripts/install.sh --apply` または `make install` でも配置します。
- 新しいシェルを開くと利用できます。
- Bash では `bash-completion` パッケージが必要です。

- 補完の生成・配置対象は `oaiso` のみです。

今のシェルだけで有効にする場合:

```bash
source <(oaiso completion bash)
```

Zshの場合は、同じホストの通常ユーザーで次を実行します。

```zsh
autoload -Uz compinit
compinit
source <(oaiso completion zsh)
```

Fishの場合:

```fish
oaiso completion fish | source
```

- `go install` などで CLI だけを導入した場合は、利用者ごとの補完ディレクトリにも保存できます。
- Bash の例:

```bash
mkdir -p ~/.local/share/bash-completion/completions
oaiso completion bash > ~/.local/share/bash-completion/completions/oaiso
```

- サブコマンドとフラグに加え、`default` のエージェント、`run` のエージェント・runtime・承認モード・テンプレート・`--image`、`start`・`stop`・`rm`・`logs` のワーカー名を補完します。
- `vm inspect/start/stop/rm` では専用ノードの VM 台帳から、コマンドに応じた候補を表示します。
- 通常は VM 名、名前が重複する場合や孤立 VM では ID を使います。
- ワーカー名の取得は最大 500 ms、VM 台帳の取得は最大 2 秒で打ち切ります。
- ノードが停止中でも、ほかの補完は使えます。

## サンドボックス操作の補完

利用可能：**未リリース（main）**。

| 入力対象 | 候補 |
|---|---|
| `connect` | 実行中のVM名。`--start`指定時は停止VMも対象 |
| `exec` | 第1引数は実行中のVM名、第2引数はVM内の実行可能コマンド名 |
| `files get` | VM名、workspace内の通常ファイル・フォルダー |
| `files list` / `files put` | VM名、workspace内のフォルダー |
| `artifact plan` / `artifact bundle` | VM名、`--include`の通常ファイル・フォルダー。秘密用パスとリンクは候補から除外 |

- ゲストパス・コマンド名の取得は最大5秒です。取得できない場合、ホストのファイル名へ切り替えず候補を返しません。静的なサブコマンド・フラグ補完は引き続き使えます。
- ゲストパスの補完後は自動で空白を追加しません。フォルダーを続けてたどり、引数を確定するときは空白を入力してください。
- コマンド名はVM内のPATHから取得します。候補のプログラムやshell profile、補完スクリプトは実行しません。

## TUIのコマンド入力

利用可能：**未リリース（main）**。一覧で実行中のVMを選び、`X`を押します。最初のコマンド名を入力すると候補が表示され、`↑↓`で選び、`Tab`または`Enter`で補完します。実行には`Run`を選びます。`Esc`は候補の表示中も入力画面を取り消します。

基本のshellコマンド候補はすぐに使え、入力を止めずにVMの実行可能コマンドを最大10秒で追加取得します。取得に失敗した場合は基本候補と手入力を使えます。引数や複雑なshell式の補完は対象外です。
貼り付けたコマンド名も、`Tab`で候補を再評価して補完できます。
