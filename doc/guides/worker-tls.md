# VMと認証Proxyの間を暗号化する

**日本語** | [English](../en/guides/worker-tls.md)

- 利用可能：v0.0.4。
- 標準の`proxy run`とGo APIの`EnsureCaseProxy`で作る認証Proxyに適用します。

## 通信と認証

| 区間・情報 | 扱い |
|---|---|
| Codex／Claude Code → VM内中継 | VMの`127.0.0.1:18083`上のHTTP。VMの外へ公開しない |
| VM内中継 → Auth Proxy | TLS 1.3＋クライアント証明書。証明書をworker tokenと公開鍵に結び付ける |
| APIキー・OAuth access/refresh/ID token | 認証Proxyだけで保持。VMへ渡さない |
| VMに渡す鍵 | その実行の短命クライアント鍵。Proxyの発行鍵ではない |
| AI HTTP・SSE・WebSocket、token取得・OAuth更新、broker | 同じ暗号化・照合を適用。既存のモデル・用途・利用上限・失効確認を維持 |
| 一般egress Proxy | 従来どおり。HTTP proxy接続やCONNECT開始部分は、このTLS化の対象外 |
| host → admin | 従来の専用管理経路。公開network向け管理TLSを追加する機能ではない |

- コピーしたtokenだけでは接続できません。別の実行の証明書と組み合わせても拒否します。
- VMが完全に侵害されると、そのVMのtoken・短命鍵を使った許可済み操作までは防げません。
- 同じUID・ホストroot・node管理者を攻撃者とする保証ではありません。
- エージェントごとのmTLS設定は不要です。固定bootstrapが、検証・token有効化の後で中継を起動してからユーザーコードを解放します。

## 起動方法ごとの適用範囲

| 起動方法 | VM向け通信 |
|---|---|
| `oaiso proxy run`の認証Proxy | mTLSを自動で有効化 |
| Go APIの `EnsureCaseProxy` | mTLSを自動で有効化 |
| `oaiso proxy serve`の直接起動 | `--worker-mtls`の明示が必要。別admin listenerと管理secretも必要 |
| 独自のin-process Proxy | `WorkerMTLS`の明示設定が必要 |
| Go APIの `StartCredentialBroker` | 現行helperはHTTP。TLS必須のVM向け経路には使わない |

- 固定origin brokerは、標準の認証Proxyに`--credential-routes`で設定すれば、同じmTLSで保護されます。
- `StartCredentialBroker`の平文互換経路まで、自動的にTLSへ切り替える実装ではありません。
- 独自組み込みでは `ProxyCapabilities().WorkerMTLS` を確認し、TLSが必要な用途では未対応をエラーとして扱ってください。
- 既存の稼働環境への自動適用は行いません。対応する版をそろえ、以下の手順で再作成します。

## 更新して使う

旧ワーカーと旧Proxyを使っている場合、作業を保存して停止・削除してから、新しいCLI・node・guard・Proxyをそろえてください。既存環境を自動で停止・置換しません。

現在の設定が新しいCLIに対応しているホストで実行します。

```bash
oaiso proxy run
oaiso run --name tls-example --detach --keep --no-internet --exec 'sleep 300'
oaiso stop tls-example
oaiso start tls-example
```

- 管理secretは32バイト以上が必要です。標準の管理Proxyで未設定なら、`~/.config/codex-dock/proxy-admin-secret`へ0600で自動作成します。既定のローカルadmin接続だけが暗黙に読み取り、外部接続へは送りません。独立login・`init`の手順は[認証](authentication.md)を参照してください。
- TLS対応前のワーカーと新Proxy、TLSワーカーと旧Proxyの組合せは再作成を要求します。HTTPへ自動で切り替えません。
- `proxy serve`を直接組み込む場合は、別のadmin listener・32バイト以上の管理secretに加えて`--worker-mtls`を指定します。
- 既定以外のadmin URLへ接続する場合は、管理secretファイルを明示してください（`oaiso run --proxy-admin-secret-file /絶対パス`）。
- Go APIの`ProxyCapabilities().WorkerMTLS`で確認できます。独自の旧Proxy・従来のin-process Proxyは明示設定なしに変更しません。

## 有効期限・再起動・保存

| 条件 | 動作 |
|---|---|
| 実行token／クライアント証明書 | 最大3600秒。長いTTLを指定した新規TLSワーカーは拒否 |
| 期限切れ・失効・置換 | 新規要求を拒否。既存streamも終了 |
| ワーカー停止・開始 | 検証後に新しい鍵と証明書を取得。以前の鍵は再利用できない |
| Proxy再起動 | メモリ内の発行鍵を更新。旧証明書は信頼しない。ワーカーも停止・開始して再認証 |
| CA・Proxy秘密鍵 | Proxyのメモリだけ。VMへ渡さない。各Proxyプロセスで生成 |
| VMの短命鍵 | 管理側からstdinで渡し、`/run/codex-dock-control/transport.json`へ0600で保存。tmpfs内。argv・image・共有領域へ置かない |
| checkpoint | `/run`を保存しない。復元先で新しい実行用認証を取得 |
| 時刻ずれ・証明書不正・中継起動失敗 | 接続／起動を拒否。nodeとguestの時刻を確認 |

- 短命認証を自動更新し続ける機能ではありません。長時間の利用は作業を保存し、期限内に停止・開始してください。
- [保護範囲](../security.md)・[実施した試験](../testing.md)。
