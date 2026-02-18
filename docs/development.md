
# Thrum Development Guide

This guide explains how to set up and work with the Thrum codebase.

## Prerequisites

- **Go 1.25+**: [Install Go](https://go.dev/dl/)
- **Node.js 18+** and **pnpm**: Required for building the UI monorepo
- **Make**: Build automation
- **Playwright**: E2E testing (`npx playwright install chromium`)
- **golangci-lint**: Code linting (auto-installed by `make lint`)
- **markdownlint-cli**: Markdown linting (auto-installed by `make lint-md`)

## Quick Start

```bash
# Clone repository
git clone <repo-url>
cd thrum

# Install dependencies
go mod download
cd ui && pnpm install && cd ..

# Run Go tests
make test

# Full build (UI + Go binary) and install to ~/.local/bin
make install

# Start daemon and verify
thrum daemon start
thrum daemon status
```

## Project Structure

```
thrum/
├── cmd/
│   └── thrum/               # CLI entry point
│       ├── main.go          # Cobra command tree (all CLI commands)
│       └── mcp.go           # MCP server command
├── internal/                # Private packages
│   ├── cli/                 # CLI business logic (one file per command)
│   ├── config/              # Configuration loading and identity files
│   ├── daemon/              # Daemon core
│   │   ├── cleanup/         # Agent work context cleanup
│   │   ├── rpc/             # JSON-RPC 2.0 method handlers
│   │   ├── state/           # Persistent state (JSONL + SQLite)
│   │   ├── server.go        # Unix socket server
│   │   ├── lifecycle.go     # Signal handling, defer cleanup, flock
│   │   ├── pidfile.go       # JSON PID file with repo-affinity metadata
│   │   ├── client.go        # Client library
│   │   ├── flock.go         # FileLock struct definition
│   │   ├── flock_unix.go    # flock() implementation (Unix)
│   │   ├── flock_other.go   # No-op stubs (non-Unix)
│   │   └── testutil_test.go # StartTestDaemon() helper
│   ├── gitctx/              # Git-derived work context (branch, uncommitted files)
│   ├── identity/            # ID generation (ULID-based: repo, agent, session, message, event)
│   ├── jsonl/               # JSONL reader/writer with file locking
│   ├── mcp/                 # MCP stdio server (5 tools, WebSocket waiter)
│   ├── paths/               # Path resolution, .thrum/redirect, sync worktree path
│   ├── projection/          # JSONL to SQLite event replay (projector)
│   ├── schema/              # SQLite schema, DDL, and migrations (v7)
│   ├── subscriptions/       # Notification dispatcher and subscription service
│   ├── sync/                # Sync engine (loop, merge, push, dedup, branch management)
│   ├── transport/           # Transport abstraction layer
│   ├── types/               # Shared event type definitions
│   ├── web/                 # Embedded SPA (//go:embed React build)
│   └── websocket/           # WebSocket server, connections, registry
├── ui/                      # UI monorepo (Turborepo + pnpm workspaces)
│   ├── packages/
│   │   ├── shared-logic/    # Framework-agnostic business logic (TanStack, Zod)
│   │   ├── web-app/         # React web application (Vite, shadcn/ui)
│   │   └── tui-app/         # Terminal UI (Ink, placeholder)
│   ├── turbo.json           # Turborepo configuration
│   ├── pnpm-workspace.yaml  # pnpm workspace configuration
│   └── package.json         # Root monorepo scripts
├── tests/
│   └── e2e/                 # Playwright E2E tests (13 spec files)
│       ├── helpers/         # Test helpers (CLI wrapper, fixtures)
│       ├── global-setup.ts  # Daemon start + agent registration
│       └── global-teardown.ts
├── scripts/                 # Setup scripts
│   ├── setup-worktree-thrum.sh  # Configure .thrum/redirect for worktrees
│   └── setup-worktree-beads.sh  # Configure .beads/redirect for worktrees
├── docs/                    # User documentation
├── dev-docs/                # Design documents and prompts
├── .agents/                 # Agent workflow instructions (hidden directory)
├── .beads/                  # Issue tracking (beads)
├── Makefile                 # Build targets
├── go.mod                   # Go module (github.com/leonletto/thrum)
├── playwright.config.ts     # Playwright E2E configuration
└── llms.txt / llms-full.txt # Agent reference files
```

## Development Workflow

### 1. Running Tests

#### Go Tests

```bash
# Run all Go tests
make test

# Run unit tests only (fast, skips integration)
make test-unit

# Run integration tests
make test-integration

# Run tests with verbose output
make test-verbose

# Run specific package tests
go test ./internal/config/... -v

# Run with race detector
go test -race ./...

# Run resilience tests (requires build tag)
go test -tags=resilience ./internal/daemon/...
```

**Resilience Test Suite (v0.4.3):**

The resilience test suite includes 39 tests covering crash recovery, concurrent access, and timeout enforcement. These tests require the `-tags=resilience` build flag:

- Crash recovery scenarios (daemon restart, state restoration)
- Concurrent access patterns (multiple goroutines, race conditions)
- Timeout enforcement (I/O timeouts, RPC timeouts, WebSocket timeouts)

Run the full resilience suite:

```bash
go test -tags=resilience -v ./internal/daemon/resilience_test.go
```

#### UI Tests

```bash
# Run all UI tests (from monorepo root)
cd ui && pnpm test

# Run web-app tests only
cd ui/packages/web-app && pnpm test

# Run shared-logic tests only
cd ui/packages/shared-logic && pnpm test

# Watch mode
cd ui/packages/web-app && pnpm test:watch

# Coverage report
cd ui/packages/web-app && pnpm test:coverage
```

#### E2E Tests (Playwright)

E2E tests require the daemon to be running and the binary to be built:

```bash
# Build and install
make install

# Start daemon
thrum daemon start

# Run all E2E tests (serial execution required)
npx playwright test --workers=1

# Run a specific spec file
npx playwright test tests/e2e/messaging.spec.ts --workers=1

# View HTML report
npx playwright show-report
```

The E2E test suite uses `global-setup.ts` to start the daemon and register a
test agent, and `global-teardown.ts` to stop the daemon after all tests
complete.

### 2. Code Coverage

```bash
# Generate Go coverage report
make test-coverage
# Report output: output/coverage.html
```

### 3. Linting

```bash
# Run Go linter (auto-installs golangci-lint if missing)
make lint

# Auto-fix Go lint issues
make lint-fix

# Run Markdown linter
make lint-md

# Auto-fix Markdown issues
make lint-md-fix

# Run all linters (Go + Markdown)
make lint-all
```

### 4. Formatting

```bash
# Format Go code
make fmt

# Format Markdown files (requires prettier)
make fmt-md

# Format all files (Go + Markdown)
make fmt-all
```

### 5. Building

```bash
# Full build: UI + Go binary
make build
# Output: ./bin/thrum

# Build Go binary only (skip UI rebuild, uses existing internal/web/dist/)
make build-go

# Build UI only (pnpm install + build, copies to internal/web/dist/)
make build-ui

# Full build + install to ~/.local/bin
make install

# Run built binary
./bin/thrum
```

The build embeds the React SPA into the Go binary via `//go:embed` in
`internal/web/embed.go`. The `make build-ui` step copies the Vite build output
into `internal/web/dist/` so the Go embed directive can include it.

A `.gitkeep` file in `internal/web/dist/` ensures `go build` and `go vet` work
even when the UI has not been built.

## Makefile Targets

| Target                  | Description                                            |
| ----------------------- | ------------------------------------------------------ |
| `make help`             | Show all available targets (default)                   |
| `make build`            | Full build: UI + Go binary                             |
| `make build-ui`         | Build UI and copy to embed location                    |
| `make build-go`         | Build Go binary only (skip UI rebuild)                 |
| `make install`          | Full build and install to `~/.local/bin`               |
| `make test`             | Run all Go tests                                       |
| `make test-unit`        | Run unit tests only (fast)                             |
| `make test-integration` | Run integration tests                                  |
| `make test-coverage`    | Generate coverage report to `output/`                  |
| `make test-verbose`     | Run tests with verbose output                          |
| `make fmt`              | Format Go code                                         |
| `make fmt-md`           | Format Markdown files with prettier                    |
| `make fmt-all`          | Format all files (Go + Markdown)                       |
| `make lint`             | Run golangci-lint                                      |
| `make lint-fix`         | Run golangci-lint with auto-fix                        |
| `make lint-md`          | Run markdownlint                                       |
| `make lint-md-fix`      | Run markdownlint with auto-fix                         |
| `make lint-all`         | Run all linters (Go + Markdown)                        |
| `make vet`              | Run `go vet`                                           |
| `make tidy`             | Tidy Go dependencies                                   |
| `make clean`            | Remove build artifacts (`output/`, `bin/`, `dist/`)    |
| `make install-tools`    | Install dev tools (golangci-lint, markdownlint-cli)    |
| `make quick-check`      | Fast pre-commit checks: format, vet, test, build       |
| `make ci`               | Full CI checks: format-all, lint-all, vet, test, build |
| `make pre-commit`       | Alias for `quick-check`                                |
| `make pre-push`         | Alias for `ci`                                         |

## Common Tasks

### Adding a New Event Type

1. Define event struct in `internal/types/events.go`
2. Add handler in `internal/projection/projector.go`
3. Add case in the `Apply()` switch statement
4. Write tests in `internal/projection/projector_test.go`

Current event types handled by the projector:

- `message.create`, `message.edit`, `message.delete`
- `agent.register`
- `agent.session.start`, `agent.session.end`
- `agent.update`

Example:

```go
// 1. Define event type in internal/types/events.go
type MyNewEvent struct {
    BaseEvent
    MyField string `json:"my_field"`
}

// 2. Add handler in internal/projection/projector.go
func (p *Projector) applyMyNew(data json.RawMessage) error {
    var event types.MyNewEvent
    if err := json.Unmarshal(data, &event); err != nil {
        return fmt.Errorf("unmarshal my.new: %w", err)
    }

    // Insert/update database
    _, err := p.db.Exec(`...`)
    return err
}

// 3. Update switch in Apply()
case "my.new":
    return p.applyMyNew(event)
```

### Modifying Database Schema

1. Update table definitions in `internal/schema/schema.go`
2. Increment `CurrentVersion` constant (currently v7)
3. Add migration logic in the `Migrate()` function
4. Write tests for the new schema
5. Update `docs/architecture.md`

### Testing with Temporary Databases

```go
func TestMyFeature(t *testing.T) {
    // Create temp database
    tmpDir := t.TempDir()
    dbPath := filepath.Join(tmpDir, "test.db")

    db, _ := schema.OpenDB(dbPath)
    defer db.Close()

    schema.InitDB(db)

    // Test your feature
    // ...
}
```

### Adding a New RPC Method

1. Create handler file in `internal/daemon/rpc/`:

```go
// internal/daemon/rpc/mymethod.go
package rpc

import (
    "context"
    "encoding/json"
)

type MyMethodHandler struct {
    // dependencies
}

func NewMyMethodHandler(deps...) *MyMethodHandler {
    return &MyMethodHandler{...}
}

func (h *MyMethodHandler) Handle(ctx context.Context, params json.RawMessage) (any, error) {
    // Parse params
    var args MyMethodArgs
    if err := json.Unmarshal(params, &args); err != nil {
        return nil, fmt.Errorf("invalid params: %w", err)
    }

    // Implementation
    result := MyMethodResponse{
        // ...
    }

    return result, nil
}
```

2. Add tests in `internal/daemon/rpc/mymethod_test.go`

3. Register in daemon startup (in `cmd/thrum/main.go`):

```go
myMethodHandler := rpc.NewMyMethodHandler()
server.RegisterHandler("mymethod", myMethodHandler.Handle)
```

4. Update documentation in `docs/rpc-api.md`

## Environment Variables

Configuration is resolved in priority order:

1. `THRUM_NAME` env var to select which identity file (highest priority)
2. Environment variables: `THRUM_ROLE`, `THRUM_MODULE`, `THRUM_DISPLAY`
3. CLI flags (`--role`, `--module`, `--name`)
4. Identity file in `.thrum/identities/{name}.json`
5. Error if required fields are missing

```bash
# Select a named agent identity
export THRUM_NAME=furiosa

# Or set agent properties directly
export THRUM_ROLE=implementer
export THRUM_MODULE=auth
export THRUM_DISPLAY="Auth Agent"
```

Identity files are stored per-agent at `.thrum/identities/{name}.json` and
contain repo ID, agent config, worktree name, and metadata.

## Storage Layout

Thrum uses a split storage model:

```
.git/thrum-sync/a-sync/              # Sync worktree (a-sync orphan branch)
├── events.jsonl                     # Agent lifecycle events (register, session, update)
└── messages/                        # Per-agent message files (sharded)
    ├── furiosa.jsonl                # Messages authored by agent "furiosa"
    └── coordinator.jsonl            # Messages authored by agent "coordinator"

.thrum/                              # Runtime directory (gitignored)
├── var/
│   ├── messages.db                  # SQLite projection cache (rebuilt from JSONL)
│   ├── thrum.sock                   # Unix socket for daemon RPC
│   ├── thrum.pid                    # JSON PID file with repo-affinity metadata
│   └── ws.port                      # WebSocket port file (default 9999)
├── identities/                      # Per-agent identity files
│   └── {name}.json                  # Agent identity (repo_id, role, module, etc.)
└── redirect                         # Points to main worktree .thrum/ (feature worktrees only)
```

### Inspecting JSONL Files

```bash
# View all events (agent lifecycle)
cat .git/thrum-sync/a-sync/events.jsonl | jq .

# View messages for a specific agent
cat .git/thrum-sync/a-sync/messages/furiosa.jsonl | jq .

# Filter by event type
cat .git/thrum-sync/a-sync/events.jsonl | jq 'select(.type == "agent.register")'

# Count events
wc -l .git/thrum-sync/a-sync/events.jsonl
```

### Inspecting the SQLite Database

```bash
# Open database
sqlite3 .thrum/var/messages.db

# List tables
.tables

# Query messages
SELECT * FROM messages LIMIT 10;

# Check schema version
SELECT * FROM schema_version;
```

## Daemon Development

### Daemon Architecture

The daemon runs as a background service handling client connections via Unix
socket, with a WebSocket server and embedded SPA all on a single port (default
9999).

**Key components:**

- **Server** (`internal/daemon/server.go`): JSON-RPC 2.0 over Unix socket
- **Lifecycle** (`internal/daemon/lifecycle.go`): Signal handling, defer cleanup
  safety net, flock-based process detection
- **PID file** (`internal/daemon/pidfile.go`): JSON format with `PIDInfo` struct
  (PID, repo path, socket path, started at). Backward-compatible reader falls
  back to plain integer format.
- **File lock** (`internal/daemon/flock.go`, `flock_unix.go`): OS-level
  `flock()` on socket file. Auto-released on process death (even SIGKILL). No-op
  stubs for non-Unix platforms.
- **State** (`internal/daemon/state/`): Manages JSONL writes (sharded per-agent)
  and SQLite projection. `NewState(thrumDir, syncDir, repoID)` separates runtime
  state from sync data.
- **RPC handlers** (`internal/daemon/rpc/`): Method implementations for agent,
  session, message, thread, health, sync, subscribe, and user operations
- **Client** (`internal/daemon/client.go`): Connection library for CLI-to-daemon
  communication
- **WebSocket** (`internal/websocket/`): Server, connection registry, event
  streaming
- **Web** (`internal/web/embed.go`): Embedded SPA served at `/` on the same port
  as WebSocket (`/ws`)

See `docs/daemon.md` for detailed architecture.

### Running the Daemon

```bash
# Start daemon (background, auto-creates sync worktree)
thrum daemon start

# Start in foreground (for debugging)
thrum daemon start --foreground

# Check status (shows PID, repo path, WebSocket port)
thrum daemon status

# Stop daemon
thrum daemon stop

# Auto-start (happens automatically via any CLI command)
thrum send "Hello" --to @coordinator
```

### Testing Daemon Code

```bash
# Run daemon tests
go test ./internal/daemon/...

# With coverage
go test -cover ./internal/daemon/...

# RPC handler tests
go test ./internal/daemon/rpc/... -v

# State tests
go test ./internal/daemon/state/... -v
```

Use the `StartTestDaemon()` helper in `internal/daemon/testutil_test.go` for
integration tests. It provides automatic `t.Cleanup()` with force-kill to
prevent test orphan processes on timeout or panic.

### Debugging Daemon

**Check if daemon is running:**

```bash
# Check PID file (JSON format)
cat .thrum/var/thrum.pid | jq .

# Check process
ps aux | grep thrum

# Check socket
ls -l .thrum/var/thrum.sock
```

**Test RPC calls manually:**

```bash
# Using netcat
echo '{"jsonrpc":"2.0","method":"health","id":1}' | nc -U .thrum/var/thrum.sock
```

**View daemon logs:**

```bash
# Run daemon in foreground for debugging
thrum daemon start --foreground
# Logs appear in stdout/stderr
```

**Clean restart:**

```bash
# Stop daemon
thrum daemon stop

# Remove stale files if needed
rm .thrum/var/thrum.sock
rm .thrum/var/thrum.pid

# Restart
thrum daemon start
```

### Common Daemon Issues

**Socket path too long:**

- Unix sockets limited to ~104 characters
- Use shorter temp directory paths in tests
- Example: `filepath.Join(tmpDir, "d.sock")` not
  `filepath.Join(tmpDir, ".thrum", "var", "thrum.sock")`

**Permission denied:**

- Socket should be 0600 (owner only)
- Check `.thrum/var/` directory permissions

**Bind: address already in use:**

- Another daemon already running
- Pre-startup duplicate detection validates no existing daemon serves this repo
- Check PID file and kill process
- Remove stale socket file

**Connection refused:**

- Daemon not running
- Check PID file exists
- Verify socket file exists

## MCP Server Development

The MCP server (`thrum mcp serve`) provides native MCP tools for Claude Code
agents instead of shelling out to the CLI. It uses stdio transport (JSON-RPC
over stdin/stdout).

**Key files:**

- `internal/mcp/server.go`: Server skeleton and tool registration
- `internal/mcp/tools.go`: Tool handler implementations
- `internal/mcp/types.go`: Request/response type definitions
- `internal/mcp/waiter.go`: WebSocket-based blocking message waiter
- `cmd/thrum/mcp.go`: `thrum mcp serve` Cobra command

**MCP Tools (5):**

| Tool                | Description                                               |
| ------------------- | --------------------------------------------------------- |
| `send_message`      | Send a message to another agent via @role addressing      |
| `check_messages`    | Poll for unread messages mentioning this agent            |
| `wait_for_message`  | Block until a message arrives (WebSocket push) or timeout |
| `list_agents`       | List registered agents with active/offline status         |
| `broadcast_message` | **Deprecated.** Use `send_message` with `to=@everyone` instead |

**Architecture:**

- Per-call `cli.Client` creation (thread-safe; Unix socket connections are
  cheap)
- WebSocket waiter with atomic incrementing JSON-RPC IDs
- Identity resolved at startup from `.thrum/identities/{name}.json`
- `THRUM_NAME` env var or `--agent-id` flag for multi-agent worktrees

```bash
# Start MCP server
thrum mcp serve

# Override agent identity
thrum mcp serve --agent-id furiosa
```

## Sync Engine

The sync engine runs in the daemon, performing fetch/merge/push every 60 seconds
(configurable via `--sync-interval`).

**Key files:**

- `internal/sync/loop.go`: `SyncLoop` with periodic and manual sync triggers
- `internal/sync/merge.go`: JSONL merge with deduplication (ULID event_id)
- `internal/sync/push.go`: Git push to remote
- `internal/sync/branch.go`: Safe orphan branch creation via `git commit-tree` +
  `git update-ref`, sync worktree with sparse checkout, 4-level health checks
- `internal/sync/dedup.go`: Event deduplication by event_id

**Sync worktree location:** `.git/thrum-sync/a-sync/` (uses `git-common-dir` for
nested worktree support).

**Sparse checkout patterns:** `/events.jsonl`, `/messages/`, `/messages.jsonl`
(migration compat).

## Worktree Setup

Thrum supports multiple git worktrees sharing a single daemon and data store via
the `.thrum/redirect` mechanism. Feature worktrees point to the main worktree's
`.thrum/` directory so all worktrees share one daemon, one SQLite database, and
one set of JSONL files.

### Setting Up a Worktree

```bash
# Option 1: Use the thrum setup command
thrum setup /path/to/worktree

# Option 2: Use the setup script
./scripts/setup-worktree-thrum.sh /path/to/worktree

# Option 3: Manual setup
mkdir -p /path/to/worktree/.thrum/identities
echo "/path/to/main/repo/.thrum" > /path/to/worktree/.thrum/redirect
```

### Beads Issue Tracking for Worktrees

All worktrees should share the same beads issue database:

```bash
# Use the setup script
./scripts/setup-worktree-beads.sh /path/to/worktree

# Or manual setup
mkdir -p /path/to/worktree/.beads
echo "/path/to/main/repo/.beads" > /path/to/worktree/.beads/redirect

# Verify
cd /path/to/worktree && bd where
```

## Testing Best Practices

### 1. Use Table-Driven Tests

```go
tests := []struct {
    name string
    input string
    want string
}{
    {"case 1", "input1", "expected1"},
    {"case 2", "input2", "expected2"},
}

for _, tt := range tests {
    t.Run(tt.name, func(t *testing.T) {
        got := MyFunction(tt.input)
        if got != tt.want {
            t.Errorf("got %v, want %v", got, tt.want)
        }
    })
}
```

### 2. Clean Up Resources

```go
// Use t.TempDir() for automatic cleanup
tmpDir := t.TempDir()

// Or defer cleanup
db, _ := schema.OpenDB(dbPath)
defer db.Close()
```

### 3. Test Error Cases

```go
// Test both happy path and error cases
_, err := MyFunction(invalidInput)
if err == nil {
    t.Error("expected error, got nil")
}
```

### 4. Use StartTestDaemon for Integration Tests

```go
// Automatically cleans up on test completion (even on panic/timeout)
daemon := StartTestDaemon(t, tmpDir)
defer daemon.Stop()
```

## Code Style

- **Formatting**: Use `go fmt` (or `gofmt -s`)
- **Imports**: Group stdlib, external, internal
- **Comments**: Document exported functions and types
- **Error messages**: Lowercase, no punctuation, wrap with `fmt.Errorf`
- **Variable names**: Short, descriptive (e.g., `db`, `cfg`, `msg`)

## Git Workflow

```bash
# Create feature branch
git checkout -b feature/my-feature

# Make changes and test
go test ./...

# Commit
git add .
git commit -m "Add my feature"

# Push
git push origin feature/my-feature

# Create PR
gh pr create
```

## Troubleshooting

### "no such table" error

The SQLite projection database is a rebuild-able cache. Delete it and restart
the daemon to rebuild from JSONL:

```bash
rm .thrum/var/messages.db
thrum daemon stop
thrum daemon start
```

### "cannot open file" error

Check file permissions and directory existence:

```bash
ls -la .thrum/
ls -la .thrum/var/
```

### Tests fail with "database is locked"

Close any open SQLite connections or delete WAL files:

```bash
rm .thrum/var/*.db-wal
rm .thrum/var/*.db-shm
```

### Daemon won't start (duplicate detection)

The daemon validates no existing instance serves the same repository before
starting. Check for a stale PID file:

```bash
cat .thrum/var/thrum.pid | jq .
# If the process is dead, remove the PID file
rm .thrum/var/thrum.pid
thrum daemon start
```

### `go build` fails with embed error

If the UI has not been built, `internal/web/dist/` needs at least a `.gitkeep`
file:

```bash
touch internal/web/dist/.gitkeep
```

Or build the UI first: `make build-ui`

## Key Dependencies

| Dependency                                                     | Purpose                       |
| -------------------------------------------------------------- | ----------------------------- |
| [cobra](https://github.com/spf13/cobra)                        | CLI command framework         |
| [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)    | Pure Go SQLite driver         |
| [oklog/ulid](https://github.com/oklog/ulid)                    | ULID generation for event IDs |
| [gorilla/websocket](https://github.com/gorilla/websocket)      | WebSocket server              |
| [go-sdk (MCP)](https://github.com/modelcontextprotocol/go-sdk) | MCP server SDK                |

## Resources

- **Architecture**: `docs/architecture.md`
- **Daemon Architecture**: `docs/daemon.md`
- **RPC API Reference**: `docs/rpc-api.md`
- **Sync Design**: `docs/sync.md`
- **Quickstart Guide**: `docs/quickstart.md`
- **CLI Reference**: `docs/cli.md`
- **Identity System**: `docs/identity.md`
- **Workflow Templates**: `docs/workflow-templates.md` (structured feature development with AI agents)
- **Agent Reference**: `llms.txt` (concise) and `llms-full.txt` (detailed)
