---
title: "Beads Migration to Embedded Mode (bd 1.0+)"
description:
  "Field report on migrating beads from any pre-1.0 source state (SQLite-era,
  transitional, or server-mode Dolt) to bd 1.0 embedded mode. Two recovery
  paths, schema-drift fixes, sync re-establishment, and the full troubleshooting
  checklist."
category: "tools"
order: 3
tags: ["beads", "migration", "bd-1.0", "embedded-mode", "guide"]
last_updated: "2026-04-11"
---

## Beads Migration to Embedded Mode (bd 1.0+)

> **Field report, not official beads documentation.** This is a user-written
> walkthrough based on a real-world sweep of a handful of personal and work
> repos from pre-1.0 beads to bd 1.0 embedded mode.
> [Beads](https://github.com/steveyegge/beads) is Steve Yegge's project — for
> canonical docs see the [beads repo](https://github.com/steveyegge/beads),
> especially `docs/INSTALLING.md` and `docs/DOLT.md`. The full long-form version
> of this guide (with the extended origin story and cross-incident notes) lives
> as a
> [public gist](https://gist.github.com/leonletto/606e8afbb3603870d14b4123707416a2).

How to upgrade any pre-1.0 beads install — **SQLite-era (bd < 0.55)**,
**server-mode Dolt (bd 0.55–0.63)**, or **orphaned JSONL export** — to bd
v1.0.0+ (embedded Dolt, no server).

This guide was born from a machine-wide bd 0.62 → 1.0 upgrade that
simultaneously broke every beads repo on the machine across a range of source
states (clean server-mode, old-schema server-mode, transitional SQLite → Dolt,
and SQLite-era). It collapses what used to be multiple migration guides into a
single source-state-aware procedure with two recovery paths: **`bd import` from
JSONL** (the clean path, preferred whenever a `.beads/issues.jsonl` exists or
can be regenerated), and **raw `dolt sql dump` + replay** (the fallback for
server-mode Dolt installs where `bd export` isn't available because bd was
already upgraded). If you have more than one unmigrated repo on the machine,
read the
[inventory section](#before-you-start-inventory-all-bd-repos-on-the-machine)
first — all your stale repos break at once and the order of migration matters.

This migration is **not automatic**. bd 1.0.0 defaults to embedded mode on a
fresh install and cannot read a v0.62 server-mode layout, a SQLite
`.beads/beads.db`, or the Dolt-native incremental archives (`.darc` files under
`.beads/backup/`). `bd backup restore` on the `.darc` files against a fresh
embedded db reports success but produces an empty database. It's a silent
failure that will burn you if you don't know to look for it.

Two recovery paths:

- **Path A — `bd import` from JSONL.** The simple path. Works when a current
  `.beads/issues.jsonl` exists (bd 0.55+ auto-exports one; SQLite-era repos
  often have one too), or when you can regenerate one from SQLite via `sqlite3`.
  Preserves all issues, dependencies, comments, labels, and memories. No raw
  dolt surgery.
- **Path B — raw `dolt sql dump` + replay.** Only needed when the source is
  server-mode Dolt **without** a usable JSONL and you can't run `bd export`
  because bd was already upgraded. Works directly against the
  content-addressable Dolt store, preserves everything, but has more steps and
  more ways to go wrong.

Pick your path in [Step 3](#step-3-choose-your-recovery-path-a-or-b) after
inventorying what you have.

---

### ⚠ STOP — Read this first if bd has already been upgraded machine-wide

**If `bd version` already prints 1.0.0 or higher, DO NOT run any `bd` commands
against this repo until you have completed Steps 1–3 manually with raw `dolt`
only.**

The `bd` binary is machine-wide (usually at `/usr/local/bin/bd`). When you
upgrade it (via `brew upgrade beads` or the install script), **every repo on the
machine that was on bd 0.62 server mode breaks at once**. The wrong move now is
running `bd` commands against the stale data — bd 1.0 can't read the 0.62
server-mode layout and will give you misleading errors like:

```text
Error: failed to open database: embeddeddolt: init schema:
  creating schema_migrations table: Error 1105: no database selected
warning: no beads configuration found in .beads/dolt/.beads
```

or

```text
Error: 'bd dolt start' is not supported in embedded mode (no Dolt server)
```

These errors do **not** mean your data is gone. Your data is at
`.beads/dolt/<dbname>/.dolt/` (the REAL database dir — not
`.beads/dolt/.dolt/`), and raw `dolt` can still read it fine. You just have to
bypass bd for the export step.

#### The safe order of operations when bd is already 1.0

1. **Do not run `bd stop`, `bd dolt stop`, `bd backup`, `bd backup --force`,
   `bd export`, `bd dolt push`, `bd doctor`, or any other `bd` command against
   the unmigrated repo.** They'll either fail or make bd think the repo is
   broken — it isn't, bd just can't see into the old layout.
2. **Kill any stale dolt server processes manually** (Step 1 below).
3. **Capture your data before touching anything bd-managed.** How depends on
   what you have:
   - **SQLite-era / has `.beads/issues.jsonl`** → **Path A**. The JSONL is
     already your export. If you need to regenerate it from SQLite, do that with
     plain `sqlite3` (Step 3A-2). No dolt surgery needed.
   - **Server-mode Dolt, no usable JSONL** → **Path B**. Run `dolt sql dump`
     directly against the content-addressable store at `.beads/dolt/<dbname>/`.
     The dolt binary is separate from bd and reads the old layout just fine.
4. **Rename `.beads/` aside**, then run `bd init` to create a fresh embedded db.
5. **Load the data into the new embedded db:**
   - **Path A**: `bd import <path-to-issues.jsonl>` — clean, committed,
     auto-pushed if configured. Use this if you can.
   - **Path B**: `dolt sql < dump.sql` against the embedded store, then manually
     commit the working set (`dolt add -A && dolt commit -m ...`) — see Step
     6B + Step 7 for why this matters. Do NOT use `bd backup restore` against
     `.beads/backup/*.jsonl` — those are Dolt-native `.darc` archives and
     silently restore nothing.
6. Verify with `bd stats` and `bd ready`. Now bd is safe to use.

#### Why this matters if you have multiple unmigrated repos

If bd got upgraded while you had several repos still on 0.62 server mode, every
one of them is in this broken-but-recoverable state. The dolt binary is separate
from bd and can still read the old server-mode data at
`.beads/dolt/<dbname>/.dolt/` — so the data is never at risk as long as you
don't run destructive `bd` commands against it. Go repo by repo, use raw dolt
for the export step only, and each repo will come up clean on the new embedded
backend.

If bd has NOT been upgraded yet (still v0.62.x), you can follow the guide top to
bottom normally — `bd dolt stop`, `bd export`, etc. will all work because bd
still understands server mode at that point.

---

### Why migrate

- **bd 1.0 stable release.** v1.0.0 completes the embedded Dolt migration that
  started in v0.55. Server mode still works but isn't the default anymore and
  has known version-skew issues.
- **No server lifecycle.** Embedded mode kills the separate `dolt sql-server`
  process entirely. No port coordination, no stale server pids, no
  "`bd dolt push` spawns a fresh process on a different port than the running
  server" class of bugs. That last one was especially annoying.
- **Simpler layout.** One binary, one lock file, one data directory.

**Tradeoff:** Embedded mode is single-writer (enforced via file lock). Multiple
concurrent `bd` processes serialize through the lock. For Thrum's 14-agent
topology this is fine — bd operations are fast and mostly serial from the
coordinator. For heavy concurrent workloads, stay on server mode with
`dolt.mode: server` explicit in `config.yaml`.

### Prerequisites

- Any pre-1.0 bd install. Supported source states:
  - **SQLite-era** (bd ≤ 0.54): `.beads/beads.db` SQLite file, often with a
    `.beads/issues.jsonl` export alongside it.
  - **Transitional SQLite → Dolt** (bd 0.55 when migration stalled):
    `.beads/beads.db` plus an empty `.beads/dolt/config.yaml` but no actual dolt
    data directory.
  - **Server-mode Dolt** (bd 0.55–0.63): `.beads/dolt/<dbname>/.dolt/` with a
    separate `dolt sql-server` process.
  - **JSONL only**: orphaned `.beads/issues.jsonl` from a deleted or corrupted
    database.
- `dolt` CLI 1.81+ (only needed for **Path B**; `dolt dump` / `dolt sql`)
- `sqlite3` CLI (only needed for **Path A** when migrating a SQLite-era source
  that has no `.beads/issues.jsonl`)
- A working git checkout of the project repo
- Enough disk space to keep `.beads/` aside as a safety copy

Verify versions before starting:

```bash
bd version
dolt version
which bd
which dolt
```

The paths tell you which channel to use for upgrades:

- `/usr/local/bin/bd` → installed via the
  [quick-install script](https://raw.githubusercontent.com/steveyegge/beads/main/scripts/install.sh)
- `/opt/homebrew/bin/bd` or `/usr/local/homebrew/bin/bd` → installed via
  Homebrew
- `/opt/homebrew/bin/dolt` → installed via Homebrew

### Before you start: inventory ALL bd repos on the machine

If you have multiple bd 0.62 repos on this machine, **upgrading bd is
machine-wide** and breaks all of them at once. Before migrating any single repo,
build a list of every stale install so you can work through them in order.

A minimal discovery one-liner — lists every `.beads/` directory under your
common dev roots and classifies it:

```bash
for beads in $(find ~/dev ~/work /srv -type d -name .beads 2>/dev/null); do
  if   [ -f "$beads/redirect" ];            then mode=redirect
  elif [ -d "$beads/embeddeddolt" ];        then mode=embedded
  elif [ -d "$beads/dolt" ];                then mode=server
  elif [ -f "$beads/metadata.json" ];       then mode=unknown
  else                                          mode=nonbd
  fi
  printf '%-9s %s\n' "$mode" "$beads"
done

# Cross-reference orphan dolt servers with their data directories:
for pid in $(pgrep -f 'dolt sql-server'); do
  cwd=$(lsof -p "$pid" 2>/dev/null | awk '$4=="cwd"{print $NF; exit}')
  echo "PID $pid  cwd=$cwd"
done
```

Mode meanings:

| Mode       | Meaning                                                                 | Action                                                 |
| ---------- | ----------------------------------------------------------------------- | ------------------------------------------------------ |
| `server`   | Old bd 0.62 layout with `.beads/dolt/`. Needs migration.                | Run this guide.                                        |
| `embedded` | Already migrated to bd 1.0 `.beads/embeddeddolt/`.                      | Done.                                                  |
| `redirect` | Worktree pointing at another `.beads/` via `.beads/redirect` file.      | Follows the target repo's state.                       |
| `unknown`  | bd-managed (has metadata.json) but neither `dolt/` nor `embeddeddolt/`. | Investigate — partial init?                            |
| `nonbd`    | Directory happens to be named `.beads` but is not a bd install.         | Ignore. Hidden by default; use `--show-nonbd` to list. |

The script also lists every orphan `dolt sql-server` process and
cross-references each one with the repo that owns it. You can see at a glance
which repos still have a stale background server and which processes are
pointing at deleted or trashed paths that can be killed outright.

**Pick a migration order:**

1. Repos you actively work in, most important first
2. Then the rest, in any order

Orphan processes for already-deleted repos (e.g. cwd inside `~/.Trash/dolt` or a
path that `ls` says doesn't exist) are safe to kill immediately — they're
holding nothing useful.

---

### Step 1: Stop any running bd / dolt server

**Critical.** The old bd server can't be holding the dolt database open when you
dump it.

```bash
# If bd is still 0.62.x (has `bd dolt stop`):
bd dolt stop 2>/dev/null || true

# If bd has already been upgraded to 1.0+, skip the bd command entirely
# and go straight to killing orphan dolt processes.
```

#### Orphan `dolt sql-server` processes across the machine

On a machine with multiple bd 0.62 repos, upgrading to 1.0 leaves **one orphan
`dolt sql-server` per unmigrated repo** running in the background. bd 1.0 has no
concept of the server lifecycle, so it can't clean them up. They keep running
indefinitely and can block dolt dumps if they still hold a file lock on the data
directory.

List them:

```bash
ps aux | grep '[d]olt sql-server'
```

Typical output looks like:

```text
youruser  <pid1>  ... /opt/homebrew/bin/dolt sql-server -H 127.0.0.1 -P <port1>
youruser  <pid2>  ... /opt/homebrew/bin/dolt sql-server -H 127.0.0.1 -P <port2>
...
```

Figure out which process belongs to which repo by checking its working
directory:

```bash
for pid in $(pgrep -f 'dolt sql-server'); do
  cwd=$(lsof -p $pid 2>/dev/null | awk '$4=="cwd"{print $NF; exit}')
  echo "PID $pid  cwd=$cwd"
done
```

**For this migration**, kill ONLY the process whose `cwd` is the repo you're
migrating. Leave the others running until you migrate their repos — killing them
now just means bd would fail to read their data if it tried (which it won't,
since you're not running bd in those repos until they migrate).

```bash
kill <pid-for-this-repo>
```

Verify:

```bash
ps aux | grep '[d]olt sql-server'  # should no longer show this repo's pid
```

#### If you have many repos to migrate

Use the discovery one-liner in the **"Before you start"** section to inventory
every `.beads/` directory before touching any of them. Work through the
`server`-mode entries one at a time, killing each repo's orphan
`dolt sql-server` process as part of its own migration.

### Step 2: Locate the real database directory

This is where every manual recovery goes wrong. In bd 0.62 server mode, the data
layout is:

```text
.beads/
├── dolt/                     # bd's dolt data root
│   ├── config.yaml           # dolt server config
│   ├── .dolt/                # ← server-level config (empty branches)
│   └── thrum/                # ← THIS is the actual database
│       └── .dolt/            # ← THIS is the real content-addressable store
│           └── noms/         # ← dolt data blocks
```

**The database lives at `.beads/dolt/thrum/`, not `.beads/dolt/`.** The outer
`.beads/dolt/.dolt/` is just the dolt-sql-server workspace config with
`"branches": {}` — it's not a Dolt database. Run `dolt sql` or `dolt status`
from `.beads/dolt/` and you'll get confusing errors like
`database not found: thrum` (show databases lists it, but use fails) or
`non-string column in 'SELECT active_branch()'`.

Confirm the real location:

```bash
ls .beads/dolt/<project-name>/.dolt/
# Should contain: noms/, repo_state.json, config.json
```

Replace `<project-name>` with your bd database name (check the `"dolt_database"`
field in `.beads/metadata.json`).

#### Git worktrees: per-worktree migration vs shared DB via redirect

If your project has git worktrees that each have their own `.beads/dolt/`
directory, you have two options post-migration:

1. **Per-worktree embedded dbs.** Run this whole guide inside each worktree
   independently. Each ends up with its own `.beads/embeddeddolt/<prefix>/` — no
   shared state, no coordination cost, but diverged issue histories per branch.

2. **Shared db via `.beads/redirect`** (recommended for most setups). Migrate
   only the main repo, then in each worktree create a `.beads/redirect` file
   pointing at the main repo's `.beads/` directory. All worktrees read/write the
   same database:

   ```bash
   # In the main repo (already migrated to embedded):
   MAIN_BEADS="$(cd <main-repo>/.beads && pwd)"

   # In each worktree:
   cd <worktree>
   rm -rf .beads         # remove the old server-mode .beads/dolt/
   mkdir .beads
   echo "$MAIN_BEADS" > .beads/redirect
   bd ready              # sanity check — should list the main repo's issues
   ```

   This is the bd 1.0 worktree pattern (see `docs/ADVANCED.md` in the beads
   repo: "Database Redirects"). Single-level redirects only; the target
   `.beads/` must exist and have a valid embedded database.

If you went with option 2, each worktree's old `.beads/dolt/` can be safely
deleted after the main repo migration is verified — no safety backups needed for
the worktree deletions, since the data all lives in the main repo now.

### Step 3: Choose your recovery path (A or B)

The `.beads/backup/` directory contains dolt-native incremental archives
(`.darc` files plus a manifest referencing dolt commit hashes). These are **NOT
full database snapshots**. `bd backup restore` on them against a fresh embedded
db will report success and restore nothing. Don't rely on them as your only
backup. The two working paths are:

| Path  | When to use                                                                                            | Mechanism                                                                                                   | Preserves                                                                                                                                                    |
| ----- | ------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **A** | SQLite-era source, OR you have a current `.beads/issues.jsonl`, OR you can regenerate one from SQLite  | `bd init` + `bd import <file.jsonl>` — cleanest, one command to load everything                             | Issues, dependencies, comments, labels, memories. Commits automatically under auto-commit=on.                                                                |
| **B** | Server-mode Dolt without a usable JSONL, and you can't run `bd export` because bd was already upgraded | Raw `dolt sql dump` from the content-addressable store, then `dolt sql <` replay into the fresh embedded db | Everything in the source Dolt — full row-level fidelity, but raw; requires manual schema-drift fixes and a manual `dolt commit` on the working set (Step 6B) |

**Path A is strongly preferred** whenever a JSONL export is available or can be
produced. It's simpler, auto-commits, and avoids schema-drift surprises from
very old bd versions (see Troubleshooting: "`bd ready` errors with column
`no_history` could not be found"). If you have a JSONL, use it.

#### Step 3.1: Move the entire `.beads/` directory aside (filesystem snapshot) — COMMON

Same for both paths. This is your ground-truth recovery point if anything goes
wrong later.

```bash
cp -a .beads .beads.server-backup
# or (saves space, only if you're confident):
# mv .beads .beads.server-backup
```

Keep the safety copy until the new embedded install has been exercised for at
least a day.

#### Step 3A: Produce a JSONL export (PATH A)

If `.beads.server-backup/issues.jsonl` already exists and is current — line
count matches the source database's issue count — you can skip ahead to
[Step 6A](#step-6a-import-the-jsonl-into-the-new-embedded-database-path-a).
Otherwise regenerate it.

##### Verify an existing `.beads/issues.jsonl`

```bash
# Count lines (= number of issue records):
wc -l .beads.server-backup/issues.jsonl

# Spot-check against the source database if possible. For SQLite-era:
sqlite3 .beads.server-backup/beads.db "select count(*) from issues;"

# For server-mode dolt — count via the old store:
cd .beads.server-backup/dolt/<dbname>
dolt sql -q "select count(*) from issues;"
```

If the counts match, the JSONL is usable — proceed to Step 4. If it's stale or
missing, regenerate it:

##### Regenerate the JSONL from SQLite (bd ≤ 0.54 source)

bd 1.0 can read its own `bd export` JSONL format but can't read the old SQLite
directly. The re-export path is plain `sqlite3`:

```bash
sqlite3 .beads.server-backup/beads.db "SELECT json_object(
  'id', id,
  'title', title,
  'description', description,
  'design', coalesce(design, ''),
  'acceptance_criteria', coalesce(acceptance_criteria, ''),
  'notes', coalesce(notes, ''),
  'status', status,
  'priority', priority,
  'issue_type', issue_type,
  'assignee', coalesce(assignee, ''),
  'created_at', created_at,
  'updated_at', updated_at,
  'closed_at', closed_at,
  'close_reason', close_reason
) FROM issues ORDER BY created_at;" \
  > .beads.server-backup/issues.jsonl

wc -l .beads.server-backup/issues.jsonl   # should equal SQLite issue count
```

This gives you a minimal but `bd import`-compatible JSONL. Dependencies and
comments need separate SELECTs if you want them — in practice most SQLite-era
repos are small enough (< 100 issues) that losing dep/comment history is
acceptable. If you do need them, add `dependencies` and `comments` table SELECTs
following the same pattern.

##### Regenerate the JSONL from server-mode Dolt (bd 0.55–0.63 source)

If the old install has a `.beads/issues.jsonl` exported by `bd export` at any
point, use that file — it already has the full schema. If you only have the Dolt
data directory and the JSONL is missing, use Path B instead. Raw dolt → JSONL
conversion isn't worth the effort when `dolt sql dump` works end-to-end.

#### Step 3B: Produce a full SQL dump via raw dolt (PATH B)

The fallback path. `dolt dump` writes a complete SQL file — every table schema
and every row.

> **⚠ Do NOT write the dump to a shared path like `/tmp/doltdump.sql`.** If
> you're migrating more than one repo — even sequentially, let alone in parallel
> — every repo would dump to the same file and clobber each other. In a real
> multi-repo migration I ran, a 3 MB dump was silently overwritten by a smaller
> dump from a different repo mid-verification because another session was
> running a parallel migration to the same path.
>
> **Always write the dump INSIDE `.beads.server-backup/` with a repo-scoped
> name.** That way there's no collision with other repos and the dump gets
> cleaned up automatically when you `rm -rf .beads.server-backup/` in Step 12.

```bash
cd .beads.server-backup/dolt/<project-name>

# Derive the repo name from git (or set REPO_NAME manually)
REPO_NAME=$(git -C ../../.. rev-parse --show-toplevel 2>/dev/null | xargs basename)
REPO_NAME=${REPO_NAME:-$(basename "$PWD")}

# Write the dump into .beads.server-backup/ (two levels up from dolt/<dbname>/)
dolt dump -f -fn "../../${REPO_NAME}-bd-dump.sql"

# Output: "Successfully exported data."
DUMP_PATH="$(cd ../.. && pwd)/${REPO_NAME}-bd-dump.sql"
ls -lh "$DUMP_PATH"
head -5 "$DUMP_PATH"
# Should start with: CREATE DATABASE IF NOT EXISTS `<project-name>`; USE `<project-name>`;
```

Verify the dump contains your current state by grepping for a recent issue ID
you know exists:

```bash
grep -c "your-issue-id" "$DUMP_PATH"
```

Hold onto the `$DUMP_PATH` value —
[Step 6B](#step-6b-load-the-dolt-sql-dump-into-the-new-embedded-database-path-b)
needs it.

### Step 4: Upgrade bd and dolt

Use the channel that matches your current install.

#### bd via quick-install script (path: `/usr/local/bin/bd`)

```bash
curl -fsSL https://raw.githubusercontent.com/steveyegge/beads/main/scripts/install.sh | bash
```

The installer detects your platform, downloads and verifies the release
checksum, installs the binary in place (overwrites `/usr/local/bin/bd`), and
creates a `beads` alias.

#### bd via Homebrew

```bash
brew upgrade beads
```

#### dolt via Homebrew

```bash
brew upgrade dolt
```

#### Verify

```bash
bd version   # should print 1.0.0 or later
dolt version # should print 1.86.0 or later
```

### Step 5: Initialize the new embedded database

`bd init` creates a fresh `.beads/` directory in embedded mode. Since the old
`.beads/` has been moved aside, bd sees nothing and initializes cleanly.

```bash
cd <project-root>
bd init --prefix=<your-prefix> --non-interactive --role=maintainer
```

Replace `<your-prefix>` with your original issue prefix (e.g. `thrum`,
`my-project`, `MYPROJECT`). Check `.beads.server-backup/metadata.json` if you
can't remember it.

`bd init` will:

- Create `.beads/embeddeddolt/<prefix>/.dolt/` (the new embedded store)
- Create `.beads/metadata.json` with `"dolt_mode": "embedded"`
- Create `.beads/config.yaml` (template with all options commented out)
- Install git hooks (pre-commit, post-merge, etc.)
- Add a beads section to `CLAUDE.md` / `AGENTS.md` (see Step 9 caveat)
- Register a Claude Code SessionStart/PreCompact hook in `.claude/settings.json`
- Set a dolt remote pointing at the current git origin

Verify the init:

```bash
bd version
bd stats
# Expected: 0 total issues (the db is empty at this point)
```

### Step 6: Load your data into the new embedded database

#### Step 6A: Import the JSONL into the new embedded database (PATH A)

From the project root, with the fresh embedded db in place:

```bash
cd <project-root>
bd import .beads.server-backup/issues.jsonl
# Output: "Imported N issues from .beads.server-backup/issues.jsonl"
```

`bd import` is the bd 1.0 replacement for the old `bd init --from-jsonl` flow.
It creates or upserts issues from the file, auto-detects memory records
(`"_type":"memory"`) and imports them as persistent memories (equivalent to
`bd remember`), then commits the new rows to the embedded Dolt working set. If
you set `dolt.auto-commit=on` before importing (recommended — see Step 7), the
import commits automatically. Otherwise it sits in the working set and you'll
need to commit it manually like Path B (see Step 6C).

Verify the import:

```bash
bd stats
bd ready
bd show <known-issue-id>
```

If `bd ready` and `bd show` both work and the issue count matches the source,
**skip ahead to
[Step 7](#step-7-verify-dolt-auto-commit-and-commit-the-working-set-if-needed)**.
Path A is done for this repo.

#### Step 6B: Load the dolt SQL dump into the new embedded database (PATH B)

The new embedded db is a standard dolt database, so `dolt sql` works against it
directly. Load the dump produced in Step 3B:

```bash
cd .beads/embeddeddolt/<your-prefix>
# $DUMP_PATH was set in Step 3B; the file lives inside .beads.server-backup/
dolt sql < "$DUMP_PATH"
# Silent on success. Takes 5–30 seconds for a typical project.
```

> **⚠ Database name mismatch when prefix contains a dash.** If your prefix has a
> dash (e.g. `my-project`), the dump's `CREATE DATABASE IF NOT EXISTS` / `USE`
> statements use the dashed name — that's what the old server-mode dolt used —
> but `bd init` may have created the embedded db under an underscore-normalized
> name (e.g. `my_project`) or a short name like `MP`. The dump will then fail
> silently or create an unlinked orphan database. Fix with a one-liner sed on
> the dump before loading:
>
> ```bash
> # Edit the dump's CREATE DATABASE / USE lines to match the embedded db name.
> # Replace <old-dash-name> and <new-name> with the actual names for your repo:
> sed -i '' 's/`<old-dash-name>`/`<new-name>`/g' \
>   .beads.server-backup/<your-repo>-bd-dump.sql
> ```
>
> Then re-run `dolt sql < $DUMP_PATH`. Verify with
> `dolt sql -q "show databases;"` — there should be exactly one project
> database.

Verify the load worked:

```bash
dolt sql -q "select count(*) from issues;"
# Should match the count from the old database

dolt sql -q "select id, status, title from issues where id like '<your-prefix>%' order by id;"
# Substitute your own recent-work prefix
```

#### Step 6C: Patch schema drift from very old bd versions (PATH B, old sources only)

If the source dolt data came from a very old bd version (pre-0.60ish), the
loaded schema will be missing columns and tables that bd 1.0 requires at query
time. Symptoms:

```bash
bd ready
# Error: get ready work: fetch issues: get issues by IDs from issues:
#   Error 1105: column "no_history" could not be found in any table in scope
```

or

```bash
bd close <id>
# Error: resolving ID <id>: failed to search issues: search wisps (merge):
#   search wisps: Error 1105: column "no_history" could not be found in any table in scope
```

The dump replayed the old CREATE TABLE DDL, which overwrote the fresh bd 1.0
schema with the old one. Patch in place against the embedded store:

```bash
cd .beads/embeddeddolt/<your-prefix>

# Add missing columns to issues and wisps:
dolt sql -q "ALTER TABLE issues ADD COLUMN no_history TINYINT(1) DEFAULT 0;"
dolt sql -q "ALTER TABLE wisps ADD COLUMN no_history TINYINT(1) DEFAULT 0;"

# Add missing tables (bd 1.0 expects them to exist, even if empty):
dolt sql -q "CREATE TABLE IF NOT EXISTS custom_statuses (
  name varchar(64) NOT NULL,
  category varchar(32) NOT NULL DEFAULT 'unspecified',
  PRIMARY KEY (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;"

dolt sql -q "CREATE TABLE IF NOT EXISTS custom_types (
  name varchar(64) NOT NULL,
  PRIMARY KEY (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_bin;"
```

The leftover `crystallizes` and `quality_score` columns from the old `issues`
schema are harmless — bd 1.0 ignores unknown columns. Leave them alone; dropping
them is unnecessary risk.

To diff an old-source schema against a known-good bd 1.0 schema (e.g. from a
freshly-initialized reference repo):

```bash
(cd <reference-repo>/.beads/embeddeddolt/<prefix> && dolt sql -q "describe issues;") > /tmp/good.txt
(cd .beads/embeddeddolt/<your-prefix> && dolt sql -q "describe issues;") > /tmp/mine.txt
diff /tmp/good.txt /tmp/mine.txt
```

Re-run `bd ready` and `bd close <id>` after the ALTERs to confirm the drift is
fixed.

### Step 7: Verify dolt auto-commit and commit the working set if needed

**bd 1.0 defaults `dolt.auto-commit` to `off`.** After a Path B migration — or
any Path A migration where you didn't set auto-commit _before_ running
`bd import` — the loaded rows are sitting in the dolt **working set**. They're
queryable by bd, but never committed to dolt history and never pushed to any
remote. Subsequent bd writes will also stay in the working set until you either
turn auto-commit on or commit manually.

Fix in this order:

```bash
cd <project-root>

# 1. Tell bd to commit to dolt on every write from now on:
bd config set dolt.auto-commit on

# 2. Commit the existing working set (the Path B data or pre-auto-commit Path A data):
cd .beads/embeddeddolt/<your-prefix>
dolt status   # should show modified tables: issues, wisps, events, etc.
dolt add -A
dolt commit -m "migrate: import historical data from bd <old-version> <mode>"
dolt log --oneline | head -5   # should now show your migrate commit at HEAD
cd -
```

> **auto-commit values:** `bd config set dolt.auto-commit <value>` accepts
> `off`, `on`, or `batch`. Default is `off`. `on` commits synchronously after
> every bd write. `batch` defers commits to `bd dolt commit` but flushes on
> SIGTERM/SIGHUP. For migrated repos, use `on` unless you have a specific reason
> to batch.

Now verify bd can read the restored data end-to-end:

```bash
cd <project-root>
bd stats
bd ready
bd show <some-issue-id>
bd migrate   # confirms schema version is current
```

All of these should succeed. `bd migrate` with no subcommand is a schema check —
no-op if everything is current — and should print:

```text
Dolt database version: 1.0.0
✓ Version matches
✓ All metadata fields present
```

### Step 8: Configure backup and sync per repo

bd 1.0 has two sync mechanisms that got conflated in pre-1.0 docs:

- **`backup.git-push`** (`.beads/config.yaml`) — enables JSONL auto-export to
  `.beads/backup/*.jsonl` and **git push of those files** when a git remote is
  detected. Useful when beads data should travel as tracked files in the repo.
  Leave `false` if you already have an external backup script handling
  off-machine backup.
- **`dolt.auto-push`** (`bd config set`) — enables background **dolt push** of
  the embedded store to a hidden `refs/dolt/data` git ref on the remote. This is
  the native Dolt sync path; the hidden ref is invisible to `git log` /
  `git clone` but travels with `git push --all` /
  `git fetch origin 'refs/dolt/*'`. Interval is controlled by
  `dolt.auto-push-interval` (default 5m). Useful for private repos where you
  want beads history to sync across machines without living in tracked files.

**Public repos / external backup workflow** (e.g. thrum):

```yaml
# .beads/config.yaml
backup:
  git-push: false
```

Don't set `dolt.auto-push=true` on a public repo unless you want the hidden dolt
ref published to the public git remote.

**Private repos with no external backup script** (most personal repos):

```bash
# .beads/config.yaml:
backup:
  git-push: true

# And via bd config (stored in-db):
bd config set dolt.auto-push true
# (dolt.auto-commit=on was already set in Step 7)
```

**Initial push to establish the remote baseline:**

After `bd init` auto-configures the dolt remote and you commit the working set
in Step 7, run one initial manual push. `dolt.auto-push` only fires on an
interval, so the remote won't have your data until the first scheduled push —
don't wait for that:

```bash
cd .beads/embeddeddolt/<your-prefix>
dolt push origin main
# Expect: "[new branch] main -> main" on first run
```

If the push fails with `unknown push error; no common ancestor`, the remote
already has a divergent `refs/dolt/data` ref from a prior install. See
Troubleshooting: "dolt push fails with 'no common ancestor'".

**Verify the remote ref:**

```bash
cd <project-root>
git ls-remote origin 'refs/dolt/*'
# Should show a single hash next to refs/dolt/data
```

### Step 9: Clean up `CLAUDE.md` / `AGENTS.md` integration

`bd init` adds a "Beads Issue Tracker" section to `CLAUDE.md` (between
`<!-- BEGIN BEADS INTEGRATION -->` and `<!-- END BEADS INTEGRATION -->`
markers). In embedded mode, the boilerplate session-close workflow in that
section references `bd dolt push`, which is **server-mode only**:

```bash
git pull --rebase
bd dolt push        # ← invalid in embedded mode
git push
```

Remove that line, or the whole section if you maintain your own. It's safe to
edit inside the markers — bd won't regenerate the section on subsequent
`bd init` runs unless you pass `--force`.

### Step 10: Smoke test

```bash
# Read path
bd ready
bd show <some-open-issue>

# Write path (create a throwaway test issue)
bd create --title="migration smoke test" --type=task --priority=4
# Note the ID, then:
bd close <that-id>

# Dependency path
bd dep --help
```

### Step 11: Audit your agent memory for stale bd guidance

**This step is easy to skip and will bite you later.** If you have any AI agent
with persistent memory — `MEMORY.md` files, Claude Code memory, `bd remember`
entries, CLAUDE.md project instructions, user global CLAUDE.md, notes in
`.claude/`, or equivalent in other runtimes — those memory stores almost
certainly contain bd 0.62-era commands that won't work anymore. Future sessions
will confidently quote them and burn time debugging why `bd dolt push` fails or
why `bd import` doesn't exist.

Grep your memory stores for stale bd patterns and fix each hit:

```bash
# Adjust paths to match where your agent keeps persistent memory.
# Include global agent definitions — they apply to ALL projects on the
# machine and almost always contain stale bd guidance.
grep -rn "bd backup --force\|bd import\|bd sync\|bd onboard\|bd dolt push\|bd dolt start\|bd dolt stop\|bd dolt status\|bd dolt show" \
  ~/.claude/projects/*/memory/ \
  ~/.claude/CLAUDE.md \
  ~/.claude/*.md \
  ~/.claude/agents/*.md \
  ~/.codex/ \
  ~/.config/opencode/ \
  .claude/ \
  CLAUDE.md \
  AGENTS.md \
  .beads/memories/ 2>/dev/null
```

#### ⚠ Global agent definitions: hold until ALL repos on the machine are migrated

Files under `~/.claude/agents/*.md` (and equivalent global agent definitions in
other runtimes) apply to **every project on the machine**, not just this one. If
you still have other repos stuck in bd 0.62 server mode, **updating the global
files now will break them** — they depend on the old commands like `bd sync` and
`bd dolt push`.

Two safe orderings:

1. **Migrate all repos first, then update global files.** Run the discovery
   script (Step 1) to find every stale repo, migrate each one, then do the
   global memory audit last. Cleaner if you have ≤10 repos.
2. **Update global files immediately, but annotate them** with "requires bd 1.0+
   embedded mode; see `dev-docs/beads_server_to_embedded_migration_guide.md`".
   Unmigrated repos will still break, but you won't accumulate drift across
   future sessions.

Project-level files (`.claude/`, project CLAUDE.md, `AGENTS.md`) are **always
safe to update immediately** — they only affect this repo, which is now on bd
1.0.

Patterns to fix:

| Stale (bd 0.62)                                | Replace with (bd 1.0+)                                                                                                                                                                                                   |
| ---------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `bd backup --force`                            | `bd export --all -o <path>`                                                                                                                                                                                              |
| `bd import < file.jsonl` (shell-redirect form) | `bd import file.jsonl` (positional arg — the command still exists in 1.0, syntax changed)                                                                                                                                |
| `bd sync`                                      | Removed — no replacement, delete the line                                                                                                                                                                                |
| `bd onboard`                                   | `bd prime`                                                                                                                                                                                                               |
| `bd dolt push` / `bd dolt pull`                | Removed in embedded mode. Delete from session-close workflows. For private repos wanting dolt sync, use `bd config set dolt.auto-push true` or run `dolt push origin main` directly from `.beads/embeddeddolt/<prefix>`. |
| `bd dolt start` / `bd dolt stop`               | Removed in embedded mode                                                                                                                                                                                                 |
| `bd dolt status` / `bd dolt show`              | Removed in embedded mode; use `bd stats` and `bd migrate`                                                                                                                                                                |
| `bd doctor` (as a health check)                | `bd migrate` (no args) — `bd doctor` is a no-op placeholder in embedded mode                                                                                                                                             |
| "dolt server on port X"                        | No server. Single binary, file lock.                                                                                                                                                                                     |
| `.beads/dolt/` as the data path                | `.beads/embeddeddolt/<prefix>/.dolt/`                                                                                                                                                                                    |

Also update:

- Any **session-close scripts** that call `bd dolt push` before `git push`.
- Any **worktree setup scripts** that initialize bd via `bd import`.
- Any **tooling notes** that describe the beads backend as "Dolt via
  sql-server".
- Any **backup scripts** that pipe through `bd backup --force` — these fail
  silently with `|| true` and produce stale backups without telling you.

If your memory system has index files (e.g. `MEMORY.md` listing topic files),
update the one-line descriptions so future memory-recall picks up the refreshed
content rather than the stale entries.

### Step 12: Clean up the safety copy

**Only after** you've exercised the new install for at least a day and are
confident nothing is missing:

```bash
rm -rf .beads.server-backup
# If you also took a .beads.pre-migration/ copy (double-safety), remove it too:
rm -rf .beads.pre-migration 2>/dev/null || true
```

The dump file lives inside `.beads.server-backup/` (per Step 3b), so removing
the directory removes the dump too. No `/tmp/` cleanup needed.

Keep them longer if you have any doubt. Disk is cheap; re-migrating is not.

#### .gitignore the safety copies

Before committing anything post-migration, add the safety copies to your ignore
list so they don't get accidentally staged:

```bash
cat >> .git/info/exclude <<'EOF'
# Beads 0.62 → 1.0 migration safety copies (see migration guide Step 12)
.beads.server-backup/
.beads.pre-migration/
EOF
```

`.git/info/exclude` (per-clone, untracked) is better than `.gitignore` for
temporary entries — otherwise the `.gitignore` entry lingers in history forever.

---

### Troubleshooting

#### `bd doctor` says "No dolt database found" but one exists

`bd doctor` in 1.0.0 isn't supported in embedded mode. It prints a placeholder
pointing you at manual checks:

```text
Note: 'bd doctor' is not yet supported in embedded mode.
  • Verify database exists:  ls -la .beads/embeddeddolt/
  • Check bd version:        bd version
  • Reinitialize if needed:  bd init --force
```

Use `bd migrate` (no args) instead — it verifies the schema version and metadata
fields.

#### `bd dolt start` / `bd dolt push` / `bd dolt status` all error

Expected. Embedded mode has no dolt server, so all `bd dolt <server-lifecycle>`
commands return:

```text
Error: 'bd dolt ...' is not supported in embedded mode (no Dolt server)
```

That includes `bd dolt push`. For dolt-native off-machine backup in embedded
mode, use `bd backup sync` (writes to a local directory or DoltHub remote) or
run `dolt` directly against the embedded store.

#### `bd backup restore <path>` succeeds but database is empty

The `.beads/backup/*.jsonl` files from a v0.62 server-mode install are **Dolt
archive `.darc` format**, not full snapshots. `bd backup restore` doesn't
extract row data from them against a fresh embedded db — it reports success and
inserts nothing. This one is especially annoying to discover after you thought
you were done.

Use one of:

- **Path A** (preferred): regenerate a JSONL via `bd export` (if the source bd
  version can still open the database) or via raw `sqlite3` for SQLite-era
  sources, then `bd import` it. See Step 3A and Step 6A.
- **Path B**: raw `dolt sql dump` against the content-addressable store
  directly. See Step 3B and Step 6B.

#### `bd ready` errors with `column "no_history" could not be found`

Full error:

```text
Error: get ready work: fetch issues: get issues by IDs from issues:
  Error 1105: column "no_history" could not be found in any table in scope
```

Path B only, and only when the source dolt data came from a very old bd version
(pre-0.60ish). The dump replayed the old CREATE TABLE DDL, which overwrote the
fresh bd 1.0 schema with the old one. Fix with the ALTER + CREATE TABLE
statements in
[Step 6C](#step-6c-patch-schema-drift-from-very-old-bd-versions-path-b-old-sources-only).

Write operations show the same symptom:

```text
Error: resolving ID <id>: failed to search issues: search wisps (merge):
  search wisps: Error 1105: column "no_history" could not be found in any table in scope
```

Same fix — the `wisps` table needs the column too.

#### `dolt push origin main` fails with `unknown push error; no common ancestor`

The remote already has a `refs/dolt/data` ref from a prior install. The
freshly-migrated local dolt history has no commits in common with it.

Fix:

```bash
cd <project-root>

# Verify the stale remote ref exists:
git ls-remote origin 'refs/dolt/*'
# e.g.: abc1234def5678...  refs/dolt/data

# Delete the remote ref (destructive — but this is a migrated repo,
# and the old ref is no longer reachable from any current install):
git push origin :refs/dolt/data

# Confirm it's gone:
git ls-remote origin 'refs/dolt/*'
# (no output)

# Now the dolt push will establish a new baseline:
cd .beads/embeddeddolt/<your-prefix>
dolt push origin main
# Expect: "[new branch] main -> main"
```

Safe if you're the only active consumer of that beads database. If multiple
machines still have copies pointing at the old ref, migrate them all before
deleting the remote ref.

#### `dolt push origin main` says `Everything up-to-date` but local has uncommitted data

`dolt push` only pushes committed data. If you loaded data via Path B's
`dolt sql < dump.sql` and didn't commit the working set, the push sees no new
commits to send. Exit code is 0, the remote looks current, but the issues you
just loaded are stranded locally in the dolt working set.

Verify:

```bash
cd .beads/embeddeddolt/<your-prefix>
dolt status
# If it shows "Changes not staged for commit" on issues/wisps/events/etc.,
# you have uncommitted data in the working set.

dolt log --oneline
# HEAD should be at a "migrate: ..." commit, not at "bd init"
```

Fix: commit and push.

```bash
dolt add -A
dolt commit -m "migrate: import historical data"
dolt push origin main
```

Set `bd config set dolt.auto-commit on` (Step 7) so future writes don't
accumulate in the working set.

#### `dolt sql -q "use <dbname>;"` says "database not found" but `show databases` lists it

You're running dolt from the wrong directory. The actual database is at
`.beads/dolt/<dbname>/.dolt/`, not `.beads/dolt/.dolt/`. The outer `.dolt/` is a
dolt-sql-server workspace config with no branches.

```bash
cd .beads/dolt/<dbname>
dolt status          # should show branch info, not nil-column error
dolt sql -q "select count(*) from issues;"
```

#### `dolt dump` fails with "The current directory is not a valid dolt repository"

Same cause: you need to be inside `.beads/dolt/<dbname>/` (or
`.beads.server-backup/dolt/<dbname>/`), not its parent.

#### `.beads/dolt/` only contains `config.yaml` — no database subdirectory

This is a **transitional SQLite → Dolt** source: bd 0.55 was installed and
started setting up dolt (creating `.beads/dolt/config.yaml`) but the actual
`bd migrate --to-dolt` step never ran. All the issue data is still in
`.beads/beads.db` (SQLite). Don't use Path B — there's nothing to dump. Use
**Path A**:

1. Check for an existing `.beads/issues.jsonl`. If its line count matches
   `sqlite3 .beads/beads.db "select count(*) from issues;"`, use it as-is.
2. If not, regenerate via raw `sqlite3` (see Step 3A).
3. Proceed to Step 4 (upgrade) → Step 5 (`bd init`) → Step 6A (`bd import`).

#### `dolt dump -d /path` or `dolt dump --data-dir` errors

`dolt dump` doesn't support global data-dir flags. Run from inside the database
directory and use `-fn /path/to/output.sql` instead:

```bash
cd .beads.server-backup/dolt/<dbname>
dolt dump -f -fn /tmp/doltdump.sql
```

#### `bd init` says "database 'thrum' already exists"

The embedded store at `.beads/embeddeddolt/<prefix>/` already has schema and
schema_migrations rows. Pass `--force`:

```bash
bd init --force --prefix=<your-prefix> --non-interactive --role=maintainer
```

Or delete `.beads/embeddeddolt/` first.

#### `bd backup restore --force /tmp/dump` says "Error 1105: database already exists, use --force"

That error refers to the dolt database itself, not the backup destination. Pass
`--force`:

```bash
bd backup restore --force /path/to/backup
```

Only useful for restoring from a proper `bd backup sync` destination, not from
the old server-mode `.beads/backup/` directory.

#### `bd dolt pull` fails with merge conflict on the `issues` table (pre-upgrade)

This is usually what triggers the migration in the first place. When an old
server-mode bd 0.62 client and a freshly-upgraded bd 1.0 client share the same
`.beads/dolt/` data, they talk to the server on different ports and the remote
sync can drift. Complete the migration from Step 2 onward — don't force-merge.

#### `/tmp/doltdump.sql` got overwritten mid-migration

Another session (or a parallel migration loop) writing to the same hardcoded
path. Two dumps racing to the same file is not fun. The fix is baked into Step
3b: write to `.beads.server-backup/<repo>-bd-dump.sql` instead, so each repo's
dump lives inside its own safety directory. If you already hit this, re-run Step
3b with the corrected path — the source data in
`.beads.server-backup/dolt/<dbname>/` is still intact.

#### Orphan `dolt sql-server` processes accumulating over time

Every unmigrated bd 0.62 repo on the machine has exactly one orphan
`dolt sql-server` process that bd 1.0 can't stop. They're **safe but
pointless**: they hold locks on their `.beads/dolt/` directories and consume a
tiny bit of RAM. They don't interfere with bd 1.0 operations in other repos
(each server listens on a different port).

To clean them up, run the discovery script (see Step 1) to cross-reference each
PID with its repo, then migrate repos one by one. Each migration naturally kills
its own orphan in Step 1.

If you find an orphan whose `cwd` points at a deleted directory or the trash,
just kill it — nothing depends on it:

```bash
lsof -p <pid> 2>&1 | grep cwd  # sanity check the cwd first
kill <pid>
```

#### Worktrees still have old `.beads/dolt/` directories

See the worktree subsection after Step 2: per-worktree migration vs shared DB
via `.beads/redirect` file. The redirect option is the bd 1.0 documented pattern
for sharing a database across worktrees.

#### The dolt remote bd init created is pointed at the wrong place

`bd init` on a repo with a git remote auto-configures a dolt remote pointed at
the git origin. In an offline-backup workflow (where you have your own external
script) this is harmless noise. But if it causes confusion:

```bash
# Remove the auto-configured dolt remote:
bd dolt remote remove origin 2>/dev/null || true

# Or disable auto-push in .beads/config.yaml:
# (covered in Step 8 of this guide)
```

#### Backup scripts break: `bd backup --force` no longer exists

In bd 0.62 your script probably does:

```bash
(cd "$PROJECT" && bd backup --force 2>/dev/null || true)
cp "$PROJECT/.beads/backup/"*.jsonl "$BACKUP_DEST/" 2>/dev/null || true
```

The `|| true` silently swallows the error, and the `cp` copies whatever JSONL
files happen to be lying around — which after the migration may be **empty or
stale**. Update the script to use `bd export`:

```bash
(cd "$PROJECT" && bd export --all -o "$PROJECT/.beads/backup/issues.jsonl") \
  2>/dev/null || true
cp "$PROJECT/.beads/backup/"*.jsonl "$BACKUP_DEST/" 2>/dev/null || true
```

`bd export` writes a single JSONL containing all issues (and memories,
dependencies, comments, labels, etc.) in a format that's human-readable and
greppable — which is usually what these backup scripts actually want.

---

### References

- Beads docs: `docs/INSTALLING.md`, `docs/DOLT.md`, `docs/DOLT-BACKEND.md` in
  the [beads repo](https://github.com/steveyegge/beads)
- Changelog entries for the 0.55 → 1.0 embedded-mode transition: search the
  beads CHANGELOG.md for "embedded" and "schema version"
- Originating incidents:
  - **First session (Path B discovery):** recovery of a recently-filed epic in
    the `thrum` public repo via raw `dolt sql dump` after `bd backup restore` on
    the `.beads/backup/*.jsonl` files produced an empty database. That incident
    produced Step 6B / Step 3B and made clear that the `.darc` archives in
    `.beads/backup/` are not full snapshots.
  - **Second session (multi-repo migration sweep):** systematic migration of
    several personal and work-private repos across a range of source states —
    SQLite-era (bd ≤ 0.54), transitional SQLite → Dolt, old-schema server-mode
    (bd 0.55 range), and clean server-mode (bd 0.62). That sweep turned up: (1)
    `bd import <file.jsonl>` exists in bd 1.0 and is the preferred path for any
    source with a JSONL or SQLite backend, (2) very old server-mode sources have
    schema drift (missing `no_history` column, missing `custom_statuses` /
    `custom_types` tables) that the dolt dump replays back onto the fresh
    embedded schema, (3) `dolt.auto-commit` defaults to `off` in bd 1.0 so
    loaded data sits in the working set and is never pushed until committed
    manually, and (4) a stale `refs/dolt/data` on the remote from a prior
    install causes `no common ancestor` push errors that must be cleared with
    `git push origin :refs/dolt/data` before the fresh baseline will push.
