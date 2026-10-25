# 認証情報を暗号化して保存する

**日本語** | [English](../en/guides/encrypted-credentials.md)

- **v0.0.3以降で利用できます。**
- 新規`login`は[キーリング方式](keyring.md)が既定。既存プロファイルと低水準の`auth profile set/import`は従来の方式を維持します。
- APIキー・サブスクの両方が対象です。認証Proxyを維持し、作業VMへ実資格情報を渡しません。

## 方式を選ぶ

| 方式 | 選ぶ場面 | 必要なもの |
|---|---|---|
| パスフレーズ | WSL・Linux VM・SSH・GUIなし | 12バイト以上の長いパスフレーズ |
| キーリング（新規loginの既定） | デスクトップ・SSH・WSL・VM | Secret Service、利用者D-Bus、`secret-tool`。[準備とGUIなしの使い方](keyring.md) |

- ageをバイナリへ組み込むため、追加のageコマンド・サービス・DB・Vault・コンテナは不要です。ローカルで保存・解錠できます。
- 既存のデスクトップサービス、または明示的な専用セッションを使い、利用不能ならエラーにします。OSキーリング側の保存時の保護は、その設定に依存します。
- OSキーリングのロック解除は、既存サービスの画面が開く場合があります。応答できない場合は15秒で失敗します。
- `secret-tool`：[公式マニュアル](https://manpages.debian.org/trixie/libsecret-tools/secret-tool.1.en.html)。Ubuntuでは`libsecret-tools`に含まれます。

## 暗号化してログインする

```bash
oaiso login --agent codex --method subscription --profile work --encrypt
```

- パスフレーズを非表示で2回入力。
- APIキーなら`--method api-key`、Claude Codeなら`--agent claude`を選択。
- サブスク認証は一時Kataで実施し、回収した認証情報を直接暗号化して保存。
- 既存プロファイルは上書きしません。別名を指定してください。

OSキーリングを使う場合は`--encrypt`の代わりに`--keyring`を指定します。

```bash
oaiso login --agent claude --method api-key --profile claude-work --keyring
```

## 起動・確認・ロック

```bash
oaiso init --agent codex
oaiso status
oaiso auth profile show work
oaiso auth profile lock work
```

| 操作 | 動作 |
|---|---|
| Proxyの初回起動・再起動 | ホストで解錠し、既存Proxyへ鍵を標準入力で渡す |
| Proxy稼働中の追加タスク | 同じ暗号化ヘッダーと接続世代を確認して再利用。再入力不要 |
| `status`・`profile show/list` | 復号せず、保存方式と未確認の状態を表示 |
| `status --check-connection` | 明示的に解錠してAPIキーをモデル一覧APIで確認。推論は行わない |
| `profile lock` | 対象ワーカーと認証Proxyを停止。成果物と暗号文は保持 |

- `show`の`locked`は、そのコマンドが復号していないという意味です。既存Proxyの稼働は`status`のProxy欄で別に確認します。
- ロックで実行中の作業は止まります。先に成果を保存してください。
- 通常のProxy停止・ホスト再起動でも、プロセスメモリの鍵は失われます。
- OSキーリングのロックだけでは、起動済みProxyの鍵は消えません。`profile lock`で別に停止します。このコマンドはOSキーリング自体をロックしません。
- 暗号化に未対応の古いProxyイメージは更新が必要です。実行中ワーカーを排出してから再作成します。

## 既存の平文を移行する

1. 成果を保存して対象ワーカーを停止。
2. 認証Proxyを停止。
3. 明示的に移行。

```bash
oaiso auth profile encrypt work
```

- 暗号化保存・再読込の検証後に平文の`work.json`を削除。
- 接続世代を更新するため、旧ワーカーの再認可は拒否します。新しく起動してください。
- 移行途中で両ファイルが残ると、通常利用を拒否します。同じコマンドとパスフレーズで内容を照合して移行を完了。
- 内容が異なる場合は自動削除しません。
- 過去のbackupや削除済みディスクブロックの安全な消去までは保証しません。

## 旧暗号化形式からageへ移行する

```bash
oaiso auth profile show work
oaiso auth profile lock work
oaiso auth profile migrate-age work
oaiso init --agent codex
```

- `show`の`Format: legacy-v1`が対象。新しい形式は`Format: age`。
- 作業を保存してからlock。旧Proxyを停止・削除し、旧パスフレーズまたはキーリングで移行します。
- age保存・再読込の検証後に切り替え、接続世代を更新。旧ワーカーは新しく作成してください。
- `work.vault.v1-backup`を0600の暗号文で残します。通常利用で自動的に読み込みません。
- 中断時は同じコマンドで再試行できます。異なる内容のbackupを上書きしません。
- 移行済みの再実行は内容を検証し、世代を再度更新しません。
- キーリング方式では、backupの復旧用に旧キーリング項目も残します。確認後に管理者が整理してください。
- 旧形式の新規Proxy起動と更新保存は拒否します。平文やホスト認証へ切り替えません。
- ホストの`setup --rollback`はプロファイル形式を戻しません。旧版への復旧では再認証を基本とし、旧ワーカーを再開しないでください。

## パスフレーズを変更する

```bash
oaiso auth profile lock work
oaiso auth profile passwd work
```

- 旧パスフレーズで解錠後、新しいパスフレーズを2回入力。
- 対象の停止済みProxyを削除してから、同じage秘密identityと認証鍵を新しいパスフレーズで保護し直します。
- 起動中ワーカー・Proxyがあれば変更を拒否。OSキーリング方式のパスフレーズ変更には使えません。
- 新しいパスフレーズで`oaiso init`を実行してProxyを起動します。

## 非対話で解錠する

```bash
oaiso --passphrase-fd 3 init --agent codex 3< /private/runtime/passphrase-input
```

- 例は専用FDの接続方法です。入力元は利用者が管理する秘密入力・一時pipe等を使い、パスフレーズをプロジェクトや暗号文の隣へ保存しないでください。
- FDは3以上、最大4096バイト。末尾の改行1個を除去。APIキーのstdinとは別にします。
- 秘密を引数・環境変数に入れる入力方法は提供しません。
- `passwd`の非対話変更は、旧入力に`--passphrase-fd`、新入力に`--new-passphrase-fd`を別々に指定。

## 保存形式と保護の範囲

| 項目 | 仕様 |
|---|---|
| 保存先 | `~/.config/codex-dock/profiles/NAME/NAME.vault`、0700ディレクトリ・0600ファイル |
| ファイル構成 | age形式の暗号文・保護済み鍵ファイルを含むJSON manifest。`.vault`全体をageコマンドへ直接は渡せない |
| 暗号 | age v1.3.2のX25519 recipient。公開鍵だけで差し替えられないようHMAC-SHA256でも検証 |
| 鍵束の保護 | ageのscrypt、`logN=17`（約128 MiB）。起動時の解錠だけで実行 |
| OSキーリング | age秘密identityと認証鍵の鍵束を、既存Secret Serviceへ保存 |
| 秘密の所在 | ホストの解錠中プロセスと認証Proxyのメモリ。作業VMには仮トークンのみ |
| 主な保護対象 | 停止中の保存ファイル・ディスク・backupの読取り |
| 保護できないもの | ホストroot・同じUIDのプロセス読取り、稼働メモリ・swap・外側VM snapshot |

- WSLではLinux側の私有領域を使用。DrvFS・NFS・FUSE等の未検証filesystemを拒否します。Kata起動には別途KVM条件が必要です。
- パスフレーズ・キーリングの鍵を失うと復号できません。新しいプロファイルで再認証してください。
- 鍵を含むVM snapshotの巻戻しは、ローカルだけで完全には検知できません。
- [形式・保存契約の詳細](../development/local-credential-store.md)、[動作確認と未確認事項](../testing.md)。
