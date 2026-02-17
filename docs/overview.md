
# Technical Overview

> **New here?** Start with [Why Thrum Exists](philosophy.md) for the philosophy
> behind the project, or the [Quickstart Guide](quickstart.md) to get running in
> 5 minutes. This document covers the technical architecture.

Thrum is a Git-backed messaging system that helps you coordinate AI agents
across sessions, worktrees, and machines. It provides:

- **Persistent messaging** that survives session boundaries
- **Automatic synchronization** via Git
- **Real-time visibility** into what other agents are working on
- **Subscription-based notifications** for targeted communication

Everything is inspectable — messages are JSONL files on a Git branch, state is a
queryable SQLite database, and sync is plain Git push/pull.

**Quick Setup:** After initialization, run `thrum setup claude-md --apply` to
generate agent coordination instructions for your CLAUDE.md file. Running
`thrum prime` (or `thrum context prime`) checks for CLAUDE.md and suggests
`thrum setup claude-md --apply` if no Thrum section is found.

## System Architecture

```
                    ┌─────────────────────────────────────────────────────────┐
                    │                     Thrum Daemon                         │
                    │  (Background service managing coordination & sync)       │
                    ├─────────────────────────────────────────────────────────┤
                    │                                                          │
   ┌────────────┐   │  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  │
   │   CLI      │◄──┼──┤ Unix Socket  │  │  Sync Loop   │  │  WebSocket   │  │
   │  (thrum)   │   │  │ JSON-RPC 2.0 │  │  (60s)       │  │  + SPA (9999)│  │
   └────────────┘   │  └──────────────┘  └──────────────┘  └──────────────┘  │
                    │          │                │               │    │        │
   ┌────────────┐   │          │                │               │    └──►Web UI
   │ MCP Server │◄──┼──────────┘                │               │            │
   │  (stdio)   │   │          │                ▼               │            │
   └────────────┘   │          │  ┌─────────────────────────────────────┐   │
                    │          ▼  │         State Management             │   │
                    │             │  ┌─────────────┐  ┌──────────────┐  │   │
                    │             │  │  JSONL Logs  │  │   SQLite     │  │   │
                    │             │  │  (sharded)   │  │  Projection  │  │   │
                    │             │  └─────────────┘  └──────────────┘  │   │
                    │             └─────────────────────────────────────┘   │
                    │                          │                             │
                    └──────────────────────────┼─────────────────────────────┘
                                               │
                    ┌──────────────────────────┼──────────────────────────┐
                    │                          ▼                          │
                    │  .git/thrum-sync/a-sync/     .thrum/var/            │
                    │  ├── events.jsonl            ├── messages.db        │
                    │  └── messages/               ├── thrum.sock         │
                    │      └── {agent}.jsonl       ├── thrum.pid (JSON)   │
                    │                              ├── thrum.lock (flock) │
                    │                              └── ws.port            │
                    └──────────────────────────┬──────────────────────────┘
                                               │ Git sync
                                               ▼
                                        ┌─────────────┐
                                        │   Remote    │
                                        │  (a-sync    │
                                        │   branch)   │
                                        └─────────────┘
```

## The Daemon: Central Coordinator

The daemon is the one process that everything else talks to. Start it once and
it handles messaging, sync, and state for all your agents — CLI, Web UI, and
MCP server all go through it.

### Core Services

| Service                     | Purpose                                       | Benefit                       |
| --------------------------- | --------------------------------------------- | ----------------------------- |
| **RPC Server**              | JSON-RPC 2.0 API over Unix socket             | CLI and programmatic access   |
| **WebSocket Server**        | Real-time bidirectional communication         | Web UI and live updates       |
| **Sync Loop**               | Automatic Git fetch/merge/push (60s interval) | Cross-machine synchronization |
| **Subscription Dispatcher** | Route notifications to interested clients     | Targeted communication        |
| **State Management**        | JSONL log + SQLite projection                 | Persistence + fast queries    |

### Everything Depends on the Daemon

```
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
Unix socket for RPC and WebSocket for real-time push notifications. Provides 11 tools:
5 core messaging tools (`send_message`, `check_messages`, `wait_for_message`, `list_agents`,
`broadcast_message`) and 6 group management tools (`create_group`, `delete_group`, `add_group_member`,
`remove_group_member`, `list_groups`, `get_group`).

## Key Features

### 1. Persistent Messaging

Messages are stored in append-only JSONL logs on a dedicated `a-sync` orphan
branch, accessed via a sync worktree at `.git/thrum-sync/a-sync/`:

```
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

Messages survive:

- Session restarts
- Machine reboots
- Context window compaction
- Agent replacement

### 2. Git-Based Synchronization

The daemon automatically syncs messages via the sync worktree at
`.git/thrum-sync/a-sync/`, checked out on the `a-sync` orphan branch. No branch
switching is needed -- all git operations happen within the worktree:

```
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

**Why Git?**

- Works offline (changes accumulate locally)
- Leverages existing authentication (SSH keys, HTTPS)
- Natural audit trail
- No additional infrastructure needed

### 3. Agent & Session Management

Agents register with a human-readable name, role, and module:

```bash
thrum agent register --name furiosa --role=implementer --module=auth
```

Agent names follow the pattern `[a-z0-9_]+` (lowercase alphanumeric and
underscores). Reserved names: `daemon`, `system`, `thrum`, `all`, `broadcast`.
Identity is resolved with the following priority: `THRUM_NAME` env var >
`--name` flag > solo-agent auto-select.

Each agent gets an identity file at `.thrum/identities/{name}.json`. Multiple
agents can coexist in a single worktree, each with their own identity file.
Legacy `agent:{role}:{hash}` IDs are still supported for backward compatibility.

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

This enables:

- Identity tracking across sessions with human-readable names
- Multi-agent support per worktree
- Crash recovery (orphaned sessions auto-close)
- Orphan detection and cleanup
- Work context visibility

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

### 5. Live Git State Tracking (Epic 21)

The daemon tracks what each agent is working on in real-time:

```sql
-- agent_work_contexts table
session_id        | agent_id        | branch      | unmerged_commits | uncommitted_files
ses_01HXE...      | furiosa         | feature/auth| 3                | ["src/auth.go"]
ses_02HXF...      | maximus         | feature/db  | 1                | []
```

**What it tracks:**

- Current branch
- Unmerged commits vs main
- Changed files
- Uncommitted modifications
- Agent-set task and intent

**Why this matters:**

- Agent2 can see "furiosa is working on auth.go with 3 unmerged commits"
- No manual investigation needed
- Prevents duplicate work
- Enables intelligent handoffs

### 6. Dual-Transport API (Single Port)

The daemon serves the WebSocket API and embedded Web UI SPA on the same port
(default 9999, configurable via `THRUM_WS_PORT`). The WebSocket endpoint is at
`/ws`, and all other paths serve the React SPA with SPA fallback routing.

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

Agents can save and retrieve volatile project state that doesn't belong in git
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

Context files are stored at `.thrum/context/{agent-name}.md` and integrated into
`thrum status` output. Use the `/update-context` skill in Claude Code for guided
context updates.

**Use cases:**

- Documenting architectural decisions under consideration
- Tracking partial investigation results
- Recording TODOs or questions for the next session
- Preserving context when handing off work

### 10. Understanding the CLI

Thrum has ~30 commands. Here's why that's not as many as it sounds.

#### Daily Drivers (8 commands)

These are the commands you'll actually use. If you're directing agents and
checking on their work, this is your whole toolkit:

| Command    | What it does                                  |
| ---------- | --------------------------------------------- |
| `quickstart` | Register + start session + set intent — one step |
| `send`       | Send a message                                |
| `inbox`      | Check your messages                           |
| `reply`      | Reply to a message                            |
| `team`       | What's everyone working on?                   |
| `overview`   | Status + team + inbox in one view             |
| `status`     | Your current state                            |
| `who-has`    | Who's editing this file?                      |

That's it for daily use. Everything else is infrastructure.

#### Designed for Agents (~16 commands)

These commands exist because agents need programmatic lifecycle control. You
rarely use them directly — `quickstart` handles the common case — but agents
call them constantly:

| Area          | Commands                           | Why agents need them                   |
| ------------- | ---------------------------------- | -------------------------------------- |
| Identity      | `agent register`, `agent whoami`   | Agents register on startup             |
| Sessions      | `session start/end/heartbeat`      | Track work periods, extract git state  |
| Work context  | `session set-intent/set-task`      | Declare what they're doing             |
| Notifications | `subscribe`, `wait`                | Block until relevant messages arrive   |
| Context       | `context save/show/clear`          | Persist state across compaction        |
| Messages      | `message get/edit/delete/read`     | CRUD on individual messages            |
| Groups        | `group create/add/remove/list`     | Organize teams programmatically        |
| MCP           | `mcp serve`                        | Native tool access for Claude Code etc.|

#### Setup & Admin (run once)

`init`, `daemon start/stop`, `setup`, `migrate`, `agent delete/cleanup`,
`sync force/status`, `runtime list/show`, `ping`

#### Aliases (because agents get creative)

AI agents are unpredictable in how they guess command names. An agent told to
"start working" might try `thrum agent start` or `thrum session start`. Both
work. These duplicates exist on purpose — they reduce friction for non-human
users:

| Alias              | Points to          | Why it exists                                 |
| ------------------ | ------------------ | --------------------------------------------- |
| `agent start`      | `session start`    | Agents think "I'm an agent, I should start"   |
| `agent end`        | `session end`      | Same pattern                                  |
| `agent set-intent` | `session set-intent` | Natural grouping under `agent`              |
| `agent set-task`   | `session set-task`   | Same                                        |
| `agent heartbeat`  | `session heartbeat`  | Same                                        |
| `whoami`           | `agent whoami`     | Common enough to promote to top-level         |

## Storage Architecture

Thrum uses event sourcing with CQRS:

```
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

**Benefits:**

- JSONL can be merged conflict-free (immutable events with unique IDs)
- SQLite provides fast queries with indexes
- SQLite can be rebuilt from JSONL anytime
- Offline-first: works without network

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
enabling LLM agents to communicate via native MCP tools:

| MCP Tool              | Description                                                       |
| --------------------- | ----------------------------------------------------------------- |
| `send_message`        | Send a message to another agent, role, or group                   |
| `check_messages`      | Poll for unread messages mentioning this agent, auto-marks read   |
| `wait_for_message`    | Block until a message arrives (WebSocket push) or timeout         |
| `list_agents`         | List registered agents with active/offline status                 |
| `broadcast_message`   | Send to all active agents with exclude filters (uses `@everyone`) |
| `create_group`        | Create a named group                                              |
| `delete_group`        | Delete a group                                                    |
| `add_group_member`    | Add member to group                                               |
| `remove_group_member` | Remove member from group                                          |
| `list_groups`         | List all groups                                                   |
| `get_group`           | Get group details with optional expansion                         |

Configure in Claude Code's `.claude/settings.json`:

```json
{
  "mcpServers": {
    "thrum": {
      "type": "stdio",
      "command": "thrum",
      "args": ["mcp", "serve"]
    }
  }
}
```

Use `--agent-id NAME` to override the agent identity, or set `THRUM_NAME` env
var. The MCP server connects to the daemon via Unix socket for RPC and WebSocket
for real-time push notifications via the `wait_for_message` tool.

## Getting Started

### Quick Start

```bash
# 1. Initialize in your project
cd your-project
thrum init

# 2. Start the daemon
thrum daemon start

# 3. Register, start session, and set intent in one step
thrum quickstart --name myagent --role implementer --module feature --intent "Working on feature X"

# 4. Send your first message
thrum send "Starting work on feature X" --scope module:feature

# 5. Subscribe to your module
thrum subscribe --scope module:feature
```

### Check What Other Agents Are Working On

```bash
# List all agent work contexts
thrum agent list --context

# Output:
# AGENT      BRANCH         UNMERGED  FILES
# furiosa    feature/auth   3         src/auth.go, src/auth_test.go
# maximus    feature/db     1         internal/db/schema.go
```

### Monitor Messages in Real-Time

```bash
# Wait for relevant messages
thrum wait --scope module:feature --timeout 5m
```

## Documentation Index

| Document                                    | Description                                 |
| ------------------------------------------- | ------------------------------------------- |
| [Philosophy](philosophy.md)                 | Why Thrum exists and how it thinks about agents |
| [Quickstart Guide](quickstart.md)           | 5-minute getting started                    |
| [Daemon Architecture](daemon.md)            | Technical daemon internals                  |
| [RPC API Reference](rpc-api.md)             | All RPC methods                             |
| [Sync Protocol](sync.md)                    | Git synchronization details                 |
| [WebSocket API](api/websocket.md)           | WebSocket-specific docs                     |
| [Event Streaming](event-streaming.md)       | Notifications and subscriptions             |
| [CLI Reference](cli.md)                     | All CLI commands                            |
| [Identity System](identity.md)              | Agent identity and registration             |
| [Context Management](context.md)            | Agent context storage and persistence       |
| [Multi-Agent Support](multi-agent.md)       | Groups, runtime presets, and team coordination |
| [Tailscale Sync](tailscale-sync.md)         | Cross-machine sync via Tailscale with security |
| [Agent Coordination](agent-coordination.md) | Multi-agent workflows and Beads integration |
| [Workflow Templates](workflow-templates.md) | Three-phase agent development templates     |
| [Architecture](architecture.md)             | Foundation packages                         |

## Design Principles

### You Stay in Control

Thrum is infrastructure you can inspect, not a service you depend on. Everything
is files, Git branches, and a local daemon. See [Why Thrum Exists](philosophy.md)
for the full philosophy.

### Offline-First

Everything works locally. Network is optional for sync.

### Git as Infrastructure

No additional servers. Uses existing Git authentication and hosting.

### Event Sourcing

JSONL log is the source of truth. SQLite is a rebuildable projection.

### Conflict-Free

Immutable events + unique IDs = conflict-free merging.

### Minimal Dependencies

Pure Go with minimal external packages. No CGO.

### Graceful Degradation

Network failures, missing remotes, and partial sync all handled gracefully.

## Summary

Thrum's daemon is the foundation that enables:

- **Persistent communication** across session boundaries
- **Automatic synchronization** via Git (60s interval)
- **Real-time notifications** for targeted messaging
- **Work context visibility** so agents know what others are doing
- **Multiple access methods** (CLI, WebSocket, MCP)
- **Full message lifecycle** (get, edit, delete, mark-read)
- **Coordination shortcuts** (who-has, ping, quickstart, overview)
- **Agent naming** with human-readable identities and cleanup
- **Daemon lifecycle hardening** (flock, JSON PID, defer cleanup)

The CLI, Web UI, and MCP server are all thin clients that communicate through
the daemon. This architecture ensures consistency, enables real-time updates,
and provides a single point for synchronization and coordination.
