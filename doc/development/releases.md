# リリースの作成

## 配布物

| ファイル | 内容 |
|---|---|
| `oaiso-linux-amd64.tar.gz` | 静的CLI・起動検証／ログ用プログラム・版とSHA-256・ライセンス／notice |
| `install.sh` | そのリリースの版を埋め込んだ単独インストーラー |
| `SHA256SUMS` | 配布アーカイブ・インストーラー・SBOMのSHA-256 |
| `sbom.cdx.json` | 未リリースmain：実バイナリのGo依存・固定runtimeの一覧 |
| `provenance.json` | 未リリースmain：GitHub OIDC／Sigstoreの署名付き作成元証跡 |

- 対象ホストはUbuntu 26.04 x86_64です。
- `go.mod` のGo版、uv 0.13.5、uv管理Python 3.12で生成します。
- ツールの導入と公式手順：[開発用ツールを準備する](toolchain.md)。
- 作業イメージ・Kata等の固定配布物・認証情報は、このアーカイブには含みません。

## ローカルで配布物を確認する

```bash
: "${release_tag:?先に作成する版を設定してください}"
uv python install 3.12
make node-bundle VERSION="$release_tag"
uv run --no-project --no-config --managed-python --python 3.12 python scripts/package-release.py \
  --bundle dist/node-bundle --output dist/release --version "$release_tag"
cd dist/release
sha256sum --check SHA256SUMS
bash -n install.sh
```

- `release_tag` は次に作成する版です。
- バイナリの版・SHA-256・Linux amd64 ELF・静的リンクを検査します。
- Goと実際の依存モジュールのライセンス／noticeを同梱します。
- 通常CIでも `v0.0.0-ci` の配布物を生成して整合性を確認します。

## 公開する

1. `doc/release-notes.md` を更新し、先頭を `# Oaiso vX.Y.Z` にします。
2. 変更をmainへcommit・pushし、CIの成功を確認します。
3. 同じcommitにリリースタグを作成・pushします。

```bash
git tag -a "$release_tag" -m "Oaiso $release_tag"
git push origin "$release_tag"
```

- `Release` workflowがGo/Pythonテスト、ビルド、アーカイブ生成、SHA-256確認を実行します。
- 下書きリリースへ全ファイルを添付してから公開します。
- 安定版は最新版、`-rc`等を含む版はプレリリースとして公開します。
- 公開済みリリースは上書きしません。修正を配布する場合は新しい版を作ります。
- 未公開の下書きを再試行する場合は、Actionsの `Release` を既存タグとともに手動実行できます。`publish=false` なら下書きのまま保持します。

## 公開後に確認する

- 署名付き版は5ファイルを取得し、署名・作成元・SHA-256を確認します。公開v0.0.3は従来の3ファイルです。
- 署名付き版は`install.sh`自体を実行前に検証し、通常ユーザーで選択版の配置計画を表示できること。パイプ実行はHTTPS bootstrapを信頼する手順として区別します。
- 完全な配置・更新・復旧は、[使い捨てKataノードの試験](../testing.md#新規導入更新復旧を検証する)で確認します。

## 署名付き配布（未リリースmain）

- [利用者の検証・信頼範囲・巻戻し防止](../guides/signed-releases.md)。公開v0.0.3の配布物は変更しません。
- Release workflowが対象タグをcheckoutし、ビルド後にarchive・installer・checksum・SBOMへ署名付き作成元証跡を付けます。
- workflow内でrepo・workflow・tag・commit・runnerを検証してから、5つのアセットを下書きへ添付し、最後に公開します。
- 手動起動でもworkflowのrefと入力tagを一致させます。main上のworkflowで別タグを署名する操作は拒否します。
- 公開後は、installer自体の署名を検証してから計画・適用を確認します。新規導入・更新・復旧の検証には、旧版拒否・明示した巻戻し・版の下限維持を含めます。
- ローカルpackage生成にはGitHubの署名は付きません。ローカルの整合性確認、main CIの実署名試験、公開配布物の確認を分けて報告します。
