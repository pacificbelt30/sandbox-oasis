# Oaiso v0.0.8

公開準備中（未公開）。現在の公開版はv0.0.7です。

## 主な変更

Kata VMを開発・テスト用のサンドボックスとして使う操作を増やし、ファイルの取り出し、長時間ジョブ、復元点と状態の確認をTUIから行えるようにしました。

| 分野 | 変更 |
|---|---|
| サンドボックス | AI認証なしのshell VM、共有・独立Gitコピー・容量付き管理workspace、レビュー付きの作成、保存した環境の再利用 |
| ファイルとプレビュー | workspaceの一覧・テキスト表示・新規ファイルのアップロード／ダウンロード、認証付きWebプレビュー、読取取消と再試行 |
| 作業の保存 | 停止VMのrootfsと管理workspaceを組にした復元点、新規VMへの復元、参照中のcheckpoint・workspaceの削除拒否 |
| ジョブと成果物 | `J`で開始レビュー・ログ追尾・再接続・対象ジョブの停止。`W`で現在HEAD／起動時点の比較、ファイル別表示と選択、レビュー済み内容だけを新規tarへ出力 |
| タスク | 独立した作業場所でのタスク実行・テスト・レビューと、選んだ変更の新規結果ブランチへの取り込み |
| 操作と診断 | CLIとTUIの補完、検索・狭い画面・Esc・取消後の復帰を改善。`L`でCPU・メモリ・quota・停止要求と取得不能を区別 |
| 安定性 | 失敗した転送の未完成ファイルを残さない。失敗した画面更新では現在の一覧・選択を保持。終了コード取得失敗や未起動を成功として表示しない |

- タスクのGit参照は通常の`.git`ディレクトリか相互登録されたworktreeに限定します。別リポジトリへの手書きポインタ、保存後のメタデータ置換、登録の逆参照変更を拒否します。該当する既存タスクは元の登録を確認してから作り直してください。

- CLIの通常のエージェント選択と承認設定は維持します。認証なしのCLI起動は`--agent shell --no-ai`で指定します。
- lowercase `j`は一覧移動を維持し、ジョブ管理はuppercase `J`です。TUIを閉じても実行中ジョブは継続します。
- 起動時点の比較は、新しいVMでユーザーコード開始前に取得したGit commitがある場合に使えます。旧VM・非Git・管理workspaceには後から現在HEADを起動時点として補いません。
- CPU・メモリの観測値はVMM等を含むホストcgroupの値です。終了137だけでOOMとは判定しません。
- 固定Kata 4.3.0、containerd 2.4.2、BuildKit 0.34.0、runc 1.4.3、Codex CLI 0.163.0、Claude Code 2.1.306はv0.0.7から維持します。

## v0.0.7から更新する

1. アプリの保存を完了し、workspace・必要なHOME内データ・ホスト設定を保管します。停止したVMのrootfs checkpointと専用DBは別々に保存し、バックアップを確認してから先に全VMを削除し、その後Proxyを停止・削除します。AI認証付きVMの削除では、失効確認のためProxyを稼働させておきます。停止済みコンテナが残る更新も拒否します。
2. 同じ版のCLI・node・guard・loggerを一組で更新し、通常ユーザーで`oaiso runtime-check`を確認します。専用XFSとproject quotaは引き続き必要です。
3. 認証Proxyのimageも同じ版へ再ビルドし、独自imageはソースとdigestを確認して認可します。
4. 互換性のある保存済みrootfsと専用DBを新規VMへ復元し、workspaceとともに内容を確認します。元のVM IDは引き継ぎません。新機能は対応するCLI・nodeがそろってから使います。

- 旧版へ戻す前にも必要なデータを保管して、全VM・Proxyを停止・削除します。`setup --rollback --apply`は明示した復旧操作です。到達済みの公開版の下限は下げません。
- 新しい管理workspace・復元点・タスクのデータを保管し、必要な成果物を先に取り出してください。v0.0.7で新機能の操作が使えるとは保証しません。
- v0.0.6以前のext4環境には専用XFSへの移行が必要です。setupは既存ディスクをformat・変換しません。

## 検証範囲

開発候補`preflight-42dd882`でGo全件race・build・vet・lint・8系統fuzz、strict文書、実Kataの18スイート（失敗・skip 0）を確認しました。公開v0.0.7からの保存・削除・更新・新規VM復元、故障時の自動復旧、v0.0.7へのrollbackと元バックアップの再復元、資源回収も成功しています。v0.0.8の配布候補・新規導入・巻戻し防止を含む更新／復旧は最終確認中です。

外部AIの実アカウント認証・推論、実WSL、物理ホストの再起動、すべての端末・ブラウザーでの目視は対象外です。

## 配布物

公開時にarchive・install.sh・SHA256SUMS・sbom.cdx.json・provenance.jsonの5ファイルを配布します。archiveは静的Linux amd64のCLI・補助プログラム・manifest・ライセンス／noticeを含み、Kata等のruntime資産・作業image・認証情報は含みません。

ローカル候補にはGitHubの署名は付きません。公開物はタグのクリーンなソースからworkflowが生成し、署名・作成元・checksumを検証します。

---

## English

Release preparation, not published. The current public release is v0.0.7.

Oaiso v0.0.8 expands Kata VM development and test workflows through the TUI: credential-free shell sandboxes, reviewed creation, independent Git copies, bounded managed workspaces, reusable environments, files and authenticated web previews, and stopped-VM recovery points.

Uppercase `J` reviews and launches background jobs, follows bounded logs, reconnects and stops only the selected job. Lowercase `j` keeps list navigation. Closing the TUI leaves jobs running. `W` compares current HEAD or the immutable pre-start Git reference, reviews individual files and exports selected content with stale-plan and no-clobber checks. Legacy/non-Git VMs never invent a launch reference. `L` reports host cgroup CPU/memory, quota and stop evidence, distinguishing unavailable observations and never treating exit 137 alone as proof of OOM.

Task records support independent execution, test/review and selected-change adoption into a new result branch. CLI/TUI completion, read cancellation, retry, selection retention after failures and narrow-screen interactions improve everyday operation. Normal CLI agent and approval defaults remain; credential-free CLI runs explicitly use `--agent shell --no-ai`.

Task Git metadata requires an ordinary `.git` directory or a reciprocally registered worktree. Unregistered pointers to another repository, metadata replacement after creation and changed backlinks are rejected. Check the source registration and create a new task when an existing record no longer matches.

For v0.0.7 upgrades, back up workspace, required HOME data and host configuration; save stopped-VM rootfs checkpoints and private database backups separately. Verify backups, remove all VMs including stopped ones, then stop/remove proxies. AI-authenticated workers require a live proxy for token revocation during deletion. Install matching CLI/node/guard/logger and verify runtime readiness. Rebuild and authorize a matching authentication proxy image. Dedicated quota-enabled XFS remains required. Restore compatible rootfs and private database backups into new VMs and check files; original VM IDs are not retained. Before rollback, preserve data and remove all containers, including stopped ones, and retain new workspace/recovery/task data; v0.0.7 does not provide those new controls. Rollback preserves the release high-water mark. v0.0.6 ext4 installations still require storage migration; setup never formats or converts existing data.

Runtime and agent pins are unchanged from v0.0.7. Validation details are recorded above and in the testing guide; live external AI authentication/inference, real WSL, physical host reboot and exhaustive visual checks are excluded. Local candidates are unsigned. Published artifacts are built from the clean tag, with archive, installer, checksums, SBOM and provenance; runtime downloads, worker images and credentials are separate.
