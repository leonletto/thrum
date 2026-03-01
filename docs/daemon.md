
## Thrum Daemon Architecture

> **See also:** [System Overview](overview.md) for how the daemon fits into the
> larger Thrum ecosystem.

## Overview

The Thrum daemon is a background service that manages the `.thrum/` directory
and the sync worktree (at `.git/thrum-sync/a-sync/` on the `a-sync` orphan
branch), handles client connections via Unix socket and WebSocket, serves the
embedded web UI, and coordinates message synchronization with Git. It serves as
the central coordinator for all Thrum clients (CLI, Web UI, and MCP server).

## Architecture

```go
┌─────────────┐  ┌──────────────┐  ┌──────────────┐
│   CLI       │  │   Web UI     │  │  MCP Server  │
│  (client)   │  │  (browser)   │  │ (thrum mcp)  │
└──────┬──────┘  └──────┬───────┘  └──────┬───────┘
       │ Unix socket     │ WebSocket       │ Unix socket
       │ JSON-RPC 2.0    │ JSON-RPC 2.0    │ JSON-RPC 2.0
       ▼                 ▼                 ▼
┌──────────────────────────────────────────────────┐
│                     Daemon                        │
├──────────────────────────────────────────────────┤
│ • Unix Socket Server    (.thrum/var/thrum.sock)   │
│ • WebSocket + SPA       (localhost:9999)           │
│ • Lifecycle (flock, JSON PID, defer cleanup)       │
│ • RPC Handlers          (agent, message, sync)     │
│ • Sync Loop             (60s interval)             │
│ • Stale Context Cleanup                            │
└──────────────────┬───────────────────────────────┘
                   │
                   ▼
  .git/thrum-sync/a-sync/    (worktree on a-sync branch)
  ├── events.jsonl           (agent lifecycle events)
  └── messages/              (per-agent message logs)
      └── {agent_name}.jsonl

  .thrum/
  ├── var/
  │   ├── thrum.sock         (Unix socket)
  │   ├── thrum.pid          (JSON PID file)
  │   ├── thrum.lock         (flock for SIGKILL resilience)
  │   ├── ws.port            (WebSocket port number)
  │   ├── sync.lock          (sync loop file lock)
  │   └── messages.db        (SQLite projection)
  ├── identities/            (per-worktree agent identity files)
  │   └── {agent_name}.json
  └── redirect               (feature worktrees only)
```

## Components

### 1. Unix Socket Server

**Location:** `internal/daemon/server.go`

The socket server implements JSON-RPC 2.0 protocol for client-daemon
communication.

**Key features:**

- Listens on `.thrum/var/thrum.sock`
- Accepts multiple concurrent connections
- Dispatches requests to registered handlers
- Handles JSON-RPC 2.0 protocol correctly
- Sets socket permissions to 0600 (owner-only)
- Detects and removes stale sockets from dead daemons

**JSON-RPC 2.0 format:**

```json
// Request
{
  "jsonrpc": "2.0",
  "method": "health",
  "params": {},
  "id": 1
}

// Response (success)
{
  "jsonrpc": "2.0",
  "result": {"status": "ok"},
  "id": 1
}

// Response (error)
{
  "jsonrpc": "2.0",
  "error": {
    "code": -32601,
    "message": "Method not found"
  },
  "id": 1
}
```

### 2. PID File Management

**Location:** `internal/daemon/pidfile.go`

Manages process ID file with JSON metadata for daemon lifecycle.

**JSON PID format:**

```json
{
  "pid": 12345,
  "repo_path": "/Users/leon/dev/myproject",
  "started_at": "2026-02-08T12:00:00Z",
  "socket_path": "/Users/leon/dev/myproject/.thrum/var/thrum.sock"
}
```

**Functions:**

- `WritePIDFileJSON(path, info)` - Write JSON PID with metadata
- `ReadPIDFileJSON(path)` - Read JSON PID (falls back to plain integer format
  for backward compatibility)
- `CheckPIDFileJSON(path)` - Check if process is running and return PID info
- `ValidatePIDRepo(info, repoPath)` - Verify PID belongs to the same repository
- `RemovePIDFile(path)` - Clean up PID file

**PID file location:** `.thrum/var/thrum.pid`

**Features:**

- JSON format with repository affinity (repo path, socket path, start time)
- Backward-compatible reader supports plain integer format
- Pre-startup duplicate detection prevents multiple daemons per repo
- Automatic cleanup on shutdown

### 3. File Locking (flock)

**Location:** `internal/daemon/flock.go`, `internal/daemon/flock_unix.go`,
`internal/daemon/flock_other.go`

Provides SIGKILL-resilient daemon process detection using OS-level file locking.

**How it works:**

- Uses `syscall.Flock()` with `LOCK_EX|LOCK_NB` for exclusive non-blocking lock
- Lock is held on `.thrum/var/thrum.lock` for the daemon's entire lifetime
- The OS automatically releases the lock when the process dies, even on SIGKILL
- Non-unix platforms have no-op stubs (lock detection falls back to PID file
  only)

**Key functions:**

- `AcquireLock(path)` - Try to acquire exclusive lock, returns error if held
- `FileLock.Release()` - Release lock and remove lock file (idempotent,
  nil-safe)
- `IsLocked(path)` - Check if lock is currently held (unix only)

### 4. Lifecycle Management

**Location:** `internal/daemon/lifecycle.go`

Manages daemon startup, signal handling, and graceful shutdown with
defense-in-depth cleanup.

**Startup sequence (`Lifecycle.Run()`):**

1. Acquire file lock (flock) for SIGKILL resilience
2. Pre-startup validation: check for existing daemon (repo affinity)
3. Write JSON PID file with metadata
4. Register defer safety net (catches panics, early returns)
5. Start Unix socket server
6. Start WebSocket server (if configured), write port file
7. Start signal handler goroutine
8. Wait for shutdown signal

**Signal handling:**

- `SIGTERM` - Graceful shutdown
- `SIGINT` - Graceful shutdown
- `SIGHUP` - Reserved for config reload (future)

**Shutdown sequence:**

1. Stop WebSocket server, remove port file
2. Stop Unix socket server (waits up to 5s for in-flight requests), remove
   socket
3. Remove PID file
4. Release file lock

**Defer cleanup safety net:**

- A `defer` block in `Run()` catches any exit path (panic, early return,
  unexpected error)
- Uses `atomic.Bool` to prevent double cleanup with the normal shutdown path
- All cleanup operations are idempotent (safe to run twice)

### 5. RPC Handlers

**Location:** `internal/daemon/rpc/`

RPC method handlers implement daemon functionality. All handlers are registered
on both the Unix socket and WebSocket servers unless noted.

**Registered handlers:**

| Category         | Methods                                                                                                     | Notes                                              |
| ---------------- | ----------------------------------------------------------------------------------------------------------- | -------------------------------------------------- |
| **Health**       | `health`                                                                                                    |                                                    |
| **Agent**        | `agent.register`, `agent.list`, `agent.whoami`, `agent.listContext`, `agent.delete`, `agent.cleanup`        | `delete` and `cleanup` are Unix socket only        |
| **Session**      | `session.start`, `session.end`, `session.list`, `session.heartbeat`, `session.setIntent`, `session.setTask` |                                                    |
| **Message**      | `message.send`, `message.get`, `message.list`, `message.edit`, `message.delete`, `message.markRead`         |                                                    |
| **Subscription** | `subscribe`, `unsubscribe`, `subscriptions.list`                                                            | Subscriptions auto-cleanup on session end (v0.4.3) |
| **Sync**         | `sync.force`, `sync.status`                                                                                 | Both Unix socket and WebSocket                     |
| **User**         | `user.register`, `user.identify`                                                                            | `user.register` restricted to WebSocket transport  |

See [RPC API Reference](rpc-api.md) for full documentation.

**Health check response:**

```json
{
  "status": "ok",
  "uptime_ms": 12345,
  "version": "1.0.0",
  "repo_id": "abc123",
  "sync_state": "synced"
}
```

### 6. WebSocket Server and Embedded SPA

**Location:** `internal/websocket/`, `internal/web/`

The WebSocket server provides real-time communication and serves the embedded
web UI on a single port.

**Key features:**

- Full JSON-RPC 2.0 support (same protocol as Unix socket)
- Real-time push notifications via subscriptions
- Client registry for tracking connections by session_id
- Default port: 9999 (configurable via `THRUM_WS_PORT`)
- Embedded React SPA served from the same port

**Route layout (with UI):**

- `/ws` - WebSocket upgrade handler
- `/assets/` - Static file server (with immutable cache headers)
- `/` - SPA fallback (serves `index.html` for all other paths)

**Route layout (without UI):**

- `/` - WebSocket handler (backward compatible)

**Embedded SPA (`internal/web/embed.go`):**

- Uses `//go:embed all:dist` to bundle the React UI into the Go binary
- Build pipeline: `pnpm build` -> copy to `internal/web/dist/` -> `go build`
- Dev mode: set `THRUM_UI_DEV=./ui/packages/web-app/dist` to serve from disk

**Browser auto-registration:**

- `user.identify` RPC extracts git config `user.name` and `user.email` for the
  repo
- `user.register` RPC (WebSocket-only) registers browser users with
  `kind="user"`
- Idempotent: returns existing user info if already registered

**Components:**

- `server.go` - HTTP server with mux routing, WebSocket upgrade, SPA handler
- `connection.go` - Per-connection read/write loops
- `handler.go` - Handler registry interface
- `client_registry.go` - Tracks connected clients by session_id

### 7. Sync Loop Integration

**Location:** `internal/sync/loop.go`, `internal/daemon/rpc/sync_rpc.go`

The sync loop runs as a goroutine within the daemon, periodically synchronizing
JSONL data via Git.

**Key features:**

- Default interval: 60 seconds (configurable via `--sync-interval` flag)
- Sync worktree at `.git/thrum-sync/a-sync/` (uses `git-common-dir` for nested
  worktree support)
- File lock (`sync.lock`) prevents concurrent sync operations
- Manual sync via `sync.force` RPC method

**Sync cycle:**

1. Acquire sync lock (`.thrum/var/sync.lock`)
2. Fetch remote (`git fetch` in sync worktree)
3. Merge all JSONL files (`events.jsonl` + `messages/*.jsonl`) with dedup
4. Apply new events to SQLite projection
5. Push merged changes back to remote
6. Release lock

**RPC methods:**

- `sync.force` - Trigger manual sync (non-blocking), returns current status
- `sync.status` - Return current sync state (`stopped`, `idle`, `synced`,
  `error`)

### 8. Agent Work Context (Live Git State)

**Location:** `internal/gitctx/`, `internal/daemon/cleanup/`

Tracks what each agent is working on in real-time by extracting Git state on
heartbeat.

**`internal/gitctx/` package:**

Extracts git-derived work context from a worktree path:

- `WorkContext` struct: branch, worktree path, unmerged commits, uncommitted
  files, changed files, extraction timestamp
- `CommitSummary` struct: SHA, message (first line), list of changed files
- `ExtractWorkContext(worktreePath)` - Runs git commands to gather state (~80ms)
  - `git branch --show-current` - Current branch
  - `git log --oneline base..HEAD` - Unmerged commits vs origin/main or
    origin/master
  - `git diff --name-only base...HEAD` - Changed files vs base branch
  - `git status --porcelain` - Uncommitted/staged files

**RPC methods:**

- `session.heartbeat` - Extracts and stores git context automatically
- `session.setIntent` - Agent sets work intent (free text)
- `session.setTask` - Agent sets current task description
- `agent.listContext` - Query all agent work contexts (filterable by agent_id)

**Cleanup logic (`internal/daemon/cleanup/`):**

- `CleanupStaleContexts(db, now)` - Removes stale work contexts from SQLite
  - Rule 1: Contexts > 24h old with no unmerged commits
  - Rule 2: Contexts from sessions ended > 7 days ago
  - Rule 3: Contexts where git data was never collected
- `FilterStaleContexts(contexts, now)` - In-memory filter (same rules, for sync)
- Runs at daemon startup and before sync

## Local-Only Mode

When your repository is public, the daemon's sync loop pushes the `a-sync`
branch to `origin`, which would expose private agent messages. Local-only mode
disables all remote git operations while keeping everything else working.

### Enable local-only mode

```bash
# Via CLI flag
thrum daemon start --local

# Via environment variable
THRUM_LOCAL=1 thrum daemon start
```

The setting persists in `.thrum/config.json`:

```json
{ "local_only": true }
```

**Priority order:** CLI flag > environment variable > config file > default
(`true` via `thrum init`).

### What changes in local-only mode

| Component                 | Normal mode | Local-only mode |
| ------------------------- | ----------- | --------------- |
| Messaging                 | Works       | Works           |
| Sessions                  | Works       | Works           |
| SQLite projection         | Works       | Works           |
| WebSocket / MCP           | Works       | Works           |
| `git push origin a-sync`  | Every 60s   | **Skipped**     |
| `git fetch origin a-sync` | Every 60s   | **Skipped**     |
| Remote branch setup       | Automatic   | **Skipped**     |

### Check if local-only mode is active

```bash
thrum sync status
# Shows "Mode: local-only" or "Mode: normal"

thrum sync force
# Shows "local-only (remote sync disabled)" when active
```

### 9. State Management

**Location:** `internal/daemon/state/`

The `State` struct manages the daemon's persistent state: JSONL event logs and
SQLite projection.

**Constructor:** `NewState(thrumDir, syncDir, repoID)` splits paths:

- `thrumDir` (`/path/to/.thrum/`) - Runtime files: `var/messages.db`,
  `var/thrum.sock`, etc.
- `syncDir` (`/path/to/.git/thrum-sync/a-sync/`) - JSONL data: `events.jsonl`,
  `messages/*.jsonl`

**Event routing (per-agent JSONL sharding):**

- `message.*` events -> `messages/{agent_name}.jsonl` (per-agent file, keyed by
  message author)
- All other events -> `events.jsonl` (agent lifecycle, threads, sessions)
- `WriteEvent()` auto-generates `event_id` (ULID) and `v` (version) fields

**On initialization:**

1. Opens/migrates SQLite database
2. Runs JSONL sharding migration (monolithic `messages.jsonl` -> per-agent
   files) if needed
3. Backfills `event_id` for events that lack it
4. Creates writers for `events.jsonl` and lazy-creates per-agent message writers

**Exported methods:**

- `WriteEvent(event)` - Write to JSONL + apply to SQLite projection
- `DB()` - Returns SQLite connection for queries
- `RepoID()`, `RepoPath()`, `SyncDir()` - Path accessors
- `Lock()/Unlock()`, `RLock()/RUnlock()` - Read/write mutex for agent/session
  operations
- `Projector()` - Returns the projection engine
- `Close()` - Closes all JSONL writers and SQLite connection

### 10. Timeout Enforcement (v0.4.3)

All I/O paths enforce timeouts to prevent indefinite hangs:

| Path                | Timeout        | Implementation      |
| ------------------- | -------------- | ------------------- |
| CLI dial            | 5s             | net.DialTimeout     |
| RPC call            | 10s            | context.WithTimeout |
| Server per-request  | 10s            | http.TimeoutHandler |
| WebSocket handshake | 10s            | websocket.Upgrader  |
| Git commands        | 5s/10s         | safecmd wrapper     |
| SQLite queries      | context-scoped | safedb wrapper      |

The `safedb` and `safecmd` packages wrap all database and command operations
with context-aware timeouts. All DB operations go through `safedb` wrappers and
all command executions go through `safecmd` wrappers for context-aware timeout
enforcement.

Lock scope has been reduced in v0.4.3 — no mutex is held during I/O, git, or
WebSocket dispatch operations.

### 11. Client Library

**Location:** `internal/daemon/client.go`

Client library for connecting to the daemon.

**Key functions:**

- `NewClient(socketPath)` - Connect to daemon
- `Call(method, params)` - Make RPC call
- `EnsureDaemon(repoPath)` - Auto-start daemon if needed

**Auto-start logic:**

1. Try to connect to existing daemon
2. If not running, start daemon in background
3. Wait for socket to become available (10s timeout)
4. Return connected client

## Daemon Lifecycle

For setup instructions, see [Quickstart Guide](quickstart.md).

### Daemon States

```text
 NOT RUNNING
     │
     ▼
 STARTING ────────┐
     │            │ (error)
     ▼            ▼
 RUNNING ──────▶ ERROR
     │
     ▼
 SHUTTING DOWN
     │
     ▼
 STOPPED
```

### Checking Status

```bash
# Check if daemon is running (shows repo path from JSON PID)
thrum daemon status
```

### Stopping the Daemon

```bash
# Graceful stop
thrum daemon stop

# Force stop (if graceful fails)
kill <pid>
# flock auto-released by OS; PID file cleaned up on next start
```

## Directory Structure

```go
.git/thrum-sync/a-sync/          # Sync worktree on a-sync orphan branch
├── events.jsonl                # Agent lifecycle events (source of truth)
└── messages/                   # Per-agent message logs (source of truth)
    └── {agent_name}.jsonl

.thrum/                         # Gitignored entirely
├── var/                        # Runtime files
│   ├── thrum.sock              # Unix socket for CLI/RPC
│   ├── thrum.pid               # JSON PID file (PID, RepoPath, StartedAt, SocketPath)
│   ├── thrum.lock              # flock file for SIGKILL resilience
│   ├── ws.port                 # WebSocket port number
│   ├── sync.lock               # Sync loop file lock
│   └── messages.db             # SQLite projection (query cache)
├── identities/                 # Per-worktree agent identity files
│   └── {agent_name}.json
└── redirect                    # (feature worktrees only) points to main .thrum/
```

**Key files:**

- `.git/thrum-sync/a-sync/events.jsonl` and `messages/*.jsonl`: Append-only
  JSONL event logs on the `a-sync` branch, synced via the worktree. Per-agent
  sharding means each agent's messages go to a separate JSONL file.
- `var/messages.db`: SQLite database rebuilt from JSONL, stores all queryable
  state including `agent_work_contexts` table
- `var/thrum.pid`: JSON format with PID, repo path, start time, and socket path.
  Enables repo-affinity checks to prevent conflicts.
- `var/thrum.lock`: Held via `flock()` for the daemon's lifetime. OS releases on
  process death (even SIGKILL).
- `var/ws.port`: Contains the WebSocket port for UI clients to discover
- `redirect`: Present only in feature worktrees; points to the main worktree's
  `.thrum/` so all worktrees share one daemon

## Development

### Running Tests

```bash
# All daemon tests
go test ./internal/daemon/...

# With coverage
go test -cover ./internal/daemon/...

# With race detector
go test -race ./internal/daemon/...
```

### Adding New RPC Methods

1. Create handler in `internal/daemon/rpc/`
2. Implement `Handle(ctx, params)` method
3. Register in daemon startup (`cmd/thrum/main.go`)
4. Add tests in corresponding `_test.go` file

Example:

```go
// internal/daemon/rpc/mymethod.go
type MyMethodHandler struct {
    // dependencies
}

func (h *MyMethodHandler) Handle(ctx context.Context, params json.RawMessage) (any, error) {
    // implementation
    return response, nil
}

// Register in daemon (cmd/thrum/main.go)
server.RegisterHandler("mymethod", myMethodHandler.Handle)
```

## Troubleshooting

### Daemon won't start

Check:

1. Is `.thrum/var/` directory writable?
2. Is socket path too long? (Unix sockets have 104-char limit)
3. Is another daemon already running?

```bash
# Check JSON PID file
cat .thrum/var/thrum.pid

# Check if flock is held
# (if daemon died via SIGKILL, flock is released but PID file may remain)

# Check if process is running
ps aux | grep thrum

# Remove stale PID file (only if process is definitely not running)
rm .thrum/var/thrum.pid
```

### Socket connection errors

Check:

1. Is daemon running?
2. Socket permissions correct (should be 0600)
3. Socket file exists?

```bash
# Check socket
ls -l .thrum/var/thrum.sock

# Test connection
echo '{"jsonrpc":"2.0","method":"health","id":1}' | nc -U .thrum/var/thrum.sock
```

### Graceful shutdown hangs

The daemon waits up to 5 seconds for in-flight requests to complete. If shutdown
hangs longer:

```bash
# Send SIGKILL (flock auto-released by OS)
kill -9 <pid>

# Stale files are cleaned up automatically on next daemon start
# Manual cleanup if needed:
rm .thrum/var/thrum.sock
rm .thrum/var/thrum.pid
```

## Implemented Features

| Epic            | Feature                                                     | Status   |
| --------------- | ----------------------------------------------------------- | -------- |
| Epic 2          | Daemon core (Unix socket, JSON-RPC)                         | Complete |
| Epic 3          | Agent & session management                                  | Complete |
| Epic 4          | Message send/receive                                        | Complete |
| Epic 5          | Git sync protocol                                           | Complete |
| Epic 6          | Subscription & notifications                                | Complete |
| Epic 8          | WebSocket server                                            | Complete |
| Epic F          | Embedded SPA (single port)                                  | Complete |
| Epic 21         | Agent Work Context (live git state)                         | Complete |
| DLH             | Daemon Lifecycle Hardening (flock, JSON PID, defer cleanup) | Complete |
| MCP             | MCP Server (`thrum mcp serve`)                              | Complete |
| JSONL Sharding  | Per-agent JSONL files, event_id, naming, cleanup            | Complete |
| Sync WT         | Sync worktree at `.git/thrum-sync/a-sync/`                  | Complete |
| Agent Naming    | Human-readable agent names (`--name`, `THRUM_NAME`)         | Complete |
| Agent Cleanup   | `agent.delete`, `agent.cleanup` (orphan detection)          | Complete |
| Browser Auth    | Browser auto-registration via git config                    | Complete |
| Local-Only Mode | Disable remote sync for public repos                        | Complete |

## References

- Design document: `dev-docs/2026-02-03-thrum-design.md`
- RPC API reference: `docs/rpc-api.md`
- Development guide: `docs/development.md`
```
