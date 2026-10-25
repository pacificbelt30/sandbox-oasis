# 署名と作成元を確認してインストールする

**日本語** | [English](../en/guides/signed-releases.md)

- 利用可能：未リリース（main）。次の署名付き配布版から適用します。
- 公開済みv0.0.3はchecksum方式です。署名を後付けしたり、公開物を上書きしたりしません。
- mainの新インストーラー・`upgrade`は、署名のないオンライン配布物を拒否します。

## 確認するもの

| 対象 | 確認・保護範囲 |
|---|---|
| 署名 | GitHub OIDC／Sigstore。GitHub CLIが管理する認証局と署名鍵で検証。配布物に付属する鍵を新たに信頼しない |
| ビルド元 | 3つの実行ファイルのGo build情報を、変更のないcheckoutのcommitと照合 |
| 作成者 | `pacificbelt30/sandbox-oasis`の`.github/workflows/release.yml`と、選択した`refs/tags/<版>`に限定 |
| 実行環境 | GitHub管理runner。self-hosted runnerの証跡は拒否 |
| 対象ファイル | 配布archive、`install.sh`、`SHA256SUMS`、`sbom.cdx.json`を署名付き作成元証跡に結び付ける |
| 証跡 | `provenance.json`。署名・証明書・透明性の証拠を含む検証用bundle |
| 依存一覧 | CycloneDX 1.6。実バイナリに結合したGo module・Go標準ライブラリ・固定runtime資産の版とhash |
| 一覧の対象外 | guest OCI image内のpackage・OSのpackage。別途管理する |
| 旧版への巻戻し | root管理の`release-floor.json`に到達済みの最も新しい版を保存。下位版の適用は既定で拒否 |

- checksumは破損検出用です。署名・作成元の確認の代わりにはしません。
- 長期の配布用秘密鍵をローカルやリポジトリへ保存しません。CIが短命の署名用証明書を取得します。
- 信頼するGitHub／Sigstore、workflow、依存元が侵害された場合の安全まで保証するものではありません。

## 必要なツール

| ツール | 必要な場面 | 公式手順 |
|---|---|---|
| GitHub CLI（`gh`） | 署名付き配布物の導入・`upgrade`。通常のVM利用には不要 | [Linuxへの導入](https://github.com/cli/cli/blob/trunk/docs/install_linux.md) |
| Bash・curl・CA証明書・uv管理Python | 従来の配布インストーラー | [インストール](../installation.md) |

- `gh attestation verify`の`--bundle`・`--signer-workflow`・`--source-ref`・`--cert-identity`に対応する版を使用します。
- 保存した証跡bundleを使うため、GitHubへのログインは要求しません。検証に使う信頼情報の取得・更新に通信する場合があります。
- ツール不足・署名不一致・未知の署名者・別workflow・別タグ・証跡不足は、配置前にエラーにします。

## スクリプトも実行前に確認する

通常ユーザーで実行します。**署名付き配布版のタグを`RELEASE_TAG`へexportしてから**、次のブロックを実行してください。署名導入前のv0.0.3では使用できません。

```bash
bash <<'SH'
set -euo pipefail
: "${RELEASE_TAG:?先に署名付き配布版のタグをexportしてください}"
oaiso_install_dir=$(mktemp -d)
trap 'rm -rf -- "$oaiso_install_dir"' EXIT
base="https://github.com/pacificbelt30/sandbox-oasis/releases/download/$RELEASE_TAG"
curl -fLsS --proto '=https' --proto-redir '=https' "$base/install.sh" -o "$oaiso_install_dir/install.sh"
curl -fLsS --proto '=https' --proto-redir '=https' "$base/provenance.json" -o "$oaiso_install_dir/provenance.json"
gh attestation verify "$oaiso_install_dir/install.sh" \
  --bundle "$oaiso_install_dir/provenance.json" \
  --repo pacificbelt30/sandbox-oasis \
  --signer-workflow pacificbelt30/sandbox-oasis/.github/workflows/release.yml \
  --source-ref "refs/tags/$RELEASE_TAG" \
  --cert-identity "https://github.com/pacificbelt30/sandbox-oasis/.github/workflows/release.yml@refs/tags/$RELEASE_TAG" \
  --cert-oidc-issuer https://token.actions.githubusercontent.com \
  --deny-self-hosted-runners --hostname github.com
bash "$oaiso_install_dir/install.sh" --version "$RELEASE_TAG"
SH
```

- 成功：署名を検証し、変更を適用しない導入計画を表示します。
- 適用：検証するタグを固定したまま、末尾のinstaller呼出しへ`--apply`を追加します。
- `curl ... | bash`は、最初のスクリプトをHTTPS配布元から信頼して実行する方法です。スクリプト自体の実行前検証には上の手順を使います。
- `oaiso upgrade`は、取得したinstallerの署名も確認してから実行します。

## 旧版・オフライン・障害

| 操作・条件 | 扱い |
|---|---|
| 通常の導入・更新 | 到達済みの版より古ければ、サービス停止前に拒否 |
| 意図して古い署名付き配布版を導入 | `upgrade`／installer／`setup`に`--allow-downgrade`。最も新しい版の記録は下げない |
| インストール失敗からの自動復旧 | 既存のtransaction復旧を維持 |
| `setup --rollback --apply` | 明示した復旧操作。復旧後も到達済みの最も新しい版を維持 |
| `--bundle PATH` | 利用者が明示したローカルの信頼済みソースビルド／bundle用。署名付きオンライン配布の検証は行わない |
| オフラインで配布archiveを持ち込む | 接続できる環境で署名・hashを検証し、証跡とともに持ち込む。検証に使う鍵の準備は[GitHubの公式手順](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/verify-attestations-offline)を参照 |
| rootによるfloor削除・検証用の鍵・ツールの差替え | 信頼するホスト管理者の操作。ローカル情報だけで防ぐことはできない |

- Go／Makeは、ビルド済み配布物の導入には不要です。
- 署名の成功は、脆弱性なし・再現可能ビルド・guest image内の安全性の保証ではありません。
- CIはmain向けの試験artifactでも実署名を作り、改変・別workflow・別タグの拒否を確認します。この証跡はrelease用の検証条件では受理しません。
- [公式の証跡機能](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations)・[検証CLI](https://cli.github.com/manual/gh_attestation_verify)・[確認した範囲](../testing.md)。
