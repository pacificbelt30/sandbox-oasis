# 依存関係の検査と対応

**日本語** | [English](../en/development/dependency-security.md)

- 検査日：2026-10-24。対象はLinux amd64、全Goパッケージ。
- 依存更新・到達性の精査・追加の境界検査は**未リリース（main）**。公開v0.0.3の配布物には未反映。
- 検査ツール：govulncheck v1.9.0、Go 1.27.3。検出を除外する設定やDBの改変は行っていません。

## 結果

| 対象 | symbol検出 | 扱い |
|---|---:|---|
| age移行前のcommit `1168123` | 19 | 当時の比較元 |
| age＋HTTP/2修正後、公開v0.0.3 | 15 | 今回の変更前にも15件を再確認 |
| 今回の依存更新後 | **2** | 修正版への更新で13件が検出されなくなった |
| 残る2件 | 2 | 対象はdaemonのプラグイン機能。本製品の実行構成では前提機能なしと確認 |

- **検出0件・脆弱性がないという結果ではありません。** 残る2件は検出されたまま記録します。
- 検出数、実行経路での該当性、実際の攻撃成立は別の判断です。全体のセキュリティ監査を完了したものではありません。
- 新しいDB・別OS・別runtime・依存変更では結果が変わり得ます。

## 更新した依存

| Go module | 変更前 | 未リリースmain | 解消した検出 |
|---|---|---|---:|
| containerd SDK | v2.1.5 | v2.2.9 | 8 |
| gRPC | v1.78.0 | v1.83.1 | 2 |
| OpenTelemetry | v1.40.0 | v1.44.0 | 1 |
| moby/sys/user | v0.4.0 | v0.4.1 | 1 |
| SELinuxライブラリ | v1.12.0 | v1.13.1 | 1 |

- 関連API・runtime-spec・計測・圧縮等の依存もGoのmodule解決に従って整合。go.mod／go.sumを更新しました。
- gRPCの要求に合わせてOpenTelemetryはv1.44.0、containerd SDKの要求に合わせてSELinuxはv1.13.1を使用。
- containerd SDKは、確認した8件の修正を含み、最低Go版を引き上げないv2.2.9を選択。
- 最低Go版1.26.0、開発・CIのGo 1.27.3、固定containerd daemon 2.4.2は維持。稼働ホストのサービスや配布物は更新していません。
- age v1.3.2と既存のHTTP/2修正版x/net v0.60.0も維持しています。

### 解消したID

| module | ID |
|---|---|
| containerd SDK | GO-2026-6791、GO-2026-6597、GO-2026-5758、GO-2026-5622、GO-2026-5475、GO-2026-5378、GO-2026-5338、GO-2026-5064 |
| gRPC | GO-2026-6348、GO-2026-6061 |
| OpenTelemetry | GO-2026-5506 |
| moby/sys/user | GO-2026-6686 |
| SELinux | GO-2025-4098 |

- 詳細は[Go脆弱性DB](https://pkg.go.dev/vuln/)の各IDを参照。検出が消えたことを、そのdaemon機能を以前使っていた証拠とは扱いません。

## 残る2件の適用条件

| ID・公式告知 | 問題となる条件 | 本製品で確認したこと |
|---|---|---|
| [GO-2026-4887](https://pkg.go.dev/vuln/GO-2026-4887)／[AuthZ pluginの告知](https://github.com/moby/moby/security/advisories/GHSA-x744-4wpc-v9h2) | daemonがAuthZ pluginへ要求本文を渡す処理 | daemon・AuthZ実装をlinkしない。SDKのHTTP clientと型を、専用nodeとの互換通信に使用 |
| [GO-2026-4883](https://pkg.go.dev/vuln/GO-2026-4883)／[plugin installの告知](https://github.com/moby/moby/security/advisories/GHSA-pxq6-2prw-chj9) | daemonのpluginインストール時の権限検証 | daemon・plugin実装をlinkせず、nodeはplugin APIを提供しない |

- 両IDは、Go DBでは旧SDK moduleの全版が対象となる未レビューの登録です。修正元の公式告知は、対象機能を使わない場合は非該当と説明しています。
- 実行基盤は[containerd／Kataのnode](../architecture.md)。旧SDK moduleはv28.5.2のまま、daemonを導入・起動するためには使いません。
- `TestNativeEntrypointsDoNotLinkDockerDaemon`で、CLI・guard・logger・公開Go APIの依存が、旧SDKのclient・API型・stdcopyの範囲に収まることを自動検査。
- `TestNodeRejectsDaemonPluginEndpointsBeforeRPC`で、plugin・認証・未知のAPIをRPC前に404で拒否することを検査。版付き・版なしのURLと不正本文を含みます。
- 検査中に見つかった未知のパスのコンテナ処理への流入も修正しました。
- client／API型が大きく変わる新SDKへの移行は、この2件に対する必須修正とは分けます。将来daemon機能を組み込む場合は、この判定を流用できません。

## 検証範囲

| 確認 | 範囲 |
|---|---|
| 依存検査 | 変更前15件、更新後2件。13件解消、検出除外なし |
| Go | build、race付き全件test、vet、lint、module checksum検証 |
| 最低Go版 | Go 1.26.0でCLIをビルド |
| 追加の回帰検査 | daemon実装をlinkしないこと、未対応APIをRPC前に拒否すること |
| クロスビルド | macOS arm64／amd64、Linux arm64。各OSでのVM実行確認ではない |
| 受け入れテスト | 後続の[通信・失効スイート](network-acceptance.md)は実Kataで成功。全8スイート・実アカウント・WSLの再受入は未実施 |

- 既存の[実機・認証の検証範囲](../testing.md)も参照してください。今回の自動テストを実機受入の成功へ読み替えません。

## 再検査する

- 前提：[開発ツール](toolchain.md)のGo 1.27.3。通常ユーザーでrepo rootから実行。
- 公開DB・Go moduleを取得します。AIへの認証・推論は行いません。
- ツールはGit管理外のディレクトリに置きます。

```bash
mkdir -p .kata-verification/dependency-tools
GOBIN="$PWD/.kata-verification/dependency-tools" go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
.kata-verification/dependency-tools/govulncheck ./...
```

- 同じDBでは上記2件が残ります。text形式は検出があると終了コード3になるため、0件合格とは報告しません。
- 生ログ・JSON・ビルド物は`.kata-verification/dependency-hardening-20261025/`に保存。巨大なレポートや実資格情報はGitへ追加しません。
