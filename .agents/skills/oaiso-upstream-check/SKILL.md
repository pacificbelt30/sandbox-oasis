---
name: oaiso-upstream-check
description: "Codex・Claude Codeの候補版やAPI・認証の静的契約差分をOaisoで確認し、採用時に基準を更新するときに使う。通常テストだけならオフライン検査に限定する。"
---

# 上流との互換性を確認する

## 通常の変更確認

リポジトリのルートで、uv管理Python 3.12を用意して実行する。外部のAPI・SDK・CLIを取得する必要はない。

```bash
make contracts-check
```

- 対象は[manifest・基準](../../../compat/upstream/)と[監視ルール](../../../doc/upstream-compatibility.md)。オフライン検査は公開配布物の再取得までは行わない。

## 候補版を調べる場合

1. 調べるCLI版と対象の契約を明示し、manifest・worker Dockerfileの固定版を確認する。
2. [取得・capture手順](../../../doc/upstream-compatibility.md#ローカルで実行する)に従う。Python依存はuvの分離環境へ入れ、npmは専用ディレクトリを使い、ホストのグローバルCLIを更新しない。
3. `scripts/upstream-contracts.py capture`で候補を`.kata-verification/contracts/`へ保存し、`compare`のJSON・Markdownを読む。
4. 差分の固定URL・型・フラグ・変更箇所を確認する。静的差分を根拠なく実行障害と断定しない。

| 結果 | 扱い |
|---|---|
| `pass`／終了0 | 監視対象に要確認の差分なし。実通信の成功とは別 |
| `hold`／終了1 | review・breakingの内容を調べ、採用・対応を判断 |
| `error`／終了2 | 取得・解析などの検査不能。成功扱いにせず失敗元を記録 |

- CLIのhelp・version・schema exportは実行する。信頼する配布物を使い、captureの新規HOMEと環境変数の許可リストを維持する。
- ホストのAPIキー・OAuth・SSH agentを渡さず、ログイン・推論・トークン更新の負荷を上流へかけない。
- SDK型は静的解析し、監視のために上流モジュールをimportして実行しない。

## 採用するときだけ基準を更新する

- 必要な修正と[変更後のテスト](../oaiso-test/SKILL.md)を終えてから、確認済み候補を`compact`で`compat/upstream/baseline.json`へ変換する。
- CLIを採用する場合はDockerfileの固定版もそろえ、`make contracts-check`を再実行する。manifest変更時は新しい監視範囲で再取得する。
- 候補JSON全体や大きな一時成果物を基準へコピーしない。毎回の検査を理由に基準を更新しない。
- 認証・起動・ストリームの互換性は、必要に応じて実Kataとfixtureで別途確認する。
- 調べた版、差分、採否、検査できなかった対象を報告する。このスキルだけでリリースや自動更新を開始しない。
