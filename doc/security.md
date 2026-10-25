# 認証情報・共有ファイルの保護と制限

## 最初に確認すること

| 対象 | Oaisoの動作 | 利用者が確認すること |
|---|---|---|
| APIキー・ログイントークン | 作業VMへ渡さず、認証Proxyで保持 | 認証ファイルを作業フォルダーやGitへ入れない |
| ホストのプロジェクト | VMの `/workspace` と共有。既定は読み書き可能 | 変更・削除はホストにも反映される。必要なら `--read-only` を指定 |
| 外部通信 | 既定では、一般通信は許可した宛先だけ。AI用の接続先は別の認証経路 | Git・パッケージ取得先など、必要な宛先を設定 |
| ホスト管理 | 管理グループとBuildKitの利用者はroot相当の権限 | 信頼する利用者だけに管理権限を与える |
| 認証情報の保存 | v0.0.3以降の新規loginはキーリング＋age。既存方式は維持 | [暗号化保存](guides/encrypted-credentials.md)の方式・鍵管理・保護範囲を確認 |

## VMと管理サービス

| 管理するもの | 使用する仕組み |
|---|---|
| ワーカーの隔離 | Kata runtime-rs / Dragonball。ワーカーごとに独立したVM |
| 起動・CPU・メモリ・ネットワーク | containerdとroot権限の管理サービス |
| 詳細 | [配置と実機検証](kata.md)・[ネットワーク](network.md)・[通信ポリシー](proxy-policy.md) |

## 資格情報

- このページの「資格情報」は、APIキーやOAuthのログイントークンを指します。

### 保持する場所と返却する値

| 情報 | 保持先・ワーカーへの返却 |
|---|---|
| 実APIキー・OAuth access/refresh/ID token | Auth Proxyで保持。ワーカーへ返さない |
| ワーカーの認証 | 短命placeholder。固定AI経路の中継時に実資格情報へ置換 |
| Codex login用ID欄 | account ID・plan・FedRAMPフラグだけの新規ダミーJWT |
| 元JWTのheader・署名・email・user ID・任意claim | ワーカーへ返さない |
| ダミーJWTの用途 | CLI設定用。上流認証には使わない |
| egress Proxy | 実資格情報をmount・注入しない |

### 初回取得とOAuth更新

- 返却項目：初回取得・更新ともに許可した項目だけ。
- 更新エラー：既知コードとHTTP statusだけを保持。
- 非返却：上流本文・任意フィールド・通信エラー詳細。
- 確認方法：[固定CLI試験](testing.md#codexのidトークン分離)。
- 更新前のワーカーファイル・過去ログ：自動消去しない。
- 旧ワーカーへの対応：Proxy更新後に停止・再作成。

### 保存・mount・権限

| 対象 | 扱い |
|---|---|
| 従来の認証ファイル | 呼出元UID/GID・専用HOMEでreadonly mount。元の0600を維持 |
| 独立プロファイル | ホスト認証のmount・環境変数を省略。選択した0700ディレクトリだけRWで共有 |
| プロファイルのJSON・lock | 0600。CAS・fsync・atomic renameで更新 |
| ワーカー再開 | プロファイルと接続世代を照合 |
| capabilities | 全削除。認証ファイル読取用にも追加しない |
| 管理secret | 別の0600ファイルをAuth Proxyだけにreadonly mount可能。値をargvへ載せない |
| ワーカーへの共有 | CLIはprofile領域との重複を拒否。組み込み側もStore・管理secretを共有外へ置く |
| 保存暗号化 | v0.0.3以降の新規loginはSecret Service＋ageが既定。age＋HMAC-SHA256、明示パスフレーズ方式も対応。作業VMへ鍵や暗号文を渡さない |

- 「ホスト」：信頼済みの制御環境。
- WSL／VM内に置く構成も含む。
- 作業VM：任意コードを動かすワーカー。
- 実資格情報を渡さない。
- 設計要件と未確認条件：[WSL・VM・ヘッドレスの認証管理](credential-store.md#独立した認証管理の設計要件wslvmヘッドレス)。

### 保存暗号化の限界

- 停止中の資格情報ファイルを保護します。制御ホストのroot・同一UIDによるプロセス読取り・稼働メモリ・swap・外側VM snapshotは保護対象外です。
- パスフレーズを暗号文と一緒に保存しません。紛失時は復号できないため再認証します。
- 解錠失敗時にホスト認証・平文・別backendへ切り替えません。移行途中で平文と暗号文が残れば利用を拒否します。
- OSキーリング自体の暗号化・ロックは利用者のOS設定に依存します。
- [仕様](development/local-credential-store.md)・[実施済み試験と未確認事項](testing.md)。

### 認証チェックと管理API

| 条件・経路 | 動作 |
|---|---|
| AI HTTP・WebSocket | 上流接続前に発行済み・有効期限内の短命トークンを検証 |
| 未発行・欠落・失効・期限切れ・重複・矛盾 | 401で拒否 |
| 失効・期限切れ・置換 | 既存HTTP stream・WebSocketも終了 |
| admin listener | Auth Proxyのprimary egress IPへbind。ホストでは127.0.0.1へ公開 |
| worker向けdata listener | admin操作を公開しない |
| placeholderの失効・再起動時の復元 | ホストのManagerだけが実施 |

## ワーカーの制限と起動ゲート

| 項目 | 既定条件 |
|---|---|
| kernel | ワーカーごとに別のDragonball VM |
| UID/GID | 解決済みの非root ID。既定はホストのcurrent user |
| capabilities | bounding / effective / permitted / inheritable / ambientをすべて0 |
| NNP・seccomp | `NoNewPrivs=1`、guest `Seccomp=2` |
| guest資源 | CPU 1、memory 2 GiB、PID 512 |
| host VM全体 | CPU 1、memory 3 GiB、swap 0、PID 512、OOM group |
| network | 専用bridge・namespace・静的Proxy hosts。直接外向き・ホスト・peer通信を遮断 |
| management | containerd/node/BuildKit socket・資産・台帳・host kernel操作用パスを渡さない |

### ユーザーコードを解放する順序

1. 固定bootstrapをPID 1で起動。

    - 元のimage entrypointは待機。

2. ホストのshim/logger実体・worker ID・cgroup所属・上限を検証。
3. guestの別boot ID・実権限・資源制限を検証。
4. placeholderを有効にし、nonceでユーザーコードを解放。

- Kata・資源の検証失敗：エラー。
- runc/QEMUへ自動fallbackしない。
- 検証済みKata内のCodex：内側sandboxを `danger-full-access` に設定。
- 設定の理由：VM内での追加namespaceとseccompの不整合を回避。
- CLIの承認設定：独立して維持。
- 非対話execではCLI自身がapprovalを選ぶ場合あり。

## ホスト側の信頼境界

| 対象 | 信頼・権限の範囲 |
|---|---|
| nodeのoperator group・BuildKit利用者 | root相当。image・command・host bind mountを選択可能 |
| guest | operator socketを公開しない |
| Proxy | runcで実行 |
| ワーカー | 既定はKata |
| 明示した `--runtime runc` | 同じcontainerd上の互換モード。独立kernelは提供しない |

### 固定資産・プロセスの検証

| 検証項目 | 方法・制限 |
|---|---|
| 固定対象 | runtime・kernel・guest image・bootstrap・IO logger・containerd設定／本体・network操作ツール |
| 所有権 | root所有、一般ユーザーから書き換え不可 |
| 実プロセス | inode・cmdlineのworker ID・start ticks・実cgroup所属を照合 |
| manifest | checksum記録。配布元・署名の確認の代わりにはならない |
| 資産hash | 毎回全ファイルを再読込しSHA-256を計算 |
| 検証の並列数 | 1回につきGOMAXPROCS以下、最大4 reader |
| 結果cache | 更新時刻を根拠に検証を省略しない |

[性能計測の手順](performance.md)

### cgroup・孤児回収

- Dragonballの管理プロセス：所有権を検証し、ユーザーコードの開始前・再接続時・停止待ちの前に、同じ資源上限内の別cgroupへ移す。
- `cgroup.kill`：専用予約の全占有プロセスを確認した場合だけ使用。
- 対象外：他サービスのgroup・任意PID・不明namespace。
- 管理層へ接続不能：`unknown` とし、孤児として削除しない。

## workspace・Git・削除

| 対象 | 保護・制限 |
|---|---|
| workspace・追加mount | 指定したreadonlyを維持 |
| 共通Git metadata | worktreeのcommit互換性のため同じ絶対パスで共有。必要な場合はRW |
| 元の `.git` pointer | 書き換えない |
| 明示共有したファイル・Git設定 | VM隔離だけで不変にはならない |
| 削除journal | 実オブジェクト削除前に永続化 |
| network・token失効・cgroup・worktreeの回収が不確実 | intentを残す。worktreeを先に消さない |
| rootのVM予約・network・OCI spec・ログ | privateな状態ディレクトリへ保存 |
| VM inventory | APIキー・環境変数・タスク本文を出さない |

## 制限と確認範囲

### 通信と認可

| 項目 | 制限・既定動作 |
|---|---|
| ワーカー―Proxy間 | 専用network上の平文HTTP。TLSなし |
| 管理API・data port | 公開networkへ直接公開しない |
| 短命トークン | Bearer認証。送信元コンテナIDとの照合なし。盗まれた有効トークンは失効まで利用され得る |
| 一般通信 | 既定拒否。宛先許可が空なら一般egressを接続しない |
| ワーカー別許可 | ポリシー別Proxyで強制。同じ規則のワーカーは共有可 |
| 公開宛て通信を広く許可 | ホストが明示的に選択 |
| 通信規則の変更 | Proxy単位で固定。ホットリロードなし |
| モデル制限 | ホストがAuth Proxyへ明示設定した場合だけ有効。起動CLIのモデル指定だけでは認可しない |

- モデル制限の対応経路・WebSocket拒否：[追加制御](sandbox-features.md#モデル認可と段階的な起動)。

### 対象ワーカーを即時隔離する

| 操作 | 効果・条件 |
|---|---|
| `policy quarantine` / Go API `Quarantine` | 対象ワーカーを即時停止し、再起動を禁止 |
| 停止対象 | 一般通信・AI通信・全ジョブ |
| workspace | 保持。新しい規則で再開する場合は新規ワーカーを作成 |
| 隔離状態 | nodeで永続化 |
| 操作失敗 | 停止完了と判断しない。再試行 |

[停止と隔離](sandbox-features.md#通信の停止と隔離)

### 検証済み・別途確認が必要なもの

| 区分 | 範囲 |
|---|---|
| [実機・実AI試験](testing.md)の記録あり | 隔離・権限・資源・機能、既存auth.jsonによるWebSocket/HTTP SSE推論 |
| [通信・失効の追加検査](development/network-acceptance.md)（未リリースmain） | DNS・停止失敗の自動検査、実Kataからの拒否、既存SSE/WSの失効、隔離停止による一般通信の切断。接続先はfixture |
| 別途確認 | 長期間の実OAuth更新・対話キーボード操作・任意のカスタムイメージ |
| Claudeのfixture試験 | APIキー／サブスクのplaceholder変数を分離。実access/refresh tokenをworkerへ保存しない |
| Claudeで未検証 | 実Anthropic推論・サブスク利用枠 |
| 継続的な更新が必要 | host kernel・KVM・Kata・guest kernel |

[Claude Code](claude-code.md)・[受入試験の記録](testing.md#claude-codeの受入)

## 操作の監査記録

- 未リリース（main）：[保存・欠損検知・容量・障害時の扱い](guides/audit.md)。ローカルrootに耐える改ざん防止は保証しません。
