## Beads Setup Guide

[Beads](https://github.com/steveyegge/beads) is a Dolt-backed, dependency-aware
issue tracker designed for AI agent workflows. It pairs with Thrum to give
agents persistent task memory that survives session boundaries.

> **Upgrading from bd 0.62 or earlier?** bd 1.0 switched the default backend
> from a separate `dolt sql-server` process to an embedded, single-writer store
> and can't read the old on-disk layouts. If you have existing bd data, follow
> the [Beads Migration to Embedded Mode](beads-migration-to-embedded.md) guide
> before running any `bd` commands — several commands (`bd dolt push`,
> `bd dolt start`, `bd backup --force`) have been removed or replaced.

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

bd 1.0+ ships with an embedded Dolt store baked into the binary. There is no
separate `dolt` prerequisite for the default install — you just need `bd`. (The
`dolt` CLI is only needed if you want to run raw SQL against the embedded
database, or for some advanced backup/migration workflows.)

**Minimum versions:** bd 1.0.0+

```bash
# Option A: Homebrew
brew install beads

# Option B: Install script (installs to /usr/local/bin)
curl -fsSL https://raw.githubusercontent.com/steveyegge/beads/main/scripts/install.sh | bash

# Verify
bd version    # must be 1.0.0+
```

> **Already have bd installed at 0.62 or earlier?** Upgrade is machine-wide (the
> `bd` binary lives in a shared path like `/usr/local/bin/bd`) and will break
> every unmigrated repo on the machine at once. Before running
> `brew upgrade beads` or the install script, follow the
> [migration guide](beads-migration-to-embedded.md) — it walks through
> inventorying your repos, capturing each one's data via `dolt dump` or JSONL
> export, and loading everything back into the fresh embedded store.

### Initialize in Your Project

```bash
cd your-project
bd init
```

`bd init` in bd 1.0+ creates an **embedded Dolt store** inside the repo — no
background server, no port coordination, no dolt-server process to manage. All
writes go through a single-writer file lock.

The `.beads/` directory it creates:

| Path                     | Purpose                                                |
| ------------------------ | ------------------------------------------------------ |
| `config.yaml`            | Team-level config (committed to git)                   |
| `metadata.json`          | Local state: backend, mode, database name (gitignored) |
| `.gitignore`             | Ignores lock files and internal dolt data              |
| `interactions.jsonl`     | Interaction log                                        |
| `README.md`              | Quick reference                                        |
| `embeddeddolt/<prefix>/` | Embedded Dolt database (single-writer, file-locked)    |

`bd init` also creates `AGENTS.md` in the repo root with agent instructions.

> **Tradeoff:** Embedded mode is single-writer. Multiple concurrent `bd`
> processes serialize through the file lock. For Thrum's agent topologies
> (14-ish agents, serial coordinator-driven writes) this is fine. For heavy
> concurrent workloads, opt in to server mode explicitly by setting
> `dolt.mode: server` in `.beads/config.yaml` — but the server-mode lifecycle
> commands (`bd dolt start`/`stop`/`push`) are gone in 1.0, so you'd be managing
> `dolt sql-server` directly.

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

### Sync Setup (bd 1.0+ embedded mode)

Beads stores its state inside the embedded Dolt database at
`.beads/embeddeddolt/<prefix>/.dolt/`. For off-machine sync, bd 1.0 offers two
complementary mechanisms:

- **`backup.git-push`** in `.beads/config.yaml` — auto-exports to
  `.beads/backup/*.jsonl` and pushes those files alongside your normal commits.
  Useful when beads data should travel as tracked files in the repo.
- **`dolt.auto-push`** (set via `bd config set dolt.auto-push true`) — pushes
  the embedded Dolt store to a hidden `refs/dolt/data` git ref on the remote at
  an interval. The hidden ref is invisible to `git log` / `git clone` but
  travels with `git push --all` / `git fetch origin 'refs/dolt/*'`. `bd init`
  auto-configures a dolt remote pointed at your current git origin.

**Typical private-repo setup (Dolt ref sync, no tracked files):**

```bash
# 1. Turn on auto-commit (required for auto-push to have anything to push)
bd config set dolt.auto-commit on

# 2. Turn on auto-push
bd config set dolt.auto-push true

# 3. Run one initial manual push to establish the remote baseline
cd .beads/embeddeddolt/<prefix>
dolt push origin main
cd -

# Verify the ref is live:
git ls-remote origin 'refs/dolt/*'
# Expected: <hash>  refs/dolt/data
```

**Typical public-repo setup (external backup script, no dolt-ref sync):**

```yaml
# .beads/config.yaml — don't publish the hidden dolt ref publicly
backup:
  git-push: false
```

> **bd 0.62 sync commands removed in 1.0.** `bd dolt push`, `bd dolt pull`,
> `bd dolt start`, `bd dolt stop`, and `bd dolt commit` no longer exist in
> embedded mode. If your scripts or CLAUDE.md reference them, update to
> `bd config set dolt.auto-push true` (or run `dolt push origin main` directly
> from inside `.beads/embeddeddolt/<prefix>` when you need an explicit push).
> The [migration guide](beads-migration-to-embedded.md) has the full
> before/after command table.

### Setting Up Beads from an Existing Clone (legacy bd 0.59–0.63 server mode)

> **This procedure applies to bd 0.59–0.63 server mode only.** On bd 1.0+
> embedded mode, a fresh clone is usually `bd init` followed by
> `dolt pull origin main` from inside `.beads/embeddeddolt/<prefix>/` (if the
> remote has a `refs/dolt/data` ref) or `bd import <jsonl-file>` (if the data
> travels as `.beads/backup/*.jsonl` tracked files). If you're on bd 1.0 and
> want to bootstrap from an existing remote, see the
> [migration guide](beads-migration-to-embedded.md) — the `bd init` → data load
> → verify flow is the same shape as a fresh migration.

The steps below are preserved for teams still on bd 0.59–0.63 server mode. They
assume bd 0.61.0 and dolt 1.81.8+. Follow the steps in order — each step depends
on the previous one.

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

### Claude Code Integration (`bd setup claude`)

Run `bd setup claude` to install the SessionStart hook that auto-loads
`bd prime` context into every Claude Code session. Upstream Beads ships this as
the canonical Claude Code integration — lighter-weight than the standalone
plugin and recommended over it.

```bash
brew install beads
bd setup claude
```

Restart Claude Code so the hook loads.

If you're using Thrum, you don't need to run these commands yourself —
`thrum init` (and runtime-init on each session) installs the bd `SessionStart`
hook in `.claude/settings.json` automatically whenever `Worktrees.BeadsEnabled`
is true (default) and `bd` is on `PATH`. If `bd` state changes after the first
`thrum init`, re-run `thrum init` to refresh the hook presence.

**Migrating from the standalone Beads plugin** (existing users) — run these five
steps in order:

1. `/plugin uninstall beads@beads-marketplace` (inside Claude Code)
2. `/plugin marketplace remove beads-marketplace` (inside Claude Code)
3. `brew install beads`
4. `bd setup claude`
5. Restart Claude Code

The standalone plugin is no longer recommended; upstream Beads now treats the
SessionStart-hook path as canonical.

### CLAUDE.md Configuration

To pin agent behavior in your repo's `CLAUDE.md` (or to give sub-agents the same
workflow context the SessionStart hook loads into the main session), add these
instructions:

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

**`bd dolt ...` errors with "not supported in embedded mode"** Expected on bd
1.0+. Use `bd config set dolt.auto-push true` or run `dolt push origin main`
directly from inside `.beads/embeddeddolt/<prefix>/`. See the
[migration guide](beads-migration-to-embedded.md) for the full before/after
command table.

**"no common ancestor" on push** Stale `refs/dolt/data` from a previous
database. Clear the remote ref and retry:

```bash
# Delete the stale remote ref:
git push origin :refs/dolt/data

# Re-push from the embedded store:
cd .beads/embeddeddolt/<prefix>
dolt push origin main
```

**"database is locked" or "lock file already exists"** Another `bd` process is
already writing. Embedded mode is single-writer via file lock. Wait for the
other process to exit, or inspect `.beads/embeddeddolt/<prefix>/.dolt/` for a
stale lock if no process is running.

**`bd doctor` says "not yet supported in embedded mode"** Known placeholder on
bd 1.0. Use `bd migrate` (no args) for the schema/metadata health check and
`ls -la .beads/embeddeddolt/` to verify the database exists.

**`bd ready` errors with `column "no_history" could not be found`** Schema drift
from a very old bd version's data being loaded into a fresh bd 1.0 store. The
[migration guide](beads-migration-to-embedded.md) Step 6C has the ALTER + CREATE
TABLE fixes.

**brew / dolt / bd not found over SSH** Homebrew on ARM64 Mac installs to
`/opt/homebrew/bin`, not in the default SSH PATH. Use a login shell:
`ssh host 'bash -lc "bd version"'`

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
| `bd update <id> --claim`                           | Atomic claim (assign + in_progress)        |
| `bd close <id>`                                    | Mark complete                              |
| `bd close <id> --suggest-next`                     | Close and show newly unblocked work        |
| `bd dep <blocker> --blocks <blocked>`              | Blocker must close before blocked is ready |
| `bd dep tree <epic-id>`                            | Show dependency graph                      |
| `bd epic status <epic-id>`                         | Progress on epic children                  |
| `bd comments add <id> "msg"`                       | Add comment (subcommand before ID)         |
| `bd search "query"`                                | Search issues by text                      |
| `bd import <file.jsonl>`                           | Load issues from a JSONL file              |
| `bd export --all -o <file.jsonl>`                  | Export all issues to JSONL                 |
| `bd config set dolt.auto-commit on`                | Auto-commit dolt writes (recommended)      |
| `bd config set dolt.auto-push true`                | Auto-push embedded store to `refs/dolt/*`  |
| `bd migrate`                                       | Schema/metadata health check               |

> **bd 0.62 commands removed or replaced in 1.0+:** `bd dolt start`,
> `bd dolt stop`, `bd dolt push`, `bd dolt pull`, `bd dolt commit`,
> `bd dolt status`, `bd backup --force`, `bd sync`, `bd onboard`, `bd doctor`
> (as a health check). See the [migration guide](beads-migration-to-embedded.md)
> for the full before/after mapping.

**Comments syntax note:** The correct syntax is `bd comments add <id> "msg"` —
subcommand comes before the issue ID.

### Further Reading

- [Beads and Thrum](../beads-and-thrum.md) — Conceptual overview of how the two
  tools complement each other
- [Beads Migration to Embedded Mode](beads-migration-to-embedded.md) — Field
  report on upgrading existing installs to bd 1.0 embedded mode
- [Beads UI Setup](beads-ui-setup.md) — Visual dashboard for Beads
- [Beads GitHub](https://github.com/steveyegge/beads) — Full documentation and
  source
