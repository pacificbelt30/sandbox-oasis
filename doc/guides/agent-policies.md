# ホスト管理のエージェントポリシー

> **日本語** | [English](../en/guides/agent-policies.md)

利用可能：**v0.0.6以降**。

Codex / Claude Code の承認、推論、履歴などの既定値をホスト側でまとめて管理できます。ポリシーは `~/.config/codex-dock/agent-policies/config.toml` に `0600` で保存され、リポジトリからは変更できません。

## まず使う

```bash
oaiso agent-policy list
oaiso agent-policy show approve-for-me
oaiso run --agent codex --agent-policy approve-for-me
```

組み込みポリシーは次の6個です。

| 名前 | 用途 |
|---|---|
| `interactive` | 利用者が承認を判断する保守的な既定値 |
| `approve-for-me` | Codexは `approvals_reviewer=auto_review`、Claudeは `auto` |
| `autonomous` | 対話承認なし。Kataと通信規則は維持 |
| `review` | 高い推論量とplan寄りのレビュー |
| `private` | Web検索と履歴保存を無効化 |
| `restricted` | 読み取り中心でエージェントの道具を狭める |

`approve-for-me` はCodexのレビュー機構に承認判断を委ねます。これはCodexのsandbox、Kata VM、ワーカー別通信規則を無効化しません。

## ホスト既定値にする

```bash
oaiso agent-policy init
oaiso agent-policy default approve-for-me
oaiso agent-policy audit
oaiso agent-policy plan
```

`run.agent_policy = "approve-for-me"` を通常の `config.toml` に書く方法もあります。優先順位は、ネイティブエージェント引数、Oaisoの明示フラグ、`oaiso.toml` の移植可能な設定、グローバル設定、ホストポリシー、組み込み値です。

## 独自ポリシー

組み込み値を直接変更せず、継承して差分だけ設定します。

```bash
oaiso agent-policy clone approve-for-me team
oaiso agent-policy set team description "Team default"
oaiso agent-policy set team codex.reasoning_effort high
oaiso agent-policy set team codex.web_search disabled
oaiso agent-policy set team claude.effort high
oaiso agent-policy default team
```

値を親から再び継承するには `unset`、差分確認には `diff` を使います。

```bash
oaiso agent-policy unset team codex.web_search
oaiso agent-policy diff interactive team
oaiso agent-policy doctor
```

利用可能な項目・列挙値・上流キー・安全上の意味は `oaiso agent-policy fields [SEARCH]` で確認できます。任意のTOML断片やCLI引数は保存できず、固定した許可項目だけがJSON経由でゲストの設定へ変換されます。

## 安全境界

- `full-auto` と `danger` は引き続き明示的な強い設定です。`agent-policy audit` は危険度と代替案を表示します。
- Codexの `web_search=live` やClaudeのWeb機能は、Oaisoの通信許可先を増やしません。
- モデル名は既定値にすぎません。Auth Proxyのモデル許可規則が優先されます。
- Kata検証済みワーカーではKataを主境界とするため、Codex内側のsandbox既定は従来どおり `danger-full-access` です。
- プロジェクトの `oaiso.toml` はホスト専用項目を設定できません。

## ファイルを直接編集する場合

```toml
version = 1
default = "team"

[profiles.team]
description = "Team default"
extends = "approve-for-me"

[profiles.team.codex]
reasoning_effort = "high"
web_search = "disabled"

[profiles.team.claude]
effort = "high"
```

編集後は `oaiso agent-policy validate` と `oaiso agent-policy doctor` を実行してください。未知のキー、継承循環、不正な列挙値、グループ・他ユーザーから読めるファイルは拒否されます。
