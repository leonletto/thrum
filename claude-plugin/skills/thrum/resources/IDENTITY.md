# Agent Identity

## Registration

Every agent must register before sending or receiving messages.

```bash
thrum quickstart --role <role> --module <module> --intent "<description>"
```

This registers the agent, starts a session, and sets the work intent.

## Identity Components

- **Name** — Human-readable identifier (auto-generated or via `--name`)
- **Role** — Function category: `implementer`, `planner`, `reviewer`, `tester`,
  `coordinator`
- **Module** — Work area: `auth`, `website`, `api`, `testing`, etc.
- **Agent ID** — Format: `<name>:<role>:<module>` (e.g.,
  `claude_impl:implementer:auth`)

## Identity Storage

Identities persist in `.thrum/identities/<name>.json`. Registration is
idempotent — re-registering with the same name updates the existing identity.

## Multi-Worktree Identity

See [WORKTREES.md](WORKTREES.md) for multi-worktree identity setup.

## Checking Identity

```bash
thrum whoami          # Current identity
thrum status          # Identity + session + daemon health
thrum prime           # Full context including identity
```

## Session Lifecycle

```bash
thrum session start                        # Begin session
thrum session set-intent "new intent"      # Update intent
thrum session heartbeat                    # Keep alive
thrum session end                          # End session (do this when done)
```

Sessions make your agent visible in `thrum team`. Forgetting to end sessions
creates stale entries.
