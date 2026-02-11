---
name: beads-agent
description: >
  Beads integration guide. Git-backed, dependency-aware issue tracker for
  AI-supervised coding workflows. Covers task tracking, dependencies,
  multi-session coordination, and context recovery after compaction.
---

# Beads Integration Guide

Git-backed, dependency-aware issue tracker for AI-supervised coding workflows.
Use Beads to track complex tasks, manage dependencies, and coordinate work
across sessions.

## When to Use Beads

**Use when:**

- Managing complex multi-step tasks
- Tracking dependencies between work items
- Discovering side quests during implementation
- Coordinating with other agents via Git
- Maintaining context across sessions

**Don't use when:**

- Simple single-file edits with no follow-up
- Temporary notes or scratch work
- Real-time communication (use Thrum for that)

## Quick Start

```bash
# 1. Check if beads is available
bd version
bd info --json

# 2. Find ready work (no blockers)
bd ready --json

# 3. Claim task
bd update bd-abc --status in_progress --json

# 4. Work on implementation...

# 5. Complete
bd close bd-abc --reason "Done" --json

# 6. CRITICAL: Sync at end
bd sync
```

## Core Concepts

### Issue Types

- `bug` - Something broken
- `feature` - New functionality
- `task` - Work item (tests, docs, refactoring)
- `epic` - Large feature with subtasks
- `chore` - Maintenance work

### Priorities (0-4)

- `0` - Critical (security, data loss)
- `1` - High (major features, important bugs)
- `2` - Medium (nice-to-have)
- `3` - Low (polish)
- `4` - Backlog (future ideas)

### Status Values

- `open` - Ready to be worked on
- `in_progress` - Currently being worked on
- `blocked` - Cannot proceed (waiting on dependencies)
- `deferred` - Deliberately put on ice
- `closed` - Work completed

### Dependency Types

**Only `blocks` affects `bd ready` output.**

- **`blocks`** - Hard dependency (issue X blocks issue Y). Blocked issues
  excluded from `bd ready`.
- **`related`** - Soft link (informational only). No impact on ready state.
- **`parent-child`** - Epic/subtask hierarchy (structural only).
- **`discovered-from`** - Track side quests discovered during work (preserves
  context).

**Direction:** `bd dep add <issue> <depends-on>` — first arg depends on second
arg. So `bd dep add blocked-id prerequisite-id` means "blocked depends on
prerequisite" (prerequisite blocks blocked).

**Epic/task pattern:** Tasks depend on their epic (epic is the parent). Write:
`bd dep add <task-id> <epic-id>`. This produces:
- Epic shows `CHILDREN ↳ task` (or `BLOCKS ← task`)
- Task shows `PARENT ↑ epic` (or `DEPENDS ON → epic`)
- bdui renders the task nested under the epic

**Common mistake:** Writing `bd dep add <epic-id> <task-id>` — this makes the
epic depend on the task, which is backwards. bdui will show the epic as empty
with no children.

### Hash-Based IDs

- Auto-generated (e.g., `bd-a1b2`, `bd-f14c3`)
- Epic children use dotted notation: `bd-a3f8.1`, `bd-a3f8.2`
- No collisions across branches or agents

### Auto-Sync

- **Export**: After changes (30s debounce)
- **Import**: When JSONL newer than DB
- **Manual**: `bd sync` forces immediate sync
- **End of session**: Always run `bd sync`

## Essential Commands

### Finding Work

```bash
bd ready --json                    # Unblocked tasks
bd list --status open --json       # All open tasks
bd show <id> --json                # Task details
bd list --priority 0 --json        # Critical tasks
bd list --type bug --json          # All bugs
```

### Creating Issues

```bash
# Basic creation (always use --json)
bd create "Title" -t bug|feature|task -p 0-4 -d "Description" --json

# Create with notes (for documentation links)
bd create "Task title" -t task -p 1 \
  --notes "**Guide:** path/to/guide.md
**URL:** https://example.com
**Hours:** Mon-Fri 7am-10pm ET" --json

# Create and link discovered work
bd create "Found bug" -t bug -p 1 --deps discovered-from:<parent-id> --json

# Create epic, then tasks, then link them
bd create --title="Auth System" --type=epic --priority=1 --json   # Returns: bd-a3f8
bd create --title="Login UI" --type=task --priority=1 --json      # Returns: bd-c2d4
bd dep add bd-c2d4 bd-a3f8                                        # task depends on epic
```

### Updating Issues

```bash
bd update <id> --status in_progress --json
bd update <id> --priority 0 --json
bd update <id> --assignee "agent-name" --json
bd update <id> --notes "New notes" --json
```

### Completing Work

```bash
bd close <id> --reason "Done" --json
bd close <id1> <id2> --reason "Batch complete" --json  # Multiple
bd reopen <id> --reason "Reopening" --json
```

### Dependencies

```bash
# Add hard blocker: blocked depends on prerequisite (affects bd ready)
bd dep add <blocked> <prerequisite> --type blocks --json

# Add soft link (informational)
bd dep add <task1> <task2> --type related --json

# Track discovery
bd dep add <discovered> <original> --type discovered-from --json

# View dependency tree
bd dep tree <id>
```

### Epics

```bash
# View epic status
bd epic status --json

# Close all eligible epics (all children closed)
bd epic close-eligible --json

# List children of an epic
bd list --json | jq '[.[] | select(.id | startswith("bd-a3f8."))]'
```

### Sync Operations

```bash
bd sync                           # Full sync (export/commit/pull/import/push)
bd sync --flush-only              # Export only
bd sync --import-only             # Import only
```

## Common Workflows

### Workflow 1: Solo Agent Session

```bash
# Start session
bd sync
bd ready --json

# Claim task
bd update bd-abc --status in_progress --json
bd sync

# Work on implementation...

# Discover new work? Link it
bd create "Found issue" -t bug -p 1 --deps discovered-from:bd-abc --json

# Complete
bd close bd-abc --reason "Done" --json

# End session (CRITICAL)
bd sync
```

### Workflow 2: Multi-Agent Coordination

```bash
# Agent A: Claim task
bd ready --json
bd update bd-123 --status in_progress --json
bd sync  # Push immediately

# Agent B: Sees updated state
bd ready --json  # bd-123 no longer appears (claimed by Agent A)

# Agent A: Complete and sync
bd close bd-123 --reason "Done" --json
bd sync
```

### Workflow 3: Working with Dependencies

```bash
# Create prerequisite
bd create --title="Create DB schema" --type=task --priority=1 --json
# Returns: bd-789

# Create dependent task
bd create --title="Add API endpoints" --type=task --priority=1 --json
# Returns: bd-790

# Link: bd-790 depends on bd-789 (bd-789 must finish first)
bd dep add bd-790 bd-789 --type blocks --json

# Check ready work
bd ready --json  # Shows bd-789 only (bd-790 blocked)

# Complete prerequisite
bd close bd-789 --reason "Schema complete" --json

# Check again
bd ready --json  # Now shows bd-790 (unblocked)
```

### Workflow 4: Epic with Tasks

```bash
# 1. Create the epic
bd create --title="v1.0 Auth System" --type=epic --priority=1 --json
# Returns: thrum-abc1

# 2. Create tasks
bd create --title="Set up credentials" --type=task --priority=1 --json
# Returns: thrum-abc2
bd create --title="Implement auth flow" --type=task --priority=1 --json
# Returns: thrum-abc3
bd create --title="Add token refresh" --type=task --priority=1 --json
# Returns: thrum-abc4

# 3. Link tasks to epic (task depends on epic = epic is parent)
bd dep add thrum-abc2 thrum-abc1    # task depends on epic
bd dep add thrum-abc3 thrum-abc1    # task depends on epic
bd dep add thrum-abc4 thrum-abc1    # task depends on epic

# Verify: epic should show CHILDREN/BLOCKS, tasks should show PARENT/DEPENDS ON
bd show thrum-abc1
# BLOCKS
#   ← thrum-abc2: Set up credentials
#   ← thrum-abc3: Implement auth flow
#   ← thrum-abc4: Add token refresh

# 4. Work through tasks
bd ready --json
bd close thrum-abc2 --reason "Credentials configured" --json
bd close thrum-abc3 --reason "Auth flow implemented" --json
bd close thrum-abc4 --reason "Token refresh done" --json

# 5. Close epic when all tasks complete
bd close thrum-abc1 --reason "All tasks done" --json
```

## Adding Notes for Context

**CRITICAL: Always search for supporting documentation before creating tasks.**

```bash
# Search for guides
ls -la docs/ legal/ progress/ | grep -i "keyword"

# Create with notes linking to documentation
bd create "Obtain EIN from IRS" -t task -p 0 \
  --notes "**Guide:** legal/corporate/EIN_APPLICATION_GUIDE.md
**URL:** https://www.irs.gov/businesses/...
**Hours:** Mon-Fri 7am-10pm ET only
**References:** legal/LEGAL_STATUS.md" --json

# Brief notes if no documentation exists
bd create "Implement auth" -t feature -p 1 \
  --notes "**Approach:** JWT tokens **Timeline:** 2-3 hours" --json
```

**Notes format:**

- Keep CONCISE - link to docs, don't duplicate
- Include direct file paths or URLs
- Add key constraints (hours, prerequisites)
- Use markdown formatting

## Reading Task Fields (JSON)

**IMPORTANT:** `bd show` returns arrays. Always use `.[0]` to access.

```bash
# String fields (use -r for raw)
bd show <id> --json | jq -r '.[0].title'
bd show <id> --json | jq -r '.[0].description'
bd show <id> --json | jq -r '.[0].notes'
bd show <id> --json | jq -r '.[0].status'

# Numeric fields
bd show <id> --json | jq '.[0].priority'
bd show <id> --json | jq '.[0].estimated_minutes'

# Array fields
bd show <id> --json | jq -r '.[0].labels[]'           # Each label
bd show <id> --json | jq '.[0].labels | length'       # Count

# Dependencies (complex objects)
bd show <id> --json | jq -r '.[0].dependencies[]?.id'
bd show <id> --json | jq '[.[0].dependencies[]? | select(.dependency_type == "blocks")]'

# Check if field exists
bd show <id> --json | jq '.[0].notes != null and .[0].notes != ""'
bd show <id> --json | jq '.[0].parent != null'
```

## Best Practices

### DO ✅

- Always use `--json` flag
- Run `bd sync` at end of session
- Check `bd ready` before creating new tasks
- Use `discovered-from` liberally for side quests
- Close tasks with `--reason`
- Add notes with documentation links
- Claim tasks immediately (update to in_progress)
- Use specific priorities (P0 critical, P1 high, P2+ lower)
- Create epics for work >4 hours with 3+ tasks

### DON'T ❌

- Skip `bd sync` (leaves work stranded)
- Use `blocks` for soft relationships (use `related`)
- Reverse epic dependency direction (`bd dep add epic task` is WRONG —
  use `bd dep add task epic` so the task depends on the epic as its parent)
- Create duplicate issues (search first)
- Leave tasks `in_progress` when switching work
- Use vague titles ("Fix auth" → "Add JWT auth")
- Mix unrelated work in one epic
- Ignore ready work (check `bd ready` first)

## Integration with Thrum

Use Thrum for real-time coordination, Beads for task state:

```bash
# Register with Thrum
thrum quickstart --role implementer --module auth --intent "Working on bd-123"

# Check Beads for work
bd ready --json

# Claim in Beads
bd update bd-123 --status in_progress --json
bd sync

# Announce via Thrum
thrum send "[bd-123] Starting auth implementation" --to @coordinator

# Work on task...

# Complete in Beads
bd close bd-123 --reason "Auth complete" --json
bd sync

# Notify via Thrum
thrum send "[bd-123] Complete - auth implemented with tests" --to @coordinator
```

## Troubleshooting

### "not in a bd workspace"

```bash
# Check if .beads exists
ls -la .beads

# If not, check if you're in right directory
cd /path/to/repo
bd info --json
```

### No ready work found

```bash
# Check all open issues
bd list --status open --json

# Check blocked issues
bd list --status blocked --json

# Import latest from git
bd sync
```

### Sync conflicts

```bash
# Accept remote (safest)
git checkout --theirs .beads/issues.jsonl
bd import -i .beads/issues.jsonl
git add .beads/issues.jsonl
git commit -m "Resolve beads sync conflict"
bd sync
```

### Warnings in worktrees

Warnings like "git status failed: exit status 128" or "snapshot validation
failed" are **normal and safe** in worktree environments. Check final output -
if it shows "✓ Pushed beads-sync to remote", sync succeeded.

## Session Template

```bash
# === START ===
bd sync
bd ready --json
bd update <id> --status in_progress --json
bd sync

# === WORK ===
# (make changes, discover issues, etc.)
bd create "Found issue" -t bug -p 1 --deps discovered-from:<current-id> --json

# === END ===
bd close <id> --reason "Done" --json
bd sync
git status  # Verify "up to date"
```

## Summary

Beads provides:

- ✅ Dependency-aware task management
- ✅ Git-based sync (automatic export/import)
- ✅ Ready work detection (unblocked tasks)
- ✅ Discovery tracking (side quests with context)
- ✅ Multi-agent coordination (share state via Git)
- ✅ Persistent memory across sessions

**Always use `--json`, always run `bd sync` at end, always check `bd ready`
first.**
