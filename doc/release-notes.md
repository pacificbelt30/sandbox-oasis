# Oaiso v0.0.3

## 主な変更

| 項目 | 内容 |
|---|---|
| 独立ログイン | `oaiso login`でCodex／Claude CodeのAPIキー・サブスク認証を登録。プロファイル選択・管理シークレット設定を自動化 |
| サブスク認証 | 一時Kataで固定版の公式CLIを使用。認証ファイルを通常の作業VMへ持ち込まない |
| ローカル暗号化 | 新規loginはキーリング＋ageが既定。OSキーリングには復号用の鍵を保存し、資格情報は0600の暗号化ファイルで管理 |
| GUIなしの利用 | `oaiso auth keyring session`で専用D-Bus／GNOME Keyringセッションを開始。パスフレーズ方式・平文保存も明示選択可能 |
| 状態と更新 | `oaiso status`、`oaiso upgrade`で準備状態と配布版の更新計画を確認 |
| 起動の修正 | Kata前面TTYの停止・短いコマンドの出力欠落を修正。管理サービス再起動後も同じVMでTTYログ収集を再開 |
| ドキュメント | インストール→キーリング→login→初回起動を日本語・英語で整理 |

- Codex CLI 0.163.0／Claude Code 2.1.306を使用。
- 認証Proxyを維持。通常の作業VMへ実APIキー・OAuth access/refresh/ID tokenを返さず、短命のプレースホルダーを渡します。

## インストール

```bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/download/v0.0.3/install.sh | bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/download/v0.0.3/install.sh | bash -s -- --apply
```

- 対象：Ubuntu 26.04 x86_64、systemd、KVM、cgroup v2、sudoが使える通常ユーザー。
- Go・Make・ソース取得は不要。uv管理Python 3.12と不足するキーリング用パッケージを準備。
- 配置後はログアウト・再ログインし、`oaiso runtime-check`を実行。
- 続きは[キーリング解錠・login・初回起動](https://pacificbelt30.github.io/sandbox-oasis/guides/first-run/)。

## 更新手順

1. 作業を保存し、保持中のワーカーとProxyを停止・削除。
2. 上のv0.0.3インストーラーで計画を確認して適用。
3. 再ログイン後、`oaiso --version`・`oaiso runtime-check`で確認。
4. ワーカーイメージを`oaiso build --template plain --tag codex-dock:latest`で更新し、利用する側の`oaiso init --agent codex`または`--agent claude`を実行。

- 既存の平文プロファイル・ホスト認証連携は維持。自動で暗号化・再登録しません。
- 新規loginはキーリングが使えない場合に停止し、平文へ自動変更しません。
- 暗号化の設定方法は[暗号化保存](https://pacificbelt30.github.io/sandbox-oasis/guides/encrypted-credentials/)を参照してください。

## 配布ファイル

| ファイル | 内容 |
|---|---|
| `oaiso-linux-amd64.tar.gz` | 静的CLI・guard・IO logger・`bundle.json`・ライセンス／依存notice |
| `install.sh` | v0.0.3を選択する単独インストーラー |
| `SHA256SUMS` | 配布アーカイブとインストーラーのSHA-256 |

- Kata等の固定配布物と作業イメージは、導入・初期化時に別途取得・準備。
- 資格情報・プロジェクトは配布物に含みません。

## 検証範囲

- 同じランタイム実装で、空のHOMEからの導入・実キーリング保存・両CLI起動・TTY入出力・workspace操作を確認。
- 実Kata 8スイート、更新・自動復旧・明示rollback、Go race／vet／lint、Python 77試験、ドキュメントstrict検査が成功。
- 実アカウントでのログイン・推論、実WSL、ホスト再起動、長期OAuth更新は未確認。[検証記録](https://pacificbelt30.github.io/sandbox-oasis/testing/)。

## English

- Independent Codex and Claude Code login for API keys and subscriptions, with automatic profile/admin-secret setup.
- New logins default to keyring-backed age encryption. Headless sessions use private D-Bus/GNOME Keyring; existing plaintext profiles remain supported.
- Adds consolidated status/update commands and fixes primary Kata terminal startup, final output draining and log recovery after node restart.
- Prebuilt Linux amd64 distribution; no host Go/Make required. See the [first-run guide](https://pacificbelt30.github.io/sandbox-oasis/en/guides/first-run/).
- Tests use synthetic credentials; live account authentication/inference and real WSL remain unverified.
