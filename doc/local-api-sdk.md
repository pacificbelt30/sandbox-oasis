# ローカルAPIとPython・TypeScript SDK

**日本語** | [English](en/local-api-sdk.md)

- 利用可能：**未リリース（main）**。公開レジストリには未配布です。
- 同じLinuxホストから、ワーカーの作成・起動・実行・照会・回収を行います。
- SDK → `oaiso api stdio`の専用パイプ → [公開Go API](go-api.md) → 既存nodeのUnix socket、の順に呼び出します。
- TCPの待受・管理socketのnetwork公開は追加しません。呼び出し元には既存socketへの権限が必要です。任意のhost mountなどを指定できるため、信頼したプロセスだけで使います。

## 前提と対応範囲

| 項目 | 条件 |
|---|---|
| ホスト | [実行基盤](setup.md)と認証Proxyを準備済み。ワーカーイメージはビルド・import済み |
| 版 | CLI・node・Proxyを対応するビルドで配置。ローカルAPIはv1、nodeはv2 |
| Python | 3.12以降。SDK実行時の追加依存なし。[uvの導入](https://docs.astral.sh/uv/getting-started/installation/) |
| TypeScript | Node.js 22以降。SDK実行時の追加依存なし。[Node.jsの導入](https://nodejs.org/en/download)。コンパイラ・Node型は開発時だけ使用 |
| 設定 | `open`へ明示。CLIの設定ファイル・認証profileは自動で読み込まない |
| 認証 | Proxy管理secretを既存の0600ファイルから読み、`Options.ProxyAdminSecret`へ渡す。実APIキー・OAuthは認証Proxyに保持 |
| 作業ファイル | `ProjectDir`は実在する絶対パス。通常の`/workspace`共有と同じ影響がある |
| 対応操作 | `create`・`launch`・`start`・`exec`・`get`・`list`・`wait`・`stop`・`remove`・`request`・`remove_request`・`quarantine`・資源とcapabilityの照会 |
| Go APIで行う操作 | PTY・入出力stream・background job・image build・checkpoint・StoreやケースProxyの作成など。今回の薄いSDKには未収録 |

- 一般通信はGo APIと同じく既定拒否。許可を追加する場合は`Options.EgressPolicy`を設定します。
- 作業ファイルへ書き込む場合は、所有者に合う数値UID/GIDを`LaunchRequest.User`へ指定します。省略時はイメージのユーザーを使用し、SDKは所有者を変更しません。
- 実行用の仮トークンや、ホストの実資格情報を返すAPIは追加しません。
- SDKの内部stdoutはプロトコル専用。ゲストのstdout・stderrは`exec`の戻り値で明示的に受け取ります。利用者が実行したコマンド自身の出力は、利用者側で管理してください。

## Pythonを導入する

- 通常ユーザーで、取得済みの**この機能を含むソース**のルートから実行。
- 仮想環境を新規作成します。既存環境がある場合は別の場所を選びます。

```bash
uv venv --python 3.12 .venv-sdk
uv pip install --python .venv-sdk/bin/python ./sdk/python
.venv-sdk/bin/python -c 'from oaiso import Client; print("SDK import OK")'
```

- 成功の目印：`SDK import OK`。nodeへの接続確認は次の例で行います。

### 作成・実行・回収

- `oaiso`をPATHへ配置済み。以下を`example.py`として保存します。
- `/srv/work/project`は実在する作業ディレクトリへ置換します。管理secretのパスは配置に合わせます。
- 要求IDは例です。新しい作業には別のIDを使い、同じ作業の再送では変えません。

```python
from pathlib import Path
import os
from oaiso import Client

secret = (Path.home() / ".config/codex-dock/proxy-admin-secret").read_text().strip()
request = {
    "RequestID": "my-app/project-1/task-1/attempt-1",
    "ProjectDir": "/srv/work/project",
    "Image": "codex-dock:latest",
    "Profile": "compact",
    "User": f"{os.getuid()}:{os.getgid()}",
    "NoInternet": True,
}

with Client({"ProxyAdminSecret": secret}) as client:
    worker = client.create(request)
    try:
        client.start(worker["ID"])
        result = client.exec(worker["ID"], ["printf", "hello"])
        print(result["exit_code"], result["stdout"].decode())
    finally:
        client.remove_request(request["RequestID"], force=True)
```

```bash
.venv-sdk/bin/python example.py
```

出力例：

```text
0 hello
```

- `Client(...)`は接続と版照合だけを行います。
- `create`は起動前の資源を確保します。ゲストのコードは`start`で開始します。
- `with`の終了は接続を閉じる操作です。VMの停止・回収は`stop`・`remove_request`を使います。

## TypeScriptを導入する

- 通常ユーザーで、この機能を含むソースのルートから実行。

```bash
npm --prefix sdk/typescript ci --ignore-scripts --no-audit --no-fund
npm --prefix sdk/typescript run build
```

- アプリのディレクトリへ移動し、ソースの絶対パスを置換して実行。

```bash
npm install /absolute/path/to/sandbox-oasis/sdk/typescript
```

### 作成・実行・回収

- 以下をアプリ側の`example.mjs`へ保存します。TypeScriptでも同じAPIを使え、型定義を読み込めます。
- 作業ディレクトリ・管理secret・要求IDの条件はPythonの例と同じです。

```javascript
import { readFile } from 'node:fs/promises';
import { homedir } from 'node:os';
import { Client } from '@oaiso/local';

const secret = (await readFile(`${homedir()}/.config/codex-dock/proxy-admin-secret`, 'utf8')).trim();
const request = {
  RequestID: 'my-app/project-1/task-2/attempt-1',
  ProjectDir: '/srv/work/project',
  Image: 'codex-dock:latest',
  Profile: 'compact',
  User: `${process.getuid()}:${process.getgid()}`,
  NoInternet: true,
};
const client = await Client.open({ ProxyAdminSecret: secret });
try {
  const worker = await client.create(request);
  try {
    await client.start(worker.ID);
    const result = await client.exec(worker.ID, ['printf', 'hello']);
    console.log(result.exit_code, result.stdout.toString());
  } finally {
    await client.removeRequest(request.RequestID, true);
  }
} finally {
  await client.close();
}
```

```bash
node example.mjs
```

- 出力例は`0 hello`。認証情報を`console.log`や起動引数へ渡しません。
- TypeScriptの数値は安全な整数の範囲で指定します。非有限値・小数・安全範囲外の整数は送信前に拒否し、受信時も丸めた値を返しません。大きな64 bit整数を必要とする場合はGoまたはPythonを使います。

## 取消・タイムアウト

| 操作 | 取消の意味 |
|---|---|
| `wait` | 待機のみを中断。VMは継続 |
| `exec` | 実行中のゲストコマンドへ取消を伝える。完了や切断との競合では結果照合が必要 |
| `create` / `launch` | Go APIの取消・回収処理を呼ぶ。要求IDを照会し、残る資源を回収 |
| `stop` / `remove` | 応答がない場合、停止・回収の完了は未確認。`get`・`request`で照合 |
| `cancel`の`accepted: true` | 対象callの取消を受け付けた。停止・回収の完了を示す値ではない |
| 接続切断・`close` | 処理中のcallを取消し、接続を閉じる。作成済みVMの回収完了は保証しない |

- Python：`pending = client.submit("wait", {"worker_id": worker_id})`、`pending.cancel().result()`、`pending.result()`。
- TypeScript：`const pending = client.submit('wait', {worker_id: workerID})`、`await pending.cancel()`、`await pending.result`。
- TypeScriptは各操作の末尾のoptionsで`{signal: controller.signal}`も指定できます。
- サーバー側の期限は既定180秒、最大1,200秒。Pythonは`timeout_ms`、TypeScriptは`timeoutMs`で指定します。`0`は既定値を使用。
- Pythonの`result(timeout=秒)`が時間切れになると取消を要求します。既に成功していた可能性があるため、変更操作では結果を照合します。
- `stop`の停止猶予は別の秒数です。省略時10秒、`0`は強制停止。nodeへの通信期限とは異なります。

## 再送とエラー

| 値・状態 | 対応 |
|---|---|
| callの`id` | 一つの接続内の応答照合番号。1から増加。SDKが付与 |
| `LaunchRequest.RequestID` | node全体で永続する作成要求ID。1〜256 UTF-8 byte。組み込み元が付与 |
| `pending` / `in_progress` | 処理中・復旧待ち。同じ要求IDで照会。別IDで重複作成しない |
| `complete` | 台帳上で作成が確定。現在の稼働状態は`get`で照会 |
| `failed` | 前回の作成が失敗。同じ不変設定・要求IDで再試行 |
| `cleanup-pending` / `cleanup_pending` | 回収が未完了。ワーカーID・要求IDから回収を再試行 |
| `conflict` | 同じ要求IDの不変設定が違う。元の設定を確認 |
| `request_gone` | 完了済み要求のワーカーは削除済み。同じIDでは再作成しない |
| `outcome_unknown` / `transport_lost` | 結果が不明。`request`・`get`で照合してから判断 |
| `version_mismatch` | 対応するCLI・node・Proxyを配置して接続し直す |
| `permission_denied` | 既存socket・ファイル権限を管理者に確認。SDKが権限を増やすことはない |
| `internal` | Go APIの未分類エラー。詳細文字列は秘匿。信頼したホスト側で状態・監査を確認 |

- **自動再送はしません**。作成の再送は同じ要求ID・同じ設定・同じ接続Optionsで行います。
- `create`と`launch`の間で同じ要求IDを使い回すと競合です。
- `exec`は再送で二重実行する可能性があります。応答消失後に自動実行し直さないでください。
- Pythonの`APIError`は`code`・`codes`・`requires_reconciliation`・`partial_result`、TypeScriptは`code`・`codes`・`requiresReconciliation`・`partialResult`を提供します。
- エラーに含まれる`Worker.ID`は回収に使えます。エラー文字列には元の資格情報・引数・下位エラーを含めません。
- `requires_reconciliation`は状態の照合が必要という意味です。`false`でも、別のcallで作ったVMの停止を保証する値ではありません。
- ワーカーの`Status`は観測値です。未知の文字列を「停止済み」とみなしません。

## 機械可読な仕様

| 項目 | 定義 |
|---|---|
| 接続・版 | private pipeのNDJSON、ローカルAPI v1。改行ごとにJSON objectを1つ送信 |
| 要求 | `version`・`id`・`method`・`params`、任意の`timeout_ms` |
| 応答 | `version`・`id`と、`result`または`error`。エラー時に部分結果を併記する場合がある |
| 型 | Options・LaunchRequest・WorkerなどはGo DTOのJSON名を維持。`RequestID`と`request_id`などの綴りは仕様を参照 |
| バイト列・時間 | 入出力はbase64。Go DTOのdurationはナノ秒、call期限はミリ秒、stop猶予は秒 |
| 上限 | 要求1 MiB、応答4 MiB、exec出力はstdout・stderr各256 KiB、同時処理16 call。取消は枠外で受付 |
| 超過 | 出力超過は処理を取消し、`output_limit`と部分出力を返す。大量出力・PTY・継続streamはGo APIを使用 |
| JSON検査 | 未知フィールド・重複キー・不正なframeを拒否。不正な外側frameでは接続を終了し、処理中の変更は照合が必要 |
| 保存・再生成 | [JSON Schema](https://github.com/pacificbelt30/sandbox-oasis/blob/main/api/local-v1/schema.json)。Go DTOから生成し、CIで差分を検出 |

```bash
oaiso api schema > local-api-v1.schema.json
```

- このコマンドはnodeや認証へ接続しません。JSON Schemaはdraft 2020-12です。
- `x-oaiso`に操作ごとの再送・取消、要求状態、結果型の対応を収録します。
- Schemaは型とwire契約を定義します。資源予約・通信とモデルの権限・パス制約は、実行時にGo APIとnodeが検査します。
- 互換性を壊す変更では新しいローカルAPI版と仕様を用意します。追加操作は`capabilities.methods`で確認します。
- Go APIをすべてJSON化したものではありません。capabilityにない操作は使用しません。
- 内部node HTTP APIをSDKから直接呼ばないため、内部DTOの変更と公開契約を分けて管理できます。

## 開発時の確認

- リポジトリのルートで実行。Go・uv・Node.jsを準備します。

```bash
go run ./api/generate
npm --prefix sdk/typescript ci --ignore-scripts --no-audit --no-fund
go test -race ./pkg/cxd/localapi
go test -c -o /tmp/oaiso-local-api.test ./pkg/cxd/localapi
uv run --no-project --no-config --managed-python --python 3.12 python scripts/verify-local-sdks.py --fixture /tmp/oaiso-local-api.test
```

- 版違い・再送・競合・応答消失・取消・出力上限・秘密非記録・回収の試験を、両SDKへ同じGo adapterで実行します。
- 実Kataの準備・実施範囲は[動作確認](testing.md)を参照。fixture成功と実アカウントへの推論成功は区別します。
