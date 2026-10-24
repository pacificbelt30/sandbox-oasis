---
name: oaiso-development
description: Oaisoリポジトリの実装・ドキュメントを変更するときに使う開発ルール。隔離・認証の条件と、README・機能ガイド・コマンド例の編集方針を確認する。
---

# Oaiso development patterns

Read [CLAUDE.md](CLAUDE.md) for conventions and [architecture](doc/architecture.md) for the current containerd/Kata/Dragonball design.
These patterns describe this repository. They do not replace the host setup or security checks.

## ドキュメント編集

README・利用ガイド・リファレンス・リリース情報の編集では、次の6点を守ります。

| 項目 | 必須ルール |
|---|---|
| 日本語 | 簡潔な日本語で書く。難解な言い回しや不要な横文字を使わない |
| 構造 | 比較・設定は表、手順は番号付き箇条書き、補足は短い箇条書きにする。長い文章で説明を重ねない |
| README | 概要・導入・基本操作・詳細ページへの案内を書く。リリースの変更点や新バージョンの告知は書かず、`doc/release-notes.md`・GitHub Releasesへ分ける |
| 機能の導入版 | 各機能の説明に初収録版を書く（例：`利用可能：v0.0.3以降`）。タグ・履歴で確認し、未公開なら`未リリース（main）`と明記する |
| 実行例 | 具体的なコマンドを`bash`ブロックで示す。必要に応じて出力例を別の`text`・`json`ブロックに載せ、成功の目印を書く |
| まとめて実行 | 同じコードブロックは、上からまとめてコピーして実行できるようにする。条件別・エージェント別・代替手順は見出しとコードブロックを分ける。コメントアウトした代替コマンドを同居させない |

- 実行ユーザー・作業ディレクトリ・前提条件・置換が必要な値を、コマンドの前に示す。
- 対話入力・シェル切り替え・再ログインを挟む場合は、その前後でコードブロックを分ける。
- 日本語と英語で、操作・条件・導入版・確認範囲をそろえる。
- 詳しい書き方と確認手順：[ドキュメント編集ガイド](doc/development/documentation.md)。

### 説明の正確さも確認する

| 観点 | 確認すること |
|---|---|
| 読む順番 | 読者の目的から始め、前提→設定→実行→確認→後片付けの順にする。初回利用と更新を混ぜない |
| 実装との一致 | CLIのhelp・設定例・実装を照合する。存在しないフラグ・環境変数・自動処理を書かない |
| 必須と任意 | 既定値、必要な設定、任意の設定を分ける。利用者に全選択肢を実行させない |
| 実行の影響 | ホストとVM、実行ユーザーと権限、共有ファイルの変更、外部通信、停止・削除の範囲を明記する |
| 成功の意味 | 終了コードと次の確認を示す。準備・保存・接続・推論・タスク成功、例示・実測・未確認を区別する |
| 失敗時の操作 | どの条件に失敗したか、直す場所、再確認するコマンドを示す |
| 説明の置き場所 | 詳細は一か所で管理し、要約からリンクする。パス・見出し・目次・日英・設定例も更新する |
| 秘密と証跡 | 架空の値を使い、実資格情報や秘密を含むログを載せない。文書ビルドの成功を実動作の確認と取り違えない |

## 作業別のスキル

- `.agents/skills/`に置く。作業に合うものだけを読み、詳細はリンク先の既存手順を参照する。
- スキルを使うだけで操作の許可は増えない。利用者の依頼と既存の許可の範囲で進める。

| 作業 | スキル |
|---|---|
| 文書・README・コマンド例の編集 | [oaiso-docs](.agents/skills/oaiso-docs/SKILL.md) |
| 変更後のGo・Python・CLI検証 | [oaiso-test](.agents/skills/oaiso-test/SKILL.md) |
| 実Kata・初回導入・更新・復旧 | [oaiso-kata-test](.agents/skills/oaiso-kata-test/SKILL.md) |
| ボトルネック調査・性能比較 | [oaiso-performance](.agents/skills/oaiso-performance/SKILL.md) |
| Codex／Claude Code・API契約の差分確認 | [oaiso-upstream-check](.agents/skills/oaiso-upstream-check/SKILL.md) |
| 配布物作成・リリース・公開物の確認 | [oaiso-release](.agents/skills/oaiso-release/SKILL.md) |
| Git同期・競合解消・commit／push | [oaiso-git-sync](.agents/skills/oaiso-git-sync/SKILL.md) |

## Add a command or setting

1. Put Cobra wiring in `cmd/` and business logic in `internal/`.
2. Register the command and preserve explicitly supplied flags over config defaults.
3. Wire environment overrides explicitly; do not assume a `CODEX_DOCK_*` name works automatically.
4. Update `configs/config.toml.example` and both configuration references.
5. Add focused tests for new runtime behavior or contracts.

Do not import `cmd/` from internal packages. Avoid new global state.

## Change worker lifecycle

Use `sandbox.RunOptions` and the existing Manager. The engine talks to the dedicated node, not a Docker daemon.
New and restarted workers must pass pinned asset, host process/cgroup and guest restriction checks before the guard releases user code.
Keep token activation after successful verification.

Stop revokes the token and preserves restart state. Restart restores the placeholder for the same worker ID.
Remove persists intent first and retains cleanup retries for network, token, worktree and cgroup failures.
Use existing ownership checks for recovery; do not kill arbitrary PIDs or delete unknown resources.

## Change AI proxy behavior

Keep credential loading and refresh in `internal/authproxy`, separate from sandbox management.
Use the `Service` boundary for management calls.

- `worker_auth.go`: authenticate issued tokens before upstream access.
- `reverse.go`, `websocket.go`: shared forwarding with a provider-specific injector.
- `openai.go`, `anthropic.go`: provider credentials and refresh.
- `tokens.go`, `admin.go`, `restore.go`: leases and management API.
- `forward.go`, `transport.go`: credential-free general egress.
- `internal/networkpolicy`: destination rules independent of authentication and engine details.

Preserve HTTP/SSE/WS cancellation on revoke, expiry, replacement, restore and stop.
Credential-file changes must keep caller UID/GID, private HOME, read-only mounts and CapDrop ALL.
Use local upstream fixtures for credential injection and rejection tests.

## Change network behavior

Use `internal/network` for worker network lifecycle and `internal/engine/network*.go` for native host resources.
Worker names resolve through static hosts, not Docker embedded DNS.
Admin remains on the primary egress interface, separate from worker-facing data.
Do not reintroduce a shared worker network or bypass destination policy through a generic upstream proxy.

General allow/deny rules belong in `internal/networkpolicy` and must apply consistently to HTTP, redirects and CONNECT.
Private IP blocks, full DNS-answer checks and fixed-IP dialing must remain effective.
See [policy extension boundaries](doc/proxy-policy.md#コードの境界).

## Change images and templates

Build through `internal/engine.Build` and standalone BuildKit. Import into the same containerd namespace.
Keep the auth entrypoint protocol compatible; expose the matching script through `build --print-entrypoint`.
User templates under `~/.config/codex-dock/templates/<name>/Dockerfile` override built-ins.
Preserve tag resolution, validation and on-disk Dockerfile timestamp checks.
Package auto-detection already exists; use explicit `apt:`, `pip:` and `npm:` prefixes in examples.

## Check a change

Use gofmt for Go files. Return contextual errors, close bodies and use request contexts.
Run focused tests, then the required checks for the affected behavior:

```bash
make build
make test
make vet
make lint
```

Runtime changes also need `make kata-e2e` on a prepared Linux/KVM host.
Documentation changes need `mkdocs build --strict`.
See [testing](doc/testing.md) for live AI checks and limitations.
Do not put real credentials, tokens or private acceptance logs into the repository.
