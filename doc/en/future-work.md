# Future work

- Priorities, feature additions and security measures: [improvement candidates](development/improvements.md). Implemented controls and proposed additions remain distinct.

## Simple setup, authentication and operation

- Implement the following six items, validating, documenting and committing each feature.
- Retain existing profiles and the Auth Proxy. New login profiles default to [keyring plus age](guides/keyring.md), with explicit passphrase/plaintext alternatives.

| Item | State and completion criteria |
|---|---|
| `oaiso login` | Implemented in v0.0.3; live accounts unverified: agent/method selection, API connection check, independent profile storage |
| Automatic first configuration | Implemented in v0.0.3: admin authority and profile selection while preserving existing policy/settings |
| Subscription login Kata | Real Kata with fake credentials verified; live login unverified: pinned CLI, memory-only login, temporary VM cleanup, no secret retention in console logs or work VMs |
| Consolidated status | Implemented in v0.0.3: [`oaiso status`](commands/status.md) distinguishes saved, connected, inference, runtime and update states |
| Lightweight local encrypted storage | [Implemented in v0.0.3](guides/encrypted-credentials.md): keyring-default new login profiles, explicit migration, passphrase changes and locking; see [verification](../testing.md) |
| Unified update guidance | Implemented in v0.0.3: `oaiso upgrade` plans/applies distribution updates and explains image update order |

- [Login is available in v0.0.3+](commands/login.md); v0.0.2 manual setup remains supported.

- In addition to Codex CLI and Claude Code, the following coding agents are candidates for future integration.
- They are not currently supported.
- Adoption, implementation order and release dates are undecided.

## Additional coding agents

| Agent | Integration work to investigate and implement |
|---|---|
| [Gemini CLI](https://github.com/google-gemini/gemini-cli) | Launch adapter, authentication, Gemini proxy routing and headless execution |
| [GitHub Copilot CLI](https://github.com/github/copilot-cli) | Copilot authentication and routing, GitHub integration, interactive and non-interactive execution |
| [OpenCode](https://opencode.ai/docs/) | Launch adapter, provider configuration, proxy routing and non-interactive execution |
| [Aider](https://aider.chat/) | API/proxy configuration, Git changes and commits, non-interactive execution |
| [Cursor CLI](https://cursor.com/docs/cli/overview) | Launch adapter, authentication, endpoint configuration and headless execution |
| [Cline CLI](https://docs.cline.bot/usage/cli-overview) | Launch adapter, provider/auth configuration, headless execution and tools |
| [Kilo Code CLI](https://kilo.ai/docs/code-with-ai/platforms/cli) | Launch adapter, provider/auth configuration and proxy routing |
| [Goose](https://github.com/aaif-goose/goose) | Launch adapter, provider/auth configuration and tool/MCP integration |

- Investigate local CLI execution inside Kata separately from editor extensions and hosted offerings.
- API and subscription authentication must be assessed per agent/provider; support for both is not assumed.

## Shared acceptance conditions

- Retain the authentication proxy.
- Never pass real API keys, access tokens, refresh tokens or ID tokens to work VMs.
- Workers receive only short-lived, per-run, proxy-scoped placeholders.
- Use independent product authentication profiles, without automatically importing host CLI login files or inherited credentials.
- Hold integrations whose storage or proxy use cannot satisfy official authentication terms.
- Login-only Kata sessions must not persist secrets; discard temporary state on completion, cancellation and failure.
- Do not replace the proxy with a persistent VM holding real credentials.
- Prioritize independent credential management.
- Real ID-token passthrough has been removed; do not weaken credential isolation or model authorization to add an agent.
- Pin CLI versions, distributions and images; provide explicit launch settings independent of user/project configuration.
- Extend [static upstream checks](upstream-compatibility.md) for each agent.
- Verify startup, tools, streaming, cancellation, stop/restart, revocation and credential leakage into logs, files and snapshots in real Kata.
- Record verified behavior and unsupported constraints in the [testing guide](../testing.md).
- Review CLI/SDK licenses, retained baseline data, distribution terms and authentication/subscription conditions before integration.

## Independent authentication inside WSL and VMs

- The initial 0600 FileStore, independent profile creation/explicit import/selection and auth-proxy API/OAuth integration with durable refresh are implemented.
- See [usage](commands/auth.md#independent-profiles).
- Dedicated login Kata and API connection checks are [implemented in v0.0.3](commands/login.md). Encryption/explicit migration are also implemented. Live accounts, WSL/VM deployment acceptance and an actual desktop Secret Service still require verification.

- Design the product's encrypted vault for trusted control environments running in WSL 2, Linux VMs and SSH/headless systems.
- Retain the auth proxy and work-VM credential boundary without requiring an OS keyring, systemd or TPM.
- Acceptance must include explicit passphrase unlocking, filesystem guarantees, lock/revocation, a browser on another device, backup, cloning and rollback.

- See the [detailed requirements](credential-store.md#independent-credential-management-wsl-vms-and-headless-systems).
- Encryption/key backends are implemented in v0.0.3. Kata execution inside WSL/VMs and differences between desktop keyring services require separate deployment testing.

## Existing SDK dependency findings

- **Unreleased (main)**: dependency updates and applicability review completed; symbol findings reduced from 15 to 2. The earlier age migration reduced 19 to 15.
- Both remaining findings concern unused daemon features. No finding exclusions; inapplicability rationale and dependency/API boundary regressions added.
- Automated checks and follow-up [real Kata network/revocation acceptance](development/network-acceptance.md) pass. No full eight-suite rerun, live-account or WSL acceptance.
- [Findings and affected modules](development/dependency-security.md).
