# Multi-Worktree Coordination

Thrum enables agents in different git worktrees to communicate via a shared
daemon and git-backed message storage.

## Setup

Use `thrum worktree create` to create a worktree and register an agent in one
step. This handles git, redirects, identity, and tmux session setup.

```bash
# Two-step pattern (create + launch)
thrum worktree create my-feature \
  --name impl_my_feature --role implementer --module my-feature \
  --intent "Feature implementation"
# → creates worktree, registers agent, creates tmux session
# → output tells you to run: thrum tmux launch my-feature

thrum tmux launch my-feature
# → starts the runtime (claude/codex/cursor) in the tmux pane
```

The worktree is created at `<base_path>/<name>` (default
`~/.workspaces/<repo-name>/<name>`). Branch defaults to `feature/<name>`,
override with `-b <branch>`.

`worktree create` automatically:

- Creates the git worktree
- Sets up the `.thrum/` redirect to the main repo
- Creates a tmux session with the worktree as cwd
- Runs quickstart inside the pane (PID-isolated, retries if shell init swallows
  the command)
- Reports the next-step `tmux launch` command

The agent is **NOT running** until `thrum tmux launch <name>` is called. The
launch step is what actually starts the AI runtime (claude/codex/etc).

### Worktree without an agent

If you want the worktree set up but no agent registered yet, omit the agent
flags:

```bash
thrum worktree create my-feature
# → creates worktree + redirect only
# → output tells you: thrum tmux create my-feature --cwd <path> --name <agent> --role <r> --module <m>
```

### Existing worktree

For a worktree that already exists without thrum setup, use `tmux create`
directly:

```bash
thrum tmux create existing-feature --cwd /path/to/worktree \
  --name impl_existing --role implementer --module existing
thrum tmux launch existing-feature
```

## Shared Daemon

All worktrees share one daemon instance. The daemon auto-discovers worktrees via
the shared `.thrum/` directory at the git root.

```bash
# Start daemon once (from any worktree)
# Note: thrum init also starts the daemon automatically if not already running
thrum daemon start

# All worktrees connect to the same daemon
thrum daemon status    # Shows shared daemon health from any worktree
```

## Cross-Worktree Messaging

```bash
# From main worktree
thrum send "Feature branch ready for integration" --to @feature_impl

# From feature worktree
thrum inbox    # Sees message from @main_coordinator
thrum sent     # Verifies what this worktree sent and who read it
thrum reply <msg-id> "Integration tests passing, ready to merge"
thrum message read --all  # Mark all messages as read
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
