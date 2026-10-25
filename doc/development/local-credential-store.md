# ローカル暗号化ストアの実装仕様

**日本語** | [English](../en/development/local-credential-store.md)

- v0.0.3以降の仕様です。
- 操作：[暗号化して保存する](../guides/encrypted-credentials.md)。
- 保存API：[Credential Store契約](../credential-store.md)。

## 基本方針

| 項目 | 仕様 |
|---|---|
| 既定 | 新規loginはキーリング。既存profile・低水準set/importは維持。`login --encrypt`／`--plaintext`で明示選択 |
| 保存先 | 制御ホストの製品専用profile。workspace・clone・追加mountと共有しない |
| 対象 | APIキー・OAuth access／refresh／ID token・資格情報版・接続世代 |
| 暗号化の実装 | `filippo.io/age` v1.3.2をGoバイナリへ組込み |
| 外部依存 | パスフレーズ方式はネットワーク・外部ageコマンド・追加daemon・DB不要 |
| Proxy管理シークレット | 暗号化ストアとは別の0600ファイル。作業VMへ渡さない |
| Go | ソースの最低版1.26.0、開発・CI toolchain 1.27.3。配布バイナリ利用者にGoは不要 |

- [age公式リポジトリ](https://github.com/FiloSottile/age/tree/v1.3.2)・[形式仕様](https://age-encryption.org/v1)・[Go API](https://pkg.go.dev/filippo.io/age@v1.3.2)。

## ファイル構成と暗号

| 要素 | 仕様 |
|---|---|
| 保存ファイル | `NAME/NAME.vault`。1ファイルのJSON manifestとして原子的に更新 |
| manifestの版 | Oaisoのschema 2。age標準形式の版とは別 |
| 公開メタデータ | profile名、解除backend、鍵ID、age公開recipient、保護済み鍵ファイル。実秘密は含めない |
| 資格情報の暗号文 | 標準age形式のバイナリをmanifestへbase64格納。X25519 recipientへ暗号化 |
| 秘密の鍵束 | 製品専用のage秘密identityと、独立した32バイト乱数の認証鍵 |
| 保存元の認証 | HMAC-SHA256でmanifestと資格情報の暗号文を検証 |
| 完全な読取り | age streamをEOFまで読み、最終chunkの認証・サイズ制限まで確認してから使用 |

- ageの公開鍵暗号化は送信者の認証を提供しません。公開recipientを知るだけで資格情報を差し替えられないよう、認証鍵を秘密identityと同じ保護範囲へ置きます。
- 資格情報と鍵束の暗号化はageライブラリへ任せます。独自の暗号アルゴリズム・age recipient・ageファイルparserは実装しません。
- `.vault`全体はJSON manifestです。外部age CLIへ直接渡すファイルではありません。内部のage暗号文を取り出して公式CLIで復号できることを試験しています。
- 1ファイルにまとめることで、通常のOAuth保存を複数ファイルの同時更新へ依存させません。

## 鍵の保護と解錠

| 方式 | 仕様 |
|---|---|
| パスフレーズ | ageのscrypt recipientで鍵束を暗号化。非表示TTYまたは専用FDで入力 |
| 新規scrypt負荷 | `logN=17`、`r=8`、`p=1`。約128 MiBの作業領域。小さい制御環境を考慮して明示設定 |
| 読取り時の上限 | `SetMaxWorkFactor(18)`。過大なファイル指定値による無制限な鍵導出を拒否 |
| 入力制限 | 新規パスフレーズ12〜4096バイト。専用FDは3以上、最大4096バイト |
| OSキーリング | 新規loginの既定。Secret Serviceへ鍵束を保存し、読戻しを検証。専用sessionも利用可能 |
| キーリングの前提 | ホストの`secret-tool`・利用者D-Bus。操作は15秒でタイムアウト |
| 日常操作 | Proxy起動時に解錠。起動済みProxyでは再入力不要 |
| 更新時の負荷 | 解錠済みidentityを再利用し、公開recipientで暗号化。推論・OAuth保存ごとにscryptを実行しない |

- age CLIの既定scrypt負荷は`logN=18`です。本製品は128 MiBの予算で17を設定し、読取りは18まで許可します。[実装の根拠](https://github.com/FiloSottile/age/blob/v1.3.2/scrypt.go)。
- 鍵を暗号文の隣へ平文保存する自動解錠は提供しません。
- OSキーリングの保存時の暗号化・lockはOS設定に依存します。利用不能時にパスフレーズ・平文・ホスト認証へ切り替えません。
- backendを直接変更するコマンドは提供しません。別backendの新しいprofileで再認証します。

## 認証ProxyとVM

1. ホストのCLIで解錠し、age秘密identityと認証鍵をメモリへ読み込みます。
2. 既存の認証Proxyへ、長さ付きの専用stdin streamで鍵束を渡します。
3. Proxyの起動確認後、長時間動くホストCLI側の鍵参照を破棄します。
4. 作業VMには短命・Proxy限定の仮トークンだけを渡します。

| 境界 | 扱い |
|---|---|
| argv・環境変数・設定・コンテナメタデータ・通常ログ | 鍵束・実資格情報を載せない |
| stdin protocol | 最大1024バイト。切断・途中入力・過大な長さを拒否 |
| 起動済みProxyの再利用 | 同じprofile・保護済み鍵のfingerprint・接続世代・protocolを確認 |
| 旧protocolのProxy | ワーカーを排出し、明示的に削除・再作成。異なるprotocolを混在させない |
| 作業VM | 暗号文・復号鍵・資格情報socket・制御ホストのHOMEを共有しない |
| 認証専用Kata | 公式CLIのログイン結果を一時メモリから回収。通常の作業VMとは別用途 |

## 保存・ロック

| 操作 | 仕様 |
|---|---|
| OAuth保存 | プロセス間lockで期待版をCAS比較し、接続世代を維持 |
| 永続化 | 暗号文の一時ファイル→fsync→atomic rename→ディレクトリfsync |
| 保存失敗・版競合 | 成功扱いしない。Proxyの既存の失効処理へ伝える |
| 明示lock | 対象の保存先に属するワーカーと認証Proxyを停止。成果と暗号文は保持 |
| OSキーリングのlock | 起動済みProxyのメモリは消えない。製品の`auth profile lock`を別に実行 |
| プロセス終了 | 所有する鍵・認証鍵のバイト列をclearし、age identityの参照を破棄 |

- 公開`credentialstore.Store`契約は変更しません。
- Goは秘密の全メモリコピーのゼロ化を保証しません。プロセス終了も含めて保持期間を限定します。

## 旧形式からの移行

| 元の状態 | 操作・動作 |
|---|---|
| 平文`.json` | `auth profile encrypt NAME`。age保存・再読込確認後に平文を削除 |
| 旧暗号化schema 1 | `auth profile migrate-age NAME`。旧方式で解錠し、ageへ明示移行 |
| schema 1の通常書込み・新Proxy起動 | 拒否。自動変換やホスト認証への切替は行わない |
| 移行時の世代 | 資格情報版と接続世代を更新。旧ワーカーを再認可しない |
| 復旧用backup | `NAME.vault.v1-backup`を0600で保持。暗号文のみ。自動で読み込まない |
| 競合するbackup | 上書きせず拒否。既存の復旧用データを保持 |
| 中断・再実行 | パスフレーズ／キーリングで検証して再開。移行済みなら再度世代を更新しない |
| 平文と暗号文の共存 | 移行未完了として通常利用を拒否。明示移行で内容照合後に完了 |
| パスフレーズ変更 | 同じage identityと認証鍵を新しいパスフレーズで再保護。接続世代は維持 |

- schema 1のArgon2id・XChaCha20-Poly1305コードは、旧ファイルを読むためだけに残します。新規保存には使いません。
- キーリング移行では旧backupを復旧できるよう、旧キーリング項目も残します。移行確認後のbackup・旧項目の整理は管理者が行います。
- backupも旧パスフレーズ／旧鍵で復号できます。削除済みブロックや過去backupの安全な消去は保証しません。

## 保護の範囲と検証

- 主な対象：停止中のファイル・ディスク・backupからの資格情報読取り。
- 対象外：制御ホストroot、同一UIDのプロセス読取り、稼働メモリ・swap・外側VM snapshot。
- 外側VM snapshotで鍵・暗号文・世代情報が同時に戻る場合、ローカルだけで完全な巻戻し検知はできません。
- WSLはLinux側の私有領域を使用。DrvFS・NFS・FUSE等の未検証filesystemは拒否します。
- [実施済み試験・測定値・未確認事項](../testing.md)を参照してください。

## キーリングのセッション管理

- [操作手順](../guides/keyring.md)：デスクトップの既存サービスを探索し、GUIなしでは専用セッションを起動。
- D-BusとGNOME KeyringをCLIのforeground子プロセスとして管理。通常終了、SIGINT・SIGTERM・SIGHUPで取消・回収。
- 専用のキーリングファイルとProxyメモリで鍵を管理し、パスフレーズファイルやPAM設定の自動変更は行わない。
- キーリングの保管先とfilesystem上のD-Busソケットを、作業VMへの共有対象にできないよう検証。
