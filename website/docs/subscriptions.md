---
title: "Subscriptions & Notifications"
description:
  "Real-time push notification system with scope, mention, and all-message
  filters - subscription lifecycle and dispatch"
category: "messaging"
order: 2
tags:
  [
    "subscriptions",
    "notifications",
    "push",
    "filtering",
    "dispatcher",
    "broadcaster",
  ]
last_updated: "2026-02-10"
---

# Subscriptions & Notifications

## Overview

The subscription system allows agents to receive real-time push notifications
when messages match their interests. Agents can subscribe to:

- **Scopes** - Messages with specific scope (e.g., `module:auth`,
  `file:main.go`)
- **Mentions** - Messages that @mention a specific role (e.g., `@reviewer`) or
  agent name (e.g., `@furiosa`)
- **All messages** - Wildcard subscription to receive all messages

When a new message matches a subscription, the daemon:

1. Identifies matching subscriptions via the **dispatcher**
2. Sends **push notifications** to connected clients via the **broadcaster**
3. Stores the message for later retrieval via `message.list` API

## Architecture

### Components

```
+-------------------------------------------------------------+
|                      Message Flow                            |
+-------------------------------------------------------------+

  message.send RPC
       |
       v
  +--------------+
  | Message      |  1. Write to JSONL (sharded per-agent)
  | Handler      |  2. Insert into SQLite
  +---------+----+  3. Extract scopes/refs
            |
            v
  +--------------+
  | Dispatcher   |  1. Query all subscriptions
  |              |  2. Match against message (scope/mention/all)
  +---------+----+  3. Build notifications
            |
            v
  +--------------+
  | Broadcaster  |  1. Try Unix socket clients first
  |              |  2. Fall back to WebSocket clients
  +---------+----+  3. Best-effort delivery
            |
       +----+----+
       |         |
       v         v
  Unix Socket   WebSocket
  Clients       Clients (port 9999)
       |         |
       v         v
    Connected clients receive notification.message
```

### Database Schema

**subscriptions table:**

```sql
CREATE TABLE subscriptions (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id   TEXT NOT NULL,
  scope_type   TEXT,                    -- NULL for non-scope subscriptions
  scope_value  TEXT,                   -- NULL for non-scope subscriptions
  mention_role TEXT,                  -- NULL for non-mention subscriptions
  created_at   TEXT NOT NULL,
  UNIQUE(session_id, scope_type, scope_value, mention_role)
);

-- Indexes for efficient matching
CREATE INDEX idx_subscriptions_scope ON subscriptions(scope_type, scope_value);
CREATE INDEX idx_subscriptions_mention ON subscriptions(mention_role);
CREATE INDEX idx_subscriptions_session ON subscriptions(session_id);
```

**Note:** The subscriptions table does not have a foreign key constraint on
`session_id`. Subscription cleanup on session end is handled at the application
level.

**Subscription types (mutually exclusive):**

| Type    | scope_type | scope_value | mention_role | Description                               |
| ------- | ---------- | ----------- | ------------ | ----------------------------------------- |
| Scope   | `"module"` | `"auth"`    | `NULL`       | Matches messages with scope `module:auth` |
| Mention | `NULL`     | `NULL`      | `"reviewer"` | Matches messages with `@reviewer` mention |
| All     | `NULL`     | `NULL`      | `NULL`       | Matches all messages (wildcard)           |

### Duplicate Prevention

SQLite's `UNIQUE` constraint doesn't work correctly with NULL values (treats
each NULL as unique). We implement **application-level duplicate checking** in
`service.go`:

```go
func (s *Service) subscriptionExists(...) (bool, error) {
    // Build query with explicit NULL checks for each combination
    if scopeType != nil && scopeValue != nil {
        query = "WHERE session_id = ? AND scope_type = ? AND scope_value = ?"
    } else if mentionRole != nil {
        query = "WHERE session_id = ? AND mention_role = ?"
    } else {
        query = "WHERE session_id = ? AND scope_type IS NULL AND ..."
    }
}
```

## Subscription Lifecycle

### Creating Subscriptions

1. Client calls `subscribe` RPC with subscription criteria
2. Handler validates:
   - Session is active (resolved from agent identity config)
   - At least one of scope, mention_role, or all specified
   - No duplicate subscription exists (application-level check)
3. Insert into `subscriptions` table
4. Return subscription ID

**Example:**

```json
// Request
{
  "jsonrpc": "2.0",
  "method": "subscribe",
  "params": {"scope": {"type": "module", "value": "auth"}},
  "id": 1
}

// Response
{
  "jsonrpc": "2.0",
  "result": {
    "subscription_id": 42,
    "session_id": "ses_01HXE...",
    "created_at": "2026-02-03T10:00:00Z"
  },
  "id": 1
}
```

### Removing Subscriptions

1. Client calls `unsubscribe` RPC with subscription ID
2. Handler verifies subscription belongs to current session
3. Delete from `subscriptions` table
4. Return `{"removed": true}` or `{"removed": false}` (idempotent)

**Example:**

```json
// Request
{
  "jsonrpc": "2.0",
  "method": "unsubscribe",
  "params": {"subscription_id": 42},
  "id": 2
}

// Response
{
  "jsonrpc": "2.0",
  "result": {"removed": true},
  "id": 2
}
```

### Listing Subscriptions

```json
// Request
{
  "jsonrpc": "2.0",
  "method": "subscriptions.list",
  "id": 1
}

// Response
{
  "jsonrpc": "2.0",
  "result": {
    "subscriptions": [
      {
        "id": 42,
        "scope_type": "module",
        "scope_value": "auth",
        "created_at": "2026-02-03T10:00:00Z"
      },
      {
        "id": 43,
        "mention_role": "reviewer",
        "created_at": "2026-02-03T10:05:00Z"
      },
      {
        "id": 44,
        "all": true,
        "created_at": "2026-02-03T10:10:00Z"
      }
    ]
  },
  "id": 1
}
```

## Message Dispatch

### Matching Algorithm

When `message.send` is called, the dispatcher:

1. **Query all subscriptions** from database (joins with sessions and agents
   tables for mention resolution)
2. **For each subscription**, check if message matches:
   - **Scope match**: Any message scope matches subscription scope
   - **Mention match**: Any message ref has `type="mention"` and matches the
     subscription's `mention_role`, the agent's role, or the agent's ID/name
   - **All match**: Always matches (wildcard)
3. **Build notification** for each match
4. **Push to connected clients** via the Broadcaster

**Implementation (`dispatcher.go`):**

```go
// matchSubscription checks if a message matches a subscription.
// Supports both role-based mentions (@reviewer) and name-based mentions (@furiosa).
func matchSubscription(msg *MessageInfo, scopeType, scopeValue, mentionRole, agentID, agentRole sql.NullString) string {
    // All subscription - always matches
    if !scopeType.Valid && !scopeValue.Valid && !mentionRole.Valid {
        return "all"
    }

    // Scope subscription
    if scopeType.Valid && scopeValue.Valid {
        for _, scope := range msg.Scopes {
            if scope.Type == scopeType.String && scope.Value == scopeValue.String {
                return "scope"
            }
        }
    }

    // Mention subscription - matches on:
    // 1. subscription's mention_role
    // 2. agent's role (for role-based mentions)
    // 3. agent's ID/name (for name-based mentions like @furiosa)
    if mentionRole.Valid {
        for _, ref := range msg.Refs {
            if ref.Type == "mention" {
                if ref.Value == mentionRole.String {
                    return "mention"
                }
                if agentRole.Valid && ref.Value == agentRole.String {
                    return "mention"
                }
                if agentID.Valid && ref.Value == agentID.String {
                    return "mention"
                }
            }
        }
    }

    return "" // No match
}
```

### Notification Building

For each match, the dispatcher builds a notification payload:

```json
{
  "method": "notification.message",
  "params": {
    "message_id": "msg_01HXE...",
    "thread_id": "thr_01HXE...",
    "author": {
      "agent_id": "furiosa",
      "name": "furiosa",
      "role": "implementer",
      "module": ""
    },
    "preview": "First 100 characters of content...",
    "scopes": [{ "type": "module", "value": "auth" }],
    "matched_subscription": {
      "subscription_id": 42,
      "match_type": "scope"
    },
    "timestamp": "2026-02-03T10:00:00Z"
  }
}
```

**Author parsing:**

- Uses `identity.ParseAgentID()` to extract the role from the agent ID
- The `name` field is set to the raw agent ID (which is the agent's name for
  named agents)
- The `module` field is empty -- module is not encoded in the agent ID and would
  require a database lookup

**Preview truncation:**

- If content is 100 chars or less: Use as-is
- If content exceeds 100 chars: Truncate to 100 chars and append `"..."`

### Thread Update Notifications

The dispatcher also sends `notification.thread.updated` events to all subscribed
sessions when a thread is updated:

```json
{
  "method": "notification.thread.updated",
  "params": {
    "thread_id": "thr_01HXE...",
    "message_count": 5,
    "unread_count": 2,
    "last_activity": "2026-02-03T10:00:00Z",
    "last_sender": "furiosa",
    "preview": "Latest message text...",
    "timestamp": "2026-02-03T10:00:00Z"
  }
}
```

Thread update notifications are sent to **all** sessions with any active
subscription (not filtered by scope/mention). These are real-time only and not
persisted to JSONL.

## Push Notifications

### Broadcaster

The daemon uses a `Broadcaster` (`internal/daemon/broadcaster.go`) that
implements the `ClientNotifier` interface. It tries both transport registries in
order:

1. **Unix socket clients** first (via `ClientRegistry` from
   `internal/daemon/notify.go`)
2. **WebSocket clients** as fallback (via `ClientRegistry` from
   `internal/websocket/registry.go`)

If the notification is delivered successfully via either transport, the
Broadcaster returns immediately. This means each session only receives one
notification per match, regardless of how many transports are available.

### Unix Socket Client Registry

**Location:** `internal/daemon/notify.go`

```go
type ClientRegistry struct {
    mu      sync.RWMutex
    clients map[string]*ConnectedClient
}

type ConnectedClient struct {
    sessionID string
    conn      net.Conn
}
```

**Operations:**

- `Register(sessionID, conn)` - Add client when they connect
- `Unregister(sessionID)` - Remove client on disconnect
- `Notify(sessionID, *Notification)` - Send notification with newline framing

### WebSocket Client Registry

**Location:** `internal/websocket/registry.go`

```go
type ClientRegistry struct {
    mu      sync.RWMutex
    clients map[string]*Connection
}
```

**Operations:**

- `Register(sessionID, conn)` - Add client when they connect
- `Unregister(sessionID)` - Remove client on disconnect
- `Get(sessionID)` - Look up client by session ID
- `Count()` - Number of connected clients
- `CloseAll()` - Close all connections (used during shutdown)
- `Notify(sessionID, notification)` - Send JSON-RPC notification via WebSocket
  frame

### Sending Notifications

When the dispatcher finds matches, it calls `Broadcaster.Notify()` for each:

1. **Lookup session** in Unix socket registry, then WebSocket registry
   - If not found in either: Silently succeed (client will see message via
     `message.list`)
2. **Marshal notification** to JSON-RPC format
3. **Write to transport**:
   - Unix socket: Newline-delimited JSON
   - WebSocket: Text frame via buffered send channel (256-message buffer)
4. **Handle errors**:
   - Write error: Client disconnected - auto-unregister
   - Buffer full (WebSocket): Client disconnected - auto-unregister
   - Success: Continue

**JSON-RPC notification format:**

```json
{
  "jsonrpc": "2.0",
  "method": "notification.message",
  "params": {
    /* NotifyParams */
  }
}
```

**Note:** No `id` field - notifications are one-way, no response expected.

### Connection Management

**Client responsibilities:**

1. Keep connection open during session
2. Listen for incoming notifications
3. Parse JSON-RPC notifications (no `id` field)
4. Fetch full message content with `message.get`

**Daemon responsibilities:**

1. Track connected clients by session
2. Auto-unregister on write errors
3. Don't block message.send on notification failures
4. Silently ignore notifications to disconnected clients

## Testing

### Coverage

**Key test scenarios:**

1. Subscription CRUD (create, list, unsubscribe)
2. Duplicate prevention (all subscription types)
3. Scope matching (exact match, multiple scopes, no match)
4. Mention matching (role-based @reviewer, name-based @furiosa)
5. All subscription matching (wildcard)
6. Multiple subscriptions per session
7. Client registry (register, unregister, notify) for both Unix socket and
   WebSocket
8. Notification format (JSON-RPC, field validation)
9. Preview truncation (short, long, exact 100 chars)
10. Disconnected client handling (auto-unregister)
11. Broadcaster routing (Unix socket first, WebSocket fallback)
12. Thread update notifications

### Test Patterns

**Database tests** use temp directories:

```go
tmpDir := t.TempDir()
db, err := schema.OpenDB(filepath.Join(tmpDir, "test.db"))
defer db.Close()
```

**Unix socket connection tests** use `net.Pipe()`:

```go
server, client := net.Pipe()
defer server.Close()
defer client.Close()

// IMPORTANT: net.Pipe() is synchronous - use goroutines
go func() {
    buf := make([]byte, 1024)
    n, _ := client.Read(buf)
    // Process buffer
}()
registry.Notify("ses_001", notification)
```

## Performance Considerations

### Matching Efficiency

**Current implementation:**

- Load ALL subscriptions from DB on every message
- O(N) matching where N = number of subscriptions
- Joins with sessions and agents tables for mention resolution

**Rationale for simple approach:**

- Expected subscription count: < 100 per daemon instance
- Message send frequency: < 10/second
- Premature optimization avoided - measure first

### Notification Delivery

**Current:**

- Synchronous notification sending via Broadcaster
- Blocks message.send briefly

**Trade-off:**

- Simplicity vs. throughput
- Current approach is correct and maintainable
- Optimize when proven necessary

## Error Handling

### Subscription Errors

| Scenario               | Behavior                                                                       |
| ---------------------- | ------------------------------------------------------------------------------ |
| Duplicate subscription | Return error `"subscription already exists"`                                   |
| Invalid session        | Return error `"no active session found"`                                       |
| Missing parameters     | Return error `"at least one of scope, mention_role, or all must be specified"` |
| Database error         | Return error with details                                                      |

### Notification Errors

| Scenario                 | Behavior                            |
| ------------------------ | ----------------------------------- |
| Client not connected     | Silently succeed (client will poll) |
| Write error (disconnect) | Auto-unregister, return error       |
| Buffer full (WebSocket)  | Auto-unregister, return error       |
| Marshal error            | Return error (should never happen)  |

### Recovery

**Client reconnection:**

- Registry tracks by session ID
- Re-register on reconnect
- Previous subscriptions still active (tied to session, not connection)

## Design Notes

### Why Application-Level Duplicate Checking?

SQLite's `UNIQUE` constraint fails with NULL values:

- `(ses_001, NULL, NULL, NULL)` can be inserted multiple times
- SQLite treats NULL != NULL in uniqueness checks

**Alternatives considered:**

1. Use empty string `""` instead of NULL - Semantically incorrect
2. Separate tables per subscription type - Over-engineered
3. **Application-level checking** - Simple, correct, maintainable (chosen)

### Why Silently Ignore Disconnected Clients?

When a notification can't be sent (client disconnected):

1. Message is already in database (via `message.send`)
2. Client will see it when they call `message.list`
3. Failing the entire `message.send` would be wrong

**Design:**

- Notifications are **best-effort delivery**
- Database is **source of truth**
- Clients must poll `message.list` on reconnect

### Why Preview Truncation?

- Full message content can be large (megabytes)
- Notifications should be lightweight
- Clients can fetch full content with `message.get`
- 100 chars is enough for preview/triage

## References

- RPC API: `docs/rpc-api.md`
- Messaging: `docs/messaging.md`
- Daemon Architecture: `docs/daemon.md`
- Event Streaming: `docs/event-streaming.md`
- WebSocket API: `docs/api/websocket.md`
- SQLite Schema: `internal/schema/schema.go`
- Subscription Service: `internal/subscriptions/service.go`
- Dispatcher: `internal/subscriptions/dispatcher.go`
- Broadcaster: `internal/daemon/broadcaster.go`
- Unix Socket Client Registry: `internal/daemon/notify.go`
- WebSocket Client Registry: `internal/websocket/registry.go`
