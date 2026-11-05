# `oaiso completion` — shell completion

> [日本語](../../commands/completion.md) | **English**

- `oaiso` can generate completion scripts for Bash, Zsh, Fish, and PowerShell.
- The [release installer](../installation.md) with `--apply` installs Bash, Zsh and Fish completion in the standard system directories.
- Source installations also install completion through `scripts/install.sh --apply` or `make install`.
- Open a new shell to use them.
- Bash requires the `bash-completion` package.

- Completion is generated and installed only for `oaiso`.

For the current Bash session:

```bash
source <(oaiso completion bash)
```

For Zsh, as the same normal host user:

```zsh
autoload -Uz compinit
compinit
source <(oaiso completion zsh)
```

For Fish:

```fish
oaiso completion fish | source
```

- If you installed only the CLI with `go install`, you can save the script in your user's completion directory.
- For Bash:

```bash
mkdir -p ~/.local/share/bash-completion/completions
oaiso completion bash > ~/.local/share/bash-completion/completions/oaiso
```

- Completion covers commands and flags, agent choices for `default`, agent/runtime/approval/template/`--image` values for `run`, and worker names for `start`, `stop`, `rm`, and `logs`.
- For `vm inspect/start/stop/rm`, candidates come from the dedicated node's VM inventory and reflect the action.
- Completion uses VM names when unambiguous and IDs for duplicate names or orphans.
- Worker lookups have a 500 ms deadline; VM inventory lookups have a 2 second deadline.
- Other completion remains available if the node is stopped.

## Sandbox operation completion

Available: **unreleased (main)**.

| Argument | Candidates |
|---|---|
| `connect` | Running VM names; stopped VMs are also offered with `--start` |
| `exec` | Running VM name first, then executable command names inside that VM |
| `files get` | VM name, then regular files and directories beneath its workspace |
| `files list` / `files put` | VM name, then workspace directories |
| `artifact plan` / `artifact bundle` | VM name and files/directories for `--include`; credential paths and links are excluded |

- Guest path and command discovery has a 5 second deadline. Failure returns no candidates and never falls back to host filenames. Static command/flag completion remains available.
- Guest path completion does not append a space. Continue into a directory, or type a space to finish the argument.
- Command names are discovered from the VM's PATH without running candidate programs, shell profiles or completion scripts.

## TUI command input

Available: **unreleased (main)**. Select a running VM and press `X`. Type the first command word to see suggestions; arrows select and Tab or Enter completes. Choose `Run` to execute. Esc cancels the form even while suggestions are visible.

Basic shell command suggestions are available immediately; executable names from the VM are added with a 10 second deadline without blocking input. Basic suggestions and manual input remain available if discovery fails. Argument and complex shell-expression completion are outside this feature's scope.
Tab also reevaluates suggestions after pasting a command name.
