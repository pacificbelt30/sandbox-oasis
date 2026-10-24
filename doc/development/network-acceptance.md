# 通信・失効の受け入れ検査

**日本語** | [English](../en/development/network-acceptance.md)

- 追加の検査：**未リリース（main）**。
- 実アカウントを使わず、既存試験とローカルの接続先を再利用します。
- 自動試験・実Kata・未確認の条件を分けて記録します。

## 確認する条件

| 条件 | 自動試験 | 実Kataでの追加確認 |
|---|---|---|
| DNS | 応答の全件検査、A/AAAA混在、順序変更、検証済みIPへの接続、次の接続での応答変更、空の応答 | DNS条件は自動試験。実Kataでは接続先をIPで固定 |
| IPv4/IPv6 | private・loopback・link-local・IPv4-mapped・metadataの拒否 | VMからHTTP/CONNECTで拒否。公開IPv4/IPv6宛てCONNECTの往復も確認 |
| redirect | 許可範囲外の接続先へ転送しない | 許可IPからmetadata宛てのredirectを拒否 |
| CONNECT | HTTPと同じ宛先・ポート制限、許可したTLS通信 | IPv4/IPv6の継続中トンネルを隔離停止で終了 |
| SSE | 3つのAI経路×失効・期限切れ・再発行・復元・Proxy停止 | VMを動かしたままトークンを失効し、接続先側の切断を確認 |
| WebSocket | 失効・期限切れ・再発行・復元・Proxy停止 | SSEと同時接続し、失効で接続先側も切断 |
| 停止失敗 | node障害・偽の完了応答・保存失敗・状態不明でも遮断完了としない | 成功時の実停止・全接続終了・再起動拒否を確認。停止失敗は自動の障害注入試験 |
| CLI表示 | 不明・稼働中・一時停止中・再起動中は`QUARANTINE PENDING`。JSONも`quarantine_pending: true` | 実停止後の`show`・`explain`を照合 |

- 一般通信とAI通信は別の制御です。AIトークンの失効だけでは一般CONNECTを止めません。全通信の停止には`policy quarantine`を使います。
- IPv6の確認はProxyでの宛先検証・トンネル転送です。VMへのIPv6アドレス割当てや、外部IPv6回線への接続を検証したものではありません。
- 接続先側の観測は試験専用イメージだけに追加。実APIキー・要求内容を記録せず、接続数のみを読み取ります。

## 自動試験を実行する

- 前提：[開発ツール](toolchain.md)。リポジトリのルートで通常ユーザーとして実行。
- 実AIへの認証・推論は行いません。

```bash
go test -race ./internal/networkpolicy ./internal/authproxy ./internal/engine ./internal/sandbox ./internal/workeregress ./cmd
```

- 実Kata用の試験は、このコマンドだけでは実行されません。
- 主な検査：`TestMixedDNSFamiliesFailClosedAndPublicAddressesArePinned`、`TestWorkerSSELeaseEndsOnRevocationAndExpiry`、`TestWorkerWebSocketLeaseEndsOnRevocationAndExpiry`、`TestPolicyCommandsNeverReportUnconfirmedStopAsBlocked`。
- 停止失敗：`TestQuarantinePersistsBeforeStopAndBlocksStartAfterReload`、`TestQuarantineRequiresUpdatedNodeAndConfirmedTargetStop`。

## 実Kataで実行する

1. [使い捨てノードの準備](../testing.md#新規導入更新復旧を検証する)に従い、`setup-test-a`／`setup-test-b`のバンドル、対応する受入バイナリ、標準のProxy・workerイメージを用意します。
2. 同じ`setup-test-a`を指定して、追加の試験専用Proxyを作ります。製品の認証Proxyとしては使用しません。

```bash
mkdir -p .kata-verification/network-proxy
CGO_ENABLED=0 go test -c -ldflags "-X github.com/pacificbelt30/codex-dock/internal/buildinfo.Version=setup-test-a" \
  -o .kata-verification/network-proxy/fixture.test ./internal/authproxy
docker build -f deploy/kata/Dockerfile.proxy-fixture \
  --build-arg CODEX_DOCK_BUILD_VERSION=setup-test-a \
  -t oaiso-network-fixture:verification .kata-verification/network-proxy
docker save -o .kata-verification/setup-e2e/network-images.tar \
  codex-dock:kata-guard codex-dock-proxy:policy-verification oaiso-network-fixture:verification
```

3. 通信・失効の試験だけを選んで実行します。作成したノード・ボリュームはスクリプトが回収します。

```bash
CODEX_DOCK_DISPOSABLE_SETUP=1 uv run --no-project --no-config --managed-python --python 3.12 \
  python scripts/verify-setup.py \
  --bundle dist/node-bundle-a --next-bundle dist/node-bundle-b \
  --images .kata-verification/setup-e2e/network-images.tar \
  --acceptance .kata-verification/setup-e2e/acceptance.test \
  --network-proxy-image oaiso-network-fixture:verification \
  --acceptance-run '^TestKataNetworkRevocation$'
```

- 成功条件：`TestKataNetworkRevocation`が成功し、導入・更新・復旧後にVM・task・コンテナ・専用ネットワークが残らないこと。
- `acceptance.txt`の成功と、接続先側でSSE/WS・一般通信の接続数が0になった検査を確認します。
- 版不一致・イメージ未準備・KVM不足は失敗です。skipやruncへの置換を成功とは扱いません。
- 実行結果・証跡：[テストと動作確認](../testing.md)。
