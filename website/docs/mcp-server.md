---
title: "MCP Server"
description:
  "Thrum MCP server for native AI agent integration — tools, identity, message
  handling, and Claude Code setup"
category: "integrations"
---

## MCP Server

> **TL;DR:** The MCP server lets AI agents use Thrum through native MCP tools
> instead of CLI shell-outs. Start with `thrum mcp serve`. It provides 4 core
> messaging tools — no polling, no wasted tokens.
>
> **See also:** [Daemon Architecture](daemon.md) for the underlying daemon the
> MCP server connects to, [Identity](identity.md) for agent identity resolution.

## Overview

The MCP (Model Context Protocol) server enables Claude Code agents to send and
receive messages using native MCP tools instead of shelling out to CLI commands.
It runs as a long-lived child process (`thrum mcp serve`) communicating over
stdio with JSON-RPC, and connects to the Thrum daemon via Unix socket for all
message operations.

The server provides 5 MCP tools: 4 for core messaging operations and 1
deprecated broadcast tool.

The primary motivation is eliminating polling overhead. Without MCP, agents must
periodically call `thrum inbox` (burning tokens and context). With MCP, a cheap
background sub-agent blocks on `wait_for_message` and wakes the main agent
instantly when a message arrives.

## Architecture

```text
Claude Code (Opus/Sonnet)
  |
  +-- long-lived child: thrum mcp serve (stdio JSON-RPC)
  |     |
  |     +-- Daemon Client (Unix socket, per-call)
  |     |   +-- message.send     -> send_message tool
  |     |   +-- message.list     -> check_messages tool + wait_for_message poll
  |     |   +-- message.markRead -> check_messages tool (auto-mark consumed)
  |     |   +-- agent.list       -> list_agents tool
  |     |
  |     +-- Identity: .thrum/identities/{name}.json
  |
  +-- background sub-agent: message-listener (Haiku)
        +-- calls wait_for_message(timeout=300) -> blocks until message or timeout
```

### Package Structure

```text
internal/mcp/
  server.go    -- NewServer(), tool registration, Run(), InitWaiter()
  tools.go     -- send_message, check_messages, list_agents handlers
  waiter.go    -- WebSocket client, notification routing, wait_for_message handler
  types.go     -- MCP-specific input/output structs

cmd/thrum/mcp.go  -- thrum mcp serve cobra command
```

### Startup Sequence

1. Resolve repo path (defaults to `.`)
2. If `--agent-id` is provided, set `THRUM_NAME` env var before config load
3. Verify daemon is running (connect to Unix socket, call `health` RPC)
4. Load agent identity from `.thrum/identities/{name}.json` via
   `config.LoadWithPath`
5. Resolve daemon socket path (follows `.thrum/redirect` in feature worktrees)
6. Generate composite agent ID via
   `identity.GenerateAgentID(repoID, role, module, name)`
7. Create MCP server with the official Go SDK
   (`github.com/modelcontextprotocol/go-sdk/mcp`)
8. Register all 5 tool handlers (4 core messaging + 1 deprecated)
9. Initialize polling waiter (no network connection at construction;
   `wait_for_message` opens lazy Unix socket connections per call)
10. Start MCP stdio server (blocks until client disconnects or context
    cancelled)

### Shutdown

When Claude Code terminates the process (closes stdin) or a signal is received
(SIGINT/SIGTERM):

- Context is cancelled
- Any active `wait_for_message` polling loop exits on the next tick
- Unix socket connections are closed (per-call, so nothing to clean up)
- Process exits

### Key Design Decisions

- **Per-call `cli.Client` creation**: `cli.Client` is not concurrent-safe. Each
  tool handler creates a fresh Unix socket connection. This is cheap (local
  socket) and avoids concurrency issues.
- **Atomic WebSocket request IDs**: The waiter uses `atomic.Int64` for
  incrementing JSON-RPC request IDs, ensuring uniqueness across concurrent
  calls.
- **Single-waiter enforcement**: Only one `wait_for_message` can be active at a
  time per server instance. A second call returns an error. Enforced with a
  mutex.
- **Polling-only delivery**: `wait_for_message` polls `message.list` via Unix
  socket on a 500ms ticker (mirroring the CLI `thrum wait` path), reconnecting
  on transport errors. The previous WebSocket subscribe approach was removed
  alongside the subscribe RPC; polling shares the same `message.list` path the
  CLI already exercises.

## Usage

### Command

```bash
thrum mcp serve [--agent-id NAME]
```

**Prerequisites:**

- Thrum daemon must be running (`thrum daemon start`)
- Agent must be registered
  (`thrum quickstart --name NAME --role ROLE --module MODULE`)

**Flags:**

| Flag         | Default              | Description                                                  |
| ------------ | -------------------- | ------------------------------------------------------------ |
| `--agent-id` | (from identity file) | Override agent name; selects `.thrum/identities/{name}.json` |

### Claude Code Configuration

Add to `.claude/settings.json` (project or user level):

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

Once configured, Claude Code starts `thrum mcp serve` as a child process and
exposes its tools as `mcp__thrum__<tool_name>`.

## API Reference

### send_message

Send a message to another agent, role, or group.

**Input:**

| Parameter  | Type   | Required | Description                                                                                                          |
| ---------- | ------ | -------- | -------------------------------------------------------------------------------------------------------------------- |
| `to`       | string | yes      | Recipient: `@role`, agent name, `@groupname`, or composite `agent:role:hash`                                         |
| `content`  | string | yes      | Message text (markdown)                                                                                              |
| `reply_to` | string | no       | Message ID to reply to (creates a reply chain; a `thread_id` is automatically assigned and returned in the response) |
| `metadata` | object | no       | Key-value metadata (passed as structured data)                                                                       |

**Output:**

| Field         | Type   | Description                                               |
| ------------- | ------ | --------------------------------------------------------- |
| `status`      | string | `delivered`                                               |
| `message_id`  | string | ID of the sent message                                    |
| `resolved_to` | string | How the recipient was resolved (`name`, `role`, `group`)  |
| `warnings`    | array  | Any routing warnings (e.g., `@name` matched a role group) |

**Addressing:** The `to` field is parsed to extract a mention role:

- `@ops` becomes mention for role `ops`
- `agent:ops:abc123` extracts role `ops`
- `ops` is used as-is

**Daemon RPC:** `message.send`

### check_messages

Poll for unread messages mentioning this agent. Messages are automatically
marked as read after retrieval.

**Input:**

| Parameter | Type    | Required | Description                         |
| --------- | ------- | -------- | ----------------------------------- |
| `limit`   | integer | no       | Max messages to return (default 50) |

Agent identity is resolved at server startup; the client does not pass an agent
ID.

**Output:**

| Field       | Type    | Description                                          |
| ----------- | ------- | ---------------------------------------------------- |
| `status`    | string  | `messages` or `empty`                                |
| `messages`  | array   | List of `MessageInfo` objects                        |
| `remaining` | integer | Count of remaining unread messages (clamped to >= 0) |

**MessageInfo:**

| Field        | Type   | Description        |
| ------------ | ------ | ------------------ |
| `message_id` | string | Message identifier |
| `from`       | string | Sender agent ID    |
| `content`    | string | Message content    |
| `timestamp`  | string | Creation timestamp |

**Behavior:**

1. Lists unread messages mentioning this agent's role via `message.list` RPC
2. Marks all returned messages as read via `message.markRead` RPC (best-effort)
3. Returns consumed messages (they will not appear on the next `check_messages`
   call)

**Daemon RPC:** `message.list` + `message.markRead`

### wait_for_message

Block until a message arrives or timeout expires. Designed for background
listener sub-agents running on Haiku.

**Input:**

| Parameter | Type    | Required | Description                                |
| --------- | ------- | -------- | ------------------------------------------ |
| `timeout` | integer | no       | Max seconds to wait (default 300, max 600) |

**Output:**

| Field            | Type        | Description                            |
| ---------------- | ----------- | -------------------------------------- |
| `status`         | string      | `message_received` or `timeout`        |
| `message`        | MessageInfo | The received message (null on timeout) |
| `waited_seconds` | integer     | How long the wait lasted               |

**Behavior:**

1. Check the internal notification queue (messages that arrived while no waiter
   was active)
2. If a queued message exists, pop it and return immediately
3. If queue is empty, block on a channel with the specified timeout
4. When a WebSocket `notification.message` arrives, the `readLoop` pushes it to
   the queue and closes the waiter channel
5. Fetch the full message via `message.get` RPC
6. Mark as read via `message.markRead` RPC (best-effort)
7. Return the message

**Concurrency:** Only one `wait_for_message` can be active at a time. A second
concurrent call returns an error.

**Requires:** WebSocket waiter initialized at startup. If the waiter failed to
connect, this tool returns an error.

**Daemon RPC:** WebSocket notifications + `message.get` + `message.markRead`

### list_agents

List all registered agents and their status.

**Input:**

| Parameter         | Type    | Required | Description                                                      |
| ----------------- | ------- | -------- | ---------------------------------------------------------------- |
| `include_offline` | boolean | no       | Include inactive agents (default `true`; uses pointer semantics) |

**Output:**

| Field    | Type    | Description                 |
| -------- | ------- | --------------------------- |
| `agents` | array   | List of `AgentInfo` objects |
| `count`  | integer | Number of agents returned   |

**AgentInfo:**

| Field          | Type   | Description              |
| -------------- | ------ | ------------------------ |
| `name`         | string | Agent display name       |
| `role`         | string | Agent role               |
| `module`       | string | Agent module             |
| `status`       | string | `active` or `offline`    |
| `last_seen_at` | string | Last heartbeat timestamp |

**Status derivation:** Based on `last_seen_at` relative to current time:

- Less than 2 minutes ago: `active`
- 2+ minutes ago or missing: `offline`

**Daemon RPC:** `agent.list`

### broadcast_message (Deprecated)

> **Deprecated:** Use `send_message(to="@everyone", content="...")` instead.
> This tool is registered but deprecated and may be removed in a future release.

Broadcast a message to all agents. Equivalent to `send_message` with
`to="@everyone"`.

**Input:**

| Parameter | Type   | Required | Description  |
| --------- | ------ | -------- | ------------ |
| `content` | string | yes      | Message text |

**Daemon RPC:** `message.send`

## Identity Resolution

The MCP server resolves agent identity once at startup. The client never passes
agent IDs in tool calls.

**Resolution order:**

1. `--agent-id` flag on `thrum mcp serve` (sets `THRUM_NAME` env var)
2. `THRUM_NAME` environment variable
3. Auto-select single identity file in `.thrum/identities/`
4. Error if no identity found or multiple identities exist without
   disambiguation

**Identity file:** `.thrum/identities/{name}.json` contains name, role, module,
and repo ID. The server generates a composite agent ID (`agent:{role}:{hash}`)
using `identity.GenerateAgentID()`, consistent with daemon RPC handlers.

**Multi-agent worktrees:** When multiple agents operate in the same worktree,
each must have a distinct identity file. Use `THRUM_NAME` env var or
`--agent-id` flag to select.

## Polling Waiter

The `Waiter` struct (`internal/mcp/waiter.go`) powers `wait_for_message` by
polling the daemon inbox over the Unix socket. There is no WebSocket
subscription — the previous design registered a `subscribe` RPC that was removed
when the CLI subscribe commands were retired. Polling shares the same
`message.list` path the CLI `thrum wait` already relies on.

### Polling Loop

On `wait_for_message`, the waiter:

1. Opens a fresh `cli.Client` Unix socket connection (lazy, per call)
2. Calls `message.list --unread --for-agent <id>` on a 500ms ticker
3. Returns the first unseen message addressed to this agent (by name, role, or
   `@everyone` broadcast)
4. Times out and returns empty after the supplied `timeout` (default 30s, max
   300s)
5. Reconnects on transport errors without dropping the wait

Single-waiter enforcement: only one `wait_for_message` call may be active per
`Waiter` instance. A second concurrent call returns an error. The context passed
to `WaitForMessage` is honored — cancelling it stops the loop on the next tick.

## Integration

### Message-Listener Sub-Agent

The recommended pattern for receiving messages in Claude Code is a background
Haiku sub-agent that blocks on `wait_for_message`. This is defined in
`.claude/agents/message-listener.md`.

**How it works:**

1. The main agent spawns the message-listener as a background `Task` sub-agent
2. The listener calls `check_messages` to drain any backlog
3. If messages are found, it returns them immediately
4. If none, it calls `wait_for_message(timeout=300)` and blocks
5. When a message arrives or timeout occurs, the listener loops automatically
   for up to 4 hours (30 cycles) — no manual re-arming needed
6. A cron watchdog (`CronCreate`, every 30 min) auto-respawns the listener if it
   is not running, reducing token usage by ~65% compared to the old pattern

**Return format:**

When messages are received:

```text
MESSAGES_RECEIVED
FROM: [sender]
CONTENT: [message content]
TIMESTAMP: [timestamp]
```

When timeout occurs:

```text
NO_MESSAGES_TIMEOUT
```

**Cost:** Approximately $0.00003 per cycle (Haiku-class model).

### Context Management

- The listener runs up to 30 cycles (~4 hours) before stopping; the cron
  watchdog respawns it automatically every 30 min if needed
- Compact after 10+ message cycles to prevent context overflow
- After 5 consecutive timeouts with no pending work, send status to the
  coordinator and stop the listener

### CLAUDE.md Protocol

The project `CLAUDE.md` includes instructions for agents to use MCP tools:

**Core messaging:**

```text
mcp__thrum__send_message(to="@reviewer_main", content="...")  # prefer specific names
mcp__thrum__check_messages()
mcp__thrum__list_agents()
mcp__thrum__send_message(to="@everyone", content="...")  # critical broadcast to all
mcp__thrum__wait_for_message(timeout=300)
```

## Development

### Source Files

| File                                 | Purpose                                                            |
| ------------------------------------ | ------------------------------------------------------------------ |
| `internal/mcp/server.go`             | Server struct, NewServer(), Run(), InitWaiter(), tool registration |
| `internal/mcp/tools.go`              | Tool handlers, address parsing, status derivation                  |
| `internal/mcp/waiter.go`             | WebSocket connection, readLoop, WaitForMessage, notification queue |
| `internal/mcp/types.go`              | Input/output structs for all tools                                 |
| `cmd/thrum/mcp.go`                   | Cobra command, daemon health check, waiter init, signal handling   |
| `.claude/agents/message-listener.md` | Haiku sub-agent definition                                         |

### Testing

```bash
# Unit + sequential integration tests (no daemon needed)
go test ./internal/mcp/...

# With verbose output
go test -v ./internal/mcp/...
```

Test coverage includes:

- Tool handler input validation
- Message queue push/pop semantics
- Waiter timeout and cancellation logic
- Identity resolution
- Tests passing (unit + sequential integration)

WebSocket integration tests (requiring a running daemon WebSocket) are currently
deferred.

## Next Steps

- [Claude Code Plugin](claude-code-plugin.md) — install the plugin to get slash
  commands and automatic MCP configuration without manual setup
- [Agent Coordination](agent-coordination.md) — practical workflows using MCP
  tools, including the message-listener pattern for async coordination
- [Messaging](messaging.md) — the full messaging model that these MCP tools
  wrap: scopes, mentions, groups, and threading
- [Daemon Architecture](daemon.md) — the daemon that the MCP server connects to
  via Unix socket and WebSocket

### Debugging

The MCP server logs warnings to stderr. Check for:

- `Warning: WebSocket waiter not available` -- daemon WebSocket port not found
  or connection failed; `wait_for_message` will not work but other tools
  function normally
- `agent name not configured` / `agent role not configured` -- agent identity
  not registered; run `thrum quickstart` first
- `Thrum daemon is not running` -- start the daemon with `thrum daemon start`

### Dependencies

- **Runtime:** Thrum daemon (`thrum daemon start`)
- **Go SDK:** `github.com/modelcontextprotocol/go-sdk/mcp` (official MCP Go SDK)
- **WebSocket:** `github.com/gorilla/websocket`
- **Identity:** Agent registered with `.thrum/identities/{name}.json`

## References

- Design document: `dev-docs/plans/2026-02-06-mcp-server-design.md`
- Daemon architecture: `docs/daemon.md`
- RPC API reference: `docs/rpc-api.md`
- Identity system: `docs/identity.md`
- Agent reference: `llms.txt`
