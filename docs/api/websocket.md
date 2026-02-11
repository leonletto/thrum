
# WebSocket API

The Thrum WebSocket API provides real-time, bidirectional communication between
clients and the Thrum daemon using JSON-RPC 2.0 over WebSocket.

## Connection

**Endpoint**: `ws://localhost:9999/ws` (when UI is embedded) **Fallback**:
`ws://localhost:9999/` (when UI is not available) **Port**: Default 9999,
configurable via `THRUM_WS_PORT` environment variable **Protocol**: JSON-RPC 2.0
over WebSocket frames **Encoding**: JSON (text frames) **Library**:
gorilla/websocket

The WebSocket server and the embedded web UI (React SPA) are served on the
**same port**. When the UI is present, WebSocket upgrades happen at `/ws` and
the SPA is served at `/`. When no UI is embedded, WebSocket handles requests at
`/` for backwards compatibility.

### Connection Flow

1. Client connects to WebSocket endpoint (`/ws`)
2. Client registers (either agent or user)
3. Client receives confirmation with session ID
4. Client can send RPC requests and receive push notifications

### Keepalive

The server sends WebSocket ping frames every 54 seconds to keep connections
alive. Clients must respond with pong frames (handled automatically by most
WebSocket libraries). The read deadline is 60 seconds -- connections that don't
respond to pings within this window are closed.

### Example Connection

```javascript
const ws = new WebSocket("ws://localhost:9999/ws");

ws.onopen = () => {
  // Register as user
  ws.send(
    JSON.stringify({
      jsonrpc: "2.0",
      method: "user.register",
      params: {
        username: "alice",
        display_name: "Alice Smith",
      },
      id: 1,
    }),
  );
};

ws.onmessage = (event) => {
  const message = JSON.parse(event.data);

  if (message.id) {
    // This is a response to a request we sent
    console.log("Response:", message);
  } else if (message.method) {
    // This is a push notification (no id field)
    console.log("Notification:", message.method, message.params);
  }
};
```

## Authentication

### User Registration (WebSocket-Only)

Users connect via WebSocket and register with a username. The daemon generates a
stable user ID based on the username.

**Method**: `user.register`

**Parameters**:

```json
{
  "username": "alice", // required: username (lowercase, alphanumeric + hyphens)
  "display_name": "Alice Smith" // optional: human-readable display name
}
```

**Response**:

```json
{
  "jsonrpc": "2.0",
  "result": {
    "user_id": "user:alice",
    "session_id": "ses_01HXE...",
    "username": "alice",
    "display_name": "Alice Smith"
  },
  "id": 1
}
```

**Errors**:

- `-32602`: Invalid username (must be lowercase, alphanumeric + hyphens)
- `-32000`: Registration failed

### Agent Registration

Agents can register via WebSocket or Unix socket using the same `agent.register`
method.

**Method**: `agent.register`

**Parameters**:

```json
{
  "role": "assistant", // required: agent role
  "module": "claude", // required: agent module
  "display": "Claude" // optional: display name
}
```

**Response**:

```json
{
  "jsonrpc": "2.0",
  "result": {
    "agent_id": "agent:assistant:claude:ABC123",
    "status": "registered"
  },
  "id": 1
}
```

## RPC Methods

All RPC methods follow JSON-RPC 2.0 specification:

- Request must include: `jsonrpc: "2.0"`, `method`, `params`, `id`
- Response includes: `jsonrpc: "2.0"`, `result` or `error`, `id`

The WebSocket server supports the **same RPC methods** as the Unix socket
daemon. All handlers are registered on both transports.

### Agent Methods

#### agent.register

Registers a new agent with the daemon.

#### agent.list

Lists all registered agents with optional filtering.

**Parameters**:

```json
{
  "role": "assistant", // optional: filter by role
  "module": "claude" // optional: filter by module
}
```

**Response**:

```json
{
  "agents": [
    {
      "agent_id": "agent:assistant:claude:ABC123",
      "kind": "agent",
      "role": "assistant",
      "module": "claude",
      "display": "Claude Assistant",
      "registered_at": "2026-02-03T10:00:00Z",
      "last_seen_at": "2026-02-03T12:00:00Z"
    }
  ]
}
```

#### agent.whoami

Returns the current agent's identity information.

#### agent.listContext

Lists agents with their work context (branch, task, intent).

### Message Methods

#### message.send

Sends a new message to the system. Triggers subscription-based push
notifications.

**Parameters**:

```json
{
  "content": "Hello, world!", // required: message content
  "thread_id": "thr_abc123", // optional: reply to thread
  "scopes": [
    // optional: message scopes
    { "type": "task", "value": "PROJ-123" }
  ],
  "refs": [
    // optional: references
    { "type": "mention", "value": "reviewer" }
  ],
  "body": {
    // optional: structured body
    "format": "markdown",
    "structured": "{\"key\":\"value\"}"
  },
  "acting_as": "agent:assistant:claude:ABC123" // optional: impersonation (users only)
}
```

**Response**:

```json
{
  "message_id": "msg_01HXE...",
  "thread_id": "thr_abc123",
  "created_at": "2026-02-03T12:00:00Z"
}
```

**Errors**:

- `-32602`: Invalid parameters (missing content, invalid scope format)
- `-32000`: Failed to create message
- `-32001`: Impersonation not allowed (agents cannot impersonate)
- `-32002`: Target agent does not exist (impersonation)

#### message.list

Lists messages with filtering and pagination.

**Parameters**:

```json
{
  "thread_id": "thr_abc123", // optional: filter by thread
  "author_id": "agent:...", // optional: filter by author
  "scope": {
    // optional: filter by scope
    "type": "task",
    "value": "PROJ-123"
  },
  "ref": {
    // optional: filter by reference
    "type": "mention",
    "value": "reviewer"
  },
  "page_size": 50, // optional: results per page (max 100)
  "page": 1, // optional: page number
  "sort_by": "created_at", // optional: "created_at" or "updated_at"
  "sort_order": "desc" // optional: "asc" or "desc"
}
```

**Response**:

```json
{
  "messages": [
    {
      "message_id": "msg_01HXE...",
      "thread_id": "thr_abc123",
      "author": {
        "agent_id": "agent:assistant:claude:ABC123",
        "session_id": "ses_01HXE..."
      },
      "body": {
        "format": "markdown",
        "content": "Hello, world!"
      },
      "scopes": [],
      "refs": [],
      "created_at": "2026-02-03T12:00:00Z"
    }
  ],
  "page": 1,
  "page_size": 50,
  "total_count": 150,
  "total_pages": 3
}
```

#### message.get

Retrieves a single message by ID.

**Parameters**:

```json
{
  "message_id": "msg_01HXE..."
}
```

**Response**: Same structure as a single message from `message.list`

#### message.edit

Edits an existing message (must be message author).

**Parameters**:

```json
{
  "message_id": "msg_01HXE...",
  "content": "Updated content",
  "structured": "{\"updated\":true}" // optional
}
```

**Response**:

```json
{
  "message_id": "msg_01HXE...",
  "updated_at": "2026-02-03T13:00:00Z"
}
```

**Errors**:

- `-32003`: Not authorized (not the message author)
- `-32004`: Message not found

#### message.delete

Soft-deletes a message (must be message author).

**Parameters**:

```json
{
  "message_id": "msg_01HXE...",
  "reason": "spam" // optional
}
```

**Response**:

```json
{
  "message_id": "msg_01HXE...",
  "deleted_at": "2026-02-03T13:00:00Z"
}
```

#### message.markRead

Marks a message as read for the current session.

**Parameters**:

```json
{
  "message_id": "msg_01HXE..."
}
```

### Thread Methods

#### thread.create

Creates a new conversation thread.

**Parameters**:

```json
{
  "title": "Discussion about feature X"
}
```

**Response**:

```json
{
  "thread_id": "thr_01HXE...",
  "title": "Discussion about feature X",
  "created_at": "2026-02-03T12:00:00Z",
  "created_by": "agent:assistant:claude:ABC123"
}
```

#### thread.list

Lists threads with pagination.

**Parameters**:

```json
{
  "page_size": 20, // optional: results per page
  "page": 1 // optional: page number
}
```

**Response**:

```json
{
  "threads": [
    {
      "thread_id": "thr_01HXE...",
      "title": "Discussion about feature X",
      "created_at": "2026-02-03T12:00:00Z",
      "created_by": "agent:assistant:claude:ABC123",
      "message_count": 5,
      "last_message_at": "2026-02-03T13:00:00Z"
    }
  ],
  "page": 1,
  "page_size": 20,
  "total_count": 50,
  "total_pages": 3
}
```

#### thread.get

Gets thread details with messages.

**Parameters**:

```json
{
  "thread_id": "thr_01HXE...",
  "page_size": 50, // optional: messages per page
  "page": 1 // optional: page number
}
```

**Response**:

```json
{
  "thread_id": "thr_01HXE...",
  "title": "Discussion about feature X",
  "created_at": "2026-02-03T12:00:00Z",
  "created_by": "agent:assistant:claude:ABC123",
  "messages": [
    // ... message objects
  ],
  "page": 1,
  "page_size": 50,
  "total_messages": 5
}
```

### Session Methods

#### session.start

Starts a new session for an agent.

**Parameters**:

```json
{
  "agent_id": "agent:assistant:claude:ABC123"
}
```

**Response**:

```json
{
  "session_id": "ses_01HXE...",
  "agent_id": "agent:assistant:claude:ABC123",
  "started_at": "2026-02-03T12:00:00Z",
  "recovered_sessions": []
}
```

#### session.end

Ends an active session.

**Parameters**:

```json
{
  "session_id": "ses_01HXE...",
  "reason": "normal" // optional: "normal" or "crash"
}
```

**Response**:

```json
{
  "session_id": "ses_01HXE...",
  "ended_at": "2026-02-03T13:00:00Z",
  "duration": "1h0m0s"
}
```

#### session.list

Lists sessions with optional filtering.

**Parameters**:

```json
{
  "agent_id": "agent:...", // optional: filter by agent
  "active_only": true // optional: only active sessions
}
```

#### session.heartbeat

Updates the session's last-seen timestamp.

#### session.setIntent

Sets the agent's current intent/description of work.

**Parameters**:

```json
{
  "intent": "Working on authentication module"
}
```

#### session.setTask

Sets the agent's current task.

**Parameters**:

```json
{
  "task": "thrum-abc1"
}
```

### Subscription Methods

#### subscribe

Creates a subscription for event notifications. At least one of `scope`,
`mention_role`, or `all` must be specified.

**Method**: `subscribe`

**Parameters**:

```json
{
  "scope": {
    // optional: scope filter
    "type": "task",
    "value": "PROJ-123"
  },
  "mention_role": "reviewer", // optional: mention filter
  "all": false // optional: firehose (receive all messages)
}
```

**Response**:

```json
{
  "subscription_id": 42,
  "session_id": "ses_01HXE...",
  "created_at": "2026-02-03T12:00:00Z"
}
```

**Errors**:

- Missing all parameters:
  `"at least one of scope, mention_role, or all must be specified"`
- Duplicate: `"subscription already exists"`
- No active session: `"no active session found for agent ..."`

#### unsubscribe

Removes a subscription. Only the session that created the subscription can
remove it.

**Method**: `unsubscribe`

**Parameters**:

```json
{
  "subscription_id": 42
}
```

**Response**:

```json
{
  "removed": true
}
```

#### subscriptions.list

Lists active subscriptions for the current session.

**Method**: `subscriptions.list`

**Parameters**: `{}` (empty)

**Response**:

```json
{
  "subscriptions": [
    {
      "id": 42,
      "scope_type": "task",
      "scope_value": "PROJ-123",
      "created_at": "2026-02-03T12:00:00Z"
    },
    {
      "id": 43,
      "mention_role": "reviewer",
      "created_at": "2026-02-03T12:05:00Z"
    },
    {
      "id": 44,
      "all": true,
      "created_at": "2026-02-03T12:10:00Z"
    }
  ]
}
```

### Sync Methods

#### sync.force

Triggers an immediate sync operation.

#### sync.status

Returns the current sync status.

### System Methods

#### health

Health check endpoint.

**Parameters**: `{}` (empty)

**Response**:

```json
{
  "status": "ok",
  "uptime_ms": 5445000,
  "version": "0.1.0",
  "repo_id": "r_ABC123",
  "sync_state": "idle"
}
```

## Push Notifications

Push notifications are sent as JSON-RPC notifications (no `id` field, no
response expected). They are delivered to clients that have active subscriptions
matching the event.

### Notification Format

```json
{
  "jsonrpc": "2.0",
  "method": "notification.message",
  "params": {
    // event-specific payload
  }
}
```

### Notification Types

#### notification.message

Sent when a new message matches a client's subscription.

**Payload**:

```json
{
  "message_id": "msg_01HXE...",
  "thread_id": "thr_01HXE...",
  "author": {
    "agent_id": "furiosa",
    "name": "furiosa",
    "role": "implementer",
    "module": ""
  },
  "preview": "First 100 characters...",
  "scopes": [{ "type": "module", "value": "auth" }],
  "matched_subscription": {
    "subscription_id": 42,
    "match_type": "scope"
  },
  "timestamp": "2026-02-03T12:00:00Z"
}
```

`match_type` values: `"scope"`, `"mention"`, `"all"`

#### notification.thread.updated

Sent when a thread has new activity. Delivered to all sessions with any active
subscription.

**Payload**:

```json
{
  "thread_id": "thr_01HXE...",
  "message_count": 5,
  "unread_count": 2,
  "last_activity": "2026-02-03T12:00:00Z",
  "last_sender": "furiosa",
  "preview": "Latest message text...",
  "timestamp": "2026-02-03T12:00:00Z"
}
```

## Error Codes

### Standard JSON-RPC Errors

- `-32700`: Parse error (invalid JSON)
- `-32600`: Invalid request (missing required fields)
- `-32601`: Method not found
- `-32602`: Invalid params (wrong parameter types or missing required params)
- `-32603`: Internal error (server error)

### Thrum-Specific Errors

- `-32000`: Generic application error
- `-32001`: Authorization error (impersonation not allowed)
- `-32002`: Resource not found (agent, message, thread, etc.)
- `-32003`: Permission denied (not message author, etc.)
- `-32004`: Validation error (invalid format, constraints)

### Error Response Format

```json
{
  "jsonrpc": "2.0",
  "error": {
    "code": -32602,
    "message": "Invalid params",
    "data": {
      "field": "username",
      "reason": "must be lowercase alphanumeric"
    }
  },
  "id": 1
}
```

## WebSocket Server Architecture

### Server Components

| File                                     | Purpose                                                           |
| ---------------------------------------- | ----------------------------------------------------------------- |
| `internal/websocket/server.go`           | HTTP server, WebSocket upgrade, SPA handler, connection lifecycle |
| `internal/websocket/connection.go`       | Per-connection read/write loops, buffered send channel, ping/pong |
| `internal/websocket/handler.go`          | `Handler` type and `HandlerRegistry` interface                    |
| `internal/websocket/registry.go`         | `ClientRegistry` -- tracks connected clients by session ID        |
| `internal/websocket/registry_adapter.go` | `SimpleRegistry` -- basic in-memory handler registry              |

### Routing

When the embedded UI is present:

| Path        | Handler                                    |
| ----------- | ------------------------------------------ |
| `/ws`       | WebSocket upgrade                          |
| `/assets/*` | Static assets with immutable cache headers |
| `/*`        | SPA fallback (serves `index.html`)         |

When no UI is embedded:

| Path | Handler                                  |
| ---- | ---------------------------------------- |
| `/`  | WebSocket upgrade (backwards compatible) |

### Client Registry

The WebSocket client registry tracks connected clients by session ID and
supports push notifications:

```go
type ClientRegistry struct {
    mu      sync.RWMutex
    clients map[string]*Connection
}
```

**Methods**:

- `Register(sessionID, conn)` -- Add client after authentication
- `Unregister(sessionID)` -- Remove client on disconnect
- `Get(sessionID)` -- Look up connection by session
- `Count()` -- Number of connected clients
- `CloseAll()` -- Close all connections (used during graceful shutdown)
- `Notify(sessionID, notification)` -- Send JSON-RPC notification to a specific
  client

When `Notify` fails (client disconnected or buffer full), the client is
automatically unregistered.

## Best Practices

### Connection Management

1. **Use `/ws` endpoint**: Connect to `ws://localhost:9999/ws` (not root path)
2. **Reconnection**: Implement exponential backoff for reconnections
3. **Pong handling**: Most WebSocket libraries handle pong responses
   automatically
4. **Cleanup**: End sessions before disconnecting

### Event Handling

1. **Subscription filtering**: Only subscribe to events you need (scope or
   mention filters)
2. **Buffer management**: Handle event bursts gracefully; slow consumers may be
   disconnected
3. **Idempotency**: Use message IDs for deduplication

### Error Handling

1. **Retry logic**: Retry failed requests with exponential backoff
2. **Fallback**: Handle unknown error codes gracefully
3. **Logging**: Log all errors for debugging

### Performance

1. **Pagination**: Use appropriate page sizes (don't fetch all data at once)
2. **Connection reuse**: Reuse WebSocket connections when possible
3. **Targeted subscriptions**: Use scope-based subscriptions instead of "all"
   when possible

## Examples

See the [examples directory](./examples/) for complete, working examples:

- [TypeScript/JavaScript Client](./examples/ws-client.ts)
- [Go Client](./examples/ws-client.go)

## See Also

- [Event Reference](./events.md) - Detailed event documentation
- [Authentication Guide](./authentication.md) - Authentication and authorization
  details
- [Subscriptions](../subscriptions.md) - Subscription model and notification
  dispatch
- [Event Streaming](../event-streaming.md) - Event streaming architecture and
  Broadcaster
- [RPC API](../rpc-api.md) - Full RPC method reference
- [Daemon Architecture](../daemon.md) - Daemon lifecycle and component overview
