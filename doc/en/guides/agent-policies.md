# Host-managed agent policies

> [日本語](../../guides/agent-policies.md) | **English**

Available: **unreleased (`main`)**.

Oaiso can keep Codex and Claude Code defaults for approvals, reasoning, history, and related behavior on the host. Policies live in owner-private `~/.config/codex-dock/agent-policies/config.toml`; repositories cannot change host-only fields.

## Start here

```bash
oaiso agent-policy list
oaiso agent-policy show approve-for-me
oaiso run --agent codex --agent-policy approve-for-me
```

Built-ins are `interactive`, `approve-for-me`, `autonomous`, `review`, `private`, and `restricted`. The `approve-for-me` profile selects Codex `approvals_reviewer=auto_review` and Claude `auto`. Automated review does not disable the Codex sandbox, Kata VM, or per-worker egress policy.

Select a host default:

```bash
oaiso agent-policy init
oaiso agent-policy default approve-for-me
oaiso agent-policy audit
oaiso agent-policy plan
```

Alternatively set `run.agent_policy = "approve-for-me"` in the normal global config. Precedence is native agent arguments, explicit Oaiso flags, portable repository settings, global config, host policy, then built-in defaults.

## Create a policy

Clone a built-in and store only the differences:

```bash
oaiso agent-policy clone approve-for-me team
oaiso agent-policy set team description "Team default"
oaiso agent-policy set team codex.reasoning_effort high
oaiso agent-policy set team codex.web_search disabled
oaiso agent-policy set team claude.effort high
oaiso agent-policy default team
```

Use `unset` to inherit again, `diff` to compare effective values, and `doctor` to find redundant or hard-to-maintain inheritance. `oaiso agent-policy fields [SEARCH]` lists every accepted value and its safety meaning.

Profiles cannot contain arbitrary TOML fragments or agent arguments. Oaiso validates a fixed allowlist and passes structured JSON to the guest, where the entrypoint owns the generated configuration keys.

## Boundaries

- `full-auto` and `danger` remain strong choices; `agent-policy audit` reports their risks.
- Live web features cannot grant destinations denied by Oaiso egress policy.
- A model is only a default; Auth Proxy model authorization remains authoritative.
- Verified Kata workers retain the existing Codex `danger-full-access` inner default because Kata is the primary boundary.
- Repository `oaiso.toml` cannot set host-only agent-policy fields.

Directly edited files must use schema version 1 and mode `0600`. Run `oaiso agent-policy validate` and `oaiso agent-policy doctor` afterward. Unknown keys, invalid enum values, inheritance cycles, and files readable by another user are rejected.
