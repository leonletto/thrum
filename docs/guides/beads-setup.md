
## Beads Setup Guide

[Beads](https://github.com/steveyegge/beads) is a Dolt-backed, dependency-aware
issue tracker designed for AI agent workflows. It pairs with Thrum to give
agents persistent task memory that survives session boundaries.

### Why Beads with Thrum

Thrum handles **communication** — messages, presence, coordination. Beads
handles **task state** — what needs doing, what's blocked, what's done. Together
they solve the two halves of the agent context-loss problem:

- Agent restarts? `bd ready` shows what to work on next.
- Context compacts? The pre-compact hook saves `bd stats` and `bd list` output
  to Thrum context, which agents restore on session start.
- Multiple agents? Each claims tasks with `bd update -s in_progress` and
  announces it via `thrum send`.

### Installation

Beads requires [Dolt](https://github.com/dolthub/dolt) as a prerequisite.
Install it first.

**Minimum versions:** bd 0.59.0+, dolt 1.81.8+

```bash
# 1. Install Dolt (required dependency)

# Option A: Homebrew
brew install dolt

# Option B: Install script (Linux/macOS — installs to /usr/local/bin)
sudo bash -c 'curl -L https://github.com/dolthub/dolt/releases/latest/download/install.sh | bash'

# 2. Install Beads CLI

# Option A: Homebrew
brew install steveyegge/beads/bd

# Option B: Install script (installs to ~/.local/bin/bd)
curl -fsSL https://raw.githubusercontent.com/steveyegge/beads/main/scripts/install.sh | bash

# 3. Verify
bd version    # must be 0.59.0+
dolt version  # must be 1.81.8+
```

### Initialize in Your Project

```bash
cd your-project
bd init
```

`bd init` v0.59.0+ defaults to a **Dolt backend** (not SQLite) running in server
mode. It spawns a background `dolt sql-server` process and auto-restarts it on
each `bd` command if it's not running.

The `.beads/` directory it creates:

| Path                       | Purpose                                                |
| -------------------------- | ------------------------------------------------------ |
| `config.yaml`              | Team-level config (committed to git)                   |
| `metadata.json`            | Local state: backend, mode, database name (gitignored) |
| `.gitignore`               | Ignores lock files, pid files, dolt internals          |
| `interactions.jsonl`       | Interaction log                                        |
| `README.md`                | Quick reference                                        |
| `dolt/config.yaml`         | Dolt server config                                     |
| `dolt/<reponame>/`         | Dolt database (synced via `refs/dolt/data`)            |
| `dolt-server.pid/port/log` | Server process files (gitignored)                      |

`bd init` also creates `AGENTS.md` in the repo root with agent instructions.

### Core Workflow

#### Create issues

```bash
# Create an epic (groups related tasks)
bd create --title="Add user authentication" --type=epic --priority=1

# Create tasks under it
bd create --title="Implement JWT middleware" --type=task --priority=2
bd create --title="Write auth tests" --type=task --priority=2

# Set dependencies (tests depend on middleware)
# bd dep <blocker> --blocks <blocked>: blocker must close before blocked becomes ready
bd dep <middleware-id> --blocks <tests-id>
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

### Dolt Sync Setup

Beads uses `refs/dolt/data` to sync task state via your Git remote — no separate
database server needed.

```bash
# Add remote (using your existing git remote)
bd dolt remote add origin "file:///path/to/your/repo/.git"

# For GitHub:
bd dolt remote add origin "git+ssh://git@github.com/org/repo.git"
```

On a fresh repo with no prior `refs/dolt/data`, the auto-push after `remote add`
succeeds without `--force`.

**Daily sync workflow:**

```bash
bd dolt commit    # commit working set changes
bd dolt push      # push to remote
bd dolt pull      # pull (must commit first — see common errors)
```

**Important:** Never run raw `dolt` CLI commands while the server is running —
it causes journal corruption. Always use `bd dolt ...` commands instead.

**Verify sync:**

```bash
git show-ref | grep dolt
# Expected: <hash> refs/dolt/data
```

**New machine setup:** `refs/dolt/data` is not fetched by default `git clone`.
Set `sync.git-remote` in `.beads/config.yaml` to enable auto-bootstrap on
`bd init`, or clone the dolt database manually:

```bash
cd .beads/dolt
dolt clone git+ssh://git@github.com/org/repo.git <reponame>
```

### Agent Integration with Thrum

The standard agent workflow combines both tools:

```bash
# 1. Agent starts — check for assigned work
thrum inbox --unread
thrum sent --unread
thrum message read --all       # Mark all messages as read
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

### Worktree Support

If you use git worktrees (common in multi-agent setups), Beads supports sharing
a single Dolt server across worktrees via a redirect file:

```bash
# In each worktree, point to the main repo's .beads/
mkdir -p /path/to/worktree/.beads
echo "/path/to/main/repo/.beads" > /path/to/worktree/.beads/redirect
```

All worktrees share the same Dolt server via the redirect — issues created in
any worktree are immediately visible everywhere.

If your project includes the Thrum worktree setup script, this is handled
automatically:

```bash
./scripts/setup-worktree-thrum.sh ~/.workspaces/project/feature feature/name
```

### Claude Code Plugin

Install the Beads plugin for Claude Code to get slash commands (`/beads:ready`,
`/beads:create`, etc.) and an MCP server for native tool integration:

```bash
# In Claude Code
/plugin marketplace add steveyegge/beads
/plugin install beads
```

Restart Claude Code after installation. See the
[Beads plugin docs](https://github.com/steveyegge/beads/blob/main/docs/PLUGIN.md)
for the full command reference.

### CLAUDE.md Configuration

For agents that don't use the plugin (or as a supplement), add these
instructions to your `CLAUDE.md`:

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

### Common Errors and Fixes

**"no store available" on `bd dolt push` or `bd dolt commit`** Bug in bd
v0.55.4. Upgrade: `brew upgrade beads` (must be 0.59.0+).

**"no common ancestor" on push** Stale `refs/dolt/data` from a previous
database. Clear it and retry:

```bash
git update-ref -d refs/dolt/data
```

**"cannot merge with uncommitted changes" on pull** Run `bd dolt commit` before
`bd dolt pull`.

**"fatal: Unable to read current working directory"** The dolt server's CWD no
longer exists. Restart it:

```bash
pkill -f "dolt sql-server"
bd list   # auto-restarts
```

**brew / dolt / bd not found over SSH** Homebrew on ARM64 Mac installs to
`/opt/homebrew/bin`, not in the default SSH PATH. Use a login shell:
`ssh host 'bash -lc "bd version"'`

### Stale LOCK files after crash

```bash
bd doctor --fix --yes
# If noms LOCK persists:
rm .beads/dolt/<reponame>/.dolt/noms/LOCK
```

**Journal corruption from mixed CLI/server access** Caused by running raw `dolt`
CLI while the server is running. Prevention: always use `bd dolt ...` commands.
Recovery: stop the server, delete the journal, and restart.

### Commands Reference

| Command                                            | Purpose                                    |
| -------------------------------------------------- | ------------------------------------------ |
| `bd ready`                                         | Tasks with no blockers                     |
| `bd list`                                          | All open issues                            |
| `bd list --status=in_progress`                     | Active work                                |
| `bd blocked`                                       | Blocked issues with reasons                |
| `bd show <id>`                                     | Full issue detail                          |
| `bd stats`                                         | Project health overview                    |
| `bd create --title="..." --type=task --priority=2` | Create an issue                            |
| `bd update <id> --status=in_progress`              | Claim work                                 |
| `bd close <id>`                                    | Mark complete                              |
| `bd dep <blocker> --blocks <blocked>`              | Blocker must close before blocked is ready |
| `bd dep tree <epic-id>`                            | Show dependency graph                      |
| `bd epic status <epic-id>`                         | Progress on epic children                  |
| `bd comments add <id> "msg"`                       | Add comment (subcommand before ID)         |
| `bd search "query"`                                | Search issues by text                      |
| `bd dolt commit`                                   | Commit working set changes                 |
| `bd dolt push`                                     | Push to remote                             |
| `bd dolt pull`                                     | Pull from remote (commit first)            |
| `bd doctor`                                        | Health check                               |
| `bd doctor --fix --yes`                            | Auto-fix stale locks and metadata          |

**Comments syntax note:** The correct syntax is `bd comments add <id> "msg"` —
subcommand comes before the issue ID.

### Further Reading

- [Beads and Thrum](../beads-and-thrum.md) — Conceptual overview of how the two
  tools complement each other
- [Beads UI Setup](beads-ui-setup.md) — Visual dashboard for Beads
- [Beads GitHub](https://github.com/steveyegge/beads) — Full documentation and
  source
