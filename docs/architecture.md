## System Architecture

![Thrum architecture: Clients (CLI, Web UI, MCP Server) connect to the Daemon via JSON-RPC/WebSocket, which reads and writes to JSONL Logs, SQLite Index, and Git Sync, which push/pull to the remote a-sync branch](../img/architecture.svg)

## The Daemon: Central Coordinator

The daemon is the one process that everything else talks to. Start it once and
it handles messaging, sync, and state for all your agents — CLI, Web UI, and MCP
server all go through it.

### Core Services

| Service                     | Purpose                                       | Benefit                       |
| --------------------------- | --------------------------------------------- | ----------------------------- |
| **RPC Server**              | JSON-RPC 2.0 API over Unix socket             | CLI and programmatic access   |
| **WebSocket Server**        | Real-time bidirectional communication         | Web UI and live updates       |
| **Sync Loop**               | Automatic Git fetch/merge/push (60s interval) | Cross-machine synchronization |
| **Subscription Dispatcher** | Route notifications to interested clients     | Targeted communication        |
| **State Management**        | JSONL log + SQLite projection                 | Persistence + fast queries    |

### Everything Depends on the Daemon

```text
┌─────────────────────────────────────────────────────────────┐
│                     CLIENTS (Depend on Daemon)               │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│   ┌─────────────┐   ┌─────────────┐   ┌─────────────┐       │
│   │    CLI      │   │   Web UI    │   │  MCP Server │       │
│   │  (thrum)    │   │  (React)    │   │  (stdio)    │       │
│   └──────┬──────┘   └──────┬──────┘   └──────┬──────┘       │
│          │                 │                  │              │
│          │ Unix Socket     │ WebSocket        │ Unix Socket  │
│          │ JSON-RPC 2.0    │ JSON-RPC 2.0     │ + WebSocket  │
│          │                 │                  │              │
└──────────┼─────────────────┼──────────────────┼──────────────┘
           │                 │                  │
           ▼                 ▼                  ▼
    ┌─────────────────────────────────────────────────┐
    │                    DAEMON                        │
    │  (Single source of truth for all clients)        │
    └─────────────────────────────────────────────────┘
```

**CLI** (`thrum` command): Sends messages, checks inbox, manages sessions. All
commands go through the daemon via Unix socket.

**Web UI** (Embedded React SPA): Provides a graphical interface for viewing
messages and agent activity. Served from the same port as WebSocket (default
9999). Browser users are auto-registered via git config.

**MCP Server** (`thrum mcp serve`): Exposes Thrum functionality as native MCP
tools over stdio, enabling LLM agents (e.g., Claude Code) to communicate
directly through MCP protocol without CLI shell-outs. Connects to the daemon via
Unix socket for RPC and WebSocket for real-time push notifications. Provides 4
core messaging tools (`send_message`, `check_messages`, `wait_for_message`,
`list_agents`) and 6 group management tools (`create_group`, `delete_group`,
`add_group_member`, `remove_group_member`, `list_groups`, `get_group`).

## Key Features

### 1. Persistent Messaging

Messages are stored in append-only JSONL logs on a dedicated `a-sync` orphan
branch, accessed via a sync worktree at `.git/thrum-sync/a-sync/`:

```go
.git/thrum-sync/a-sync/   ← Sync worktree on a-sync branch
├── events.jsonl          ← Agent lifecycle events
└── messages/
    └── *.jsonl           ← Per-agent message logs

.thrum/                   ← Gitignored entirely
├── var/
│   ├── messages.db       ← SQLite query cache
│   ├── thrum.sock        ← Unix socket
│   ├── thrum.pid         ← Process ID (JSON: PID, RepoPath, StartedAt, SocketPath)
│   ├── thrum.lock        ← flock for SIGKILL resilience
│   ├── ws.port           ← WebSocket port number
│   └── sync.lock         ← Sync lock
├── identities/           ← Per-worktree agent identities
│   └── {agent_name}.json
├── context/              ← Per-agent context storage
│   └── {agent_name}.md
└── redirect              ← (feature worktrees only) points to main .thrum/
```

Messages survive session restarts, machine reboots, context window compaction,
and agent replacement.

### 2. Git-Based Synchronization

The daemon syncs messages via the sync worktree at `.git/thrum-sync/a-sync/`,
checked out on the `a-sync` orphan branch. No branch switching needed — all git
operations happen within the worktree:

```text
┌─────────────────────────────────────────────────────────────┐
│          Sync Loop (60s) in .git/thrum-sync/a-sync/          │
├─────────────────────────────────────────────────────────────┤
│  1. Acquire lock (.thrum/var/sync.lock)                     │
│  2. Fetch remote in worktree                                 │
│  3. Merge JSONL (append-only dedup by event ID)             │
│  4. Project new events into SQLite                           │
│  5. Notify subscribers of new events                         │
│  6. Commit & push local changes in worktree                  │
│  7. Release lock                                             │
└─────────────────────────────────────────────────────────────┘
```

**Why Git?** Works offline (changes accumulate locally), leverages existing
authentication (SSH keys, HTTPS), provides a natural audit trail, and needs no
additional infrastructure.

### 3. Agent & Session Management

Agents register with a human-readable name, role, and module:

```bash
thrum agent register --name furiosa --role=implementer --module=auth
```

Agent names follow the pattern `[a-z0-9_]+`. Reserved names: `daemon`, `system`,
`thrum`, `all`, `broadcast`. Identity resolves in this order: `THRUM_NAME` env
var > `--name` flag > solo-agent auto-select.

Each agent gets an identity file at `.thrum/identities/{name}.json`. Multiple
agents can coexist in a single worktree.

Sessions track active work periods:

```bash
thrum session start   # Begin working
# ... do work ...
thrum session end     # Finish
```

Agents can be deleted and orphaned agents cleaned up:

```bash
thrum agent delete furiosa           # Delete a specific agent
thrum agent cleanup --dry-run        # Preview orphaned agents
thrum agent cleanup --force          # Delete all orphaned agents
```

### 4. Subscription-Based Notifications

Agents subscribe to relevant events:

```bash
# Subscribe to your module
thrum subscribe --scope module:auth

# Subscribe to @mentions
thrum subscribe --mention @reviewer
```

When matching messages arrive, subscribers receive real-time notifications:

```json
{
  "method": "notification.message",
  "params": {
    "message_id": "msg_01HXE...",
    "preview": "Auth implementation complete...",
    "matched_subscription": {
      "match_type": "scope"
    }
  }
}
```

### 5. Live Git State Tracking

The daemon tracks what each agent is working on in real-time:

```sql
-- agent_work_contexts table
session_id        | agent_id        | branch      | unmerged_commits | uncommitted_files
ses_01HXE...      | furiosa         | feature/auth| 3                | ["src/auth.go"]
ses_02HXF...      | maximus         | feature/db  | 1                | []
```

It tracks current branch, unmerged commits vs main, changed files, uncommitted
modifications, and agent-set task and intent. Agent2 can see "furiosa is working
on auth.go with 3 unmerged commits" — no manual investigation, no duplicate
work, intelligent handoffs.

### 6. Dual-Transport API (Single Port)

The daemon serves the WebSocket API and embedded Web UI SPA on the same port
(default 9999, configurable via `THRUM_WS_PORT`). The WebSocket endpoint is at
`/ws`; all other paths serve the React SPA.

| Transport       | Endpoint                 | Use Case                           |
| --------------- | ------------------------ | ---------------------------------- |
| **Unix Socket** | `.thrum/var/thrum.sock`  | CLI, MCP server, scripts           |
| **WebSocket**   | `ws://localhost:9999/ws` | Web UI, MCP waiter, real-time apps |
| **HTTP**        | `http://localhost:9999/` | Embedded React SPA (Web UI)        |

26 registered RPC methods on Unix socket (24 on WebSocket):

- `health` - Daemon status
- `agent.register`, `agent.list`, `agent.whoami`, `agent.listContext`,
  `agent.delete`, `agent.cleanup`
- `session.start`, `session.end`, `session.list`, `session.heartbeat`,
  `session.setIntent`, `session.setTask`
- `message.send`, `message.get`, `message.list`, `message.edit`,
  `message.delete`, `message.markRead`
- `subscribe`, `unsubscribe`, `subscriptions.list`
- `sync.force`, `sync.status`
- `user.register`, `user.identify` (user.register is WebSocket-only)

### 7. Message Lifecycle

Full message lifecycle management beyond send/receive:

```bash
thrum message get MSG_ID        # Retrieve a message with full details
thrum message edit MSG_ID TEXT   # Edit your own messages (full replacement)
thrum message delete MSG_ID     # Delete a message (requires --force)
```

Messages are automatically marked as read when viewed via `thrum inbox` or
`thrum message get`. Explicit mark-read is also available via the
`message.markRead` RPC method.

### 8. Coordination Commands

Lightweight commands for checking team activity:

```bash
thrum who-has auth.go           # Which agents are editing a file?
thrum ping @reviewer            # Is an agent online? Show last-seen time
```

These query agent work contexts to provide quick answers without full status
output.

### 9. Agent Context Management

Agents save and retrieve volatile project state that doesn't belong in git
commits but needs to survive session boundaries:

```bash
# Save context from a file or stdin
thrum context save --file continuation-notes.md
echo "Next steps: finish JWT implementation" | thrum context save

# View saved context
thrum context show

# Share context across worktrees (manual sync)
thrum context sync
```

Context files live at `.thrum/context/{agent-name}.md` and appear in
`thrum status` output. Use the `/update-context` skill in Claude Code for guided
context updates.

## Storage Architecture

Thrum uses event sourcing with CQRS:

```text
┌─────────────────────────────────────────────────────────────┐
│                    Event Sourcing + CQRS                     │
├─────────────────────────────────────────────────────────────┤
│                                                              │
│  ┌─────────────────────┐     ┌─────────────────────────┐    │
│  │   JSONL Event Logs  │     │   SQLite Projection     │    │
│  │   (Source of Truth) │────▶│   (Query Model)         │    │
│  │   in sync worktree  │     │   in .thrum/var/        │    │
│  └─────────────────────┘     └─────────────────────────┘    │
│        │                              │                      │
│        │ On a-sync branch             │ Gitignored           │
│        │ Append-only                  │ Rebuildable          │
│        │ Conflict-free merge          │ Fast queries         │
│        │                              │                      │
└────────┼──────────────────────────────┼──────────────────────┘
         │                              │
         ▼                              ▼
    Sync via worktree           Local CLI/UI queries
```

JSONL merges conflict-free (immutable events with unique IDs). SQLite provides
fast indexed queries. SQLite can be rebuilt from JSONL anytime. Offline-first:
works without network.

## What the Daemon Enables

### For the CLI

| Command                               | Daemon Feature Used                                           |
| ------------------------------------- | ------------------------------------------------------------- |
| `thrum send "Hello"`                  | `message.send` RPC + auto-sync                                |
| `thrum inbox`                         | `message.list` RPC with filtering                             |
| `thrum subscribe --scope module:auth` | `subscribe` RPC + push notifications                          |
| `thrum agent list --context`          | `agent.listContext` RPC (live git state)                      |
| `thrum who-has FILE`                  | `agent.listContext` RPC filtered by file                      |
| `thrum ping @role`                    | `agent.list` + `agent.listContext` RPCs                       |
| `thrum quickstart --name NAME`        | `agent.register` + `session.start` + `session.setIntent` RPCs |
| `thrum overview`                      | Multiple RPCs combined into one view                          |
| `thrum sync force`                    | `sync.force` RPC                                              |
| `thrum sync status`                   | `sync.status` RPC                                             |
| `thrum agent delete NAME`             | `agent.delete` RPC                                            |
| `thrum agent cleanup`                 | `agent.cleanup` RPC                                           |

### For the Web UI

| Feature                | Daemon Feature Used                |
| ---------------------- | ---------------------------------- |
| Real-time message feed | WebSocket + `notification.message` |
| Agent activity         | `agent.listContext` RPC            |
| Unread counts          | `message.list` with `unread: true` |
| Live updates           | WebSocket notifications            |

### For MCP Integration

`thrum mcp serve` runs an MCP server on stdio (JSON-RPC over stdin/stdout),
enabling LLM agents to communicate via native MCP tools. It provides 4 core
messaging tools (`send_message`, `check_messages`, `wait_for_message`,
`list_agents`) and 6 group management tools.

See [MCP Server](mcp-server.md) for the complete tools reference, configuration,
and setup instructions.

---

## Foundation Packages

The sections below describe the internal packages that implement the
architecture above.

### Package Structure

```text
internal/
├── config/      # Configuration loading, identity files, agent naming
├── identity/    # ID generation (repo, agent, session, message, event)
├── jsonl/       # JSONL reader/writer with file locking
├── projection/  # SQLite projection engine (multi-file rebuild)
├── schema/      # SQLite schema, migrations, JSONL sharding migration
├── paths/       # Path resolution, redirect, sync worktree path
├── gitctx/      # Git-derived work context extraction
└── types/       # Shared event types
```

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
```

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
```

### Loading

```go
// Load from current directory
cfg, err := config.Load(flagRole, flagModule)

// Load from specific repo path
cfg, err := config.LoadWithPath(repoPath, flagRole, flagModule)
```

## Identity (`internal/identity`)

### ID Formats

| Type                   | Format                            | Example                  |
| ---------------------- | --------------------------------- | ------------------------ |
| **Repo ID**            | `r_` + base32(sha256(url))\[:12\] | `r_7K2Q1X9M3P0B`         |
| **Agent ID (named)**   | name directly                     | `furiosa`                |
| **Agent ID (unnamed)** | role + `_` + base32(hash)\[:10\]  | `implementer_9F2K3M1Q8Z` |
| **User ID**            | `user:` + username                | `user:leon`              |
| **Session ID**         | `ses_` + ulid()                   | `ses_01HXF2A9Y1Q0P8...`  |
| **Session Token**      | `tok_` + ulid()                   | `tok_01HXF2A9Y1Q0P8...`  |
| **Message ID**         | `msg_` + ulid()                   | `msg_01HXF2A9Y1Q0P8...`  |
| **Event ID**           | `evt_` + ulid()                   | `evt_01HXF2A9Y1Q0P8...`  |

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
```

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
```

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
```

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
```

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
```

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
events              # Sequence-ordered, deduplicated event log (for sync)
sync_checkpoints    # Per-peer sync progress tracking
schema_version      # Migration tracking
```

### Schema Version

Current version: **13**

Key migrations:

- v3 -> v4: Impersonation support (`authored_by`, `disclosed` columns)
- v5 -> v6: Agent work contexts table, message reads, session scopes/refs
- v6 -> v7: Event ID backfill (ULID `event_id` on all JSONL events), JSONL
  sharding migration
- v7 -> v8: Groups feature (`groups` and `group_members` tables), `@everyone`
  built-in group
- v8 -> v9: `events` and `sync_checkpoints` tables added for Tailscale-style
  daemon-to-daemon sync (sequence-ordered deduplicated event log)
- v9 -> v10: `file_changes` column added to `agent_work_contexts`
- v10 -> v11: `hostname` column added to `agents` table
- v11 -> v12: `threads` table dropped (threads are now implicit — every message
  with a `thread_id` forms a thread automatically, removing the need for
  explicit thread creation events)
- v12 -> v13: Backfill NULL `display`, `hostname`, and `last_seen_at` values in
  `agents` table to empty strings (ensures NOT NULL invariants on existing rows)

### Initialization

```go
db, _ := schema.OpenDB("thrum.db")
schema.InitDB(db)  // Create tables and indexes

// Or use migration (checks version first, runs incremental migrations)
schema.Migrate(db)
```

### JSONL Migrations

The schema package also handles JSONL structure migrations:

```go
// Migrate monolithic messages.jsonl -> per-agent sharded files
schema.MigrateJSONLSharding(syncDir)

// Backfill event_id (ULID) for events that lack it
schema.BackfillEventID(syncDir)
```

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
```

### Multi-File Rebuild

`Rebuild(syncDir)` handles the sharded JSONL structure:

1. Read `events.jsonl` (agent lifecycle, sessions)
2. Glob `messages/*.jsonl` (per-agent message files)
3. Sort ALL events globally by `(timestamp, event_id)` for deterministic
   ordering
4. Apply to SQLite in order

File boundaries are transparent to the projector — it only cares about event
ordering.

### Event Types

| Event                 | Action                                               |
| --------------------- | ---------------------------------------------------- |
| `message.create`      | Insert into messages, scopes, refs                   |
| `message.edit`        | Update body_content, updated_at, record edit history |
| `message.delete`      | Set deleted=1, deleted_at, delete_reason             |
| `thread.updated`      | Notify subscribers of thread activity (UI push)      |
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

## Backup & Restore

Thrum provides built-in backup and restore via `thrum backup` /
`thrum backup restore`.

**What gets backed up:**

- **JSONL event logs** — `events.jsonl` and `messages/*.jsonl` copied from the
  sync worktree (source of truth)
- **Local-only SQLite tables** — `message_reads`, `subscriptions`, and
  `sync_checkpoints` exported as JSONL (these are not in the git-synced JSONL
  logs)
- **Config files** — `.thrum/config.json` and related runtime config

**Backup layout** (`~/.thrum-backups/<repo>/`):

- `current/` — most recent backup (JSONL + local tables + config)
- `archives/` — compressed `.zip` snapshots of previous `current/` runs
- GFS (Grandfather-Father-Son) rotation trims archives by daily/weekly/monthly
  retention windows

**Plugin hooks** — third-party data (e.g., Beads task DB) can register a backup
plugin via `thrum backup plugin add`. The plugin's command runs after the core
backup and receives `THRUM_BACKUP_DIR`, `THRUM_BACKUP_REPO`, and
`THRUM_BACKUP_CURRENT` env vars.

**Restore** creates a safety backup of existing data first, then copies JSONL
back to the sync worktree, imports local tables into SQLite, and removes
`messages.db` so the projector rebuilds from JSONL on the next daemon start.
Plugin restore commands run after the core restore.

## References

- Design document: `dev-docs/2026-02-03-thrum-design.md`
- Sharding design: `dev-docs/2026-02-06-jsonl-sharding-and-agent-naming.md`
- Daemon architecture: [Daemon Architecture](daemon.md)
- Sync protocol: [Sync Protocol](sync.md)

## Next Steps

- [Daemon Architecture](daemon.md) — deeper dive into the daemon's lifecycle,
  RPC handlers, sync loop, and WebSocket server internals
- [Sync Protocol](sync.md) — how the `a-sync` orphan branch, JSONL dedup, and
  conflict-free merging work in detail
- [RPC API Reference](rpc-api.md) — all 26 RPC methods that the CLI and Web UI
  call into
- [Development Guide](development.md) — how to build, test, and extend Thrum
