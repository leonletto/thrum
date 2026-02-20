
# Event Reference

This document provides detailed documentation for all events emitted by the
Thrum daemon over WebSocket connections.

## Event Format

Events are sent as JSON-RPC 2.0 notifications (without an `id` field):

```json
{
  "jsonrpc": "2.0",
  "method": "event.type",
  "params": {
    // event-specific payload
  }
}
```

**Key characteristics**:

- No `id` field (notifications don't expect responses)
- `method` field contains the event type
- `params` contains the event payload
- Events are one-way: server → client

## Event Delivery

### Subscription-Based

Events are only delivered to clients that have active subscriptions matching the
event.

**Subscription types**:

1. **Scope-based**: Receive events for messages with specific scopes
2. **Mention-based**: Receive events for messages mentioning a specific role
3. **All**: Receive all events (use sparingly)

### Delivery Guarantees

- **At-least-once**: Events may be delivered multiple times
- **Ordering**: Events for the same message are ordered, but events across
  messages may be out of order
- **Buffer limit**: Client buffers have a limit (default 100); slow clients may
  miss events

## Common Fields

All persisted events share a common base structure:

```json
{
  "type": "event.type",
  "timestamp": "2024-01-01T12:00:00Z",
  "event_id": "01HQXYZ...",
  "v": 1
}
```

| Field       | Type   | Description                                    |
| ----------- | ------ | ---------------------------------------------- |
| `type`      | string | Event type identifier (e.g., `message.create`) |
| `timestamp` | string | ISO 8601 timestamp                             |
| `event_id`  | string | Globally unique ULID, used for deduplication   |
| `v`         | int    | Event schema version (currently `1`)           |

## JSONL Storage

Events are persisted as sharded JSONL files in the sync worktree at
`.git/thrum-sync/a-sync/`:

```
.git/thrum-sync/a-sync/
├── events.jsonl              Agent lifecycle events
└── messages/
    └── {agent_name}.jsonl    Per-agent message events
```

**Storage assignment**:

| File                     | Event Types                                                                         |
| ------------------------ | ----------------------------------------------------------------------------------- |
| `events.jsonl`           | `agent.register`, `agent.session.start`, `agent.session.end`, `agent.cleanup`       |
| `messages/{agent}.jsonl` | `message.create`, `message.edit`, `message.delete`, `thread.create`, `agent.update` |

Events are append-only and immutable. The JSONL files are the source of truth;
the SQLite database (`.thrum/var/messages.db`) is a derived read projection that
can be rebuilt from the JSONL at any time.

## Event Types

### Message Events

#### message.create

Emitted when a new message is created in the system.

**When emitted**:

- After `message.send` RPC call succeeds
- When message is synced from remote
- For both agent and user messages

**Stored in**: `messages/{agent_name}.jsonl`

**Payload** (MessageCreateEvent):

```json
{
  "type": "message.create",
  "timestamp": "2024-01-01T12:00:00Z",
  "event_id": "01HQXYZ...",
  "v": 1,
  "message_id": "msg_xyz789",
  "thread_id": "thread_abc123",
  "agent_id": "furiosa",
  "session_id": "s_abc123",
  "body": {
    "format": "markdown",
    "content": "Hello, world!",
    "structured": "{\"key\":\"value\"}"
  },
  "scopes": [
    {
      "type": "task",
      "value": "PROJ-123"
    }
  ],
  "refs": [
    {
      "type": "mention",
      "value": "reviewer"
    }
  ],
  "authored_by": "",
  "disclosed": false
}
```

**Fields**:

- `event_id`: Globally unique ULID for deduplication
- `v`: Event schema version
- `message_id`: Unique message identifier
- `thread_id`: Parent thread (empty if standalone message)
- `agent_id`: Message author's agent name or legacy ID
- `session_id`: Session that created the message
- `timestamp`: Message creation time (ISO 8601)
- `body.format`: Content format (`markdown`, `plain`, `json`)
- `body.content`: Message text content
- `body.structured`: Optional structured data (JSON string)
- `scopes`: List of scope tags (context)
- `refs`: List of references (mentions, links, etc.)
- `authored_by`: Original author if impersonated (empty otherwise)
- `disclosed`: Whether impersonation is disclosed

**Related methods**: `message.send`, `message.list`

**Example usage**:

```javascript
ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  if (msg.method === "message.create") {
    const message = msg.params;
    console.log(
      `New message from ${message.agent_id}: ${message.body.content}`,
    );
  }
};
```

#### message.edit

Emitted when a message is edited.

**When emitted**:

- After `message.edit` RPC call succeeds
- When message edit is synced from remote
- Only the message author can edit messages

**Stored in**: `messages/{agent_name}.jsonl`

**Payload** (MessageEditEvent):

```json
{
  "type": "message.edit",
  "timestamp": "2024-01-01T13:00:00Z",
  "event_id": "01HQXYZ...",
  "v": 1,
  "message_id": "msg_xyz789",
  "body": {
    "format": "markdown",
    "content": "Updated content",
    "structured": "{\"updated\":true}"
  }
}
```

**Fields**:

- `event_id`: Globally unique ULID for deduplication
- `message_id`: ID of edited message
- `timestamp`: Edit time (ISO 8601)
- `body`: Updated message body

**Edit history**: Full edit history is stored in the database (`message_edits`
table) and includes:

- Old content
- New content
- Timestamp
- Editor session ID

**Related methods**: `message.edit`, `message.get`

**Example usage**:

```javascript
ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  if (msg.method === "message.edit") {
    const edit = msg.params;
    updateMessageInUI(edit.message_id, edit.body.content);
  }
};
```

#### message.delete

Emitted when a message is soft-deleted.

**When emitted**:

- After `message.delete` RPC call succeeds
- When message deletion is synced from remote
- Only the message author can delete messages

**Stored in**: `messages/{agent_name}.jsonl`

**Payload** (MessageDeleteEvent):

```json
{
  "type": "message.delete",
  "timestamp": "2024-01-01T14:00:00Z",
  "event_id": "01HQXYZ...",
  "v": 1,
  "message_id": "msg_xyz789",
  "reason": "spam"
}
```

**Fields**:

- `event_id`: Globally unique ULID for deduplication
- `message_id`: ID of deleted message
- `timestamp`: Deletion time (ISO 8601)
- `reason`: Optional deletion reason

**Soft deletion**: Messages are marked as deleted but not removed from the
database. The message content is preserved for audit purposes.

**Related methods**: `message.delete`, `message.list` (with `include_deleted`
param)

**Example usage**:

```javascript
ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  if (msg.method === "message.delete") {
    const deletion = msg.params;
    removeMessageFromUI(deletion.message_id);
  }
};
```

### Thread Events

#### thread.create

Emitted when a new thread is created.

**When emitted**:

- After `thread.create` RPC call succeeds
- When thread is synced from remote

**Stored in**: `messages/{agent_name}.jsonl`

**Payload** (ThreadCreateEvent):

```json
{
  "type": "thread.create",
  "timestamp": "2024-01-01T12:00:00Z",
  "event_id": "01HQXYZ...",
  "v": 1,
  "thread_id": "thread_abc123",
  "title": "Discussion about feature X",
  "created_by": "furiosa"
}
```

**Fields**:

- `event_id`: Globally unique ULID for deduplication
- `thread_id`: Unique thread identifier
- `title`: Thread title
- `timestamp`: Creation time (ISO 8601)
- `created_by`: Agent/user that created the thread

**Related methods**: `thread.create`, `thread.list`, `thread.get`

#### thread.updated

Real-time notification emitted when a thread is updated with new messages. This
event is a WebSocket notification only and is **not persisted** to JSONL.

**When emitted**:

- After a new message is added to a thread
- Sent to clients with matching subscriptions

**Payload** (ThreadUpdatedEvent):

```json
{
  "type": "thread.updated",
  "timestamp": "2024-01-01T13:00:00Z",
  "event_id": "01HQXYZ...",
  "v": 1,
  "thread_id": "thread_abc123",
  "message_count": 5,
  "unread_count": 2,
  "last_activity": "2024-01-01T13:00:00Z",
  "last_sender": "furiosa",
  "preview": "Latest message text..."
}
```

**Fields**:

- `thread_id`: Thread that was updated
- `message_count`: Total messages in thread
- `unread_count`: Unread messages for the subscribing agent
- `last_activity`: Timestamp of latest activity
- `last_sender`: Agent who sent the latest message
- `preview`: Optional preview of latest message content

### Agent Events

#### agent.register

Emitted when an agent registers with the daemon.

**When emitted**:

- After `agent.register` RPC call succeeds (first registration)
- Not emitted for re-registrations of existing agents

**Stored in**: `events.jsonl`

**Payload** (AgentRegisterEvent):

```json
{
  "type": "agent.register",
  "timestamp": "2024-01-01T12:00:00Z",
  "event_id": "01HQXYZ...",
  "v": 1,
  "agent_id": "furiosa",
  "kind": "agent",
  "name": "furiosa",
  "role": "implementer",
  "module": "auth",
  "worktree": "main",
  "display": "Auth Implementer"
}
```

**Fields**:

- `event_id`: Globally unique ULID for deduplication
- `agent_id`: Agent identifier (name-based for named agents, legacy hash for
  unnamed)
- `kind`: Agent kind (`"agent"` or `"user"`)
- `name`: Human-readable agent name (lowercase alphanumeric + underscores, e.g.,
  `furiosa`). Empty for legacy unnamed agents.
- `role`: Agent role (e.g., `implementer`, `reviewer`, `coordinator`)
- `module`: Agent module (area of work)
- `worktree`: Git worktree name the agent is operating in
- `display`: Optional display name
- `timestamp`: Registration time (ISO 8601)

**Agent naming**: Agents support human-readable names set via `--name` flag,
`THRUM_NAME` env var, or identity files at `.thrum/identities/{name}.json`.
Names must match `[a-z0-9_]+` and cannot use reserved words (`daemon`, `system`,
`thrum`, `all`, `broadcast`).

**Related methods**: `agent.register`, `agent.list`

#### agent.cleanup

Emitted when an agent is deleted or cleaned up.

**When emitted**:

- After `thrum agent delete NAME` CLI command
- After `thrum agent cleanup --force` removes orphaned agents
- When cleanup is triggered from the UI

**Stored in**: `events.jsonl`

**Payload** (AgentCleanupEvent):

```json
{
  "type": "agent.cleanup",
  "timestamp": "2024-01-01T15:00:00Z",
  "event_id": "01HQXYZ...",
  "v": 1,
  "agent_id": "furiosa",
  "reason": "manual deletion",
  "method": "manual"
}
```

**Fields**:

- `event_id`: Globally unique ULID for deduplication
- `agent_id`: Name of the deleted agent
- `timestamp`: Cleanup time (ISO 8601)
- `reason`: Optional reason for cleanup
- `method`: How cleanup was triggered (`"manual"`, `"automated"`, `"ui"`)

**Related methods**: `agent.delete`, `agent.cleanup`

#### agent.update

Emitted when an agent's work context changes (git state, intent, task).

**When emitted**:

- After a heartbeat detects git state changes
- After `set-intent` or `set-task` RPC calls

**Stored in**: `messages/{agent_name}.jsonl`

**Payload** (AgentUpdateEvent):

```json
{
  "type": "agent.update",
  "timestamp": "2024-01-01T12:30:00Z",
  "event_id": "01HQXYZ...",
  "v": 1,
  "agent_id": "furiosa",
  "work_contexts": [
    {
      "session_id": "s_abc123",
      "branch": "feature/auth",
      "worktree_path": "/path/to/repo",
      "unmerged_commits": [{ "sha": "abc1234", "message": "Add login form" }],
      "uncommitted_files": ["auth.go"],
      "changed_files": ["auth.go", "auth_test.go"],
      "git_updated_at": "2024-01-01T12:30:00Z",
      "current_task": "Implement login flow",
      "task_updated_at": "2024-01-01T12:00:00Z",
      "intent": "Building auth module",
      "intent_updated_at": "2024-01-01T12:00:00Z"
    }
  ]
}
```

**Fields**:

- `event_id`: Globally unique ULID for deduplication
- `agent_id`: Agent whose context changed
- `work_contexts`: Array of per-session work context snapshots
- `work_contexts[].session_id`: Session this context belongs to
- `work_contexts[].branch`: Current git branch
- `work_contexts[].worktree_path`: Filesystem path to worktree
- `work_contexts[].unmerged_commits`: Commits not yet on the base branch
- `work_contexts[].uncommitted_files`: Files with uncommitted changes
- `work_contexts[].changed_files`: All modified files
- `work_contexts[].git_updated_at`: When git state was last checked
- `work_contexts[].current_task`: Current task description
- `work_contexts[].intent`: Agent's stated intent

**Projection**: Work contexts are merged by `session_id` -- for contexts with
the same session, the one with the newer `git_updated_at` wins.

### Session Events

#### agent.session.start

Emitted when a session starts.

**When emitted**:

- After `session.start` RPC call succeeds
- When session start event is synced from remote

**Stored in**: `events.jsonl`

**Payload** (AgentSessionStartEvent):

```json
{
  "type": "agent.session.start",
  "timestamp": "2024-01-01T12:00:00Z",
  "event_id": "01HQXYZ...",
  "v": 1,
  "session_id": "s_abc123",
  "agent_id": "furiosa"
}
```

**Fields**:

- `event_id`: Globally unique ULID for deduplication
- `session_id`: Unique session identifier
- `agent_id`: Agent that owns this session
- `timestamp`: Session start time (ISO 8601)

**Session lifecycle**:

1. Agent registers
2. Session starts
3. Agent sends/receives messages
4. Session ends (gracefully or crash)
5. Orphan recovery on next session start

**Related methods**: `session.start`, `session.end`

#### agent.session.end

Emitted when a session ends.

**When emitted**:

- After `session.end` RPC call succeeds
- When session end event is synced from remote
- When daemon detects a crashed session

**Stored in**: `events.jsonl`

**Payload** (AgentSessionEndEvent):

```json
{
  "type": "agent.session.end",
  "timestamp": "2024-01-01T13:00:00Z",
  "event_id": "01HQXYZ...",
  "v": 1,
  "session_id": "s_abc123",
  "reason": "normal"
}
```

**Fields**:

- `event_id`: Globally unique ULID for deduplication
- `session_id`: Session that ended
- `timestamp`: Session end time (ISO 8601)
- `reason`: End reason (`normal`, `crash`)

**End reasons**:

- `normal`: Graceful shutdown
- `crash`: Unexpected termination or timeout

**Related methods**: `session.end`, `session.start`

## Subscription Filtering

Events are filtered based on active subscriptions:

### Scope-Based Subscriptions

Only receive events for messages that match the subscribed scope.

**Example**: Subscribe to all messages in task "PROJ-123"

```json
{
  "jsonrpc": "2.0",
  "method": "subscribe.create",
  "params": {
    "filter_type": "scope",
    "scope": {
      "type": "task",
      "value": "PROJ-123"
    }
  },
  "id": 1
}
```

**Matching logic**:

- Event's `scopes` array contains an exact match for the subscribed scope
- Scope type and value must both match

### Mention-Based Subscriptions

Only receive events for messages that mention a specific role.

**Example**: Subscribe to messages mentioning "@reviewer"

```json
{
  "jsonrpc": "2.0",
  "method": "subscribe.create",
  "params": {
    "filter_type": "mention",
    "mention": "reviewer"
  },
  "id": 1
}
```

**Matching logic**:

- Event's `refs` array contains a reference with `type: "mention"` and
  `value: "reviewer"`

### All-Events Subscriptions

Receive all events (use with caution, high volume).

**Example**: Subscribe to all events

```json
{
  "jsonrpc": "2.0",
  "method": "subscribe.create",
  "params": {
    "filter_type": "all"
  },
  "id": 1
}
```

**Use cases**:

- Admin dashboards
- Audit logging
- System monitoring

**Warning**: High traffic, may overwhelm slow clients

## Event Ordering

### Guarantees

- **Per-message ordering**: Events for the same message (create → edit → delete)
  are always in order
- **Cross-message ordering**: No guarantee; events may arrive out of order

### Timestamps

All events include ISO 8601 timestamps. Use these for:

- Sorting events client-side
- Detecting out-of-order delivery
- Time-based filtering

### Sequence Numbers

Future enhancement: Add sequence numbers for detecting gaps.

## Client Buffer Management

### Buffer Limits

- Default buffer size: **100 events**
- Buffer is per-client (per WebSocket connection)
- When buffer is full, oldest events are dropped

### Buffer Full Behavior

1. Client's buffer fills up (slow consumer)
2. New events are dropped (not queued)
3. Client connection may be closed if buffer remains full

### Best Practices

1. **Process events quickly**: Don't block the event handler
2. **Use background workers**: Offload heavy processing
3. **Monitor buffer**: Watch for dropped events
4. **Adjust subscriptions**: Subscribe only to necessary events

## Error Scenarios

### Missed Events

**Causes**:

- Client buffer full (slow consumer)
- WebSocket connection interruption
- Client disconnected during event delivery

**Detection**:

- Monitor timestamp gaps
- Track expected vs actual event counts

**Recovery**:

- Poll with `message.list` to catch up
- Re-subscribe after reconnection
- Use pagination to fetch missed messages

### Duplicate Events

**Causes**:

- Retry logic on server
- Network-level retransmission
- Multiple subscriptions matching same event

**Handling**:

- Use `event_id` (ULID) for deduplication -- this is the universal dedup key
  across all event types
- Maintain a set of processed event IDs
- Clear processed IDs periodically (TTL)

Note: Do not use `message_id` for deduplication, as multiple events can share
the same `message_id` (e.g., a `message.create` and subsequent `message.edit`
for the same message).

## Best Practices

### Subscription Management

1. **Specific filters**: Use scope/mention filters instead of "all"
2. **Cleanup**: Unsubscribe when no longer needed
3. **Session-bound**: Subscriptions auto-expire when session ends

### Event Processing

1. **Idempotent handlers**: Handle duplicate events gracefully
2. **Error handling**: Don't crash on malformed events
3. **Async processing**: Don't block WebSocket thread

### Performance

1. **Batch updates**: Buffer UI updates, render in batches
2. **Debounce**: Delay rapid successive events
3. **Throttle**: Limit event processing rate

### Debugging

1. **Log all events**: For development/debugging
2. **Event counters**: Track received vs processed
3. **Timestamp monitoring**: Detect delivery delays

## Example Event Handlers

### JavaScript/TypeScript

```typescript
const eventHandlers = {
  "message.create": (params: MessageCreateEvent) => {
    console.log(`New message: ${params.message_id}`);
    addMessageToUI(params);
  },

  "message.edit": (params: MessageEditEvent) => {
    console.log(`Message edited: ${params.message_id}`);
    updateMessageInUI(params);
  },

  "message.delete": (params: MessageDeleteEvent) => {
    console.log(`Message deleted: ${params.message_id}`);
    removeMessageFromUI(params);
  },
};

ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);

  // Handle events (no id field)
  if (!msg.id && msg.method) {
    const handler = eventHandlers[msg.method];
    if (handler) {
      handler(msg.params);
    } else {
      console.warn(`Unknown event type: ${msg.method}`);
    }
  }

  // Handle RPC responses (has id field)
  else if (msg.id) {
    handleRPCResponse(msg);
  }
};
```

### Go

```go
type EventHandler func(json.RawMessage) error

handlers := map[string]EventHandler{
    "message.create": func(params json.RawMessage) error {
        var event types.MessageCreateEvent
        if err := json.Unmarshal(params, &event); err != nil {
            return err
        }
        log.Printf("New message: %s", event.MessageID)
        return addMessageToUI(event)
    },

    "message.edit": func(params json.RawMessage) error {
        var event types.MessageEditEvent
        if err := json.Unmarshal(params, &event); err != nil {
            return err
        }
        log.Printf("Message edited: %s", event.MessageID)
        return updateMessageInUI(event)
    },
}

func handleMessage(data []byte) error {
    var msg struct {
        JSONRPC string          `json:"jsonrpc"`
        Method  string          `json:"method,omitempty"`
        Params  json.RawMessage `json:"params,omitempty"`
        ID      *int            `json:"id,omitempty"`
    }

    if err := json.Unmarshal(data, &msg); err != nil {
        return err
    }

    // Event (no ID field)
    if msg.ID == nil && msg.Method != "" {
        handler, ok := handlers[msg.Method]
        if !ok {
            log.Printf("Unknown event: %s", msg.Method)
            return nil
        }
        return handler(msg.Params)
    }

    // RPC response (has ID)
    return handleRPCResponse(&msg)
}
```

## See Also

- [WebSocket API](./websocket.md) - Main API documentation
- [Authentication Guide](./authentication.md) - User and agent authentication
