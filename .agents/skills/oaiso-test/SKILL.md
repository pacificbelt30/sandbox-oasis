---
name: oaiso-test
description: "Oaisoのコード・設定・配置スクリプトの変更後やCI失敗時に、必要なGo・Python・CLI検証を選んで実行する。文書だけの変更はoaiso-docsを使う。"
---

# 変更に合うテストを実行する

## 範囲を選ぶ

- [CLAUDE.md](../../../CLAUDE.md)、`go.mod`、[Makefile](../../../Makefile)、[検証手順](../../../doc/testing.md)を確認する。
- 利用者の未コミット変更を把握し、検証目的で設定・資格情報・稼働サービスを書き換えない。
- ツール不足時は[公式の導入手順](../../../doc/development/toolchain.md)で開発用環境を準備する。OSのPythonやGoを無断で置き換えない。

| 変更 | 確認 |
|---|---|
| Go | 影響パッケージのテスト後、build・race付き全件テスト・vet・lint |
| `scripts/` | 対応するPythonテスト。配布処理ならバンドル整合性も確認 |
| `deploy/kata/` | 配置・設定生成のPythonテスト。実行経路に影響する場合は実Kataも確認 |
| 認証・管理JSON・URL/path・通信規則の境界 | `make security-fuzz`も実行。[探索範囲と失敗入力の扱い](../../../doc/development/security-tests.md) |
| CLI名・引数 | `scripts/verify-cli-names.py`と通常サブコマンドのhelpを照合 |
| 上流CLI版・監視manifest | [上流互換性](../oaiso-upstream-check/SKILL.md) |
| 文書のみ | [ドキュメント編集](../oaiso-docs/SKILL.md) |

## Goを変更した場合

リポジトリのルートで、変更したGoファイルをgofmtし、影響パッケージを先に検査する。その後、次を実行する。

```bash
make build
make test
make vet
make lint
```

- 依存を変更したときだけ`go mod tidy`し、go.mod/go.sumの差分を確認する。
- VM・認証Proxy・通信・入出力・共有・資源管理・公開Go APIの実行経路に影響する場合は[実Kata検証](../oaiso-kata-test/SKILL.md)も行う。
- クロスビルド成功を、そのOSでKataを実行できた証拠にしない。

## `scripts/`を変更した場合

uv管理Python 3.12を使い、リポジトリのルートで実行する。

```bash
uv run --no-project --no-config --managed-python --python 3.12 \
  python -m unittest discover -s scripts -p 'test_*.py'
```

## `deploy/kata/`を変更した場合

```bash
uv run --no-project --no-config --managed-python --python 3.12 \
  python -m unittest discover -s deploy/kata -p 'test_*.py'
```

## 結果を扱う

- 最初の失敗を残して原因を調べ、修正した経路を再検証する。理由なく同じ成功済み試験を繰り返さない。
- 実装をなぞるだけのテストや、文書の語句だけを照合するテストを増やさない。必要なら利用者から見える挙動・不変条件を検証する。
- 未実行・skip・前提不足を成功に数えない。外部AIへの認証・推論は、fixtureや静的試験の成功とは分ける。
- コマンド、対象版、終了結果、再現条件、未確認範囲を報告する。生ログ・秘密・大きな生成物はコミットせず、`.kata-verification/`などへ置く。
- CI確認時は対象コミットSHAを照合する。別のコミットやPR専用の実行結果を、今回のpushの成功とみなさない。
