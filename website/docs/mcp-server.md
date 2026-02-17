
# MCP Server

> **See also:** [Daemon Architecture](daemon.md) for the underlying daemon the
> MCP server connects to, [Identity](identity.md) for agent identity resolution.

## Overview

The MCP (Model Context Protocol) server enables Claude Code agents to send and
receive messages using native MCP tools instead of shelling out to CLI commands.
It runs as a long-lived child process (`thrum mcp serve`) communicating over
stdio with JSON-RPC, and connects to the Thrum daemon via Unix socket for
message operations and via WebSocket for real-time push notifications.

The server provides 11 MCP tools: 5 for core messaging operations and 6 for group management.

The primary motivation is eliminating polling overhead. Without MCP, agents must
periodically call `thrum inbox` (burning tokens and context). With MCP, a cheap
background sub-agent blocks on `wait_for_message` and wakes the main agent
instantly when a message arrives.

## Architecture

```
Claude Code (Opus/Sonnet)
  |
  +-- long-lived child: thrum mcp serve (stdio JSON-RPC)
  |     |
  |     +-- Daemon Client (Unix socket, per-call)
  |     |   +-- message.send     -> send_message tool
  |     |   +-- message.list     -> check_messages tool
  |     |   +-- message.markRead -> check_messages tool (auto-mark consumed)
  |     |   +-- agent.list       -> list_agents tool
  |     |   +-- message.send x N -> broadcast_message tool
  |     |
  |     +-- WebSocket Client (ws://localhost:{port}/ws)
  |     |   +-- user.identify + user.register -> session setup
  |     |   +-- subscribe (mention_role=@{role}) -> notification stream
  |     |   +-- notification.message -> unblocks wait_for_message
  |     |
  |     +-- Internal Notification Queue (max 1000, FIFO, drops oldest)
  |     |
  |     +-- Identity: .thrum/identities/{name}.json
  |
  +-- background sub-agent: message-listener (Haiku)
        +-- calls wait_for_message(timeout=300) -> blocks until message or timeout
```

### Package Structure

```
internal/mcp/
  server.go    -- NewServer(), tool registration, Run(), InitWaiter()
  tools.go     -- send_message, check_messages, list_agents, broadcast_message handlers
  waiter.go    -- WebSocket client, notification routing, wait_for_message handler
  types.go     -- MCP-specific input/output structs

cmd/thrum/mcp.go  -- thrum mcp serve cobra command
```

### Startup Sequence

1. Resolve repo path (respects `--repo` flag, defaults to `.`)
2. If `--agent-id` is provided, set `THRUM_NAME` env var before config load
3. Verify daemon is running (connect to Unix socket, call `health` RPC)
4. Load agent identity from `.thrum/identities/{name}.json` via
   `config.LoadWithPath`
5. Resolve daemon socket path (follows `.thrum/redirect` in feature worktrees)
6. Generate composite agent ID via
   `identity.GenerateAgentID(repoID, role, module, name)`
7. Create MCP server with the official Go SDK
   (`github.com/modelcontextprotocol/go-sdk/mcp`)
8. Register all 11 tool handlers (5 core messaging + 6 group management)
9. Initialize WebSocket waiter (best-effort -- reads port from
   `.thrum/var/ws.port`)
   - Connect to `ws://localhost:{port}/ws`
   - Send `user.identify` to get git username
   - Send `user.register` with the username
   - Send `subscribe` with `mention_role` for this agent's role
   - Start background `readLoop` goroutine for incoming notifications
10. Start MCP stdio server (blocks until client disconnects or context
    cancelled)

### Shutdown

When Claude Code terminates the process (closes stdin) or a signal is received
(SIGINT/SIGTERM):

- Context is cancelled
- Waiter closes WebSocket connection (daemon auto-unregisters)
- Waiter's `readLoop` exits, unblocking any active `wait_for_message` call
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
- **Best-effort WebSocket**: If the WebSocket connection fails at startup, the
  MCP server still operates -- only `wait_for_message` returns errors. The other
  10 tools work via Unix socket RPC.

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
| `--repo`     | `.`                  | Repository path                                              |

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

| Parameter  | Type   | Required | Description                                                                   |
| ---------- | ------ | -------- | ----------------------------------------------------------------------------- |
| `to`       | string | yes      | Recipient: `@role`, agent name, `@groupname`, or composite `agent:role:hash` |
| `content`  | string | yes      | Message text (markdown)                                                       |
| `reply_to` | string | no       | Message ID to reply to (creates a reply chain)                                |
| `priority` | string | no       | `critical`, `high`, `normal` (default), or `low`                              |
| `metadata` | object | no       | Key-value metadata (passed as structured data)                                |

**Output:**

| Field              | Type   | Description                                  |
| ------------------ | ------ | -------------------------------------------- |
| `status`           | string | `delivered`                                  |
| `message_id`       | string | ID of the sent message                       |
| `recipient_status` | string | `unknown` (recipient lookup not implemented) |

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
| `priority`   | string | Message priority   |
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

| Parameter         | Type    | Required | Description                                                          |
| ----------------- | ------- | -------- | -------------------------------------------------------------------- |
| `timeout`         | integer | no       | Max seconds to wait (default 300, max 600)                           |
| `priority_filter` | string  | no       | `all` (default), `critical`, `high_and_above`, or `normal_and_above` |

**Note:** `priority_filter` is accepted but not yet implemented. All messages
pass through regardless of filter value.

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


### broadcast_message

Send a message to all agents via the `@everyone` group, with optional filtering. The sender is automatically excluded. This is a simplified wrapper around sending to `@everyone`.

**Input:**

| Parameter  | Type   | Required | Description                                      |
| ---------- | ------ | -------- | ------------------------------------------------ |
| `content`  | string | yes      | Message text to broadcast                        |
| `priority` | string | no       | `critical`, `high`, `normal` (default), or `low` |
| `filter`   | object | no       | Optional recipient filters                       |

**BroadcastFilter:**

| Field     | Type   | Description                                                  |
| --------- | ------ | ------------------------------------------------------------ |
| `status`  | string | `all` (default) or `active` (only agents seen in last 2 min) |
| `exclude` | array  | Agent names or roles to exclude                              |

**Output:**

| Field         | Type    | Description                                           |
| ------------- | ------- | ----------------------------------------------------- |
| `status`      | string  | `sent`, `partial` (some failures), or `no_recipients` |
| `sent_to`     | array   | Roles of agents that received the message             |
| `failed_to`   | array   | Roles of agents where send failed                     |
| `total_sent`  | integer | Count of successful sends                             |
| `message_ids` | array   | IDs of sent messages                                  |

**Behavior:**

1. Fetch agent list via `agent.list` RPC
2. Filter out self (by agent ID, not role -- so other agents with the same role
   still receive the message)
3. Apply exclude filter (matches against both role and display name)
4. Apply status filter (if `active`, skip agents with `offline` status)
5. Send individual messages to each remaining agent via `message.send` RPC

**Daemon RPC:** `agent.list` + `message.send` (one per recipient)


### create_group

Create a named group for targeted messaging.

**Input:**

| Parameter     | Type   | Required | Description                      |
| ------------- | ------ | -------- | -------------------------------- |
| `name`        | string | yes      | Group name (e.g., `reviewers`)   |
| `description` | string | no       | Human-readable group description |

**Output:**

| Field    | Type   | Description                 |
| -------- | ------ | --------------------------- |
| `status` | string | `created`                   |
| `name`   | string | Name of the created group   |

**Daemon RPC:** `group.create`


### delete_group

Delete a group by name. The `@everyone` group is protected and cannot be deleted.

**Input:**

| Parameter | Type   | Required | Description                     |
| --------- | ------ | -------- | ------------------------------- |
| `name`    | string | yes      | Group name to delete            |

**Output:**

| Field    | Type   | Description                 |
| -------- | ------ | --------------------------- |
| `status` | string | `deleted`                   |
| `name`   | string | Name of the deleted group   |

**Daemon RPC:** `group.delete`


### add_group_member

Add a member (agent or role) to a group.

**Input:**

| Parameter     | Type   | Required | Description                 |
| ------------- | ------ | -------- | --------------------------- |
| `group`       | string | yes      | Group name to add member to |
| `member_type` | string | yes      | `agent` or `role`           |
| `member_id`   | string | yes      | Agent name or role name     |

**Output:**

| Field         | Type   | Description                 |
| ------------- | ------ | --------------------------- |
| `status`      | string | `added`                     |
| `group`       | string | Group name                  |
| `member_type` | string | Type of member added        |
| `member_id`   | string | ID of member added          |

**Daemon RPC:** `group.member.add`


### remove_group_member

Remove a member from a group.

**Input:**

| Parameter     | Type   | Required | Description                      |
| ------------- | ------ | -------- | -------------------------------- |
| `group`       | string | yes      | Group name to remove member from |
| `member_type` | string | yes      | `agent` or `role`                |
| `member_id`   | string | yes      | Agent name or role name          |

**Output:**

| Field         | Type   | Description                 |
| ------------- | ------ | --------------------------- |
| `status`      | string | `removed`                   |
| `group`       | string | Group name                  |
| `member_type` | string | Type of member removed      |
| `member_id`   | string | ID of member removed        |

**Daemon RPC:** `group.member.remove`


### list_groups

List all groups in the system.

**Input:**

| Parameter | Type | Required | Description                 |
| --------- | ---- | -------- | --------------------------- |
| _(none)_  |      |          | Empty object or omit params |

**Output:**

| Field                     | Type   | Description                                   |
| ------------------------- | ------ | --------------------------------------------- |
| `groups`                  | array  | List of `GroupInfo` objects                   |
| `groups[].name`           | string | Group name                                    |
| `groups[].description`    | string | Group description (may be empty)              |
| `groups[].created_at`     | string | ISO 8601 creation timestamp                   |
| `groups[].created_by`     | string | Agent ID of creator                           |
| `groups[].member_count`   | integer | Number of direct members                     |

**Daemon RPC:** `group.list`


### get_group

Get detailed information about a specific group. Supports expansion to resolve roles to individual agent IDs.

**Input:**

| Parameter | Type    | Required | Description                |
| --------- | ------- | -------- | -------------------------- |
| `name`    | string  | yes      | Group name                 |
| `expand`  | boolean | no       | Resolve roles to agent IDs |

**Output:**

| Field                   | Type    | Description                                |
| ----------------------- | ------- | ------------------------------------------ |
| `name`                  | string  | Group name                                 |
| `description`           | string  | Group description (may be empty)           |
| `created_at`            | string  | ISO 8601 creation timestamp                |
| `created_by`            | string  | Agent ID of creator                        |
| `members`               | array   | List of member objects                     |
| `members[].type`        | string  | `agent` or `role`                          |
| `members[].id`          | string  | Agent name or role name                    |
| `expanded_agents`       | array   | (Only if `expand=true`) List of agent IDs  |
| `expanded_agents_count` | integer | (Only if `expand=true`) Count of agent IDs |

**Daemon RPC:** `group.info` (without expand) or `group.members` (with expand)


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

## WebSocket Waiter

The `Waiter` struct (`internal/mcp/waiter.go`) manages the WebSocket connection
for real-time message notifications.

### Connection Setup

On initialization, the waiter:

1. Connects to the daemon WebSocket at `ws://localhost:{port}/ws`
2. Sends `user.identify` to get the git username
3. Sends `user.register` with that username
4. Sends `subscribe` with `mention_role` set to the agent's role
5. Starts a background `readLoop` goroutine

### Notification Flow

```
Daemon WebSocket -> readLoop -> queue ([]MessageNotification) -> waiterCh -> WaitForMessage
```

The `readLoop` goroutine:

- Reads WebSocket messages continuously
- Filters for `notification.message` method
- Parses the notification into a `MessageNotification` (message_id, preview,
  agent_id, timestamp)
- Appends to the internal queue (max 1000 items; drops oldest on overflow)
- Closes the `waiterCh` channel to wake any blocked `WaitForMessage` call

On connection loss, `readLoop` closes `waiterCh` (if set) before exiting, which
unblocks any active waiter with a timeout-like response rather than hanging
forever.

### JSON-RPC over WebSocket

The waiter uses JSON-RPC 2.0 for setup RPCs. Request IDs are atomically
incremented (`atomic.Int64`). During `wsRPC` calls, incoming notifications are
skipped (they have no `id` field and a non-empty `method` field).

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
5. When a message arrives or timeout occurs, the listener returns to the main
   agent
6. The main agent processes the result and re-arms the listener

**Return format:**

When messages are received:

```
MESSAGES_RECEIVED
FROM: [sender]
PRIORITY: [priority]
CONTENT: [message content]
TIMESTAMP: [timestamp]
```

When timeout occurs:

```
NO_MESSAGES_TIMEOUT
```

**Cost:** Approximately $0.00003 per cycle (Haiku-class model).

### Priority Handling Protocol

When the main agent receives messages from the listener, it should handle
priorities as follows:

| Priority   | Action                                  |
| ---------- | --------------------------------------- |
| `critical` | Stop current work immediately           |
| `high`     | Process at next breakpoint              |
| `normal`   | Process when current sub-task completes |
| `low`      | Queue, process when convenient          |

### Context Management

- Compact after 10+ message cycles to prevent context overflow
- After 5 consecutive timeouts with no pending work, send status to the
  coordinator and stop the listener

### CLAUDE.md Protocol

The project `CLAUDE.md` includes instructions for agents to use MCP tools:

**Core messaging:**
```
mcp__thrum__send_message(to="@reviewer", content="...", priority="normal")
mcp__thrum__check_messages()
mcp__thrum__list_agents()
mcp__thrum__broadcast_message(content="...")
mcp__thrum__wait_for_message(timeout=300)
```

**Group management:**
```
mcp__thrum__create_group(name="backend", description="Backend team")
mcp__thrum__add_group_member(group="backend", member_type="role", member_id="implementer")
mcp__thrum__list_groups()
mcp__thrum__get_group(name="backend", expand=true)
mcp__thrum__remove_group_member(group="backend", member_type="agent", member_id="alice")
mcp__thrum__delete_group(name="backend")
```

## Development

### Source Files

| File                                 | Purpose                                                                |
| ------------------------------------ | ---------------------------------------------------------------------- |
| `internal/mcp/server.go`             | Server struct, NewServer(), Run(), InitWaiter(), tool registration     |
| `internal/mcp/tools.go`              | Tool handlers, address parsing, status derivation, priority validation |
| `internal/mcp/waiter.go`             | WebSocket connection, readLoop, WaitForMessage, notification queue     |
| `internal/mcp/types.go`              | Input/output structs for all 11 tools                                  |
| `cmd/thrum/mcp.go`                   | Cobra command, daemon health check, waiter init, signal handling       |
| `.claude/agents/message-listener.md` | Haiku sub-agent definition                                             |

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
- 18 tests passing (unit + sequential integration)

WebSocket integration tests (requiring a running daemon WebSocket) are currently
deferred.

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

- Design document: `docs/plans/2026-02-06-mcp-server-design.md`
- Daemon architecture: `docs/daemon.md`
- RPC API reference: `docs/rpc-api.md`
- Identity system: `docs/identity.md`
- Agent reference: `llms.txt`
