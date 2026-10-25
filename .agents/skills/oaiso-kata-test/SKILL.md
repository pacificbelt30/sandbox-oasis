---
name: oaiso-kata-test
description: "OaisoのVM・認証・通信・入出力・共有・Go APIの変更を、実Kataや導入・更新・復旧で検証するときに使う。単体テストや単なるクロスビルドには使わない。"
---

# 実Kataで検証する

## 対象と前提

- [検証手順](../../../doc/testing.md#新規導入更新復旧を検証する)と[隔離・認証の条件](../../../CLAUDE.md#invariants)を読む。
- 既存の利用環境と分離した使い捨てノードを優先する。ホスト上の稼働中containerd・BuildKit・nodeは停止・再設定しない。
- Linux x86_64、KVM、cgroup v2、空きメモリ、必要なデバイス・ツールを確認する。前提不足ならその項目を報告し、runcへの切替やskipで代用しない。
- CLI・node・guard・loggerと試験バイナリのBuildVersionをそろえる。イメージの固定CLI・entrypoint・Proxy protocolも照合する。検証ゲートを無効化して通さない。
- 架空の資格情報とローカルfixtureを使う。ホストの認証ファイル・SSH agent・管理ソケット・秘密の環境変数を作業VMへ渡さない。
- 開始前に`df -h /`と`docker system df`を記録し、2つのbundle、イメージarchive、使い捨てnodeのcontainerd展開領域、証跡を作れる空きを確保する。前回の同等試験の使用量と安全余裕を満たせない場合は、開始せず容量不足を報告する。

## 使い捨てノードで導入から確認する

- 実行場所：リポジトリのルート。
- [準備手順](../../../doc/testing.md#新規導入更新復旧を検証する)に従い、2つのバンドル、イメージアーカイブ、対応する受入バイナリ、試験用Ubuntuイメージを用意する。既存の試験資産がある場合は、所有者・版・内容を確かめて再利用する。
- 外側のDockerは試験ノードの隔離用。製品内部の実行はcontainerd・Kata・Dragonball・BuildKitを使う。

```bash
CODEX_DOCK_DISPOSABLE_SETUP=1 uv run --no-project --no-config --managed-python --python 3.12 \
  python scripts/verify-setup.py \
  --bundle dist/node-bundle-a --next-bundle dist/node-bundle-b \
  --images .kata-verification/setup-e2e/images.tar \
  --acceptance .kata-verification/setup-e2e/acceptance.test
```

| 目的 | 追加する指定・確認 |
|---|---|
| 受入の一部だけ再試験 | `--acceptance-run`で必要なスイートを明示。部分成功を全件成功と書かない |
| 初回利用 | `--online-onboarding`。空のHOME、実キーリング、初回ビルド、両CLIの前面起動・TTY入力・終了・削除を確認 |
| 配布インストーラー | `--release-installer`・`--setup-python`・`--uv`を組で指定し、パイプ経由の計画・適用を確認 |
| キャッシュ利用 | `--cache PATH --offline`。初回ワーカーのオンラインビルドとは別の指定 |
| 独自の試験イメージ | `--worker-image`・`--proxy-image`・`--image`を実物に合わせる |
| VM―認証ProxyのTLS・失効 | `--acceptance-run '^TestKataWorkerMTLS$'`。固定CLI・SSE/WS・再起動・broker・利用上限を確認 |
| Go API障害系 | [公開Go APIの受入](../../../doc/testing.md#公開go-apiの障害系受入)に従い、専用ノードで`make kata-api-e2e`も実行 |

## 確認する範囲

- VM隔離、非root・capability除去・NNP・seccomp、guest制限とホスト予約、token有効化前の検証を維持する。
- 認証Proxyには実資格情報を保持し、通常VMにはプレースホルダーだけを渡す。access/refresh/ID token・APIキーをファイル、環境、ログで確認する際も架空値だけを使う。
- detached起動・exec PTYだけで終えず、メインの前面TTY、短いコマンドの最終出力、stdin EOF、終了・再起動を確認する。
- 認証・通信の変更なら、失効中の既存ストリーム、保存競合・失敗、再起動・取消など変更した契約も確認する。
- 停止・削除時はワーカーに加えProxyとネットワークを確認する。共有Proxyを削除するときは停止を先に行う。

## 証跡と回収

- `verify-setup.py`は自分が作ったラベル付きノード・ボリュームだけを回収し、証跡を`.kata-verification/setup-e2e/`へ残す。
- 手動操作でもID・ラベル・所有権を照合してから回収する。一括prune、killall、他の試験や利用者の資源の削除は行わない。
- containerdに接続できない状態は「不明」。孤児・空き資源と決めつけて削除しない。
- 合格時もVM・task・コンテナ・ネットワーク・専用firewallの残留を調べる。失敗した場合は回収結果と証跡も報告する。
- キャッシュ有無、実キーリングかfixtureか、外部通信の有無を記録する。実アカウント・実WSL・対話画面の全操作まで確認したとは書かない。
- 再試験に使わない試験用Docker build cache・イメージ、展開bundle、試験バイナリは、ID・tag・labelと作成元を照合して終了時に回収する。共有Dockerの一括pruneや、Oaiso専用containerd data rootの直接削除は行わない。
- `.kata-verification/`では結果・失敗理由・回収記録を残し、再生成可能な重複archiveや一時rootだけを分けて回収する。証跡全体の削除は利用者の明示依頼がある場合に限る。
- 回収後に`df -h /`と`docker system df`を再確認し、開始前後の空き容量、残したcache・証跡、再取得が必要になる資産を報告する。
