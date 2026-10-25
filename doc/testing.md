# テストと動作確認

## 接続・帯域・FD・block I/O（2026-10-25）

- 対象：**未リリース（main）**。[設定・単位・対象外](guides/resource-limits.md)。追加設定なしでは従来の動作を維持します。

| 検査 | 結果 |
|---|---|
| 全件 | `make build test vet lint`、5系統の`make security-fuzz`、Python 65＋24件、CLI検査、文書strictビルドが成功 |
| 接続数 | 実Kataで接続元上限8。既存3接続に加えて5接続を受入、追加7接続を拒否。既存の接続は維持 |
| 帯域 | 64 KiB/秒・burst 32 KiBで64 KiBの受信が約0.51秒。再接続による枠の初期化、別接続元との枠の混同、中断不能な待機を単体でも検査 |
| FD | 実Kataの初期処理とExecへsoft/hard 512を適用。stdio以外に509個のopen後、`EMFILE`。hard上限の引上げも拒否 |
| I/O | ホストの`io.max`でread 64 MiB/秒、write 8 MiB/秒、read 4096 IOPS、write 1024 IOPSを照合。実ディスクへ16 MiBを書きfsyncする処理は約2.13秒。対象cgroupのwrite計上も約16 MiB増加 |
| 通信・認証 | リソース制限中のSSE/WS継続・失効・隔離、固定Codex 0.163.0／Claude Code 2.1.306、broker、Proxy＋VM再起動後の利用上限維持が成功 |
| Go API | 制限付きCase Proxy、異なる上限への無断再利用の拒否、FD/I/O付きprofileの起動・契約照会、再送・取消・ケース間認証分離が成功 |
| 導入・回収 | 使い捨てノード`19fca9150a96`でResourceControls（119.61秒）・GoAPI（75.04秒）が成功。更新・旧版拒否・失敗時の自動復旧・rollback・空の資源一覧まで成功。ノード・volumeを回収 |

- 修正した境界：帯域待ち中の一時的な読み取り中断では、消費済みのTCPデータを保持して再開。CONNECT／WebSocketのbuffer済みデータも転送。設定の誤記・桁あふれを無制限に読み替えないことを確認。
- 途中の失敗も保持：試験用設定ディレクトリ・node待機を修正。tmpfsではI/O速度を検証できないため実ディスクへ変更。実行中のshellが作業ツリーの編集を読んだ失敗は、検証スクリプトのコピーを固定することで防止。
- 証跡：`.kata-verification/p2-resources-20261025/`と`.kata-verification/setup-e2e/19fca9150a96/`。試験用の固定版`v0.0.0-resources-verified-a/b`を使用。実アカウント・外部AI推論・実課金は使用していません。
- 速度を実測したのはwrite BPS。read／IOPSの実ワークロード性能、全デバイス・filesystem、NICの瞬間帯域、全Kataスイートの再受入を網羅していません。FDはguestのプロセス単位、block I/Oは当該ホストcgroup・デバイスへの計上分です。
- 共有ホストのcontainerd・BuildKit・nodeの起動時刻は不変。配布版の公開・共有ホストの更新は行っていません。

再実行は[導入・更新・復旧の準備](#新規導入更新復旧を検証する)と[通信試験のfixture](development/network-acceptance.md)を使用し、`verify-setup.py`へ`--acceptance-run '^TestKata(ResourceControls|GoAPI)$'`を指定します。

- この明示した使い捨てノードだけに試験用profile・io委譲・実ディスクのworkspaceを準備します。共有ホストへ適用しないでください。
- `verification-source/`へ試験スクリプトを固定し、稼働中の作業ツリー編集の影響を防ぎます。

## 不正入力・競合・障害の追加検査（2026-10-25）

- 対象：未リリース（main）。[対象・再実行・限界](development/security-tests.md)。
- 最終検査：`make build test vet lint`、5系統の`make security-fuzz`、文書strictビルドが成功。通常の全件試験はrace付き、探索は各5秒・2 worker。
- 追加の境界：未知／重複／Unicode別表記／末尾JSON、broker URL/path、未発行認証、モデル指定、DNS・private宛先、過大TTL、64並行のENOSPC後の利用拒否、96 SSEの失効、停止と発行の競合を確認。
- 修正：停止済みProxyからの発行・復元を拒否し、期限監視goroutineを終了。OAuth更新の監査失敗時はtokenを失効し、新資格情報を採用しない。監査理由は固定コードに限定し、理由欄がない旧記録も検証可能。
- 実Kata最終確認：`e727d76aaec3`でTLS・固定CLI・SSE/WS・broker・再起動（115.84秒）、Go API・別ケースtokenの401拒否・Proxy監査の検証（74.38秒）が成功。新規導入・更新・旧版拒否・自動復旧・rollback後の版下限維持・残留資源なしも確認。
- 署名基盤のCI：commit `10e020a`の[CI 40158560900](https://github.com/pacificbelt30/sandbox-oasis/actions/runs/40158560900)で、実署名の生成・検証、改変・別workflow・別タグの拒否が成功。
- 初回の期限試験は負数TTLで期限切れを作っていました。不正TTLを拒否する実装に合わせ、期限切れfixtureを明示した時刻で作る形へ修正して再試験。
- ENOSPCは保存境界への注入。実diskを満杯にしていません。実アカウント・外部AI推論、公開した署名付き配布版の導入は今回の確認に含みません。
- 既存の共有containerd・BuildKit・nodeの起動時刻は不変。試験が作ったノード・volumeはcontrollerが回収しました。

## 配布物の署名・作成元・依存一覧（2026-10-25）

- 対象：未リリース（main）。[署名・SBOM・巻戻し防止](guides/signed-releases.md)。公開済みv0.0.3には署名を後付けしていません。
- ローカル：静的配布候補`v0.0.0-security-a`を生成。archive・installer・SBOMのSHA-256一致を確認。SBOMは約30 KiBで、生成物はGitへ追加していません。
- 自動試験：配布・スクリプトPython 65件、配置Python 24件が成功。署名失敗時の展開拒否、固定repo／workflow／タグ／issuer、古い版・壊れた版下限の拒否、古い／変更済みのビルド元情報の拒否を確認。
- 署名の呼出し単体試験はverifier fixtureを使用。実署名・改変・別workflow・別タグの拒否はmain CIの署名stepで確認します。fixtureだけの成功を暗号検証の成功とは数えません。
- 実Kata：`076cf8750e05`でGo API受入が成功（77.34秒）。新規導入、更新、意図しない旧版の拒否（サービスPID不変）、失敗時の自動復旧、明示rollback後も版の下限を保持することを確認。
- 認証回帰：`ddfbb0563f02`では暗号化profile（27.87秒）と実キーリング（28.05秒）が成功。Go APIの旧テストは別VMのURLをグローバルな宛先と誤認して失敗。VM内URLで他ケースのtokenを送る検査へ修正して上記ノードで再試験。
- 公開された署名付き配布版のダウンロード・導入確認は、次回の明示したリリース作業で行います。今回リリースは公開していません。

## VM―認証ProxyのTLS（2026-10-25）

- 対象：未リリース（main）。[方式・期限・更新手順・一般egressとの区別](guides/worker-tls.md)。
- 単体：コピーtoken＋別の鍵、再開前の旧鍵、client証明書なし、平文接続を拒否。tokenが有効でも短命証明書の期限でstreamを終了。暗黙の管理secretを外部URLへ送らないことも確認。
- 実Kata：`4d24f1854b94`の`TestKataWorkerMTLS`が成功（164.24秒）。固定Codex 0.163.0／Claude Code 2.1.306のSSE・ツール実行、SSE/WS失効、broker、Proxy＋VM再起動後の利用上限維持を確認。
- 全Go raceテスト・Vet・Lint成功。架空の認証情報・ローカル上流fixtureを使用。実アカウントでの認証・課金を伴う推論は未実施。
- 初回の失敗を保持：管理secret未設定経路（`afa5cf8a529b`）を0600自動生成で修正。保存済みProxy名をIP対応表と誤認した経路（`8f7a38c81de7`）を検証済みExtraHostsの利用で修正。対応表なしの起動も拒否する回帰試験を追加。

## 保存できる監査記録（2026-10-25）

- 対象：未リリース（main）。[保存項目・容量・障害時の扱い](guides/audit.md)。
- 自動試験：全Go raceテスト・Vet・Lint、CLI検査、配置Python 22件、文書strictビルドが成功。
- 境界：並行追記、未確定操作、容量超過、切断された末尾、head不一致、symlink、監査破損時の発行・復元拒否と失効継続を確認。
- 実Kata：使い捨てノード`0aa47aba4318`でcheckpoint受入が成功（133.95秒）。nodeの操作記録を検証し、連鎖の一致・未確定操作なしを確認。
- 初回試行：既定cacheにKata資産なしで停止。固定hashを確認して取得。旧試験nodeイメージではキーリング関連package不足で導入停止（`0b4c54c5180f`）。準備済みキーリングnodeへ変更して再試験。
- 架空の認証情報を使用。実アカウントへの認証・推論は実施していません。ローカルrootによる記録全体の書換えは保護対象外です。

## disk checkpoint（2026-10-25）

| 項目 | 結果・範囲 |
|---|---|
| 対象 | 未リリースmain。同じnodeの停止済みKata rootfsを保存、新規VMへ復元 |
| 自動検査 | 除外パスとsymlink先、認証ファイルへのhard link、特殊ファイル、容量超過・取消、旧nodeへの変更拒否、runc／不適合entrypointの拒否、公開前のmetadata検査を確認 |
| 実Kata | `TestKataDiskCheckpoint`が99.57秒で成功。稼働中の保存拒否、容量超過で未公開、node再起動後の保持、保存元の再起動・変更、新規VMのSDK／CLI復元、使用中checkpointの削除拒否を確認 |
| 保存範囲 | `/opt`内の内容・symlinkを保持。workspace・追加共有・home・一時領域・追加除外・そのhard linkは復元されず、イメージ設定へ元の認証値をコピーしない |
| 品質 | build・全Go race・vet・lint、配置関連Python 22試験、CLI名・help・completion、MkDocs strictが成功 |
| 導入・更新 | 使い捨てnodeで新規導入、稼働中更新の拒否、正常更新、起動失敗の自動復旧、明示rollbackと回収が成功 |

- 証跡：`.kata-verification/disk-checkpoint-20261025/`、`.kata-verification/setup-e2e/4afffeceef79/`。最初の試験は試験用admin secretの長さ、次は読み取り禁止パスの指定範囲で停止。既存の認証・path検証は維持し、試験条件を修正しました。
- キャッシュを再利用したオフライン導入。公開配布物・共有ホストの稼働サービスは更新していません。別nodeへの転送、RAM保存、実提供元の推論・実課金、全Kataスイートの再受入は対象外です。
- [保存範囲・容量・操作・互換性](guides/disk-checkpoint.md)。

再試験は[導入・更新・復旧の準備](#新規導入更新復旧を検証する)に従い、対応する2bundle・Proxy・受入バイナリを作成します。

- checkpoint用ワーカー：`deploy/kata/Dockerfile.checkpoint-fixture`を使い、現在のentrypointを含む標準イメージを`BASE_IMAGE`へ指定。使い捨てnodeの既定UID/GIDは1000です。
- イメージアーカイブにそのワーカーを含め、`scripts/verify-setup.py --worker-image oaiso-checkpoint-worker:verification --acceptance-run '^TestKataDiskCheckpoint$'`で試験範囲を指定します。node再起動のcontrollerもこの手順で準備されます。
- 自分の復元先ワーカーを回収してから、checkpointを削除します。試験には分離した環境を使います。


## 既存機能P1の強化（2026-10-24）

| 項目 | 結果・範囲 |
|---|---|
| 更新候補の検証基盤（10月25日） | 現行固定版Codex 0.163.0／Claude Code 2.1.306を候補として、静的検査→隔離CLI→実Kataの一括実行が`pass`。VM内のCodex SSE・Claudeツール＋SSE、導入・更新・自動復旧・rollback・回収が成功。`adopted: false`を維持 |
| 最終検査（10月25日） | build・全Go race・vet・lint、Python 84試験、静的基準整合性、CLI名とhelp、MkDocs strictが成功。実Kataの拡張通信スイート106.39秒。試験用node・volumeを回収し、共有ホストの稼働サービスは維持 |
| broker設定支援（10月25日） | CLI／TUIの秘密非表示・空欄維持・保存競合・権限／symlink／不正JSONの自動試験が成功。実Kataで設定作成→許可GETへの注入→path／method拒否→秘密ファイル共有拒否を確認 |
| 診断の保存・出力（10月25日） | 全Go race・build・vet・lintとMkDocs strictが成功。実Kataでstatus・拒否コード・hostメモリ・起動時間の保存とCLI export、秘密・workspace値の非出力を確認 |
| 実行ごとの利用上限（10月25日） | build・全Go race・vet・lint・MkDocs strictが成功。実Kata通信スイート54.78秒。要求上限・token加算・超過拒否・ProxyとVMの再起動後の維持・台帳共有の拒否を確認 |
| メイン処理・活動通知 | 未リリースmain。全Go race・build・vet・lint、MkDocs strictが成功。実Kataの追加制御スイートが59.93秒で成功 |
| 実Kataの確認内容 | node再起動後のメイン処理保護、活動通知でのidle回収防止、通知解除後のidle停止、最大寿命による実行中の停止。秘密・管理socketをVMへ追加共有しない |

- 一括検証の証跡：`.kata-verification/p1-existing-20261024/qualification-run3/qualification.json`、`.kata-verification/setup-e2e/43a22b5269f8/`。最初の候補試験はExecの存在しない環境変数、次は合成APIの`/v1`不足を検出。通常のProxy用プレースホルダーと本番同様のAPI baseへ修正して成功。実提供元へのログイン・本番推論・実課金・実WSLは未検証です。
- 保存済みの別候補Claude Code 2.1.307は、固定版との不一致で`hold`、CLI／Kataは未実行。固定版とbaselineを自動変更しないことを確認。詳細は[候補検証の手順](development/candidate-qualification.md)。
- CIで並行上限の試験に実行順依存を検出。同時数で拒否された要求はbyte予算を消費しないため、残量の明示的な消費後に拒否を検査するよう修正。要求数上限だけの並行試験も追加し、race付き30回と全Go検査が成功。制限ロジックは変更していません。
- 診断の証跡：`.kata-verification/setup-e2e/6d07ff3c8169/`。初回は試験用Proxyの識別情報・ログ出力不足、次は試験用CLIと受入の保存形式が不一致。fixtureを修正し、同じソースからCLI・試験資産を再作成して解消。
- 利用上限の証跡：`.kata-verification/setup-e2e/a6ee0873b45e/`。初回は旧認証モードのProxy起動で失敗。保護されたadmin設定がある場合だけ台帳を有効にして修正し、新規導入・更新・復旧も再検証。外部API推論・実課金額の確認は含みません。
- 証跡：`.kata-verification/setup-e2e/29115c5f7a27/`、`.kata-verification/p1-existing-20261024/`。
- [メイン処理と活動通知の使い方](sandbox-features.md#メイン処理と活動通知によるidle回収の防止)。公開配布物は更新していません。

- 通信・失効の追加検査と再実行手順：[通信・失効の受け入れ検査](development/network-acceptance.md)。

## 通信・失効の追加受け入れ（2026-10-24）

| 対象 | 結果 |
|---|---|
| 版・環境 | 未リリースmain。試験用バンドル`fa35f89-network-a`／`-b`、使い捨てUbuntu 26.04、containerd 2.4.2、Kata 4.2.0／Dragonball |
| 自動試験 | A/AAAA混在・metadata・DNS応答変更、3つのAI経路×5種類のSSE終了条件、停止失敗と未確認状態のCLI表示・JSONを検査 |
| 実Kata | `TestKataNetworkRevocation`が20.98秒で成功。private・metadata・禁止ポート・redirect拒否、公開IPv4/IPv6 CONNECTの往復を確認 |
| 通信中の失効 | VMを動かしたままAIトークンを失効。既存SSE/WSの接続先側が切断し、一般通信は継続 |
| 隔離停止 | 一般HTTP・IPv4/IPv6 CONNECTの接続先側も切断。workspace保持・再起動拒否・CLIの停止完了表示が成功 |
| 品質 | build、全Go race・vet・lint、Python 77試験、MkDocs strictが成功 |

- 実行記録：`.kata-verification/setup-e2e/2a44c7d4aded/acceptance.txt`。自動試験・ビルド記録は`.kata-verification/network-acceptance-20261024/`。
- 初回の試験用イメージはprotocol情報不足で拒否。修正後、プローブが起動処理用の削除済み環境変数を参照して失敗したため、通常のProxy用プレースホルダーと接続先を使うよう修正し再検証しました。製品の保護処理は維持しています。
- 公開v0.0.3の配布物には未反映。実アカウント・外部IPv6回線・実WSL・全8スイートの再受入は対象外です。

## 依存更新とAPI境界の回帰検証（2026-10-24）

- 対象：**未リリース（main）**。公開v0.0.3の配布物には未反映。
- 全Go race・build・vet・lint・module検証、Go 1.26.0でのビルド、macOS arm64／amd64・Linux arm64のクロスビルドが成功。
- 依存検査は15→2件。残る2件の適用条件と、追加した依存・API境界検査は[依存関係の検査と対応](development/dependency-security.md)を参照。
- 実Kataの追加確認は上の通信・失効スイートを参照。過去の全件受入を今回の依存版の全件成功とは扱いません。実アカウント・WSLは未実施です。

## インストールから初回利用までの通し検証（2026-10-24）

| 対象 | 確認結果 |
|---|---|
| バイナリ導入 | 対応するCLI・guard・loggerを含むバンドルを、パイプ経由のインストーラーで計画表示・適用。Goなしの使い捨てUbuntu 26.04で成功 |
| 新規認証設定 | Codex／Claude Codeそれぞれ空のHOMEから、実キーリングの解錠・既定login・age保存・プロファイル選択・管理シークレットの自動生成が成功 |
| 初回ビルド | 標準ワーカーイメージを未ロードの状態で、ネイティブBuildKitによる新規ビルド・containerdへの取込みが成功 |
| 両エージェント | VM内でCodex 0.163.0／Claude Code 2.1.306の起動・正常終了を確認。前面TTYへの入力・出力・ログ保持、workspace書込み・数値UIDの一致、停止・削除まで成功 |
| 実Kata受入 | VM、認証専用VM、前面TTY、共有9ケース、追加制御、ageプロファイル、実キーリング、Go APIの8スイートが成功。前面TTYを動かしたまま管理サービスを再起動し、同じguestでログ収集の再開・最終出力・正常終了を確認 |
| 更新と復旧 | 稼働中の更新拒否、正常更新、起動失敗からの自動復旧、明示rollbackが成功。VM・コンテナ・task・ネットワーク・専用firewallの残存0件 |
| 品質 | 全Go race・vet・lint、Python 77試験、MkDocs strictが成功 |

- 通し試験・8スイート・更新／復旧の実行記録：`.kata-verification/setup-e2e/4561c54c0575`。試験ノードと専用ボリュームも回収済みです。
- 修正：Kataの前面TTYをFIFO/passfd経由に変更し、短いコマンドの終了時も出力を読み終えてからCLIを終了。終了済みtaskへの入力専用接続は拒否します。
- 対象は開発版から作成した検証用バンドルです。**公開済みv0.0.2の更新・新リリースの公開は行っていません。** [開発版の通し手順](guides/first-run.md)。
- OS依存パッケージは試験用イメージで準備し、Kata等の配布物にはチェックサム検証済みキャッシュを使用。標準ワーカーは新規ビルドしています。
- 資格情報は架空で、loginの実API確認は`--skip-check`で省略。実アカウントのログイン・推論、実WSL、ホスト再起動、対話画面の全キーボード操作は未確認です。


## キーリングを使う新規loginとGUIなしの解錠（2026-10-24）

| 対象 | 結果 |
|---|---|
| 新規login | 既定でSecret Service＋ageへ保存。キーリングを利用できない場合、APIキー入力や提供元ログインの前に拒否。平文へ自動変更しない |
| 既存利用 | 既存の平文・パスフレーズ形式を維持。明示的な`login --encrypt`／`--plaintext`を提供。低水準set/importの動作は維持 |
| 実キーリング | GUI・外部ネットワークのない使い捨てUbuntu 26.04でGNOME Keyring 50.0、D-Bus 1.16.2、libsecret 0.21.7を使用 |
| 保存と再解錠 | 実CLIの既定loginで保存し、次の専用セッションで鍵を再取得。独立した公式age CLIで資格情報を復号して内容を照合 |
| 失敗時 | 誤パスフレーズを拒否し、利用者のコマンドを起動しない。誤入力後も正しいパスフレーズで元の鍵を取得できることを確認。同時セッション・不正な保管権限・symlink lockを拒否 |
| 終了・取消 | 通常終了、SIGTERM、SIGHUPでキーリング・D-Busを回収。取消試験で見つかった子プロセスの残留を修正し、再試験で回収成功 |
| 共有の保護 | キーリング保管先とfilesystem上のD-Busソケットを、作業VMのworkspace／追加mountへ共有する操作を拒否 |
| 実Kata | VM受入292.75秒、認証専用VM24.33秒、共有9ケース164.74秒、追加制御47.87秒、パスフレーズ形式24.13秒、実キーリング形式24.50秒、Go API70.36秒で成功 |
| 導入・更新・復旧 | 同じノードで新規導入・稼働中の更新拒否・正常更新・起動失敗からの自動復旧・明示rollbackが成功。VM・コンテナ・task・ネットワーク・専用firewallの残存0件 |
| 品質 | 全Go race・vet・lint、Python 77試験、MkDocs strictが成功。Go 1.26.0、Linux／macOS arm64はビルドを確認 |

- 7スイートと導入・更新・復旧の実行記録：`.kata-verification/setup-e2e/97ba3b022197`。SIGHUPの回収は別の使い捨て実キーリング環境で追加検証しています。
- メモリの参考値：架空データでの専用セッション内のRSSはGNOME Keyring 10,632 KiB、D-Bus 4,392 KiB。CLI・Proxy・VMは含みません。常駐するデスクトップサービスを再利用する場合、この2プロセスを追加起動しません。
- GUIデスクトップ全体は不要ですが、Ubuntuの依存パッケージにはGUI関連ライブラリも含まれます。
- 既存デスクトップのPAM連携、実WSL・実アカウントでのログイン・長期OAuth更新は未確認。PAM設定の自動変更は行いません。
- 公開済みv0.0.2には含まれません。[準備・CLI操作・セッション終了時の扱い](guides/keyring.md)。

## age形式への移行検証（2026-10-24）

| 対象 | 結果 |
|---|---|
| age形式 | age v1.3.2を組込み。内部暗号文を独立した公式age CLIで復号し、内容の一致を確認 |
| 旧形式の移行 | 旧commit `1168123`の実writerが生成した架空データを使用。パスフレーズ・キーリング両方式で資格情報の維持、接続世代の更新、0600の暗号化backup、再試行を確認 |
| 改ざん・失敗 | 公開recipientだけを使った暗号文の差替え、誤パスフレーズ、過大なscrypt指定、切れた鍵stream、backup競合、取消を拒否 |
| 保存契約 | Store適合性、CAS、同時更新、永続化、OAuth更新時の接続世代維持を確認 |
| 再起動時の鍵転送 | stdin専用attachが過去ログを再送し、切断時に入力を欠落させる競合を修正。実FIFOの回帰試験で修正前の欠落と修正後の完全転送を確認 |
| 品質検査 | 最終依存版で全Go race・vet・lint成功。Python 77試験、上流契約のオフライン検査、CLI名検査、MkDocs strict成功 |
| ビルド | Go 1.26.0でCLIをビルド。Go 1.27.3でLinux／macOS arm64をクロスビルド。非Linux実行は未確認 |
| 実Kataの既存機能 | VM受入236.74秒、認証専用VM23.66秒、共有9ケース160.25秒、追加制御47.90秒で成功 |
| ageプロファイルの実Kata | 24.64秒で成功。実CLIの旧形式移行、鍵のstdin転送、作業VM・通常ログの秘密非残留、lock後の再起動拒否と明示解錠、別CLIからの再利用を確認 |
| Go APIの実Kata | 68.81秒で成功。small／standard、取消・再接続・競合、ケース間のtoken分離と失効、stdin EOFを確認 |
| 導入・更新・復旧 | 最終の2スイートと同じノードで、新規導入、稼働中の更新拒否、正常更新、起動失敗からの自動復旧、明示rollbackが成功。VM・コンテナ・task・ネットワーク・専用firewallの残存0件 |
| 配布notice | 実際のGo依存からの収集が成功。age・hpkeのライセンス文書を含むことを確認 |
| 依存関係検査 | age移行時点では15件。比較元19件から減少、当時の新規検出0件。[その後の依存対応](development/dependency-security.md) |

- 最終コードのVM受入・認証・共有・追加制御・ageプロファイルは`.kata-verification/setup-e2e/05a548a9da81`で成功。この実行のGo APIは、試験バイナリの版指定漏れにより版不一致を拒否しました。
- 文書どおりbundleと試験バイナリの版をそろえ、`.kata-verification/setup-e2e/7ea07bec8e2d`でageプロファイル（24.14秒）→Go API（68.81秒）を再実行して成功。上記6スイートは分割実行の結果です。
- 最初に見つかった再起動時のstdin競合は実装を修正し、回帰試験と修正後の実Kataで再検証しています。

### age実装の測定値

| 条件 | 参考値 |
|---|---|
| 測定環境 | Linux amd64、Intel Core i5-8500、Go 1.27.3、架空データ。各benchmarkを3回測定 |
| 解錠 | 約367 ms／回、約128 MiB割当て。age scrypt `logN=17`。通常要求・OAuth保存では実行しない |
| 解錠後のOAuth保存 | 約0.66 ms／回、約180 KiB割当て |
| 読込みの調整前→後 | 約306〜313 µs→約200〜202 µs。解析済みidentityの再利用により、今回のage実装内で約35%短縮 |

- 読込みの改善率はage実装内の比較です。旧形式や製品全体の35%高速化を示しません。
- 解錠は旧形式の約138 ms／64 MiBより重くなっています。起動時に限定し、起動済みProxyは再利用します。
- filesystem・fsync・機器で変動します。実AIの応答速度を示す測定ではありません。
- 実アカウントのログイン・実APIキー・長期OAuth更新、実デスクトップSecret Service、WSL／DrvFSの実機検証は未完了です。
- 公開済みv0.0.2には含まれません。[操作手順](guides/encrypted-credentials.md)・[実装仕様](development/local-credential-store.md)。

## ローカル暗号化・状態表示・更新案内の検証（2026-10-23）

| 対象 | 結果 |
|---|---|
| 保存契約 | 暗号化版も既存のStore適合性試験に合格。期待版のCAS・接続世代維持・同時更新・永続化・失敗時の原子性・切断・独立snapshotを確認 |
| 暗号化 | 誤パスフレーズ・暗号文改ざん・巨大KDF指定・別providerへの更新を拒否。平文移行の中断／再開・内容不一致の拒否・パスフレーズ変更を確認 |
| Proxy | OpenAI／Anthropic × API／OAuth × 平文／暗号化の8組合せをローカルHTTP fixtureで確認。ホスト認証への切替・実IDトークン返却なし |
| 状態表示 | 保存・未接続・未推論を区別し、暗号化プロファイルの通常検査は復号しない。実キーをJSON／通常表示へ出さない |
| 更新案内 | 公開v0.0.2のinstaller・SHA256SUMSで実際の計画表示成功。改変・重複checksum・不正tag・HTTPS downgradeを拒否。暗号化rootの非対応版への適用を拒否 |
| 既存の実Kata | VM受入260.41秒、認証専用VM24.21秒、共有9ケース、追加制御47.45秒、Go API68.69秒で成功 |
| 暗号化の実Kata | **22.56秒で成功**。暗号文だけのmount、鍵のstdin転送、作業VMのファイル／環境／メタデータに秘密がないこと、通常consoleへの非残留、CLIによるlock、鍵なしの再起動拒否、明示解錠を確認 |
| CLIの再利用 | 実CLIでProxyを起動し、別プロセスからパスフレーズ入力なしで再利用。別保存rootの同名profileをロック対象にしない判定も試験 |
| 導入・更新・復旧 | 暗号化再試験と同じ使い捨てノードで、新規導入・稼働中更新の拒否・正常更新・起動失敗からの自動復旧・明示rollbackが成功。VM・コンテナ・ネットワーク残存0件 |
| 品質検査 | 全Go race・vet・lint、Python 77試験、上流契約のオフライン検査、MkDocs strict成功。macOS／Linux arm64はビルド確認のみ |

- 既存5スイートの実行記録：`.kata-verification/setup-e2e/9235b5f8cd96`。この実行では新規暗号化CLI試験に起動引数の不備が見つかりました。
- 修正後の暗号化CLI・実Kata再試験：`.kata-verification/setup-e2e/763401f7896d`。異常時の拒否を維持し、鍵の転送とProxy再利用が成功しました。
- runcのbinaryログ方式がstdinを接続しない条件を確認し、非TTY・stdin付きruncだけshimのfile方式を使用。Kataのbinary loggerを維持し、ノード再起動・継続ログ・PTY・stdin EOF・取消を再検証しています。
- 通常CIは架空の資格情報とfixtureを使用し、Kata試験は明示的な実行時のみ。提供元への実ログイン・推論を実行しません。

### 暗号化の測定値

| 条件 | 測定値 |
|---|---|
| 測定環境 | Linux amd64、Intel Core i5-8500。Go benchmark各3回の参考値 |
| パスフレーズ解錠 | 約138 ms／回、約64 MiB割当て。Argon2id 64 MiB・3回・並列度1 |
| 解錠後のOAuth保存 | 約0.09 ms／回、約10 KiB割当て。この環境の一時保存領域での値 |
| 通常要求・OAuth更新 | KDFを実行しない。解錠済みのデータ鍵を再利用 |

- 保存速度はfilesystem・fsync・機器で変わります。上の値は実推論の速度保証ではありません。

### 残る実機確認

- 実アカウントでのCodex／Claude Codeログイン、実APIキー、実推論、長期OAuth更新。
- 実際のデスクトップSecret Serviceとの接続・画面・セッションlock。現在の試験はbackend障害と`secret-tool`互換の模擬clientです。
- WSL 2・各Linux VM／SSH環境、実DrvFS等での拒否・鍵保管・KVM条件。
- 公開済みv0.0.2には新機能を含みません。[暗号化の操作](guides/encrypted-credentials.md)・[実装仕様](development/local-credential-store.md)。


- テストを「コードの確認」「実Kataでの確認」「実AIとの通信」に分けて説明します。
- 自動テストの成功だけで、外部APIや任意のカスタムイメージの動作を保証するものではありません。

- API仕様・SDK型・CLIフラグの更新候補を調べるルールベースの検査は、[上流仕様の静的な変更検知](upstream-compatibility.md)を参照してください。
- `make contracts-check` はネットワークを使わず、検知ルールと基準データの整合性を確認します。

## 開発版ログイン導線と認証用Kata（2026-10-23）

- `oaiso login`、初回認証設定、専用Kata、推論を送らないAPIキー接続確認を追加。
- `auth profile use NAME` で保存済みプロファイルを再選択できます。

| 対象 | 結果 |
|---|---|
| コード | Go全パッケージのテスト、関連パッケージのrace付き試験が成功 |
| API接続確認 | ローカルfixtureでOpenAI／Anthropicの固定モデル一覧経路・認証ヘッダー、HTTP失敗・通信失敗時の秘密非返却を確認 |
| 初回設定 | 明示入力だけを保存。ホストAPI環境変数を流用せず、既存プロファイル・通信制限・設定・コメントを維持 |
| 認証用Kata | 23.72秒で成功。別VM・readonly root・128 MiB tmpfs・workspace／ホスト認証非共有、認証データ回収と通常consoleへの非残留、停止後の再開拒否を確認 |
| 固定CLI | 認証用VM内でCodex 0.163.0、Claude Code 2.1.306が起動 |
| 既存VM受入 | VM受入223.10秒、共有9ケース163.77秒、追加制御47.23秒、標準Go API69.59秒で成功 |
| 導入・更新・復旧 | 同じ使い捨てノードで正常更新・失敗時の自動復旧・明示rollback、VM／コンテナ／ネットワークの回収を確認 |

- 実行環境は使い捨てUbuntu 26.04・KVM・Kata 4.2.0 / Dragonball。既存ホストのサービスは更新しません。
- 最初の試行は試験用Proxyの識別ラベル不足で拒否されました。試験定義を補正し、保護を維持したまま5スイートを再実行して成功しています。
- 試験は架空の資格情報とローカルfixtureを使用。実アカウントのログイン、実APIキーによる接続、実推論、長期OAuth更新は未確認です。
- 公開済みv0.0.2に新しいloginコマンドは含まれません。[開発版の手順](commands/login.md)を参照。

## v0.0.2配布インストーラーの検証（2026-10-23）

| 対象 | 結果 |
|---|---|
| 配布物 | 実際の静的CLI・補助プログラムからアーカイブを生成。版・SHA-256・ELF・静的リンク・同梱noticeを確認 |
| インストーラー | パイプ実行で配置計画→適用の順を確認。不一致の版・改変・パス逸脱・symlink/hardlink・重複・欠落・過大ファイル・HTTPSからのdowngradeを拒否 |
| 初回のuv準備 | 空の一時HOMEで公式uvを準備し、Go・ソースなしで実CLIの配置計画が成功。既存ホストへ実適用はしない |
| 実Kata | 配布アーカイブを展開してパイプ導入。ソース・Go・事前Proxyイメージなしで初期化とVM起動・削除が成功 |
| 更新・復旧 | 稼働中の更新拒否、正常更新、意図的な起動失敗からの自動復旧、明示rollback成功 |
| 後片付け | VM・コンテナ・task・namespace・専用nftablesの残存0件を記録し、使い捨てノードと専用ボリュームを回収 |
| 公開配布物 | [v0.0.2](https://github.com/pacificbelt30/sandbox-oasis/releases/tag/v0.0.2)の3ファイルを取得。GitHub側digestとSHA256SUMSの一致、公開インストーラーのパイプ実行による実アーカイブ取得・検証・配置計画が成功 |
| CI | Go race/coverage・vet・lint・Python 77件、通常CIの配布物生成、Release workflowが成功 |

- 試験には架空の資格情報を使い、実ログイン・実推論は行っていません。
- 通常CIは配布物の生成まで確認し、公開はタグをpushしたときのRelease workflowで実施します。

## ホストPythonのuv管理を検証（2026-10-23）

- ホストのインストール・更新にはuv管理のPython 3.12を使用します。検証版はuv 0.13.5・Python 3.12.15です。
- ソースからのビルドと、`--bundle` によるビルド済み実行ファイルの持ち込みを分けて確認しました。

| 対象 | 結果 |
|---|---|
| コード検査 | 全Goパッケージのrace/coverage・vet・lint、Python 68件、Linux amd64/arm64・macOS amd64/arm64ビルド成功 |
| uvによる実行 | 実物のuv管理Pythonでバンドル生成・配置計画・仕様基準データのオフライン確認・ドキュメントstrictビルドが成功 |
| ビルド済みバンドル | Go・Make・`python3` がPATHにない状態でも、実物のCLIで配置計画が成功 |
| sudo時の引き継ぎ | PATH・HOMEを変更する模擬sudoでも、選択したPythonの絶対パスを使って同じ配置計画が成功。ホストへ実適用はしない |
| Pythonの分離 | 不正なプロジェクト設定・`.python-version`・`PYTHONPATH`・`PYTHONHOME`を引き継がず成功 |
| 失敗時 | uv不在・オフラインでPython未準備・Python選択失敗・配置計画失敗は適用前に拒否。operator未指定のroot実行はPythonの取得前に拒否 |
| 実Kata配置 | uv管理Pythonを読み取り専用で渡した使い捨てノードで、新規導入・稼働中の更新拒否・VM起動/削除・正常更新・起動失敗からの自動復旧・明示rollbackが成功 |
| 最終回収 | VM・コンテナ・task・namespace・専用nftablesの残存0件を記録。試験ノードと専用ボリュームを回収 |

- この変更はホストの配置処理が対象です。ワーカー機能の標準4スイート・追加16ケースを再実行した記録とは分けて扱います。

## 通し試験（2026-10-23）

- 対象：名称・設定ファイル変更と、独立認証プロファイル／認証プロキシの修正を含む版。
- 実機：使い捨てUbuntu 26.04、KVM、Kata 4.2.0 / Dragonball、containerd 2.4.2、BuildKit 0.34.0。
- 固定CLI：Codex 0.163.0、Claude Code 2.1.306。
- 認証とAI応答：架空の資格情報とローカル模擬API。実サービスへのログイン・推論は実施していません。

| 対象 | 結果 |
|---|---|
| Go全パッケージ | race・coverage付きテスト、vet成功。lintは0件 |
| ビルド | Linux amd64/arm64、macOS amd64/arm64、node bundle成功 |
| 配置・補助スクリプト | Python 65件成功、上流仕様の基準データをオフライン検証 |
| Proxy | HTTP/CONNECT・通信制限など12件成功 |
| 固定CLIと認証 | Codexの初回認証／更新時の資格情報非返却、ClaudeのAPI／OAuthモード成功 |
| 独立プロファイル | 実CLI＋ローカルProxyで0600/0700、プロファイル分離、プレースホルダー、失効を確認 |
| 導入・設定・ドキュメント | CLI名称、補完、install/update/uninstall、設定ファイル移行、保護設定の保持、出力例、strictビルド成功 |
| VM受入 | 成功（225.83秒）。別kernel、Codexのツール実行、通信隔離、停止・再起動・削除、孤児回収、namespace喪失からの復旧 |
| 共有 | 9ケース成功（162.73秒）。UID/GID・0600・setgid・readonly・追加mount・worktree・双方向更新・ポーリング |
| 追加制御 | 成功（47.33秒）。mask・readonly・起動hook・readiness・ファイル転送・exec取消・PTY・背景ジョブ・ログ保持 |
| 標準Go API | 成功（73.91秒）。small/standard、再送・再接続・競合拒否、入力EOF・exec取消、資格情報非残留、ケース分離・失効・削除 |
| 追加Go API | 16ケース成功（766.89秒）。固定Codex／Claude、compact、idle回収・実行失効、OAuth保存失敗・版競合・Proxy再起動、一時領域、組み込み例、既定拒否・完全一致、通信中の隔離 |
| 回収処理の追試 | 通信完全一致・隔離の2ケースを再実行して成功（117.60秒）。未使用の共通egressネットワーク1件だけが残ることを記録し、公開CLIで回収 |
| 新規導入・更新・復旧 | 追試成功。新規setup、稼働中の更新拒否、正常更新、意図的な起動失敗からの自動復旧、明示rollback、稼働確認 |
| 最終回収 | VM・コンテナ・task・network namespace・専用nftablesの残存0件を一覧で確認。使い捨てノードと専用ボリュームも回収 |

**見つかった問題と修正**

- 稼働中の更新を拒否して管理サービスを再接続した後、VM削除時にKataのcgroup削除が `EBUSY` になる問題を再現しました。
- 検証済みのVMM管理プロセスを、ユーザーコード開始前・再接続時・停止待ちの前に、同じ資源上限内の別cgroupへ移すよう修正しました。
- 停止順序と、所有権の検証に失敗した場合に停止を進めない回帰試験を追加しています。
- 古い試験用ワーカーを検出して現行entrypoint／固定CLIへ更新し、イメージの不一致を試験前に拒否するチェックを追加しました。
- 試験用のnamespace故障注入を修正し、削除後の再作成・inode変更の証跡を返すようにしました。実機で証跡の検査まで成功しています。
- 標準4スイートと追加16ケースは成功しましたが、結合実行の最後の残存チェックが失敗しました。追加API試験が再利用用に保持する共通egressネットワークを、使い捨てノードの試験側で回収する処理を追加しています。想定外のネットワークや使用中の資源は削除せず失敗させ、最終一覧を証跡に残します。

- 回収処理の変更後は、通信完全一致・隔離の2ケースと導入・更新・復旧・最終回収を再実行しました。標準4スイートと追加16ケースの成功記録は、その直前の結合実行の結果です。
- 既存のnode・containerd・BuildKitは開始時と同じPIDで稼働し、既存nodeのhealth応答も成功しています。

**今回の対象外**

- 実OAuthサービスでのログイン・長期間の更新、実OpenAI／Anthropicでの推論。
- TUIの対話キーボード操作、WSL／他のVM製品の共有filesystem固有の動作。
- 任意のカスタムプロファイルの追加試験。組み込みプロファイルの試験とは分けて扱います。

## 一般通信の既定拒否への変更（2026-10-22）

- CLI・TUIの未設定値とGo APIのゼロ値を既定拒否へ変更しました。
- 全Goテストのrace/coverage、build、vet、lint、MkDocs strictビルドが成功しています。
- `scripts/smoke-proxy.sh` は12項目成功し、明示的な宛先許可でのHTTP/CONNECT転送、private IP拒否、未設定時の存在しないホスト名・IP直指定に対するHTTP/CONNECTの403を確認しました。

- 既存nodeとの版不一致を解消するため、専用のnode・containerd・BuildKit・cgroup・guestを用意し、CLI・node・proxyを `VERSION=dev` で揃えました。
- `make kata-e2e VERSION=dev` の標準4スイートは432.964秒で成功しました。
- VM受入174.58秒、共有9ケース158.43秒、追加制御39.38秒、標準Go API60.56秒です。
- 追加したCLI試験は、設定省略時の一般egress無効化、default routeの不在、外部IPへの直接接続失敗、authへの到達を確認します。
- authの接続先は、entrypointが消去する一時環境変数ではなく、保存されたワーカー設定から取得します。

- 追加の実Kata Go API受入は4ケースすべて成功しました（167.740秒）。
- `egress-zero-value` はゼロ値のホスト設定、`egress-default-deny` は明示設定で一般通信が無効になること、`egress-exact` は子ドメインへのHTTP/CONNECTが403になることを確認します。
- 各ケースで固定AI経路の認可と許可済み要求の成功も検査します。
- `quarantine` は通信中の対象VMのHTTP/CONNECT・AIジョブの終了、共有proxyを使う別VMの継続、再起動拒否、別ポリシーでの再作成とworkspace保持を確認しました。
- 追加のnode再起動・namespace喪失の故障注入は今回は指定していません。

- 取消試験は、添付stdinを起動前に閉じるとKataのpassfd初期化と競合する条件を避け、入力pipeを保持したままcontextを取り消す形に修正しました。
- 検討したI/O転送方式の変更案では256 KiB入力の欠落を検出したため採用していません。
- 今回の実装は実行エンジンのI/O方式を変更しません。

- 試験終了後、専用環境のコンテナ・ネットワーク・taskが0件であることを確認してから、node・containerd・BuildKit・mount・cgroup・socket・状態領域を回収しました。
- 既存3サービスは開始時と同じPIDで稼働し、元nodeのhealth応答は200です。

## コードを確認する

- [開発用ツール](development/toolchain.md)を準備。Goは[公式Linux手順](https://go.dev/doc/install)で1.26.0以上を導入します。
- race検査にはCコンパイラーも必要です。

```bash
go test ./...
make test
make vet
gofmt -l cmd internal pkg
```

- `make test` はrace検査とcoverageを有効にします。
- 通常のプロキシテストはローカルのHTTP fixtureを使うため、実APIキーは不要です。

| 対象 | 主な確認 |
|---|---|
| 認証ファイル | 所有者UID/GID、0600、読み取り専用mount、読み取り不能時の拒否 |
| ワーカートークン | 全AI経路のHTTP/WSで欠落・未発行・期限切れ・失効・重複・不一致を拒否 |
| 接続の寿命 | 期限切れ・失効・再発行・restore・プロキシ停止時にWebSocketを閉じる |
| 認証注入 | OpenAI / AnthropicのAPIキー・OAuth、プレースホルダーの除去 |
| ストリーミング | SSEの逐次受信、WebSocketの転送 |
| 通信ポリシー | ドメイン・IP・ポート、DNS応答、redirect、CONNECTの判定 |
| 実行管理 | 資産・cgroup・所有権検証、起動失敗の回収、VM一覧、削除の再試行 |
| 製品組み込み用 Go API | 公開エラー分類と要求IDの入力検査、node台帳での競合・再送・応答消失時の照合と回収、通信許可の入力検査 |
| ケース別認証 | 資格情報ソケットの権限とケース分離、OAuth更新の版競合、ケース変更時の失効、プロバイダー経路と実行IDの失効 |

## CodexのIDトークン分離

- 2026-10-22の修正では、初回`/token`とOAuth更新から実ID tokenが返る経路を、架空資格情報による回帰試験で再現してから閉じました。
- 通常のGo試験で、実access/refresh/ID tokenの非返却、任意のclaim・追加フィールド・エラー本文・通信エラーからの漏出防止、ホストでの実トークン更新、既知の更新エラー分類を確認します。

- 固定Codex 0.163.0を指定する追加試験も用意しています。
- `--version`で固定版を照合し、実際のworker entrypointとProxy listenerを使って、初回ログイン読み込みと、古い`last_refresh`によるCLI自身の更新からHTTP/SSE最終応答までを確認します。

```bash
CXD_TEST_CODEX_BINARY=/path/to/codex-0.163.0 \
  go test ./internal/authproxy -run '^TestCodexCLIWorkerCredentialPrivacy$' -count=1 -v
```

- この固定CLI試験は、ホストの認証ファイルや資格情報の環境変数を継承せず、ローカルOAuth／Responses fixtureだけを使います。
- CLI出力、Proxyのverboseログ、生成されたHOME配下のファイル（`auth.json`を含む）に架空の実トークンが残らないことと、更新後もaccess欄がplaceholder・refresh欄が空文字であることを検査します。
- email・user IDなどの未選択claimも渡しません。

独立FileStoreを使用し、OAuth更新後の実access/refresh/ID値が0600ファイルへ保存されること、別のProxyインスタンスがその保存結果を読み直せることも確認します。

- 両ケースは実行してPASSしています。
- このID token漏出の専用試験は、ホスト上のCLI／起動スクリプト結合試験です。実Kataでの一般動作は本ページ冒頭の通し試験で確認し、実OAuthサービスによる更新は対象外としています。
- 通常のCIではCLIバイナリを指定しない追加試験だけがskipされ、資格情報境界の回帰試験は実行されます。

## 独立プロファイルの受入

- 名称変更後の実CLI受入は `uv run --no-project --no-config --managed-python --python 3.12 python scripts/verify-cli-names.py --binary ./oaiso` で確認できます。
- `make build` 後、`oaiso` のヘルプ・版表示、Bash/Zsh/Fish/PowerShell補完、主要コマンド、既存の `~/.config/codex-dock/` に置いた架空の認証profileの読み込みを別HOMEで試験します。
- 旧コマンドの生成は廃止。セットアップの試験では、旧版の導入記録と内容が一致するファイルだけの回収、無関係な同名ファイルの保持、失敗時の復元も確認します。
- node・外部APIは使いません。
- インストーラーのPython試験では、新しいコマンド・補完の管理外ファイルを上書きしないこととrollback対象への登録も確認します。

- 2026-10-22の架空資格情報試験では、FileStoreの適合性・別プロセス間の同時更新CAS、0700/0600、symlink/hardlink拒否、保存失敗、CLI作成/import/メタデータ、profile専用mount、workspaceとの重複拒否、旧workerの別profile・別接続世代への再認可拒否を確認します。
- Proxy試験はOpenAI API/OAuth・Anthropic API/OAuthの4モードで、ホスト認証を無視すること、選択した資格情報の上流注入、workerへの非返却、失効を確認します。

- 実CLIプロセスとローカルProxy listenerのスモーク試験も用意しています。
- 管理secretは0600ファイルを使い、外部API・login・推論要求は送りません。

```bash
go build -o /private/path/oaiso .
uv run --no-project --no-config --managed-python --python 3.12 python scripts/verify-auth-profiles.py --binary /private/path/oaiso
```

実providerの認証成功、専用login Kata、WSLのDrvFS／VM共有filesystemの保証を確認する試験ではありません。

## Go APIの追加受入

- [Go API](go-api.md)の実機受入 `TestKataGoAPI` は `make kata-e2e` に含まれます。
- ケース別の架空資格情報でOpen → Launch → Exec → Stop → RemoveRequest、small/standardの別boot ID・ゲスト制限・ホスト予約・削除後の割当回収、取消済みLaunch context、Execの入力EOF（stdin未接続も含む）・実行中取消とプロセス回収、要求IDの再送・競合・削除後の再送、再接続後の照合、VMへの資格情報漏れ、ケース間のトークン拒否、実行IDの失効を確認します。

- 追加の障害系受入 `TestKataAPIIntegration` には別ターゲット `make kata-api-e2e` を用意しています。
- 実行中のLaunch取消、OAuth更新・保存失敗・版競合・proxy再起動、通信中ストリームの失効などの対象は[公開Go APIの障害系受入](#公開go-apiの障害系受入)を参照してください。
- 応答を通信路で破棄する故障注入とGo API経由のワーカー別通信許可は、両ターゲットともまだ含みません。
- 単体テストと実Kata受入結果を区別し、成功記録がある版・範囲を確認してください。

## 実Kataで確認する

- [Kataの配置](kata.md)を終えた専用Linuxノードで実行します。
- 試験はVM・イメージ・ネットワークを作成します。
- 既存作業と分けて実行してください。

```bash
make kata-e2e
```

- このターゲットはCLIとhelperをビルドし、`CODEX_DOCK_KATA_E2E=1`・`CODEX_DOCK_KATA_POLICY_E2E=1` と対象CLIのパスを指定して、`TestKataAcceptance`・`TestKataWorkspaceSharing`・`TestKataPolicyFeatures`・`TestKataGoAPI` を実行します。
- 通常利用者として実行してください。
- 共有試験は別HOMEと架空のAPIキーを使い、実認証ファイルをマウントしません。
- 前提条件を満たさない場合は、skipではなく失敗として扱います。
- 通常の `go test ./...` はこの実機試験を有効にしません。

- 主に、別kernel、guest権限・資源制限、ホストVM資源制限、Git、readonly/追加mount、PTY、通信遮断、停止・同一IDでの再起動・削除・孤児回収を確認します。
- カスタムDockerfileのビルドとOCI import、管理サービス再起動中のVM維持・連続ログ、ローカルResponses fixtureを使うCodexのツール実行も対象です。

- [共有試験](workspace.md#検証する)では、CLIのカレントディレクトリ、双方向のファイル操作、開いたままのファイル、UID/GID、権限、読み取り専用、追加マウント、worktree、再起動も確認します。
- ホスト由来のinotify通知とNodeのポーリング監視も区別して確認します。

- 別所有者のディレクトリとsetgidも確認する場合は、専用fixtureを追加します。
- 親は通常利用者だけが開ける一時ディレクトリとし、試験対象だけに別UID/GIDを設定します。

```bash
owner_fixture=$(mktemp -d)
sudo install -d -o 12001 -g 12002 -m 0777 "$owner_fixture/project"
sudo install -o 12001 -g 12002 -m 0600 /dev/null "$owner_fixture/project/foreign-private"
sudo install -d -o 12001 -g 12002 -m 2777 "$owner_fixture/project/setgid"
CODEX_DOCK_KATA_OWNER_FIXTURE="$owner_fixture/project" make kata-e2e
```

- 終わったら、その一時ディレクトリだけを削除してください。
- fixtureを指定しない場合も、明示UIDの試験は実行します。
- `--user dir` の対象は呼出元所有の一時ディレクトリになります。

## 実AIとの通信を確認する

- 実APIとの通信は、認証情報と外部APIの利用権限が必要な別試験です。
- 認証情報をDockerfileやイメージに含めず、ホストから認証プロキシにだけ渡します。
- 結果を確認するときは、単なるHTTP 200と、完了したAI応答を区別してください。

| 通信方式 | 成功条件 |
|---|---|
| WebSocket | トンネル確立と、期待した最終応答 |
| HTTP SSE | 明示的なHTTP POST、応答イベント、完了イベント、期待した最終応答 |
| 不正認証 | プロキシ側の401と、上流へ接続しないこと |

- Codexの設定でWebSocketを無効にしたつもりでも、使用版によってはWebSocket経路が使われます。
- SSEの確認では、実際のHTTP経路とイベントを照合します。

## 確認済みの範囲

- 2026-10-17時点の記録です。
- 試験環境はcontainerd 2.4.2、Kata 4.2.0 runtime-rs、Dragonball、BuildKit 0.34.0です。

| 試験 | 結果 |
|---|---|
| 実Kata受入 | 全項目PASS。共有設定変更後も再実行し、ワーカーの機能・隔離・ライフサイクルを確認 |
| 共有・所有者の実Kata受入 | 7ケースPASS。既定1000:1000、明示・dir指定12001:12002、0600拒否、setgid、双方向更新、開いたままのファイル、readonly、worktree、再起動 |
| ホストからの変更監視 | inotify通知は届かない。VM内の通知を対照に確認し、NodeのポーリングはPASS |
| 共有修正の回帰 | 全Goテスト、sandbox / kataguard / cmdのrace、vet、macOS arm64クロスビルド、設定生成テスト成功 |
| 認証修正の回帰 | 全Goテスト、race、vet、macOS arm64クロスビルド成功 |
| 既存auth.jsonによる実推論 | Codex 0.163.0でWebSocketと明示的HTTP SSEの最終応答を確認 |
| 認証情報の分離 | access欄はplaceholder、refresh欄は空文字、元auth.jsonは変更なし。当時残っていた実ID token返却は[後続修正](#codexのidトークン分離)で除去 |
| 後片付け | 試験VM・ネットワーク・一時ルール・専用状態を回収 |

- 実機受入の一部は、外側にDockerを使った使い捨て環境で実施しました。
- その内部の実行ノードではDocker daemonやDocker CLIを起動していません。
- 実auth.jsonの試験と今回の共有修正後の両受入試験は、専用のネイティブcontainerdで実施しました。
- 既存Docker/containerdサービスは変更していません。
- 共有試験には架空の認証情報だけを使いました。

- 今回の実通信試験には、長期間の実OAuth更新、実Anthropic認証による推論、対話キーボード操作を含めていません。
- Anthropicの認証・HTTP/WS転送とOAuth更新はfixtureで確認しています。

- 旧 `scripts/e2e-docker.sh` は移行前のDocker経路用です。
- 現行構成の受入には `make kata-e2e` を使います。

## 2026-10-19の実KVM追試

- 専用の使い捨てUbuntu 26.04ノードで、Kata 4.2.0 / Dragonball、containerd 2.4.2、BuildKit 0.34.0、runc 1.4.3を実KVM上で実行しました。
- 外側のDockerはノードの隔離用で、内部は製品専用containerdです。
- 実API資格情報は使わず、ケース別の架空資格情報を使いました。

| 試験 | 結果 |
|---|---|
| 既存実Kata受入 | PASS。別boot ID、Codexの実ツール実行、Git・PTY・通信分離・ホスト/管理ポート拒否、停止・再起動・削除・孤児回収 |
| 共有・所有者 | 7ケースPASS。readonly/追加mount、明示UID/GID、setgid、worktree、双方向変更、再起動、Nodeのポーリング |
| Go API | PASS。Open → Launch → Exec → Stop → RemoveRequest、再送・再接続照合・競合拒否、削除再試行、入力EOF、実行中Exec取消とプロセス回収 |
| ケース別認証・資源 | PASS。ゲストの既知の認証保存先・環境に元資格情報なし、ケース間トークン拒否、実行ID失効、small/standardのゲスト制限・ホスト予約・割当回収 |
| 配置・更新・復旧 | PASS。新規配置、稼働中更新拒否、正常更新時のイメージ/ファイル保持、新サービス起動失敗からの自動復旧、明示rollback、残存資源なし |
| コード検査 | 全Goテスト、vet、影響範囲のrace、配置Pythonテスト14件成功 |

- 実機で検出した要求ID照合のcontainerd namespace不足、入力EOFによるExec取消、削除済みworkerのNotFound分類欠落を修正しました。
- proxy Dockerfileのprotocol/buildラベルも現行APIと一致させました。

- 初回にはCodex fixtureの45秒タイムアウトとSSE単体試験の一度の失敗があり、再試験は成功しました。
- SSEは単独5回でも成功しています。
- 最終結果と初回の失敗を区別してください。

- この追試は冒頭のGo API受入範囲です。
- 実行中Launch取消・通信路での応答破棄、実Kata経由のOAuth更新/保存失敗/版競合/認証proxy再起動、通信中ストリームの失効、Go APIのワーカー別ドメイン許可を含む全要件の合格判定はまだ完了していません。

## ドキュメントを確認する

uvの分離した環境で依存を準備し、strictビルドを実行します。

```bash
uv run --no-project --no-config --managed-python --python 3.12 \
  --with-requirements requirements-docs.txt mkdocs build --strict
```

- ページ・見出しへのリンク切れ、ナビゲーションへの掲載漏れをエラーとして確認します。
- プレビューは同じ `uv run` の末尾を `mkdocs serve` に変更します。

## 新規導入・更新・復旧を検証する

- 専用のLinux/KVMホストから `scripts/verify-setup.py` を実行します。
- 外側の使い捨てUbuntuを作るためだけにDockerを使い、内部の製品はネイティブcontainerd/Kata/Dragonballで実行します。
- 既存の通常CIはroot権限を使わない設定生成・配置・復旧の単体テストだけを実行します。

- `deploy/kata/Dockerfile.verify` はsystemdとOS依存パッケージを準備します。
- `setup` 本体が固定配布物を展開して、専用サービスを新規導入します。
- 内部ノードへホストPID、ホストDockerソケット、実認証情報は渡しません。既定は外部ネットワークも切断します。
- containerdのデータは、イメージ展開に必要なwhiteoutを扱える専用ボリュームへ配置します。

- ソースのルートで、Go・Make・uv・Dockerを使って試験資産を作成します。
- イメージのビルドにproxy/CAが必要な環境では、[Kataの通信設定](kata.md#3-ネットワークとサービスを有効にする)と同様に環境を整えてください。

```bash
make node-bundle VERSION=setup-test-a
mv dist/node-bundle dist/node-bundle-a
make node-bundle VERSION=setup-test-b
mv dist/node-bundle dist/node-bundle-b
mkdir -p .kata-verification/setup-e2e
CGO_ENABLED=0 go test -c -ldflags "-X github.com/pacificbelt30/codex-dock/internal/buildinfo.Version=setup-test-a" ./internal/sandbox -o .kata-verification/setup-e2e/acceptance.test
docker build -t codex-dock-setup-node:verification -f deploy/kata/Dockerfile.verify deploy/kata
docker build -t codex-dock:kata-guard docker/sandbox
docker build --build-arg CODEX_DOCK_BUILD_VERSION=setup-test-a -t codex-dock-proxy:policy-verification -f docker/proxy/Dockerfile .
docker save -o .kata-verification/setup-e2e/images.tar codex-dock:kata-guard codex-dock-proxy:policy-verification
CODEX_DOCK_DISPOSABLE_SETUP=1 uv run --no-project --no-config --managed-python --python 3.12 python scripts/verify-setup.py \
  --bundle dist/node-bundle-a --next-bundle dist/node-bundle-b \
  --images .kata-verification/setup-e2e/images.tar \
  --acceptance .kata-verification/setup-e2e/acceptance.test
```

- 別名の試験用イメージを使う場合は、`--worker-image TAG --proxy-image TAG` を指定し、両方を `--images` のアーカイブに含めます。
- uvで準備したstandalone Pythonを配置処理にも使う場合は、`--setup-python "$(uv python find --no-project --no-config --managed-python --system 3.12)"` を指定します。試験ノードにはそのPythonの配置ディレクトリだけを読み取り専用で渡します。
- ワーカーのentrypointが試験するCLIと一致しない場合は、受入試験を始める前に失敗します。現在のソースからイメージを作り直してください。
- Go APIだけを再試験する場合は `--acceptance-run '^TestKataGoAPI$'` を指定できます。
- 既定では8つの受入試験（VM・認証専用VM・暗号化・キーリング・共有・追加制御・前面TTY・Go API）を実行します。

- 必要な場合だけ `--sudo-docker` を付けます。
- 配布物を準備済みなら `--cache PATH --offline` で検証できます。
- スクリプトはホストの既存サービスを変更せず、自分が作ったラベル付きノードと専用ボリュームを最後に回収します。
- 証跡は `.kata-verification/setup-e2e/<識別子>/` に残します。

- 確認範囲は、計画時にサービスを作らないこと、新規配置、通常利用者の `init`、実Kata受入、稼働資源がある場合の更新拒否、正常更新時のイメージ保持、新サービスの起動失敗からの自動復旧、明示的なロールバックです。
- 既定の試験には、OSパッケージのネットワーク取得、ホスト再起動、外部AI推論を含みません。

### 新規HOMEから初回利用までを追加確認する

- 上の呼出しに`--online-onboarding`を追加します。使い捨てノードだけを外部通信可能にし、事前ロード前に標準ワーカーイメージを新規ビルドします。
- Codex／Claude Codeそれぞれ別の空のHOMEを用意し、実Secret Serviceの解錠→既定login→プロファイル／管理シークレットの自動設定→`init`→固定CLIの起動→前面TTYの入出力→workspace書込み→削除を確認します。
- 認証情報は架空です。APIの接続確認を省略し、実アカウントへのログインや推論は実行しません。
- OS依存パッケージは試験用Ubuntuイメージで準備します。ランタイム配布物には検証済みキャッシュを利用でき、`--offline`と併用した場合もワーカーの新規ビルドには通信が必要です。
- 企業プロキシ環境では、既存のproxy/CA設定を使い捨てノードとそのBuildKitに設定します。ホストのサービスは変更しません。
- `--release-installer PATH --setup-python PATH --uv PATH`も付けると、ビルド済みバンドルの導入を`cat install.sh | bash`の形で確認します。公開済み`releases/latest`の版を変更する操作ではありません。

## 追加制御の検証（2026-10-19）

- P0/P1の追加後に、既存サービスとは別のcontainerd・BuildKit・Kataノード・cgroup・状態領域で検証しました。
- 架空の認証情報だけを使用しています。

- `make build`、全パッケージの `make test`（race/coverage）、`make vet`、`make lint`、`mkdocs build --strict` が成功。
- macOS arm64のクロスビルドと、配置・導入スクリプトのPythonテストが成功。
- `make kata-e2e` が成功。
- 既存のVM隔離・ツール実行・ノード再起動・認証・通信・停止・削除と、共有/所有者の既存7ケースを確認。
- 追加の実機試験で、Kataの固定IPから選択済みプロキシと実効policy IDを特定できることを確認。
- `TestKataPolicyFeatures` で、mask・個別readonly・rootfsのread-only mount flag、hook/readiness、symlinkを拒否するファイル送受信、exec環境変数/作業ディレクトリ/取消、PTYリサイズ、背景ジョブ、ノード再起動後のジョブとログの回収を確認。
- Go APIの新しい設定を使わない場合、既存要求IDのfingerprint入力が旧版と一致することをgolden testで確認。

- [追加制御の一覧と運用条件](sandbox-features.md)も参照してください。
- 寿命・保持期限の適用には `reap --watch` または定期的な `Reap` 呼出しが必要です。
- 実APIキーを使う外部AI推論と、実SSH/JDKサービスへの接続は今回の受入試験には含めていません。
- 認証brokerの経路制限・失効・redirect、独自CA・多段proxyはローカルfixtureで検証しています。

## 公開Go APIの障害系受入

- 対応するCLI/node、ワーカーイメージ、ベースproxyイメージ、standalone BuildKitを配置した専用Linux/KVMホストで `make kata-api-e2e` を実行します。
- `CODEX_DOCK_KATA_NODE_SOCKET` と `BUILDKIT_HOST` で対象を明示できます。
- `CODEX_DOCK_KATA_TEST_IMAGE` と `CODEX_DOCK_KATA_PROXY_IMAGE` は利用するベースイメージを選びます。
- ビルド版はnodeと一致させてください（例：`make kata-api-e2e VERSION=dev`）。

- スクリプトは `internal/authproxy` のテスト実行ファイルを入れた一意のfixtureイメージを作ります。
- 本物のproxy handler・Unix credential adapterを通し、上流だけをプロキシ内の架空HTTPサーバーへ差し替えます。
- 実APIキーや外部AI推論は使いません。
- OAuth更新の実Kata fixtureはOpenAI型に加え、[Claude Codeの受入](#claude-codeの受入)でAnthropic型も確認します。
- 外部の実Anthropic接続は含みません。
- fixtureイメージを運用に使わないでください。
- ワーカーとケースproxyを最後に削除し、スクリプト所有のfixtureイメージもテストが回収します。
- BuildKit cacheとホスト共通egress networkは通常のビルド・ノード管理に従います。

**`TestKataAPIIntegration` の試験対象**

| 分類 | 確認する動作 |
|---|---|
| 作成・再送 | Create再送とLaunchとの衝突。起動前にhookが動かないこと |
| 起動完了 | Start後のreadiness。hook中の同一Launch再送がpendingを保持 |
| 資源 | small/compactのホストcurrent・peak・OOM。compact 2台の同時稼働 |
| モデル認可 | 実要求でのモデル拒否 |
| idle回収 | 実行中ジョブ・streamを回収対象から除外 |
| 実行失効 | RevokeRunが通信中streamを終了し、再利用を拒否 |
| 取消 | Launch取消後の要求台帳・資源回収 |
| OAuth障害 | 保存失敗・版競合での失効 |
| OAuth正常更新 | Proxy再起動後に保存済み世代を再取得 |
| トークンの再開 | 古いtoken拒否。停止・再起動での再有効化 |

追加で、固定Codex 0.163.0を生成設定なしの明示HTTP/SSEで呼ぶ `codex-http`、容量超過時のENOSPC・所有者・exec/noexec・node再起動時の保持・VM停止時の消去を調べる `temporary-storage`、[公開APIの組み込み例](go-api.md#実行できる組み込み例)を実際にビルドして実行する `embedding-example` を含みます。

- 絞り込みは `uv run --no-project --no-config --managed-python --python 3.12 python scripts/verify-go-api.py --binary ./oaiso --version <nodeと同じ版> --run '^TestKataAPIIntegration/temporary-storage$'` のように指定します。
- 対象が0件、skipだけ、または途中失敗の場合は検証成功にしません。
- 登録プロファイルも試す場合は `CODEX_DOCK_KATA_CUSTOM_PROFILE=<登録名>` を設定してください。
- `custom-profile` が実際のguest CPU・メモリ・PID上限とnode/VM再起動後の値を確認します。
- 標準ケースの追加機能を使うため、対応するnodeと固定bootstrapを配置してください。

- `CODEX_DOCK_NODE_RESTART_REQUEST_DIR` を設定すると、ホスト側supervisorとの追加連携を有効にします。
- 試験はこのディレクトリの古い `complete` を削除し、`request` に `restart` または `namespace <完全なcontainer ID>` を書きます。
- supervisorは対象nodeだけを再起動し、後者では停止済みの所有権確認済みnamespaceを失わせてから復旧させ、nodeが再接続可能になった後だけ `complete` を作成してください。

- 通常ユーザーからroot専用の `/run/netns` を開けるようには変更しません。
- `namespace` の完了には、root supervisorが観測した次のJSONを返します。
- 実際のIDとdevice/inode値を使用し、`removed` はnamespaceのパス消失を確認してからtrueにします。
- 試験は対象ID、消失、変更前後のinodeの相違を照合し、単なる `ready` や再起動だけの報告を拒否します。
- 書きかけの応答を読ませないよう、完了内容は一時ファイルに書いてから `complete` へatomic renameしてください。
- 通常の `restart` の完了内容は `ready` のままで構いません。

```json
{"operation":"namespace","container_id":"<完全なID>","removed":true,"before":{"device":5,"inode":101},"after":{"device":5,"inode":102}}
```

- この環境変数を省略した場合、node再起動・namespace喪失の追加検証を実施したとは扱いません。
- 通常のAPI・VM停止/再起動試験は引き続き実行します。

## 製品組み込み改善の検証（2026-10-20）

- 既存サービスから分離したcontainerd・BuildKit・Kataノード・cgroupで、`make kata-e2e VERSION=dev`（当時の対象はAcceptance・WorkspaceSharing・PolicyFeatures、400.698秒）と `make kata-api-e2e VERSION=dev`（受入本体228.339秒）が成功しました。
- 後者はcompact・lifecycle・保存失敗・版競合・proxy再起動の5ケースすべてが成功し、参照中イメージの削除拒否と、最後の試験用イメージ削除・参照消失まで確認しています。
- 取消後のinventoryで、余分なVM・ホスト割当・実プロセスが残らないことも確認しました。

| compactの観測 | ホストcurrent | ホストpeak | 条件 |
|---|---:|---:|---|
| 起動後の待機 | 144850944 B（約138 MiB） | 144850944 B | guestワーカー上限512 MiB、ホスト上限768 MiB |
| 256 MiB確保ジョブ実行中 | 424894464 B（約405 MiB） | 425246720 B（約406 MiB） | 別のcompact VMも同時稼働 |
| 同時稼働の2台目 | 156971008 B（約150 MiB） | 157384704 B | 待機中 |

- 両VMでguestの `memory.max=536870912` とguest/hostのOOMカウンター0を確認し、負荷ジョブが意図的な停止まで動き続けることも確認しました。
- 同一ホスト・架空HTTP上流での限定した負荷試験です。
- 一般的な依存導入・大規模ビルド・実AI CLI負荷の必要量を保証しません。
- standard/smallの既存上限と既定値は維持しています。

- `make build`、全パッケージrace/coverageテスト、vet、lint、macOS arm64クロスビルド、ドキュメントstrictビルドも成功しました。
- 公開APIの要求台帳照合に必要なcontainerd namespaceを補い、OAuth保存先の一般エラーと空の保存版でもケースと通信中leaseを失効させることを試験しています。
- 運用条件とモデル制限時の対応経路は[追加制御](sandbox-features.md)を参照してください。
- 実認証情報・外部AI推論・systemd user unitの常駐稼働はこの受入には含めず、既存サービスへの更新適用もしていません。

- 検証終了後、専用ワーカー・proxy・ネットワークが0件であることを確認し、検証用containerd・BuildKit・ノード・cgroup・socket・状態領域を回収しました。
- 既存containerdとノードの稼働は継続しています。

### リモート統合後の追試

- 上記の改善とGo API実機受入の追加を統合した版で、`make kata-e2e VERSION=dev` のAcceptance（184.97秒）・WorkspaceSharing（8ケース、141.79秒）・PolicyFeatures（46.36秒）が成功しました。
- GoAPIは初回に試験用StateDirの権限チェックで失敗したため、保存先を既存の0700 fixtureディレクトリ配下へ変更し、`TestKataGoAPI` だけを再実行して成功しました（70.319秒）。
- 製品の権限チェックは維持しています。

- 統合版の `make kata-api-e2e VERSION=dev` も5ケースすべて成功しました（223.575秒）。
- 全Goパッケージのrace/coverage・vet・lint、配置Pythonテスト14件、補助スクリプトテスト2件、macOS arm64クロスビルドも成功しています。
- 試験用コンテナ・タスク・ネットワークが0件であることを確認してから、専用containerd・BuildKit・ノードと状態領域を回収しました。

### 競合関連のロジックレビューと修正

- `8cd98f2` の両親との差分を照合し、`FindRequest` のcontainerd namespace固定・排他制御と、NotFoundの分類・伝播が維持されていることを確認しました。
- 呼出元と関連処理を調べ、従来の受入では確認していなかった次の2件を修正しました。

- stdin未接続のExecに送信EOFだけを通知すると、出力前に取消されました。
- 修正前の実Kata試験は終了125・`context canceled` で失敗し、修正後は同じ試験で出力と終了0を確認しました。
- 完全切断の検知と明示的な取消は維持しています。
- 起動中に同じ要求を再送すると、作成済みワーカーの照合が有効なleaseより先に走り、台帳を早期に完了扱いにしていました。
- 修正前に単体回帰試験の失敗を確認し、有効なleaseを先に検査するよう変更しました。
- 元の起動による確定、lease失効後の照合、完了済み結果の再送を単体試験で確認し、実Kataではhook中の再送が `ErrInProgress`・`pending` を保持し、その後の取消と資源回収まで成功しました。
- 起動元消失時の最大5分の待機は[Go API](go-api.md#再送と復旧)に明記しています。

- Exec修正後の `make kata-e2e VERSION=dev` は4スイートすべて成功しました（463.303秒）。
- lease修正後は影響する `TestKataGoAPI`（75.195秒）と、拡張した `make kata-api-e2e VERSION=dev` の5ケース（219.455秒）を再実行して成功しています。
- 最終コードで全Goパッケージのrace/coverage・vet・lintも成功しました。
- 試験用コンテナ・タスク・ネットワークが0件であることを確認し、専用ノード・containerd・BuildKit・cgroup・状態領域を回収しました。
- 既存サービスへの更新適用と外部AIへの実推論は今回の確認に含みません。

## 汎用組み込み機能の検証（2026-10-21）

- 既存サービスから分離したネイティブcontainerd・BuildKit・Kata 4.2.0 / Dragonballで、公開Go APIの9ケースを確認しました。
- 上流は架空HTTPサーバーで、実際のCodex CLIとVM・Proxy・Unix Credential Store Adapterを使っています。
- 外部AIへの推論と実資格情報は使いません。

| 対象 | 確認結果 |
|---|---|
| 実行ごとのモデル認可 | 同じケースProxyの別トークンを区別し、Proxy全体との積集合・全拒否・VM再起動後の復元を確認 |
| 明示HTTP/SSE | Codex 0.163.0を空のCODEX_HOMEで呼び、生成設定に依存しない完了応答を確認。stdin未接続のEOFも確認 |
| 登録プロファイル | CPU 2、guest上限512MiB、host上限768MiB、PID 256を実guestで確認。node/VM再起動後の値も一致 |
| 一時領域 | 8MiB/1MiB tmpfsの上限・ENOSPC・UID/GID・0700・exec/noexec、node再起動時の保持、VM停止後の消去を確認 |
| 認証と回収 | OAuth保存失敗・版競合での失効、正常更新とProxy再起動後の再取得、実行中streamの失効、起動途中の取消と割当回収を確認 |
| 組み込み例 | 同じ実行ファイルでCreate → Start → 最初の出力でExec取消 → 別Exec → Codex HTTP/SSE → Stop → RemoveRequestを確認 |

- `codex-http`、`compact`、`save-failure`、`conflict`、`restart`、`temporary-storage`、`custom-profile` の7ケースが成功し、`embedding-example` は59.824秒で成功しました。
- `lifecycle` は初回、失効したstreamの `http.client.IncompleteRead` を試験用クライアントが処理せず失敗しました。
- 期待する途中切断として処理し、失効前にはstreamが継続していることも検査して再実行した結果、66.108秒で成功しました。

- 回帰確認でHTTP/1の自動request-body drainと上流の送信writerの競合によるSSE欠落を再現し、stream転送時のfull-duplexを明示して修正しました。
- 修正後のSSE反復3,000回と、upload完了前に応答が始まる回帰試験が成功しています。
- 上流の異常切断も正常EOFへ変換せず、OpenAI・Anthropic・credential broker・egressの4経路で通信エラーとして検出する試験が成功しました。
- HTTP/SSEの呼び出し側は、切断や読取りエラーと、AIの完了イベントを区別してください。

- 全Goパッケージのrace/coverage・vet・lint、配置Pythonテスト18件と補助スクリプトテスト5件、macOS arm64/amd64・Linux arm64のクロスビルドが成功しました。
- クロスビルドはそのOSでのKata実行を保証しません。
- `go mod tidy -diff` に差分はありません。
- Credential Storeの適合テストは、永続fixtureと意図的に壊したAdapterへの検出試験で確認しています。
- 各利用先DBのAdapterは、そのDBのテスト環境で適合試験を実行してください。

- 最終のKata回帰ではWorkspaceSharing（8ケース、145.60秒）、PolicyFeatures（46.83秒）、GoAPI（85.14秒）が成功しました。
- Acceptanceの初回は、追加したnamespace観測が通常ユーザーからroot専用パスをstatして失敗しました。
- 権限を緩和せず、ホスト側の故障注入supervisorによる実測証跡を照合する方式へ直して、Acceptanceを再実行し成功しました（176.486秒）。
- 所有権・task不在・空のcgroup・保存済みinodeを確認した停止済みnamespaceを実際に削除し、再起動後の新しいinode、VM再起動と孤児回収まで確認しています。
- 単なる再起動をnamespace復旧として合格させない回帰試験も追加しました。
- これにより全4スイートの合格を確認しています。

- 最後に試験用コンテナ・task・VM割当・ネットワークが0件であることを確認し、専用node・containerd・BuildKit・mount・cgroup・socket・状態領域を回収しました。
- 既存の3サービスは同じPIDで稼働し、既存nodeのhealth応答も正常でした。
- 各機能は汎用API・設定・適合テストとして実装しており、別オーケストレータ自身のAdapterやDBへの組み込み試験は対象に含みません。

## 2026-10-21の性能計測

- 対象の実装は、[認証トークンの索引化（61c9665）](https://github.com/pacificbelt30/sandbox-oasis/commit/61c9665c4077b312162ea4b5bd84e21dcede2b8d)、[停止時の終了通知の保存（b6746b6）](https://github.com/pacificbelt30/sandbox-oasis/commit/b6746b6f24003ce91712e8ca8df672fa978be660)、[資産検証の並列化（cd4f68f）](https://github.com/pacificbelt30/sandbox-oasis/commit/cd4f68f4bad31e6231095b7e8c5c7acfe16c4f5d)です。
- 再計測手順は[性能とベンチマーク](performance.md)にまとめています。

- Intel Core i5-8500、Linux amd64、Go 1.27.2、GOMAXPROCS=4で、同じbenchmarkを変更前後の別バイナリーで順次実行しました。
- 各500msを5回、中央値で比較しています。
- 測定中に他のテストやビルドを並行実行していません。
- `BenchmarkWorkerRequest` は認証ヘッダーの解析、期限・経路の確認、request contextの生成と解放を含み、ネットワーク・実AIへの通信は含みません。

| 同一Proxyのトークン数 | 認証成功：変更前 → 変更後 | 時間短縮 |
|---|---|---|
| 1 | 831.1 → 807.4 ns | 2.9% |
| 32 | 1,096 → 820.9 ns | 25.1% |
| 256 | 2,846 → 882.3 ns | 69.0% |
| 4,096 | 47,522 → 1,012 ns | 97.9% |

- 未発行トークンの拒否は4,096件で52,668 → 133.8 ns（99.7%短縮）でした。
- トークン値の索引を追加した効果であり、AI応答全体の改善率ではありません。
- 小規模Proxyでは元の認証コストも小さく、同等の体感改善を意味しません。

再計測は[Proxy認証のbenchmark手順](performance.md#proxy認証)を参照してください。

- 発行・再発行・異なる値／同じ値でのrestore・失効・期限切れ回収・実行／ケースの失効・Proxy停止で、古いstreamの取消、旧トークンの拒否、別ワーカーへのトークン重複割当拒否を確認しました。
- 全Goテストのrace検査、vet、lintも成功しました。

### Kata資産の再検証

- CPU profileでは `Load` のCPU時間の95.5%がSHA-256本体でした。
- 資産ファイル間の検証を最大4並列にし、全資産の再読込・所有権・パス・実行権限・checksum・設定との結び付けの検査を維持しています。
- タイムスタンプを根拠にしたキャッシュは追加していません。
- 並列数はGOMAXPROCSも上限とするため、GOMAXPROCS=1では1ファイルずつ検証します。

- 実際に固定した256 MiBのguest imageとruntime・kernel・containerdなどを含むmanifestで、CPU profileを無効にして測定した `BenchmarkLoadPinnedManifest` は970.55 → 636.43 ms（34.4%短縮）でした。
- 各3回の平均を5標本取り、その中央値を比較しています。
- 既にpage cacheが温まった、複数CPUのある同一ホストでの結果です。
- 総ハッシュ量を減らす変更ではなく、空いているCPUを使って待ち時間を短縮しています。
- ディスク読み出しやCPU数が制約になるホスト、SHA命令を持つCPUで同じ改善率を保証するものではありません。
- CPU飽和時のthroughputは未測定です。

- 再計測は[固定資産の検証](performance.md#固定資産の検証)と[実KataのExec](performance.md#実kataのexec)を参照してください。
- 公開Go APIの `BenchmarkKataExec` は、Openとwarmupを計測から除き、Execごとの資産・ホスト・guestの検証、`/bin/true` の実行と終了確認を含みます。
- allocation値はクライアント側の値です。

- 同じcompact VM・同じクライアントバイナリーを維持し、変更前後のnodeを切り替えて5組比較しました。
- 各標本は3 Execの平均で、組ごとの実行順は「旧版→新版」と「新版→旧版」を交互にしています。
- 中央値は2.4478 → 1.7615秒（28.0%短縮）でした。
- 比較中にエラー・VM再作成はなく、測定後にワーカーとfixture Proxyを回収しました。
- これは上記ホストでの短いコマンドの結果で、長い計算ジョブやAI推論時間の28%短縮を意味しません。

### 停止処理の回帰確認

- 停止時はcontainerdの終了通知に含まれる終了コードと時刻を保存します。
- 通知後の状態再取得に依存すると、runtimeが終了済みタスクを忘れた場合に成功した停止をNotFound扱いしてしまいます。
- 単体テストでこの順序を再現し、保存済みの終了コード・時刻を確認しました。
- 実行中taskの終了を待つとき、終了通知自体のエラーや、結果なしで閉じたchannelは引き続き失敗として扱います。
- task削除前の停止・不在確認とKataのretirement検証は維持しています。

- 初回の実Kata受入では停止APIのNotFoundが1件ありました。
- エラーに処理段階を付けた診断用nodeで同じ受入を再実行すると193.725秒で通過し、最初の失敗が上記の競合だったかは断定できていません。
- 単体テストでは競合を再現して修正を検証し、実機での初回失敗と再実行の結果を区別しています。

最終実装で `make kata-e2e VERSION=dev` を再実行し、4スイートが連続で成功しました（Go test全体432.713秒）。

| スイート | 結果 |
|---|---|
| Kata基本受入 | PASS、188.03秒。停止・再起動・削除・孤児回収を含む |
| 共有 | 8ケースPASS、143.87秒 |
| 通信・実行ポリシー | PASS、39.84秒 |
| 公開Go API | PASS、60.91秒。ExecのEOF・取消、small/standard、ケース分離・失効・削除を含む |

全Goテストのrace検査、vet、lint、Linux arm64・macOS arm64クロスビルド、ドキュメントのstrict buildも成功しました。

- 追加のGo API受入も、固定CLI 0.163.0と架空の資格情報で全9ケースが成功しました（406.251秒）。
- 明示HTTP/SSE、compact、起動取消・通信中失効、OAuth保存失敗・版競合・Proxy再起動、容量付き一時領域、組み込みサンプル、登録プロファイルのnode/VM再起動後の制約を確認しています。
- 検証用VM・コンテナ・ネットワーク・サービス・状態を回収し、既存サービスは変更していません。

## 新規Kata起動の工程別計測

- 2026-10-21、`c2bd5ac` を基準に `LaunchWithTimings` と `BenchmarkKataLaunch` を追加して、独立したnode・containerd・BuildKitで新規起動を5回計測しました。
- Intel Core i5-8500、Linux amd64、Go 1.27.2、clientのGOMAXPROCS=4、`compact`（guest 512 MiB／host予約768 MiB／PID 512）です。
- 各回は別のworker・要求ID・workspace・ケースProxyを使い、Proxy作成とclientのOpenは計測外です。
- 起動中に他のビルド・テストを実行していません。
- workerは準備済みベースに最新entrypointと固定Claudeバイナリを重ねた検証用イメージで、page cacheは温まっています。
- 一般egress・Git共有・エージェント実行を無効にし、架空のAPIキーだけを用いました。

- 再現手順は[実Kataの新規起動](performance.md#実kataの新規起動)です。
- 今回は `-benchtime=1x -count=5` で、計5標本を取りました。

| 計測対象 | 中央値 |
|---|---:|
| Launch全体 | 6.707秒 |
| VM開始API | 3.498秒 |
| host/guest検証 | 0.957秒 |
| コンテナ作成 | 0.820秒 |
| preflight | 0.667秒 |
| ネットワーク作成・Proxy接続 | 0.295秒 |
| bootstrap解放 | 0.220秒 |
| 起動後の最初のExec（別計測） | 約2.066秒 |

- Launch全体の範囲は6.529〜6.906秒でした。
- 工程ごとの中央値は別々に求めており、その合計がLaunch中央値になるとは限りません。
- 各標本でのVM開始時間の割合の中央値は52.15%です。
- これはWarm poolを次に検討する根拠であり、Warm poolによる短縮を実測した値ではありません。
- `vm_start` はnodeの開始API全体です。
- 純粋なVMM起動時間や、実エージェントが最初の応答を返すまでの時間は測っていません。

- 計測APIは既存の起動・検証・認証・後片付けの順序を変えません。
- 失敗時の部分計測、guest検証失敗時にトークン有効化・bootstrap解放へ進まず回収すること、計測有無で同じ要求IDを再送できることを受入対象にしています。

- 初回の通し試験では、Codexのツール実行・最終応答後にCLIが45秒以内に終了せず、該当サブテストが失敗しました。
- 他の3スイートは成功しました。
- オフラインfixtureを外向きメトリクス送信の既定動作に依存させないよう、fixtureだけに `analytics.enabled=false` と `otel.metrics_exporter="none"` を指定しました（[公式設定リファレンス](https://learn.chatgpt.com/docs/config-file/config-reference)）。
- 45秒の上限、終了コード0、ツール結果の返送、生成ファイルの検査を維持した再実行で、固定CLIの正常終了を確認しました。
- 製品の設定は変えておらず、初回の終了遅延の詳細原因を断定したものではありません。

- 最終の `make kata-e2e VERSION=dev` は全4スイートが404.818秒で成功しました。
- VM受入171.51秒（実Codexツール試験30.01秒）、共有8ケース134.26秒、追加制御38.41秒、Go API60.64秒です。
- 全Goテストのrace/coverage、vet、lint、Linux arm64・macOS arm64クロスビルド、MkDocs strictビルドも成功しています。
- 専用VM・コンテナ・task・ネットワークの消滅を確認してから、検証用サービス・mount・cgroup・状態領域を回収しました。
- 既存の3サービスのPID、node疎通、元ネットワークの所有権は維持されています。

## Claude Codeの受入

- 組み込みイメージの公式Claude Codeは2.1.306です。
- 実APIキーやサブスクの実アカウントは使わず、proxy handlerの上流だけを架空のMessages/SSE・OAuthサーバーへ置き換えます。

ホストで固定CLIを動かす任意試験:

```bash
CXD_TEST_CLAUDE_BINARY=/absolute/path/to/claude go test ./internal/authproxy -run '^TestClaudeCLIIntegration$' -count=1 -v
```

Linux/KVMの独立したノードでGo APIから実Kataを動かす試験:

```bash
make build
uv run --no-project --no-config --managed-python --python 3.12 python scripts/verify-go-api.py --binary "$PWD/oaiso" --version "$(./oaiso --version | awk '{print $NF}')" --run '^TestKataAPIIntegration/claude-'
```

- 既存の `make kata-api-e2e` にもClaudeのAPIキー・OAuthケースを含めています。
- 待機ワーカーの `ClaudeCommand` と、`Agent: "claude"` を指定したメインプロセス起動の両方を試験します。
- 公式CLIのMessages/SSE、Bashツールの実行と結果の返送、明示モデル制限、ワーカー停止・再起動後の仮トークン復元、OAuthの一度だけの更新・外部Store保存・接続世代維持を確認します。
- 単体試験ではsetup-token、認証方式の優先順位、余分なクエリの拒否、更新のHTTP失敗・不正応答・取消・保存失敗・版競合・再起動後の読込・stream失効も対象です。

- 実Anthropic APIへの推論、実Pro/Max等の利用枠、長期OAuth更新、対話キーボード操作、別のClaude CLI版は未確認です。
- fixtureの成功は実契約での認証・課金・上限の保証ではありません。

- 2026-10-21の独立Kataノードでは、上記4つのClaudeケースが成功しました。
- 待機ワーカーのAPIキー58.49秒／OAuth50.60秒、メインプロセスのAPIキー26.90秒／OAuth26.88秒です。
- 既存ケースも含む公開Go API全13ケースが626.378秒で成功しました。
- ワーカーは既存ベースへ最新entrypointとnpm公式配布の固定Claudeバイナリを重ねた検証用イメージで、proxyの上流だけをfixtureへ差し替えています。
- ホストの固定公式CLIを用いるrace付き試験も両認証で成功しました。

- 標準の `make kata-e2e VERSION=dev` も4スイートすべて成功しました（450.143秒）。
- VM受入207.26秒、共有8ケース141.98秒、追加制御38.91秒、標準Go API61.99秒です。
- 全パッケージrace/coverage、vet、lint、Linux arm64・macOS arm64クロスビルド、MkDocs strictビルドも成功しました。
- Anthropic APIキー要求を別プロバイダーのOAuth更新ロックから独立させる回帰テストと、固定公式CLIを含むauthproxy全体のrace試験も成功しています。

- 最後の更新ロック回避を反映した後も、実KataのAPIキー・OAuthを再試験し、両ケースが103.921秒で成功しました。
- 待機ワーカーでの公式CLI、ツール結果の往復、停止・再起動、OAuth更新のCAS保存を再確認しています。

- 検証終了後、専用VM・コンテナ・task・空のネットワークの消滅を確認してから、専用node・BuildKit・containerd・mount・cgroup・状態領域を回収しました。
- 元の3サービスと元ネットワークの所有権・node疎通も確認しており、既存サービスには更新を適用していません。

## 明示的な通信拒否・ドメイン完全一致の受入

- 2026-10-21、既存の公開宛て通信を維持したまま、`forward_default_deny` と `=example.com` の完全一致を追加しました。
- PID上限のカスタムプロファイル定義CLIと稼働nodeの参照も対象です。
- 全Goテストのrace/coverage、vet、lint、MkDocs strictビルドが成功しました。
- 独立した実Kataで `make kata-e2e VERSION=dev` の全4スイートも成功しました（427.897秒）。
- 追加のGo API受入 `egress-default-deny` / `egress-exact` は59.958秒で成功し、ワーカー個別許可リストを省略してもホスト設定が適用されること、厳格な空リストで一般Proxyへ接続されないこと、完全一致の子ドメインへのHTTP/CONNECTが403になること、固定AIルートは独立した認可で利用できることを確認しています。
- 公開の外部サービスに対する通信成功を必要としない拒否試験です。
- 追加試験の初回は拒否動作自体が成功した後、対象外のOAuth保存失敗用の分岐へ進んで失敗しました。
- 通信試験はその検証後に終了するよう修正し、両ケースを再実行しています。

## 対象ワーカーの隔離・再起動禁止

- 2026-10-21、`policy quarantine` / Go API `Quarantine` を追加しました。
- 共有Proxyの規則を途中で書き換える方式ではなく、対象VMを停止し、再起動禁止をnodeのprivate recordに保存します。
- workspace・worktreeは隔離操作では削除せず、新規ワーカーと要求IDで新しい規則を適用します。
- 既存の保持期限による回収は引き続き有効です。

- 独立Kata nodeで追加のGo API全4ケースが199.974秒で成功しました。
- 空リストの明示拒否、ドメイン完全一致、隔離、PID 256の登録プロファイルが対象です。
- CPU 2・guest 512 MiB・host 768 MiB・PID 256の値はnodeとVMの再起動後にも一致しました。

- 隔離試験は、一般HTTPのstreamとCONNECT echoを、実proxy handler・private IP制限・検証済みIPへのdialを通して開きます。
- インターネット上のサービスに依存しないよう、信頼した上流proxy chainだけをloopbackのfixtureに置き換えます。
- AI SSEも架空の上流を用います。
- 通信が開始した後で対象VMを隔離し、背景ジョブの終了、VM停止、共有proxyを使う別VMの一般HTTP/CONNECTと固定AI経路の継続、API・直接node APIからの再開拒否、node再起動後の拒否、CLIの成功応答、別StateDirからの拒否、同一要求の再送、新しい規則での作り直しとworkspace保持を確認しました。
- 共有proxyの全上流socketの終了待ちや、外部サービスの受理済み処理の取消を保証する試験ではありません。

- 初回は、`Create` の要求を `Launch` として再送する不正な試験条件でfingerprint競合になりました。
- 同じ段階の `Create` を再送するよう修正し、既存の段階的実行契約を維持しています。

- 受理後の停止がクライアント切断で中断されないよう、node所有の90秒上限を追加しました。
- 取消済みcontextからの停止続行・期限上限は単体試験で確認し、変更後の隔離受入も77.240秒で成功しました。
- 失敗した停止・保存、未対応node、偽の成功応答、停止未確認、隔離待ちのstart、node再読込後の再起動禁止もrace付き単体試験の対象です。

- 最終の `make kata-e2e VERSION=dev` も全4スイートが418.462秒で成功しました。
- VM受入186.32秒（実Codexツール実行39.67秒）、共有8ケース134.39秒、追加制御38.53秒、Go API59.22秒です。
- 全Goテストのrace/coverage、vet、lint、Linux arm64・macOS arm64のクロスビルド、MkDocs strictビルドも成功しました。
- 検証用のVM・コンテナ・task・ネットワークが0件であることを確認した後、専用node・containerd・BuildKit・mount・cgroup・socket・状態領域を回収しました。
- 既存の3サービスは元のPIDで稼働し、既存nodeのhealth応答200と元ネットワークの所有権も維持されています。
