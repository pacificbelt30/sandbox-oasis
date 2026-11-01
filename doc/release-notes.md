# Oaiso v0.0.7

## 主な変更

隔離・認証の境界を強化し、既存環境からの保管・移行と、Codex DBの復元を追加しました。

| 分野 | 変更 |
|---|---|
| Kata | 固定版を4.3.0へ更新。Dragonballの既知の脆弱性に対応し、固定資産のhashを更新 |
| ワーカーの容量 | 書き込み可能rootfsへ4 GiB・65,536 inodeのXFS project quota。OSと別の専用XFSが必須。起動時の空き・予約容量も検査 |
| 認証Proxy | 製品用image tagの予約、root管理のdigest認可、管理シークレットの所有者限定ファイル経由の受渡し |
| ホストとの共有 | 外部Git metadataはホスト側のworktree登録と相互確認。ファイル操作の出力を制限し、Pythonを隔離モードで実行 |
| 保管・移行 | `checkpoint export/import`で検証済みOCIを持ち出し。既存ファイル・image tagを上書きしない。v0.0.6の書き出し手順を追加 |
| Codex DB | 専用ext4へSQLiteを保持。停止・再起動に対応。`agent-state export/restore`で未起動の新規VMへ復元。`run --create-only`を追加 |
| 診断・TUI | `runtime-check`へquotaと空き容量、TUIへrootfsのbyte／inode使用量を追加。エージェントポリシーの編集画面と段階別の起動エラー案内を追加 |

- Codex CLI 0.163.0／Claude Code 2.1.306、containerd 2.4.2、BuildKit 0.34.0、runc 1.4.3の固定版は維持しています。
- 既存の接続・Escなどの特殊キー・ANSI／Unicodeの入出力を維持します。
- 認証プロファイルの暗号化形式とLocal API v1に破壊的変更はありません。実APIキー・OAuthは引き続き認証Proxyで保持します。
- バックアップは暗号化しません。rootfs、workspace、HOME内のデータ、専用DBの対象範囲を確認し、共有workspaceの外へ所有者限定で保管してください。

## v0.0.6から更新する

**既存ext4へそのまま適用できません。** [専用ストレージへの移行](https://pacificbelt30.github.io/sandbox-oasis/guides/storage-migration/)で保存対象と取り出し方を確認してください。

1. アプリの保存を完了し、旧CLIでworkerを停止。rootfs checkpoint・workspace・必要な利用者データとホスト設定を保管します。v0.0.6には専用DBディスクがありません。
2. 管理者が別の保存先へ専用XFSを準備し、project quotaのaccountingとenforcementを有効化。2ワーカーは32 GiB以上が目安です。setupは既存ディスクをformat・変換しません。
3. 対応するCLI・node・guard・loggerを配置し、通常ユーザーで`oaiso runtime-check`を確認します。
4. 新しい環境で認証・Proxyを準備。既定imageは再ビルドし、独自Proxyはソースとdigestを確認してから認可します。
5. checkpointを取り込み、復元したworkspaceを指定して新しいworkerを作成。成果物・DB・書き込み・認証と通信規則を確認してから旧環境を回収します。

新版同士の専用DB復元には[状態保存ガイド](https://pacificbelt30.github.io/sandbox-oasis/guides/agent-state/)を使います。既存VMへDBを上書きせず、容量とUID/GIDが一致する未起動・未復元のVMだけを受け入れます。

## インストールと配布物

[前提条件と署名検証](https://pacificbelt30.github.io/sandbox-oasis/installation/)を準備した通常ユーザーが、適用前の計画を表示します。

```bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/download/v0.0.7/install.sh | bash
```

- 対象：Ubuntu 26.04 x86_64、systemd、KVM、cgroup v2、専用XFS。Go・Make・ソース取得は不要です。
- パイプ実行はHTTPS配布元を信頼する操作です。[実行前にinstallerの署名を検証する手順](https://pacificbelt30.github.io/sandbox-oasis/guides/signed-releases/)も使えます。
- 5ファイル：`oaiso-linux-amd64.tar.gz`・`install.sh`・`SHA256SUMS`・`sbom.cdx.json`・`provenance.json`。Kata等の固定配布物・作業image・認証情報はarchiveに含みません。
- 署名・作成元はビルド経路を確認するもので、脆弱性の不存在を保証しません。

## 検証範囲

- Go build・全件raceテスト・vet・lint、既存のセキュリティfuzz、Pythonの配置／配布検査、CLI名検査、文書strictビルド。
- 実Kataでv0.0.6のext4からXFSへの移行、rootfs／SQLite WALの別VM復元、公開Go API、端末キー・ANSI・Unicode、認証・相互TLS・共有・隔離境界を確認。更新・自動復旧・rollbackと資源回収も成功。[検証記録](https://pacificbelt30.github.io/sandbox-oasis/testing/)。
- 外部AIの実認証・推論、実WSL、実ホスト再起動は対象外です。

---

## English

Oaiso v0.0.7 strengthens isolation and credential boundaries and adds portable checkpoints, private Codex database backups and storage diagnostics.

| Area | Change |
|---|---|
| Kata | Pin 4.3.0 and verified hashes to address the reported Dragonball vulnerability |
| Capacity | Enforce 4 GiB / 65,536 inode writable rootfs project quotas; require dedicated XFS separate from the OS and check admission capacity |
| Proxy trust | Reserve product image names, verify root-owned digest approval before credentials, and pass admin secrets through owner-only files |
| Host sharing | Authorize Git metadata through reciprocal host worktree records; bound file-operation output and use isolated Python |
| Backups | Add verified OCI `checkpoint export/import`; never overwrite existing files or image tags. Document export from v0.0.6 |
| Codex state | Keep SQLite on private ext4; add offline `agent-state export/restore` into a matching never-started, never-restored worker and `run --create-only` |
| UX | Add quota/free-space runtime diagnostics, rootfs bytes/inodes in TUI details, a staged policy editor and actionable startup errors |

**Existing ext4 installations require migration before applying this release.** Follow the [storage migration guide](https://pacificbelt30.github.io/sandbox-oasis/en/guides/storage-migration/): stop/save with the old CLI, retain rootfs/workspace/user data and host configuration, prepare separate quota-enabled XFS (32 GiB recommended for two workers), install matching components, then import and verify new workers before retiring the source. Setup never formats or converts existing data. v0.0.6 has no private database disk; supported new versions use the [database restore procedure](https://pacificbelt30.github.io/sandbox-oasis/en/guides/agent-state/).

Codex 0.163.0, Claude Code 2.1.306, containerd 2.4.2, BuildKit 0.34.0 and runc 1.4.3 pins remain unchanged. Existing terminal keys/ANSI/Unicode, profile encryption and Local API v1 behavior are preserved. Backups are unencrypted, exclude workspace/credentials from rootfs, and need private storage outside shared projects.

Ubuntu 26.04 x86_64/systemd/KVM/cgroup v2/dedicated XFS are required. Go/Make/source are unnecessary for installation. Verify [signatures before execution](https://pacificbelt30.github.io/sandbox-oasis/en/guides/signed-releases/). The five release files are archive, installer, checksums, SBOM and provenance; they exclude runtime downloads, worker images and credentials.

Validation covers Go/race/vet/lint/fuzz, Python deployment/distribution tests, CLI names and strict documentation. Real Kata verified v0.0.6 ext4-to-XFS migration, rootfs/private SQLite WAL restoration, public Go APIs, terminal behavior, authentication/mTLS/sharing/isolation, upgrade, automatic recovery, rollback and owned-resource cleanup. Live external AI authentication/inference, real WSL and physical host reboot are excluded.
