
# Inbox Query RPC Methods

## Overview

The Thrum daemon provides a complete set of RPC methods for querying the inbox,
listing agents, and managing read state. These methods power both the CLI
(`thrum inbox`) and the Web UI.

All methods use JSON-RPC 2.0 over Unix socket (`.thrum/var/thrum.sock`) or
WebSocket (`ws://localhost:9999`). See `docs/rpc-api.md` for the full API
reference.

## Query Methods

### 1. agent.list

Lists all registered agents with optional filtering by role or module.

**Request:**

```json
{
  "jsonrpc": "2.0",
  "method": "agent.list",
  "params": {
    "role": "implementer",
    "module": "auth"
  },
  "id": 1
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "result": {
    "agents": [
      {
        "agent_id": "furiosa",
        "kind": "agent",
        "role": "implementer",
        "module": "auth",
        "display": "Furiosa",
        "registered_at": "2026-02-03T10:00:00Z",
        "last_seen_at": "2026-02-03T15:30:00Z"
      }
    ]
  },
  "id": 1
}
```

Both `role` and `module` filters are optional. Omit both to list all agents.

### 2. message.list

Lists messages with comprehensive filtering, pagination, and sorting. This is
the primary inbox query method.

**Request:**

```json
{
  "jsonrpc": "2.0",
  "method": "message.list",
  "params": {
    "author_id": "furiosa",
    "scope": { "type": "module", "value": "auth" },
    "ref": { "type": "mention", "value": "reviewer" },
    "mentions": true,
    "unread": true,
    "mention_role": "reviewer",
    "unread_for_agent": "furiosa",
    "page_size": 50,
    "page": 1,
    "sort_by": "created_at",
    "sort_order": "desc"
  },
  "id": 1
}
```

**Filters (all optional):**

| Parameter          | Type    | Description                                                                 |
| ------------------ | ------- | --------------------------------------------------------------------------- |
| `author_id`        | string  | Filter by author agent ID                                                   |
| `scope`            | object  | Filter by scope (`{"type": "...", "value": "..."}`)                         |
| `ref`              | object  | Filter by ref (`{"type": "...", "value": "..."}`)                           |
| `mentions`         | boolean | Only messages mentioning current agent (resolved from local config)         |
| `unread`           | boolean | Only unread messages (resolved from local config)                           |
| `mention_role`     | string  | Explicit mention filter by role (for remote callers like the MCP server)    |
| `unread_for_agent` | string  | Explicit unread filter by agent ID (for remote callers like the MCP server) |

**Pagination and sorting (all optional):**

| Parameter    | Type    | Default        | Description                                  |
| ------------ | ------- | -------------- | -------------------------------------------- |
| `page_size`  | integer | 10             | Items per page (max: 100)                    |
| `page`       | integer | 1              | Page number                                  |
| `sort_by`    | string  | `"created_at"` | Sort field: `"created_at"` or `"updated_at"` |
| `sort_order` | string  | `"desc"`       | Sort direction: `"asc"` or `"desc"`          |

**Response:**

```json
{
  "jsonrpc": "2.0",
  "result": {
    "messages": [
      {
        "message_id": "msg_01HXE...",
        "reply_to": "msg_01HXD...",
        "agent_id": "furiosa",
        "body": {
          "format": "markdown",
          "content": "Auth module complete, all tests passing",
          "structured": ""
        },
        "created_at": "2026-02-03T15:41:12Z",
        "deleted": false,
        "is_read": true
      }
    ],
    "total": 150,
    "unread": 5,
    "page": 1,
    "page_size": 50,
    "total_pages": 3
  },
  "id": 1
}
```

**Filter resolution:** The `mentions` and `unread` boolean filters are resolved
using the local agent config (via `THRUM_ROLE` / identity file). The
`mention_role` and `unread_for_agent` string filters are explicit overrides for
remote callers (like the MCP server) that cannot access local config.

### 3. message.get

Retrieves a single message by ID with full details including scopes, refs,
author info, edit/delete metadata.

**Request:**

```json
{
  "jsonrpc": "2.0",
  "method": "message.get",
  "params": {
    "message_id": "msg_01HXE..."
  },
  "id": 1
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "result": {
    "message": {
      "message_id": "msg_01HXE...",
      "reply_to": "msg_01HXD...",
      "author": {
        "agent_id": "furiosa",
        "session_id": "ses_01HXE..."
      },
      "body": {
        "format": "markdown",
        "content": "Auth module complete, all tests passing",
        "structured": ""
      },
      "scopes": [{ "type": "module", "value": "auth" }],
      "refs": [{ "type": "mention", "value": "reviewer" }],
      "metadata": {
        "deleted_at": "",
        "delete_reason": ""
      },
      "created_at": "2026-02-03T15:41:12Z",
      "updated_at": "",
      "deleted": false
    }
  },
  "id": 1
}
```

### 4. message.markRead

Batch mark messages as read for the current agent and session.

**Request:**

```json
{
  "jsonrpc": "2.0",
  "method": "message.markRead",
  "params": {
    "message_ids": ["msg_01HXE...", "msg_01HXF...", "msg_01HXG..."]
  },
  "id": 1
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "result": {
    "marked_count": 3,
    "also_read_by": {
      "msg_01HXE...": ["other_agent"]
    }
  },
  "id": 1
}
```

The `also_read_by` field returns collaboration info: other agents who have also
read the same messages. This is omitted if empty.

## Read/Unread Tracking

Read state is fully implemented with the following infrastructure:

### Storage

```sql
CREATE TABLE message_reads (
  message_id TEXT NOT NULL,
  session_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  read_at TEXT NOT NULL,
  PRIMARY KEY (message_id, session_id),
  FOREIGN KEY (message_id) REFERENCES messages(message_id) ON DELETE CASCADE
);
```

Read state is tracked per message per session and per agent. A message is
considered read if any session or agent matching the current identity has a read
record.

### Query Integration

- `message.list` responses include `is_read` on each message and `unread` count
  in the response envelope
- `message.list` supports `unread` (boolean, config-resolved) and
  `unread_for_agent` (string, explicit) filters

### Auto Mark-as-Read

Several CLI commands mark messages as read automatically:

| Command                    | Behavior                                                       |
| -------------------------- | -------------------------------------------------------------- |
| `thrum inbox`              | Marks all displayed messages as read (skipped with `--unread`) |
| `thrum reply MSG_ID ...`   | Marks the replied-to message as read                           |
| `thrum message get MSG_ID` | Marks the retrieved message as read                            |

## Features Summary

- **Agent listing** -- List all agents with role/module filters and metadata
- **Message listing** -- Filter by author, scope, ref, mentions, unread status
- **Pagination** -- Configurable page size (max 100), page numbers, total counts
- **Sorting** -- By `created_at` or `updated_at`, ascending or descending
- **Reply-to references** -- Messages can reference parent messages via `reply_to` field
- **Read tracking** -- Per-session and per-agent read state, auto mark-as-read
- **Mention filtering** -- Filter by mention role (config-resolved or explicit)
- **Transport context** -- Both Unix socket and WebSocket supported

## Usage Examples

### Loading inbox for a CLI agent

```bash
# List unread messages mentioning the current agent's role
thrum inbox --mentions --unread

# Reply to a specific message
thrum reply msg_01HXE... "Here's my response"
```

### Loading inbox from the UI (WebSocket)

```javascript
// Get list of agents
const agents = await rpc("agent.list", {});

// Get unread messages mentioning a specific role
const inbox = await rpc("message.list", {
  mention_role: "reviewer",
  unread_for_agent: "furiosa",
  page_size: 50,
  sort_by: "created_at",
  sort_order: "desc",
});

// Mark messages as read
await rpc("message.markRead", {
  message_ids: inbox.messages.map((m) => m.message_id),
});
```

### Loading inbox from the MCP server

The MCP server uses the explicit `mention_role` and `unread_for_agent` filters
because it cannot access the local agent config directly:

```javascript
// MCP check_messages tool uses:
const messages = await rpc("message.list", {
  mention_role: agentRole,
  unread_for_agent: agentID,
  sort_by: "created_at",
  sort_order: "desc",
});
```

## Testing

All methods have comprehensive test coverage in:

- `internal/daemon/rpc/agent_test.go` -- Agent listing, filtering
- `internal/daemon/rpc/message_test.go` -- Message CRUD, pagination, sorting
- `internal/daemon/rpc/message_filter_test.go` -- Mention and unread filtering
- `internal/daemon/rpc/session_test.go` -- Session management
- `tests/e2e/messaging.spec.ts` -- End-to-end messaging scenarios

## See Also

- Full RPC API Reference: `docs/rpc-api.md`
- Messaging System (CLI): `docs/messaging.md`
- Daemon Architecture: `docs/daemon.md`
