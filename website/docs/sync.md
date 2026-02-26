---
title: "Sync Protocol"
description:
  "Git-based message synchronization using orphan branch, sync worktree,
  conflict-free merging, and offline-first design"
category: "sync"
order: 1
tags: ["sync", "git", "worktree", "merge", "offline", "jsonl"]
last_updated: "2026-02-10"
---

## Sync Protocol

The sync protocol keeps message logs synchronized across repository clones using
Git as the transport layer. This enables offline-first operation with eventual
consistency.

## Overview

Thrum uses a dedicated `a-sync` orphan branch to synchronize message history
independently from code changes. The JSONL event logs live in a sync worktree
(at `.git/thrum-sync/a-sync/`) checked out on this branch. The daemon
automatically fetches, merges, and pushes changes within the worktree on a
configurable interval (default: 60 seconds).

**Key design decision:** All sync operations happen within the sync worktree.
There is no `git checkout` or branch switching on the main working tree. This
avoids interfering with the developer's code work.

## Architecture

```text
┌──────────────────────────────────────────────────────────────┐
│          Sync Loop (60s) in .git/thrum-sync/a-sync/          │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  1. Acquire Lock (.thrum/var/sync.lock)                      │
│  2. Fetch Remote (git fetch origin a-sync)                   │
│  3. Merge (batch extract via git archive, JSONL dedup)       │
│  4. Project New Events into SQLite                           │
│  5. Notify Subscribers (new event IDs via channel)           │
│  6. Commit Local Changes in Worktree                         │
│  7. Push Worktree to Remote                                  │
│  8. Release Lock                                             │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

## Worktree Model

### The Sync Worktree

The sync worktree is a git worktree at `.git/thrum-sync/a-sync/`, linked to the
repository and checked out on the `a-sync` orphan branch. It contains:

```text
.git/thrum-sync/a-sync/
├── events.jsonl              Append-only agent lifecycle events
└── messages/
    └── {agent_name}.jsonl    Per-agent message logs
```

This worktree is:

- **Created automatically** by `thrum init`
- **Never interacted with** by users directly
- **Operated on** exclusively by the daemon via git commands
- **Independent** from all code branches
- **Shared** across all repository clones via the `a-sync` branch
- **Sparse checkout** -- only `events.jsonl`, `messages/`, and `messages.jsonl`
  (migration compat) are checked out
- **Hidden** from IDE file explorers (lives inside `.git/`)

### Sparse Checkout

The worktree uses sparse checkout (non-cone mode) to only include Thrum data
files:

```text
/events.jsonl
/messages/
/messages.jsonl    # old monolithic format, kept for migration support
```

This is configured automatically during `CreateSyncWorktree()` and reduces disk
usage by excluding any non-Thrum files that may appear on the branch.

### Path Resolution

The sync worktree path is resolved via `git rev-parse --git-common-dir`:

```go
// SyncWorktreePath returns: <git-common-dir>/thrum-sync/a-sync
func SyncWorktreePath(repoPath string) (string, error)
```

Using `git-common-dir` ensures correct resolution for both regular repos and
nested git worktrees. For a regular repo, `git-common-dir` returns `.git`, so
the path resolves to `.git/thrum-sync/a-sync/`. For a nested worktree, it
returns the main repo's `.git/` directory, ensuring all worktrees share the same
sync data.

### Worktree Health Checks

`CreateSyncWorktree()` performs a 4-level health check before creating or
recreating the worktree:

1. `.git` file exists (worktrees have a `.git` file, not a directory)
2. Listed in `git worktree list --porcelain`
3. HEAD points to `a-sync`
4. Sparse checkout includes expected patterns

If any check fails, the worktree is removed and recreated.

### The `a-sync` Orphan Branch

The `a-sync` branch is an orphan branch with no common history with `main` or
any code branch. It contains only JSONL event log files. This ensures:

- No merge conflicts with code
- Clean separation of concerns
- No accidental inclusion of code in sync data (or vice versa)

### Branch Lifecycle

1. **Creation**: `thrum init` creates the `a-sync` orphan branch using safe git
   plumbing commands (`git commit-tree` + `git update-ref`), which never touch
   the working tree or index
2. **Local-only**: Works without a remote (offline mode)
3. **Remote push**: Automatically pushed when remote is available
4. **Migration**: Existing repos can upgrade via `thrum migrate`

The orphan branch is created using the well-known empty tree SHA
(`4b825dc642cb6eb9a060e54bf8d69288fbee4904`), ensuring the branch has no files
initially and no shared history with any code branch.

### Feature Worktrees

When working in a feature worktree (e.g., via `git worktree add`), use
`thrum setup` to configure the worktree. This creates a `.thrum/redirect` file
pointing to the main worktree's `.thrum/` directory, so all worktrees share the
same daemon and message store.

```text
# In feature worktree:
.thrum/
├── redirect              → "/path/to/main/repo/.thrum" (absolute path)
└── identities/           ← Per-worktree agent identities (NOT redirected)
    └── furiosa.json
```

**Resolution rules** (`internal/paths/`):

| Function             | Follows Redirect | Purpose                                             |
| -------------------- | ---------------- | --------------------------------------------------- |
| `ResolveThrumDir()`  | Yes              | Returns effective `.thrum/` (follows redirect)      |
| `SyncWorktreePath()` | N/A              | Uses `git-common-dir` (inherently shared)           |
| `VarDir()`           | Depends on input | Runtime directory (SQLite, socket, PID, lock)       |
| `IdentitiesDir()`    | **No**           | Always local to worktree (per-agent identity files) |
| `IsRedirected()`     | N/A              | Checks whether a redirect file exists               |

**Safety**: Only single-hop redirects are supported. `ResolveThrumDir()` detects
and rejects redirect chains and self-references.

## Merge Strategy

### Conflict-Free Merging

Since events are **immutable** with **unique IDs**, conflicts are impossible:

1. **Fetch remote** changes into the worktree
2. **Fast-forward merge** if possible (no local changes)
3. **JSONL dedup merge** if both sides have changes:
   - Read local events from worktree JSONL files
   - Read remote events from fetched branch
   - Union by event ID -- deduplicate identical events
   - Sort by timestamp -- maintain chronological order
   - Write merged result -- atomic file replacement

### Deduplication

Every event carries a globally unique `event_id` field (ULID). This is the
universal deduplication key for all event types:

```go
// All events deduplicate on event_id (ULID)
eventID, ok := obj["event_id"].(string)
if localEvent.ID == remoteEvent.ID {
    // Same event_id = same content (immutability guarantee)
    duplicates++
}
```

The `event_id` (ULID) provides both uniqueness and lexicographic time-ordering.
This avoids collisions between different event types that share the same entity
ID (e.g., a `message.create` and `message.edit` for the same `message_id` are
distinct events with distinct `event_id` values).

### Sharded JSONL Files

Events are sharded across multiple files:

- `events.jsonl` -- agent lifecycle events (`agent.register`,
  `agent.session.start`, `agent.session.end`, `agent.cleanup`)
- `messages/{agent_name}.jsonl` -- per-agent message events (`message.create`,
  `message.edit`, `message.delete`, `agent.update`)

Each agent writes to its own message file (e.g., `messages/furiosa.jsonl`), so
different agents never contend on the same file. Sharding reduces merge
conflicts and improves performance for large deployments.

### Batch Remote Extraction

During merge, the sync engine uses `git archive` to batch-extract all remote
files in a single command, piped through `tar`:

```go
gitCmd := exec.Command("git", "archive", "origin/a-sync", "--", "messages/", "events.jsonl")
tarCmd := exec.Command("tar", "-xf", "-", "-C", tmpDir)
tarCmd.Stdin, _ = gitCmd.StdoutPipe()
```

This is significantly faster than issuing per-file `git show` calls. If
`git archive` fails (e.g., no remote tracking branch), the merger falls back to
per-file `git show` calls.

## Push & Retry Logic

### Atomic Commits

Each sync creates a single commit in the worktree:

```yaml
sync: 2024-01-15T10:30:00Z
```

### Push Rejection Handling

When remote is ahead (non-fast-forward):

1. **Retry up to 3 times**
2. **Fetch and merge** after rejection
3. **Re-commit and push** with updated state
4. **Fail** if still rejected after 3 attempts

```go
for attempt := 1; attempt <= 3; attempt++ {
    if err := push(); err == nil {
        return nil // Success
    }

    // Rejection - fetch, merge, retry
    fetch()
    merge()
}
```

## Sync Triggers

### Automatic Triggers

1. **Periodic Sync** -- Every 60 seconds (default interval)
2. **On Write** -- After local message creation (optional)

### Manual Triggers

1. **RPC**: `sync.force` -- Force immediate sync
2. **CLI**: `thrum sync force` -- Manual sync command

## Locking

### File Lock

Location: `.thrum/var/sync.lock`

The daemon uses platform-specific file locking:

- **Unix**: `flock(LOCK_EX | LOCK_NB)`
- **Windows**: `LockFileEx` with exclusive, non-blocking flags

### Lock Behavior

- **Exclusive**: Only one process can sync at a time
- **Non-blocking**: Sync skips if lock is held
- **Auto-release**: Lock released on process exit

## Offline Operation

The sync protocol is designed for offline-first operation:

### Local-Only Mode

For public repositories where you don't want to expose agent messages, enable
local-only mode to disable all remote sync:

```bash
thrum daemon start --local
```

In local-only mode, the sync loop still runs but skips remote operations
(`git fetch` and `git push` to/from the remote). Local JSONL files and the
SQLite projection continue to update normally. See
[Daemon Architecture](daemon.md#local-only-mode) for details.

### No Remote

When no remote is configured:

- Sync loop runs normally
- Remote operations (`git fetch` and `git push`) are skipped
- Local worktree state and database updates continue as normal
- Local JSONL in the sync worktree is still maintained

### Network Unavailable

When network is down:

- Fetch failures are **ignored** (offline mode)
- Push failures are **ignored** (offline mode)
- Local changes accumulate in the worktree
- Automatic sync when network returns

### Remote Ahead

When remote has changes we don't have:

- Merge brings in new events
- SQLite projection is updated
- Subscribers are notified

## Projection Updates (CQRS)

Thrum uses an event sourcing architecture with CQRS (Command Query
Responsibility Segregation):

- **Source of truth**: JSONL files in the sync worktree (append-only event log)
- **Read projection**: SQLite database at `.thrum/var/messages.db` (derived,
  rebuildable)

The projector (`internal/projection/`) replays events from JSONL into SQLite.
Two modes of operation:

### Incremental Update (Sync Loop)

During each sync cycle, new events from the merge step are passed directly to
the projector:

```go
// Phase 5 optimization: events are passed from merge step,
// eliminating redundant file I/O
for _, event := range parsedEvents {
    projector.Apply(event)
}
```

The `Apply()` method dispatches on event type:

| Event Type            | SQLite Action                            |
| --------------------- | ---------------------------------------- |
| `message.create`      | Insert into messages, scopes, refs       |
| `message.edit`        | Update body_content, insert edit history |
| `message.delete`      | Set deleted=1, deleted_at                |
| `agent.register`      | Insert/replace agent                     |
| `agent.session.start` | Insert session                           |
| `agent.session.end`   | Update ended_at, end_reason              |
| `agent.update`        | Merge work contexts by session_id        |

Unknown event types are silently ignored (forward compatibility).

### Full Rebuild

The projector can rebuild the entire SQLite database from scratch:

```go
projector := projection.NewProjector(db)
projector.Rebuild(syncDir) // reads events.jsonl + messages/*.jsonl
```

Rebuild reads all JSONL files, sorts events globally by `(timestamp, event_id)`
for deterministic ordering, and applies them in sequence. ULIDs in `event_id`
make this globally consistent across files.

## Migration from Old Layout

Repos created before the worktree architecture had JSONL files tracked directly
on `main`:

```text
# Old layout (tracked on main)
.thrum/
├── events.jsonl          # tracked on main
├── messages/             # tracked on main
├── schema_version        # tracked on main
└── var/                  # gitignored
```

Use `thrum migrate` to upgrade to the worktree layout. This command:

1. Creates the `a-sync` orphan branch (if not already present)
2. Copies existing JSONL data to the new branch
3. Sets up the sync worktree
4. Removes old tracked files from the main branch
5. Updates `.gitignore` to ignore `.thrum/` entirely

## Monitoring & Status

### Sync Status

```json
{
  "running": true,
  "last_sync_at": "2024-01-15T10:30:00Z",
  "last_error": "",
  "sync_state": "synced",
  "local_only": false
}
```

> **Note:** `thrum init` sets `local_only: true` by default. The example above
> shows a repo where remote sync has been explicitly enabled.

States:

- `stopped` -- Sync loop not running
- `idle` -- Running but no syncs yet
- `synced` -- Last sync successful
- `error` -- Last sync failed

### RPC Methods

#### `sync.force`

Force an immediate sync (non-blocking).

**Request**: `{}`

**Response**:

```json
{
  "triggered": true,
  "last_sync_at": "2024-01-15T10:30:00Z",
  "sync_state": "synced"
}
```

#### `sync.status`

Get current sync status.

**Request**: `{}`

**Response**:

```json
{
  "running": true,
  "last_sync_at": "2024-01-15T10:30:00Z",
  "last_error": "",
  "sync_state": "synced"
}
```

## Error Handling

### Transient Errors

Transient errors are logged but don't stop the sync loop:

- Network timeouts
- Temporary git failures
- Lock contention

### Permanent Errors

Permanent errors require intervention:

- Repository corruption
- Invalid JSONL format
- Worktree detached or missing (run `thrum init --force` to recreate)

## Performance

### Optimization Strategies

1. **Incremental merges** -- Only process new events
2. **File locking** -- Prevents concurrent git operations
3. **Atomic writes** -- Uses temporary file + rename
4. **Buffered channels** -- Non-blocking notification delivery
5. **Sharded files** -- Reduces merge overhead per-agent
6. **git archive batch extraction** -- Single command extracts all remote files
   (vs. per-file `git show`)
7. **Parsed event passthrough** -- Merge step passes parsed events directly to
   projector (no re-read from disk)

### Scalability

The sync protocol scales well because:

- **Append-only** -- No history rewriting
- **Sharded by agent** -- Parallel writes without contention
- **Event-driven** -- Only new events are processed
- **Asynchronous** -- Non-blocking notifications
- **Git-based** -- Leverages Git's efficiency

## Security

### Authentication

Uses git's authentication:

- SSH keys
- HTTPS credentials
- OAuth tokens

### Authorization

Repository-level access control:

- Read access required for fetch
- Write access required for push
- No additional Thrum-specific auth

## Configuration

### Sync Interval

Default: 60 seconds

Can be configured when starting the daemon:

```go
loop := sync.NewSyncLoop(syncer, projector, repoPath, syncDir, thrumDir, 60*time.Second)
```

Parameters:

- `syncer`: handles git operations (fetch, merge, push)
- `projector`: applies events to SQLite
- `repoPath`: path to the git repository
- `syncDir`: path to sync worktree (`.git/thrum-sync/a-sync/`)
- `thrumDir`: path to `.thrum/` directory (used for lock path resolution)
- `interval`: how often to sync (default: 60 seconds)

### Manual Sync

Trigger an immediate sync via RPC or CLI:

```bash
thrum sync force
```

## Troubleshooting

### Sync Not Working

1. Check daemon is running: `thrum daemon status`
2. Check sync status: `thrum sync status`
3. View daemon logs for errors
4. Verify git remote is configured
5. Check network connectivity
6. Verify the sync worktree exists: `ls -la .git/thrum-sync/a-sync/`

### Worktree Missing or Corrupt

If the sync worktree is missing or detached:

```bash
thrum init --force
```

This recreates the worktree and re-links it to the `a-sync` branch.

### Conflicts

**Conflicts cannot occur** in Thrum due to immutable events and append-only
JSONL dedup. If you see a git conflict in the worktree:

- The `a-sync` branch was manually modified
- Recovery: `cd .git/thrum-sync/a-sync && git checkout --theirs .`

### Lock Stuck

If sync is permanently stuck:

```bash
rm .thrum/var/sync.lock
```

Then restart the daemon.

### Upgrading from Old Layout

If your repo still has `.thrum/events.jsonl` or `.thrum/messages/` tracked on
`main`:

```bash
thrum migrate
```

## See Also

- [Daemon Architecture](daemon.md)
- [System Overview](overview.md)
- [RPC API Reference](rpc-api.md)
```
