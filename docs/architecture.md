
## Thrum Foundation Architecture

This document describes the foundational packages that support the Thrum agent
messaging system.

## Overview

The foundation provides core functionality that both the daemon and CLI depend
on:

- **Configuration loading** - Environment variables, identity files, CLI flags,
  agent naming
- **ID generation** - Deterministic and unique identifiers (ULID-based)
- **JSONL handling** - Append-only event log with file locking
- **SQLite schema** - Database tables, indexes, and migrations (version 7)
- **Event projection** - Replay sharded JSONL events into SQLite
- **Path resolution** - `.thrum/redirect` for multi-worktree, sync worktree path
  via `git-common-dir`
- **Git context** - Extract live git state (branch, commits, files) for agent
  work context

## Package Structure

````text
internal/
├── config/      # Configuration loading, identity files, agent naming
├── identity/    # ID generation (repo, agent, session, message, event)
├── jsonl/       # JSONL reader/writer with file locking
├── projection/  # SQLite projection engine (multi-file rebuild)
├── schema/      # SQLite schema, migrations, JSONL sharding migration
├── paths/       # Path resolution, redirect, sync worktree path
├── gitctx/      # Git-derived work context extraction
└── types/       # Shared event types
```go

## Configuration (`internal/config`)

### Resolution Order

Configuration is resolved in priority order:

1. `THRUM_NAME` env var (selects which identity file to load)
2. Environment variables (`THRUM_ROLE`, `THRUM_MODULE`, `THRUM_DISPLAY`)
3. CLI flags (`--role`, `--module`)
4. Identity file (`.thrum/identities/{name}.json`)
5. Error if required fields missing

### Identity File Format

Identity files are stored at `.thrum/identities/{agent_name}.json`
(per-worktree):

```json
{
  "version": 2,
  "repo_id": "r_7K2Q1X9M3P0B",
  "agent": {
    "kind": "agent",
    "name": "furiosa",
    "role": "implementer",
    "module": "sync-daemon",
    "display": "Sync Implementer"
  },
  "worktree": "daemon",
  "confirmed_by": "human:leon",
  "updated_at": "2026-02-03T18:02:10.000Z"
}
```go

### Agent Naming

Agents support human-readable names:

- `--name furiosa` on `quickstart` or `agent register`
- `THRUM_NAME` env var (highest priority)
- Names: `[a-z0-9_]+` (lowercase alphanumeric + underscores)
- Reserved: `daemon`, `system`, `thrum`, `all`, `broadcast`
- When name is provided, it becomes the agent ID directly (e.g., `furiosa`)
- When omitted, falls back to `{role}_{hash10}` format (e.g.,
  `coordinator_1B9K33T6RK`)

### Config Struct

```go
type Config struct {
    RepoID  string      // Repository ID
    Agent   AgentConfig // Agent identity
    Display string      // Display name
}

type AgentConfig struct {
    Kind    string // "agent" or "human"
    Name    string // Agent name (e.g., "furiosa")
    Role    string // Agent role (e.g., "implementer")
    Module  string // Module/component responsibility
    Display string // Display name
}
```text

### Loading

```go
// Load from current directory
cfg, err := config.Load(flagRole, flagModule)

// Load from specific repo path
cfg, err := config.LoadWithPath(repoPath, flagRole, flagModule)
```text

## Identity (`internal/identity`)

### ID Formats

| Type                   | Format                          | Example                  |
| ---------------------- | ------------------------------- | ------------------------ |
| **Repo ID**            | `r_` + base32(sha256(url))[:12] | `r_7K2Q1X9M3P0B`         |
| **Agent ID (named)**   | name directly                   | `furiosa`                |
| **Agent ID (unnamed)** | role + `_` + base32(hash)[:10]  | `implementer_9F2K3M1Q8Z` |
| **User ID**            | `user:` + username              | `user:leon`              |
| **Session ID**         | `ses_` + ulid()                 | `ses_01HXF2A9Y1Q0P8...`  |
| **Session Token**      | `tok_` + ulid()                 | `tok_01HXF2A9Y1Q0P8...`  |
| **Message ID**         | `msg_` + ulid()                 | `msg_01HXF2A9Y1Q0P8...`  |
| **Event ID**           | `evt_` + ulid()                 | `evt_01HXF2A9Y1Q0P8...`  |

### Deterministic IDs

- **Repo ID**: Derived from Git origin URL (normalized to https, lowercased,
  `.git` suffix stripped)
- **Agent ID (unnamed)**: Derived from repo_id + role + module (sha256 +
  Crockford base32)
- Same inputs always produce the same ID

### Unique IDs

- **Session, Message, Event IDs**: Use ULID (time-ordered, unique)
- ULID format: 26 characters, sortable by time, 128-bit random
- Thread-safe generation with mutex-protected monotonic entropy

Agent IDs are generated internally from the role and a hash. See
[Development Guide](development.md) for implementation details.

## Paths (`internal/paths`)

### Path Resolution

The `paths` package handles path resolution for multi-worktree setups and sync
worktree location.

**Key functions:**

| Function                     | Returns                        | Description                                       |
| ---------------------------- | ------------------------------ | ------------------------------------------------- |
| `ResolveThrumDir(repoPath)`  | `.thrum/` path                 | Follows `.thrum/redirect` if present              |
| `SyncWorktreePath(repoPath)` | `.git/thrum-sync/a-sync/` path | Uses `git-common-dir` for nested worktree support |
| `VarDir(thrumDir)`           | `.thrum/var/` path             | Runtime files directory                           |
| `IdentitiesDir(repoPath)`    | `.thrum/identities/` path      | Per-worktree agent identity files                 |

### Redirect File

Feature worktrees share the main worktree's daemon and state via a redirect
file:

```text
.thrum/redirect    -> /path/to/main/worktree/.thrum
```text

**Resolution rules:**

- If `.thrum/redirect` exists, read target path and use it as the effective
  `.thrum/` directory
- Target must be an absolute path
- Redirect chains (A -> B -> C) are detected and rejected
- Self-referencing redirects are rejected
- If no redirect file, use local `.thrum/` (this is the main worktree)

**Note:** `IdentitiesDir()` always uses the LOCAL `.thrum/identities/` (not the
redirect target), because agent identities are per-worktree.

### Sync Worktree Path

The sync worktree lives at `.git/thrum-sync/a-sync/`:

```go
syncDir, err := paths.SyncWorktreePath(repoPath)
// Returns: /path/to/repo/.git/thrum-sync/a-sync
```go

Uses `git rev-parse --git-common-dir` to find the correct `.git/` directory,
which handles nested worktrees correctly (where `.git` is a file pointing to the
parent repo's `.git/worktrees/` directory).

## Git Context (`internal/gitctx`)

### Work Context Extraction

The `gitctx` package extracts live Git state for agent work context tracking.
Called during `session.heartbeat` to provide real-time visibility into what each
agent is working on.

**Exported types:**

```go
type WorkContext struct {
    Branch           string          `json:"branch"`
    WorktreePath     string          `json:"worktree_path"`
    UnmergedCommits  []CommitSummary `json:"unmerged_commits"`
    UncommittedFiles []string        `json:"uncommitted_files"`
    ChangedFiles     []string        `json:"changed_files"`
    ExtractedAt      time.Time       `json:"extracted_at"`
}

type CommitSummary struct {
    SHA     string   `json:"sha"`
    Message string   `json:"message"` // First line only
    Files   []string `json:"files"`
}
```text

**`ExtractWorkContext(worktreePath)`:**

- Returns empty context (not error) if path is not a git repo
- Determines base branch automatically (`origin/main`, `origin/master`, or
  `HEAD~10`)
- Extracts unmerged commits with per-commit file lists
- Runs in ~80ms typically

## JSONL (`internal/jsonl`)

### Append-Only Log

```go
// Writing
writer, _ := jsonl.NewWriter("events.jsonl")
writer.Append(event)
writer.Close()

// Reading all
reader, _ := jsonl.NewReader("events.jsonl")
messages, _ := reader.ReadAll()

// Streaming
ctx := context.Background()
ch := reader.Stream(ctx)
for msg := range ch {
    // Process message
}
```text

### Safety Features

- **File locking**: Uses `syscall.Flock()` to prevent concurrent writes
- **Atomic appends**: Write to temp file, then append to main file, fsync
- **Auto-create**: Creates parent directories and file if needed
- **In-process mutex**: `sync.Mutex` for thread safety within the same process

### Sharded File Layout

JSONL files are sharded by type and agent (in the sync worktree at
`.git/thrum-sync/a-sync/`):

```text
events.jsonl              # Agent lifecycle, sessions, threads
messages/
  furiosa.jsonl           # Messages authored by agent "furiosa"
  coordinator_1B9K.jsonl  # Messages authored by unnamed agent
```text

Event routing is handled by `internal/daemon/state/` which directs `message.*`
events to per-agent files and all other events to `events.jsonl`.

## Schema (`internal/schema`)

### Database Tables

```text
messages            # All messages (create/edit/delete)
message_scopes      # Routing scopes (many-to-many)
message_refs        # References (many-to-many)
message_reads       # Per-session read tracking (local-only, no git sync)
message_edits       # Edit history tracking
agents              # Registered agents (kind: "agent" or "user")
sessions            # Agent work periods
session_scopes      # Session context scopes
session_refs        # Session context references
subscriptions       # Push notification subscriptions
agent_work_contexts # Live git state per session
groups              # Named collections for targeted messaging
group_members       # Group membership (agents and roles)
schema_version      # Migration tracking
```text

### Schema Version

Current version: **13**

Key migrations:

- v3 -> v4: Impersonation support (`authored_by`, `disclosed` columns)
- v5 -> v6: Agent work contexts table, message reads, session scopes/refs
- v6 -> v7: Event ID backfill (ULID `event_id` on all JSONL events), JSONL
  sharding migration
- v7 -> v8: Groups feature (`groups` and `group_members` tables), `@everyone`
  built-in group

### Initialization

```go
db, _ := schema.OpenDB("thrum.db")
schema.InitDB(db)  // Create tables and indexes

// Or use migration (checks version first, runs incremental migrations)
schema.Migrate(db)
```text

### JSONL Migrations

The schema package also handles JSONL structure migrations:

```go
// Migrate monolithic messages.jsonl -> per-agent sharded files
schema.MigrateJSONLSharding(syncDir)

// Backfill event_id (ULID) for events that lack it
schema.BackfillEventID(syncDir)
```text

### Features

- **Pure Go SQLite**: Uses `modernc.org/sqlite` (no CGO)
- **WAL mode**: Better concurrency
- **Foreign keys**: Enabled with `ON DELETE CASCADE`
- **Indexes**: Optimized for common queries

## Projection (`internal/projection`)

### Event Replay

The projector rebuilds SQLite from sharded JSONL event logs:

```go
db, _ := schema.OpenDB("thrum.db")
schema.InitDB(db)

projector := projection.NewProjector(db)

// Rebuild from sync worktree (reads events.jsonl + messages/*.jsonl)
projector.Rebuild(syncDir)

// Or apply a single event
projector.Apply(eventJSON)
```go

### Multi-File Rebuild

`Rebuild(syncDir)` handles the sharded JSONL structure:

1. Read `events.jsonl` (agent lifecycle, sessions)
2. Glob `messages/*.jsonl` (per-agent message files)
3. Sort ALL events globally by `(timestamp, event_id)` for deterministic
   ordering
4. Apply to SQLite in order

File boundaries are transparent to the projector -- it only cares about event
ordering.

### Event Types

| Event                 | Action                                               |
| --------------------- | ---------------------------------------------------- |
| `message.create`      | Insert into messages, scopes, refs                   |
| `message.edit`        | Update body_content, updated_at, record edit history |
| `message.delete`      | Set deleted=1, deleted_at, delete_reason             |
| `group.create`        | Insert into groups                                   |
| `group.delete`        | Delete group and members                             |
| `agent.register`      | Insert/replace agent                                 |
| `agent.update`        | Merge work contexts for agent                        |
| `agent.session.start` | Insert session                                       |
| `agent.session.end`   | Update ended_at, end_reason                          |

### Forward Compatibility

Unknown event types are silently ignored, allowing older projectors to process
logs with newer event types.

## Types (`internal/types`)

Shared Go structs for all event types:

- `BaseEvent` - Common fields: `type`, `timestamp`, `event_id`, `v` (version)
- `MessageCreateEvent` - Message creation with body, scopes, refs
- `MessageEditEvent` - Message body edit
- `MessageDeleteEvent` - Soft delete with reason
- `GroupCreateEvent` - Group creation with name and description
- `GroupDeleteEvent` - Group deletion
- `AgentRegisterEvent` - Agent registration (kind: "agent" or "user")
- `AgentUpdateEvent` - Agent work context updates
- `AgentCleanupEvent` - Agent cleanup/deletion
- `AgentSessionStartEvent` - Session start
- `AgentSessionEndEvent` - Session end with reason
- `SessionWorkContext` - Work context data for sync

Each event includes:

- `type`: Event type string (e.g., `"message.create"`)
- `timestamp`: ISO 8601 timestamp
- `event_id`: ULID for deduplication (auto-generated by `State.WriteEvent()`)
- `v`: Schema version (currently `1`)
- Event-specific fields

## Design Principles

### 1. Append-Only Events

JSONL is the source of truth. SQLite is a rebuildable projection for fast
queries. The projection can be deleted and rebuilt from JSONL at any time.

### 2. Per-Agent Sharding

Message events are sharded into per-agent JSONL files
(`messages/{agent}.jsonl`). This reduces merge conflicts, improves sync
performance, and enables per-agent file tracking in Git.

### 3. Deterministic Hashing

Repo and agent IDs are deterministic (SHA256-based), enabling identity
verification across machines without central coordination.

### 4. Time-Ordered IDs

ULID format ensures IDs (messages, sessions, events) are sortable by creation
time and globally unique.

### 5. Offline-First

No network required for local operation. Git handles replication via the sync
loop.

### 6. Low-Conflict

Immutable events + ULID timestamps + per-agent sharding minimize merge conflicts
during Git sync.

### 7. Path Indirection

The `.thrum/redirect` pattern allows multiple worktrees to share a single daemon
and state directory without hardcoding paths.

### 8. Timeout Enforcement (v0.4.3)

All I/O paths enforce timeouts to prevent indefinite hangs:

- **5s** CLI dial timeout (net.DialTimeout)
- **10s** RPC call timeout (context.WithTimeout)
- **10s** server per-request timeout (http.TimeoutHandler)
- **10s** WebSocket handshake timeout
- **5s/10s** git command timeouts (via `safecmd` wrapper)
- **Context-scoped** SQLite queries (via `safedb` wrapper)

Lock scope has been reduced — no mutex is held during I/O, git, or WebSocket
dispatch operations.

## References

- Design document: `dev-docs/2026-02-03-thrum-design.md`
- Sharding design: `dev-docs/2026-02-06-jsonl-sharding-and-agent-naming.md`
- Daemon architecture: `docs/daemon.md`
- Sync protocol: `docs/sync.md`
````
