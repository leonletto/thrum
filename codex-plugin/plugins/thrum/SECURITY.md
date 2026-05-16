# Security

## Reporting a vulnerability

Please report security issues via the parent
[thrum](https://github.com/leonletto/thrum) repository — open a private security
advisory rather than a public issue. Do not disclose details publicly until a
fix is available.

## Plugin scope

This plugin runs hook scripts under your local shell with your user permissions.
The scripts are auditable plain Bash under `codex-plugin/scripts/`:

- `inject-prime-context.sh` — read-only context injection at SessionStart.
- `block-sync-worktree-cd.sh` — denies a Bash invocation if it would `cd` into
  the daemon's a-sync git worktree (read-only enforcement).
- `stop-check-messages.sh` — reads inbox and listener state; may emit a
  block-stop continuation prompt.

None of the hooks write outside the `~/.thrum/` and `~/.agents/` paths the
parent `thrum` daemon already manages.
