# Future work

- 優先順位・機能追加・セキュリティ強化の候補：[機能と安全性の改善候補](development/improvements.md)。既存機能と未実装の案を分けて管理します。

## 手軽な導入・認証・運用

- 以下の6項目を実装対象とします。機能ごとに確認・ドキュメント更新・コミットを行います。
- 既存の0600保存と認証Proxyを維持します。新規loginは[キーリング＋age](guides/keyring.md)を既定にし、パスフレーズ・平文は明示選択とします。

| 項目 | 状態・完了条件 |
|---|---|
| `oaiso login` | v0.0.3で実装（実アカウント未確認）。エージェントと方式の選択、API接続確認、独立プロファイル保存 |
| 初回設定の自動化 | v0.0.3で実装。管理シークレット・プロファイル選択を準備し、既存の通信制限・設定を保持 |
| サブスク用Kata | 架空認証データで実Kata受入済み。実ログインは未確認。固定CLI、メモリ内認証、一時VM回収、通常ログ・作業VMへの秘密非残留 |
| 状態の一括表示 | v0.0.3で実装。[`oaiso status`](commands/status.md)で保存・接続・推論・基盤・更新を区別 |
| 軽量ローカル暗号化ストア | [v0.0.3で実装](guides/encrypted-credentials.md)。新規loginのキーリング既定・GUIなしの専用セッション・移行・パスフレーズ変更・ロック。実施済み試験は[動作確認](testing.md)を参照 |
| 更新の一括案内 | v0.0.3で実装。`oaiso upgrade`で配布版の計画・適用と、イメージの更新順を案内 |

- v0.0.3以降の[ログイン手順](commands/login.md)。v0.0.2の手動設定も維持します。

- Codex CLI・Claude Codeに加え、以下のコーディングエージェントへの対応を今後の作業として管理します。
- 記載したエージェントは未対応の候補です。
- 採用・実装順・提供時期は未確定です。

## その他のコーディングエージェントへの対応

| エージェント | 対応時に確認・実装すること |
|---|---|
| [Gemini CLI](https://github.com/google-gemini/gemini-cli) | CLI起動Adapter、認証方式、Gemini向けのProxy接続、headless実行 |
| [GitHub Copilot CLI](https://github.com/github/copilot-cli) | Copilot固有の認証・接続経路、GitHub連携、対話／非対話実行 |
| [OpenCode](https://opencode.ai/docs/) | CLI起動Adapter、provider設定、Proxy接続、非対話実行 |
| [Aider](https://aider.chat/) | API接続とProxy設定、Git変更・コミットの扱い、非対話実行 |
| [Cursor CLI](https://cursor.com/docs/cli/overview) | CLI起動Adapter、認証・接続先設定、headless実行 |
| [Cline CLI](https://docs.cline.bot/usage/cli-overview) | CLI起動Adapter、provider・認証設定、headless実行、ツール実行 |
| [Kilo Code CLI](https://kilo.ai/docs/code-with-ai/platforms/cli) | CLI起動Adapter、provider・認証設定、Proxy接続 |
| [Goose](https://github.com/aaif-goose/goose) | CLI起動Adapter、provider・認証設定、ツール／MCP連携 |

- エディター拡張やクラウド版の対応とは分け、まずローカルのCLIをKataで実行する形を調査します。
- API方式・サブスク方式の可否はエージェントと提供元ごとに確認し、両方式への対応を一律には約束しません。

## 追加対応の共通条件

- 認証Proxyを維持し、実APIキー・Access token・Refresh token・ID tokenを作業VMへ渡さない。
- 作業VMには短命・実行別・Proxy限定の仮トークンだけを渡す。
- ホストCLIの認証ファイルや継承環境を自動流用せず、本製品の独立した認証profileを使う。
- 認証方式・資格情報の保管・Proxy経由の利用が公式条件と両立しない場合は、対応を保留する。
- 認証専用Kataを使う場合は秘密を永続化せず、終了・取消・障害時に一時状態を破棄する。
- 資格情報を保持する常設VMへの置き換えは行わない。
- 独立した認証管理基盤を優先する。
- 実IDトークン返却は除去済みであり、新しいエージェントのために秘密分離やモデル認可を緩めない。
- CLI版・配布物・imageを固定し、ユーザー／プロジェクトの設定に依存しない明示的な起動設定を提供する。
- [上流仕様の変更検知](upstream-compatibility.md)にも監視対象を追加する。
- 実Kataで起動、ツール実行、ストリーム、取消、停止・再起動、失効、ログ・ファイル・snapshotへの資格情報漏出を確認する。
- 対応済みの機能と未対応の制約を[動作確認](testing.md)に記録する。
- 導入前にCLI配布物・SDK・保存する基準データのライセンスと、認証・再配布・サブスク利用の条件を確認する。

## WSL・VM内での独立した認証管理

- 初期の0600ファイルStore、独立profileの作成・明示import・選択、Auth Proxy経由のAPI/OAuth利用と更新保存は実装済みです。
- [使い方](commands/auth.md#独立プロファイル)を参照してください。
- 専用KataとAPI接続確認は[v0.0.3で実装](commands/login.md)。暗号化・明示移行も実装済みです。実アカウント接続、WSL/VM固有の実機受入、実際のデスクトップSecret Serviceとの接続確認は残作業です。

- 製品の制御環境がWSL 2・Linux VM・SSHヘッドレス環境でも成立する暗号化Vaultを設計対象とします。
- 作業VMとの資格情報分離と認証Proxyを維持し、OS keyring・systemd・TPMを必須にしません。
- 明示パスフレーズ、保存filesystemの保証、鍵lockと実行失効、別端末ブラウザ、backup・複製・rollbackを受入条件へ含めます。

- [詳細な設計要件](credential-store.md#独立した認証管理の設計要件wslvmヘッドレス)を参照してください。
- 暗号化ストアと鍵backendはv0.0.3で実装済みです。WSL／VMのKata実行対応と、OSキーリングのサービスごとの差異は別途の実機検証が必要です。

## 既存SDK依存の検出への対応

- age移行時の依存検査で、変更前19件から15件へ減少し、新規のsymbol検出は0件でした。
- 残るSDK依存の実到達性の精査と互換更新を優先作業として管理します。
- [検出結果と対象module](development/dependency-security.md)。
