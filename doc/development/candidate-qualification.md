# 更新候補の互換性確認

**日本語** | [English](../en/development/candidate-qualification.md)

- 利用可能：**v0.0.4**。
- 対象：Codex・Claude Codeの候補版。通常環境と分離した作業ツリー・試験用イメージで検査します。
- 固定版・baseline・公開配布物は自動変更しません。

## 判定の段階

| 段階 | 実行すること | 成功の意味 |
|---|---|---|
| 静的検査 | 既存の公開定義・CLI宣言の差分検知 | 監視範囲に要確認の変更がない。実動作は未確認 |
| `plan` | 静的検査、候補captureと作業ツリーの固定CLI版の照合 | 計画を作成。互換性の判定は`pending` |
| `run`・CLI | 新規HOMEと環境変数の許可リストで両CLIを実行 | 合成API／OAuthで認証Proxy・Refresh・SSE・ツール実行を確認 |
| `run`・Kata | 現在のソースから受入バイナリを作り、使い捨てnodeで導入・通信・診断・broker・上限・更新・復旧を実行 | VM内の両CLI版、Codex SSE、Claude Codeのツール＋SSEまで合成提供元で成功 |

- CLI／Kataの必要ケースがskip・未実行なら成功にしません。
- 結果JSONへ候補・CLI実体・イメージアーカイブ・bundle manifest・試験バイナリ・受入ログのSHA-256を記録します。
- 調査時点のcommit、追跡済み差分のhash、検証スクリプトのhashも記録。信頼するoperator向けの記録で、署名済み配布証跡ではありません。

## 準備

| 必要なもの | 条件 |
|---|---|
| 独立した作業ツリー | 候補版を`docker/sandbox/Dockerfile`の2つの正確な版へ固定。通常のmainは維持 |
| 候補capture | [静的検査のcapture](../upstream-compatibility.md#ローカルで実行する)。候補CLIと同じ版 |
| CLI実体 | 専用ディレクトリに配置。ホストのグローバルCLIを更新しない |
| 開発環境 | Go、uv管理Python 3.12、取得済みGo module cache。CLI試験中の`GOPROXY`はoff |
| 実Kata用の資産 | [導入・更新・復旧の検証](../testing.md#新規導入更新復旧を検証する)の2bundle・イメージアーカイブ・試験node・runtime cache。CLI／Proxyの版・protocolをそろえる |
| ワーカー | 候補の両CLIと現在のentrypointを含むイメージ |
| 合成Proxy | `internal/authproxy`の試験専用バイナリを含むfixtureイメージ。実資格情報は不要 |

## 計画を確認する

作業ツリーのルートで実行。`.kata-verification/contracts/candidate.json`を取得済みとします。

```bash
uv run --no-project --no-config --managed-python --python 3.12 \
  --with-requirements requirements-contracts.txt python scripts/qualify-upstream.py plan \
  --candidate .kata-verification/contracts/candidate.json \
  --out .kata-verification/candidate-plan
```

| 結果 | 次の操作 |
|---|---|
| `pending`・終了0 | 計画のみ。CLI・実Kataは未実行 |
| `hold`・終了1 | `static.md`を確認。版違いなら独立作業ツリーのpin・候補・イメージをそろえる |
| `error`・終了2 | 取得・解析・前提条件を直す。現行版を維持 |

- `--out`はこの作業ツリーの`.kata-verification/`配下の**新しい**ディレクトリが必要です。
- 静的検査のholdを確認して続ける場合だけ、`--reviewed-static-sha256`に確認済み`static.json`のSHA-256を指定します。差分を読まずに付けないでください。
- 通常の`compare`は基準復元のため固定URLへ通信する場合があります。fixtureでは実API推論・ログイン・外部OAuth更新をしません。

## 一連の試験を実行する

下記のパス・タグを自分が準備した試験資産に合わせてください。外側のDockerは使い捨てホスト用で、製品の実行基盤はKataです。

```bash
uv run --no-project --no-config --managed-python --python 3.12 \
  --with-requirements requirements-contracts.txt python scripts/qualify-upstream.py run \
  --candidate .kata-verification/contracts/candidate.json \
  --out .kata-verification/candidate-run \
  --codex .kata-verification/contracts-tools/node_modules/.bin/codex \
  --claude .kata-verification/contracts-tools/node_modules/.bin/claude \
  --bundle dist/node-bundle-a --next-bundle dist/node-bundle-b \
  --images .kata-verification/setup-e2e/images.tar \
  --cache .kata-verification/setup-e2e/cache \
  --proxy-image oaiso-candidate-proxy:verification \
  --network-proxy-image oaiso-candidate-fixture:verification \
  --worker-image oaiso-candidate-worker:verification \
  --node-image codex-dock-setup-node:verification
```

- 合格：終了0、`qualification.json`が`status: pass`、CLI／Kata両段階が`pass`、`adopted: false`。
- 失敗：現行pin・baselineを維持。保存した段階別ログを調べ、修正した資産で新しい出力先へ再実行します。
- `verify-setup.py`が自分のnode・volumeだけを回収します。共有ホストのサービスや既存ワーカーは更新しません。
- 合格後も採用・pin／baseline更新・リリースは別操作です。自動merge・自動配布は行いません。

## CIと確認範囲

- 定期CIは既存の静的検査と計画artifactの作成まで。KVM・ログイン・実API推論は自動実行しません。
- 実アカウントのサブスクlogin・提供元の本番動作・課金額・実WSLは別の確認が必要です。
- 生成JSON、バイナリ、生ログはGitへ追加しません。[確認結果](../testing.md)へ範囲と証跡を残します。
