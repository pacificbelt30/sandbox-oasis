# Oaiso v0.0.4

## 主な変更

| 分野 | 追加・改善 |
|---|---|
| 認証通信 | 標準のAuth ProxyでTLS 1.3＋mTLSを使用。短命トークンと実行別証明書を結び付け、コピーしたBearer tokenだけの利用を拒否 |
| 配布物 | GitHub OIDC／Sigstoreの署名付き作成元証跡、CycloneDX SBOM、意図しない旧版適用の拒否 |
| 監査・試験 | 操作結果の保存、保持量・欠損の検出、保存失敗時の拒否。通信・失効・競合・不正入力・障害注入の試験を追加 |
| 実行の制御 | 実行別の要求数・同時数・本文量・観測token上限、メイン処理の保護・活動通知、TCP・帯域・FD・block I/Oの制御 |
| 通信規則 | 許可候補と変更計画を確認して即時適用。世代競合を検出し、既存HTTP／CONNECTを閉じてから完了を返す。適用途中の失敗は拒否状態を維持 |
| 固定宛先broker | 設定テンプレート・事前検証・既存TUI。pathの完全一致と配下許可を選択でき、HTTP methodも指定可能 |
| 組み込み | パイプ専用のLocal API v1・JSON Schema・Python／TypeScript SDK。同じホストからVM・ファイル・jobs・checkpointを操作 |
| 作業支援 | 指定した成果物・Git差分・manifestの確認付き回収、localhost限定Webプレビュー、disk checkpointの保存・復元・保持管理 |
| 運用・更新 | 起動工程の計測・診断の保存。上流CLI候補の静的確認・隔離CLI試験・実Kata試験をつなぐ仕組み。自動採用はしない |
| 高速化 | 全資産・host／guestの検証を維持し、同じExec内の重複検証を削減。計測APIを追加 |

- Codex CLI 0.163.0／Claude Code 2.1.306の固定版を使用。
- 実APIキー・OAuth access／refresh／ID tokenは、引き続き認証Proxyだけで保持します。
- 一般CONNECTのTLS内復号・URL検査は行いません。brokerのquery・bodyの意味も制限対象外です。
- 独立したGo APIの`StartCredentialBroker`は平文通信です。暗号化が必要なbroker経路は、標準Auth Proxyの`credential_routes`を使用します。

## 高速化の実測

同一ホスト・準備済みイメージ・ウォーム条件の前後比較です。AIの応答時間を測った結果ではありません。

| 操作 | 変更前の中央値 | 変更後の中央値 | 短縮率 |
|---|---|---|---|
| Go APIの短いExec | 2.475秒 | 1.161秒 | 53.1% |
| 2台同時Exec | 3.329秒 | 1.888秒 | 43.3% |
| VM起動 | 7.740秒 | 7.103秒 | 8.2% |

- CPU合計・論理読出し量は減少。nodeのRSSは約2MiB増加し、VM単体のCPU観測値も増加しました。
- cold起動・他CPU・3台以上・AI推論の速度は未測定。収容上限・予約量は変更していません。
- [測定条件・サンプル数・制約](https://pacificbelt30.github.io/sandbox-oasis/performance/)。

## インストール

1. [前提条件とツールを準備](https://pacificbelt30.github.io/sandbox-oasis/installation/)。署名検証用のGitHub CLIが必要です。GitHubへのログインは不要です。
2. 通常ユーザーで配置計画を表示。

```bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/download/v0.0.4/install.sh | bash
```

3. 計画を確認して適用。

```bash
curl -fsSL https://github.com/pacificbelt30/sandbox-oasis/releases/download/v0.0.4/install.sh | bash -s -- --apply
```

- 対象：Ubuntu 26.04 x86_64、systemd、KVM、cgroup v2、sudoが使える通常ユーザー。
- Go・Make・ソース取得は不要。uv管理Python 3.12を使用します。
- パイプ実行はHTTPS配布元のスクリプトを信頼して実行します。[スクリプト自体を実行前に署名検証する手順](https://pacificbelt30.github.io/sandbox-oasis/guides/signed-releases/)もあります。
- 適用後は再ログインし、`oaiso --version`・`oaiso runtime-check`を確認。[認証・初回起動](https://pacificbelt30.github.io/sandbox-oasis/guides/first-run/)へ進みます。

## 更新手順

| 順番 | 操作 |
|---|---|
| 1 | 作業を保存し、既存CLIで保持中のワーカーとProxyを停止・削除 |
| 2 | 上記インストーラーで対象版の計画を確認し、`--apply`でCLI・node・補助プログラム・固定runtime資産を更新 |
| 3 | 再ログイン後、`oaiso --version`・`oaiso runtime-check`で確認 |
| 4 | 新CLIで`oaiso build --template plain --tag codex-dock:latest`を実行。独自イメージは別途確認・再ビルド |
| 5 | 利用する側の`oaiso init --agent codex`または`oaiso init --agent claude`で新Proxyを準備し、新しいワーカーを起動 |

- 実行中の既存Proxy・VMの通信方式が自動で切り替わることはありません。CLI・node・guard・logger・Proxyは対応する版にそろえます。
- 保存済み認証プロファイルは維持し、自動で暗号化・再登録しません。
- 新しい完全一致broker設定は、非対応の旧CLIでは読み取れません。従来の配下許可だけの設定は互換性を維持します。
- disk checkpointはRAM保存ではありません。共有mountは含まず、復元互換性を検査します。
- 意図したrollbackと障害復旧：[更新と復旧](https://pacificbelt30.github.io/sandbox-oasis/setup/)。最も新しい適用版の記録は保持します。

## 配布ファイル

| ファイル | 内容 |
|---|---|
| `oaiso-linux-amd64.tar.gz` | 静的CLI・guard・IO logger・`bundle.json`・ライセンス／依存notice |
| `install.sh` | 対象版を選択する単独インストーラー |
| `SHA256SUMS` | archive・installer・SBOMのSHA-256 |
| `sbom.cdx.json` | 実バイナリのGo依存・標準ライブラリ・固定runtime資産の一覧 |
| `provenance.json` | 4ファイルの署名・作成元・タグ・commitを検証する証跡bundle |

- Kata等の固定配布物・作業イメージは導入・初期化時に別途取得します。認証情報は含みません。
- 署名・作成元は正当なビルド経路を確認するものです。脆弱性の不存在やguest image内の安全性は保証しません。

## 検証範囲

| 検証 | 結果・範囲 |
|---|---|
| Go | build・全件race・vet・lint・5対象のfuzzが成功 |
| Python・SDK | 配置／配布処理89試験、Python SDK 9試験・TypeScript SDK 10試験が成功。Go adapterとの契約・再送・取消・回収を検査 |
| 実Kata | VM・認証・TTY・共有・通信・暗号化・キーリング・Go APIの8スイートが成功。SDK・作業支援・broker・TLS／失効も機能別に確認 |
| 導入・復旧 | 使い捨てノードで導入・更新・旧版拒否・障害時復旧・明示rollbackを確認 |
| 文書 | 日英の手順・導入版・コマンド例を整理し、MkDocs strict検査が成功 |
| 未確認 | 実アカウントの認証・推論、長期OAuth更新、実WSL、macOS／WindowsでのVM実行 |

- 実Kataでは架空の資格情報・ローカル上流を使用。外部AIへ推論要求を送っていません。
- [検証記録と制約](https://pacificbelt30.github.io/sandbox-oasis/testing/)・[セキュリティ上の保護範囲](https://pacificbelt30.github.io/sandbox-oasis/security/)。

## English

| Area | Changes |
|---|---|
| Security | Standard auth proxies use TLS 1.3 and execution-bound mTLS; signed distributions, provenance, SBOM, rollback floor and durable audit |
| Control | Per-run usage limits, activity protection, connection/bandwidth/FD/block-I/O limits, reviewed live egress policy changes and exact broker paths/methods |
| Integration | Pipe-only local API v1, JSON Schema and thin Python/TypeScript SDKs, including files, jobs and checkpoints |
| Workspace | Reviewed artifact export, loopback-only authenticated previews and disk-checkpoint retention/pins |
| Performance | Warm short Exec median reduced 53.1%; two-worker Exec 43.3%. Full isolation checks retained; startup 8.2%. Hardware and workload limits apply |
| Installation | Linux amd64 binaries; no host Go/Make. GitHub CLI is required for signature verification, without GitHub login |
| Testing | Go race/fuzz and SDK contracts, disposable real-Kata acceptance, install/update/recovery and strict documentation checks passed; live accounts and real WSL remain unverified |

See [installation](https://pacificbelt30.github.io/sandbox-oasis/en/installation/), [signature verification](https://pacificbelt30.github.io/sandbox-oasis/en/guides/signed-releases/) and [first startup](https://pacificbelt30.github.io/sandbox-oasis/en/guides/first-run/).
