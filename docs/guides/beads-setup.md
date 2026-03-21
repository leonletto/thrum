
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

### Setting Up Beads from an Existing Clone

When you clone a repo that already has beads data (stored in `refs/dolt/data` on
the Git remote), a standard `git clone` does **not** fetch this ref. You need to
bootstrap the dolt database manually.

This guide was verified end-to-end on bd 0.61.0 and dolt 1.81.8+. Follow the
steps in order — each step depends on the previous one.

#### Prerequisites

- `bd` and `dolt` installed (see [Installation](#installation) above)
- A git clone of the repo with SSH access to the remote
- The remote must have `refs/dolt/data` (check with the command below)

#### Step 0: Confirm your remote has beads data

```bash
git ls-remote origin | grep dolt
# Expected output: <hash> refs/dolt/data
# If you don't see this, the remote has no beads data — use bd init normally.
```

#### Step 1: Initialize beads

```bash
bd init
```

This creates the `.beads/` directory structure, config files, and an empty dolt
database. It will print a note suggesting `bd bootstrap` — as of bd 0.61.0,
`bd bootstrap` may handle the full setup automatically. Try it first and skip to
Step 7 if it succeeds.

The dolt server may or may not start successfully during init. Either way, we
need to replace the empty database with the remote data, so continue below.

#### Step 2: Stop the dolt server

```bash
bd dolt stop
```

If the server didn't start during init, you'll see "Dolt server is not running"
— that's expected, continue to the next step.

#### Step 3: Find your database name and remove the empty database

```bash
# Check your database name (look for "dolt_database")
cat .beads/metadata.json
```

The `dolt_database` field is your `<dbname>`. For a repo named `myproject`, it
will typically be `myproject`.

```bash
# Remove the empty database that bd init created
rm -rf .beads/dolt/<dbname>/
```

This must be done **before** cloning. If you skip this step, `dolt clone` will
fail because the target directory already exists.

#### Step 4: Clone the dolt data from your git remote

```bash
cd .beads/dolt
dolt clone git@github.com:org/repo.git <dbname>
cd ../..
```

Dolt automatically reads from `refs/dolt/data` on the git remote. This downloads
all beads issue data. For HTTPS remotes, use `https://github.com/org/repo.git`
instead.

**You must `cd` back to the repo root** before running any `bd` commands — they
need to discover the `.beads/` directory from the project root.

#### Step 5: Start the dolt server

```bash
bd dolt start
```

This starts a background `dolt sql-server` process. You should see output like:

```text
Dolt server started (PID <num>, port <num>)
```

If it fails, check `.beads/dolt-server.log` for errors. A common cause is a
corrupted database from a previous failed attempt — go back to Step 3 and start
over.

#### Step 6: Run schema migrations

```bash
bd migrate --yes
```

This upgrades the database schema if the remote data was created with an older
version of beads. If the schemas already match, it updates just the version
marker. Safe to run even if no migrations are needed.

#### Step 7: Ensure the remote is registered

`dolt clone` may or may not preserve the remote configuration (behavior varies
across dolt versions). Both the dolt CLI **and** the SQL server need to know
about the remote for `bd dolt push` and `bd dolt pull` to work.

Run these commands — they are safe if the remote already exists (you'll see
"remote already exists" errors, which you can ignore):

```bash
# Add to dolt CLI (run from inside the dolt database directory)
cd .beads/dolt/<dbname>
dolt remote add origin git@github.com:org/repo.git
cd ../../..

# Add to SQL server
bd sql "CALL dolt_remote('add', 'origin', 'git@github.com:org/repo.git')"
```

"remote already exists" errors on either command mean `dolt clone` already set
it up — that's fine, move on.

#### Step 8: Verify

```bash
bd dolt test          # should print: ✓ Connection successful
bd dolt remote list   # should show: origin <your-remote-url>
bd list               # should show your issues
```

If `bd list` shows your issues, you're done.

#### Why this process is necessary

You cannot simply run `bd dolt pull` after `bd init`. A freshly initialized
database and the remote have completely separate histories with no common
ancestor. Dolt's pull (and fetch + reset) either fails with "no common ancestor"
or corrupts the local database. Deleting the empty database and cloning fresh is
the only reliable path.

#### After setup

Use the normal sync workflow to keep your issues in sync:

```bash
bd dolt commit    # commit working set changes
bd dolt push      # push to remote
bd dolt pull      # pull from remote (must commit first)
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
