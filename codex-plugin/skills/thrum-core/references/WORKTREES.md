# Multi-Worktree Coordination

Thrum enables agents in different git worktrees to communicate via a shared
daemon and git-backed message storage.

## Setup

Each worktree agent uses a unique identity via `THRUM_NAME`:

```bash
# In main worktree (/path/to/repo)
export THRUM_NAME=main_coordinator
thrum quickstart --role coordinator --module main --intent "Coordinating releases"

# In feature worktree (~/.workspaces/repo/feature)
export THRUM_NAME=feature_impl
thrum quickstart --role implementer --module feature --intent "Feature implementation"
```

## Shared Daemon

All worktrees share one daemon instance. The daemon auto-discovers worktrees via
the shared `.thrum/` directory at the git root.

```bash
# Start daemon once (from any worktree)
thrum daemon start

# All worktrees connect to the same daemon
thrum status    # Shows shared daemon health from any worktree
```

## Cross-Worktree Messaging

```bash
# From main worktree
thrum send "Feature branch ready for integration" --to @feature_impl

# From feature worktree
thrum inbox    # Sees message from @main_coordinator
thrum reply <msg-id> "Integration tests passing, ready to merge"
```

## File Coordination

Check which agent is editing a file before making changes:

```bash
thrum who-has src/auth/login.ts
# Output: @feature_impl (active)

# Coordinate via message
thrum send "Need to edit login.ts, are you done?" --to @feature_impl
```

## Sync

Messages sync via git. The daemon handles push/pull automatically. Force sync if
needed:

```bash
thrum sync force
thrum sync status
```

For multi-machine setups, ensure all machines can push/pull to the same git
remote.
