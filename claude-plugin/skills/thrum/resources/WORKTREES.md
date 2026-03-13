# Multi-Worktree Coordination

Thrum enables agents in different git worktrees to communicate via a shared
daemon and git-backed message storage.

## Setup

Use the setup script to create worktrees with all required configuration:

```bash
# Full setup: creates worktree, branch, thrum redirect, .claude/settings.json, and identity
./scripts/setup-worktree-thrum.sh ~/.workspaces/thrum/feature feature/my-feature \
  --identity feature_impl --role implementer

# Existing worktree: adds thrum redirect and .claude/settings.json
./scripts/setup-worktree-thrum.sh ~/.workspaces/thrum/feature

# Auto-detect: fixes all worktrees missing redirects or settings
./scripts/setup-worktree-thrum.sh
```

### Critical: `.claude/settings.json`

This file is **gitignored** — each worktree needs its own copy. It registers
the `SessionStart` hook that runs `scripts/thrum-startup.sh` (agent
registration, daemon check, env vars). Without it, Claude Code sessions in the
worktree won't auto-register with Thrum.

The setup script copies it automatically from the main repo. If a worktree is
missing it, either re-run the setup script or copy manually:

```bash
cp /path/to/main-repo/.claude/settings.json ~/.workspaces/thrum/feature/.claude/settings.json
```

### Manual identity setup

Each worktree agent uses a unique identity via `THRUM_NAME`:

```bash
# In main worktree (/path/to/repo)
export THRUM_NAME=main_coordinator
thrum quickstart --name coordinator_main --role coordinator --module main --intent "Coordinating releases"

# In feature worktree (~/.workspaces/repo/feature)
export THRUM_NAME=feature_impl
thrum quickstart --name implementer_feature --role implementer --module feature --intent "Feature implementation"
```

## Shared Daemon

All worktrees share one daemon instance. The daemon auto-discovers worktrees via
the shared `.thrum/` directory at the git root.

```bash
# Start daemon once (from any worktree)
# Note: thrum init also starts the daemon automatically if not already running
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
