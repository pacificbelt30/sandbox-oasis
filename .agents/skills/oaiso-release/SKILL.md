---
name: oaiso-release
description: "Oaisoの指定版の配布バンドル作成、公開前確認、GitHub Release公開、公開物の検証を行うときに使う。通常のコード・文書更新を理由に公開しない。"
---

# リリースを作成・確認する

## 対象を決める

- [リリース手順](../../../doc/development/releases.md)、[Release workflow](../../../.github/workflows/release.yml)、既存タグ・リリースを確認する。
- 対象版と公開の依頼・既存の許可を確認する。作成・検証の依頼だけでタグをpushしない。公開がすでに許可されている場合は、同じ確認を繰り返さない。
- 依頼の版を使い、勝手に次の版を決めない。公開済みタグ・アセットを上書きしない。

## 公開前に準備する

1. 対象版の変更を確認し、`doc/release-notes.md`の先頭をその版に合わせる。READMEにはリリース告知を書かない。
2. 公開版とmainの手順を区別し、機能の初収録版・日英・インストール例をそろえる。既存プロファイルや暗号化形式の更新・旧版への戻し方を確認する。
3. [変更後のテスト](../oaiso-test/SKILL.md)と[文書検査](../oaiso-docs/SKILL.md)を行う。実行コードを変えていなければ、版表示だけの変更を理由に成功済み実Kata全件試験を繰り返さない。
4. 配布物のCLI・node・guard・logger・試験バイナリの版をそろえる。必要な導入・旧公開版からの更新／復旧は[使い捨てノード](../oaiso-kata-test/SKILL.md)で確認する。

以下はリポジトリのルートで実行する。`RELEASE_TAG`には依頼された版を先に設定し、Go・Make・uv管理Python 3.12を準備する。

```bash
: "${RELEASE_TAG:?先に対象版をRELEASE_TAGへ設定してください}"
make node-bundle VERSION="$RELEASE_TAG"
uv run --no-project --no-config --managed-python --python 3.12 \
  python scripts/package-release.py \
  --bundle dist/node-bundle --output dist/release --version "$RELEASE_TAG"
(cd dist/release && sha256sum --check SHA256SUMS)
bash -n dist/release/install.sh
```

- アーカイブは静的Linux amd64 CLI・補助プログラム・manifest・ライセンス／noticeを含む。Kata配布物・作業イメージ・資格情報を同梱したと誤記しない。
- ローカル候補の確認と、公開された配布物の確認を分ける。公開バイナリはタグのクリーンなソースからworkflowが生成する。

## 許可された公開を行う

1. 対象の変更をcommit・pushし、そのコミットSHAのCI成功を確認する。
2. 同じコミットへ指定タグを作成・pushする。workflow_dispatchは対象タグのrefで起動し、入力tagとも一致させる。タグpushはworkflowによる公開につながる。
3. Release workflowの成功と、下書きへの全ファイル添付後の公開を確認する。

- 公開済みリリースは不変として扱う。失敗時はジョブ・ステップ・ログを確認し、原因を説明できた場合だけ未公開の同じworkflowを再試行する。修正のない連続再実行や、タグの移動で失敗を隠さない。
- 部分的な添付・失敗は未完了として報告し、必要な修正が次の版になる場合はその判断を示す。

## 公開物を確認する

- リリースAPIでタグ、draft/prerelease、latest、対象コミット、5つのアセット（archive・install.sh・SHA256SUMS・sbom.cdx.json・provenance.json）を確認する。
- 未リリースmain以降の署名付き版は[署名・作成元の検証](../../../doc/guides/signed-releases.md)を先に行う。repo・release workflow・対象タグ・runner種別を照合し、不足や不一致をchecksumだけで代用しない。旧公開版には署名を後付けしない。
- 実ファイルを取得し、`SHA256SUMS`・bundle manifest・SBOM・版表示を照合する。Go build情報が含まれる場合はソースコミットとクリーンなビルドも確認する。
- 署名付き版はインストーラー自体を実行前に検証する。パイプ実行はHTTPS bootstrapを信頼する手順として区別する。
- 公開インストーラーを通常ユーザーで実行し、`--apply`なしの計画を確認する。Goが不要という説明はGoをPATHから外して確認する。
- 公開ファイルのダウンロード確認に、事前展開したバンドル向けの`--offline`を混ぜない。オフライン利用には`--bundle`と必要な資産の準備が必要。
- 検証目的で既存ホストへ適用・サービス再起動をしない。実配置・更新・rollbackは使い捨てノードで確認する。
- タグとmainの関係、CI・workflow・アセット・計画表示の結果を報告する。公開したリンクを示し、確認した範囲を超えて動作保証しない。
