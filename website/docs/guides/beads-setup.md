---
title: "Beads Setup Guide"
description:
  "Set up Beads for git-backed task tracking alongside Thrum — installation,
  initialization, agent workflow, and worktree configuration"
category: "tools"
order: 1
tags: ["beads", "setup", "task-tracking", "dependencies", "agents", "guide"]
last_updated: "2026-02-27"
---

## Beads Setup Guide

[Beads](https://github.com/leonletto/beads) is a git-backed, dependency-aware
issue tracker designed for AI agent workflows. It pairs with Thrum to give
agents persistent task memory that survives session boundaries.

### Why Beads with Thrum

Thrum handles **communication** — messages, presence, coordination. Beads
handles **task state** — what needs doing, what's blocked, what's done. Together
they solve the two halves of the agent context-loss problem:

- Agent restarts? `bd ready` shows what to work on next.
- Context compacts? The pre-compact hook saves `bd stats` and `bd list` output
  to Thrum context, which agents restore on session start.
- Multiple agents? Each claims tasks with `bd update --status=in_progress` and
  announces it via `thrum send`.

### Installation

```bash
# Using Go
go install github.com/leonletto/beads/cmd/bd@latest

# Using Homebrew
brew install leonletto/tap/beads

# Verify installation
bd version
```

Requires Go 1.22+ for source install, or use the Homebrew tap for a pre-built
binary.

### Initialize in Your Project

```bash
cd your-project
bd init
```

This creates a `.beads/` directory with:

| File | Purpose |
|------|---------|
| `config.yaml` | Configuration (defaults work out of the box) |
| `issues.jsonl` | Append-only event log — the git-tracked source of truth |
| `beads.db` | SQLite projection for fast queries (gitignored) |
| `metadata.json` | Database metadata |
| `README.md` | Quick reference |

The key design: `issues.jsonl` is tracked in Git, while `beads.db` is
gitignored. On any fresh clone, Beads rebuilds the SQLite database from the
JSONL automatically.

### Core Workflow

#### Create issues

```bash
# Create an epic (groups related tasks)
bd epic create --title="Add user authentication" --priority=1

# Create tasks under it
bd create --title="Implement JWT middleware" --type=task --priority=2
bd create --title="Write auth tests" --type=task --priority=2

# Set dependencies (tests depend on middleware)
bd dep add <tests-id> <middleware-id>
```

#### Find and claim work

```bash
# Show tasks ready to work (no blockers)
bd ready

# See what's blocked and why
bd blocked

# Claim a task
bd update <id> --status=in_progress
```

#### Complete work

```bash
# Close a task
bd close <id>

# Close multiple at once
bd close <id1> <id2> <id3>

# Check progress
bd stats
```

### Agent Integration with Thrum

The standard agent workflow combines both tools:

```bash
# 1. Agent starts — check for assigned work
thrum inbox --unread
bd ready

# 2. Claim a task and announce it
bd update <id> --status=in_progress
thrum send "Starting work on <id>: <title>" --to @coordinator

# 3. Do the work...

# 4. Complete and announce
bd close <id>
thrum send "Completed <id>, tests passing" --to @coordinator

# 5. Find next task
bd ready
```

### Claude Code Configuration

To use Beads with Claude Code agents, add these instructions to your
`CLAUDE.md`:

```markdown
## Task Tracking

Use `bd` (beads) for all task tracking. Do not use TodoWrite, TaskCreate, or
markdown files for tracking.

- `bd ready` — find available work
- `bd update <id> --status=in_progress` — claim a task
- `bd close <id>` — mark complete
- `bd stats` — check project progress
```

The Thrum Claude Code plugin automatically detects Beads and includes task
context in the pre-compact hook, so agents recover their task state after
context compaction.

### Worktree Support

If you use git worktrees (common in multi-agent setups), Beads supports sharing
a single database across worktrees via a redirect file:

```bash
# In each worktree, create a redirect to the main repo's .beads/
echo "/path/to/main/repo/.beads" > .beads/redirect
```

If your project includes the Thrum worktree setup script, this is handled
automatically:

```bash
./scripts/setup-worktree-thrum.sh ~/.workspaces/project/feature feature/name
```

### Git Hooks

Beads provides git hooks that keep the JSONL export fresh:

```bash
bd hooks install
```

This adds:
- **pre-commit**: Exports the SQLite state to `issues.jsonl` so it stays
  in sync with Git
- **pre-push**: Checks for stale data before pushing

### Useful Commands Reference

| Command | Purpose |
|---------|---------|
| `bd ready` | Tasks with no blockers |
| `bd list` | All open issues |
| `bd list --status=in_progress` | Active work |
| `bd blocked` | Blocked issues with reasons |
| `bd show <id>` | Full issue detail |
| `bd stats` | Project health overview |
| `bd dep add <a> <b>` | A depends on B |
| `bd epic create --title="..."` | Create an epic |
| `bd close <id>` | Mark complete |
| `bd sync --from-main` | Pull beads updates (for branches) |

### Further Reading

- [Beads and Thrum](../beads-and-thrum.md) — Conceptual overview of how the two
  tools complement each other
- [Beads UI Setup](beads-ui-setup.md) — Visual dashboard for Beads
- [Beads GitHub](https://github.com/leonletto/beads) — Full documentation and
  source
