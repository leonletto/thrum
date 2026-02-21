## Event Streaming

## Overview

Event streaming enables real-time push notifications to connected WebSocket and
Unix socket clients. When significant events occur (new messages), the daemon
automatically pushes notifications to subscribed clients.

The WebSocket server and embedded SPA are served on the same port (default 9999,
configurable via `THRUM_WS_PORT`). WebSocket connections use the `/ws` endpoint
when the UI is active, or `/` when running without UI.

## Architecture

### Components

1. **Broadcaster** (`internal/daemon/broadcaster.go`)
   - Unified notification sender for both Unix socket and WebSocket clients
   - Implements the `ClientNotifier` interface expected by the subscription
     dispatcher
   - Tries Unix socket transport first, then WebSocket as fallback
   - Handles client disconnections gracefully
   - Thread-safe with `sync.RWMutex`

2. **Subscription Dispatcher** (`internal/subscriptions/dispatcher.go`)
   - Matches events against active subscriptions
   - Filters events based on scopes, mentions (role-based and name-based), and
     subscription types
   - Pushes notifications to matched subscribers via the Broadcaster

3. **Event Streaming Setup** (`internal/daemon/event_streaming.go`)
   - Factory for wiring together Broadcaster and Dispatcher
   - Two convenience constructors:
     - `NewEventStreamingSetup(unixClients, wsServer, db)` - from raw components
     - `NewEventStreamingSetupFromState(state, unixClients, wsServer)` - from
       daemon state
   - Returns `EventStreamingSetup` struct with `Broadcaster` and `Dispatcher`
     fields

### Data Flow

````text
Event Source (message.send, message.edit)
  |
  v
Subscription Dispatcher
  | (query subscriptions, match against message scopes/mentions)
  v
Broadcaster
  |         |
  v         v
Unix Socket    WebSocket
Clients        Clients (port 9999, /ws endpoint)
```go

> **Note:** All WebSocket connections enforce a 10s handshake timeout.
> Server-side requests have a 10s per-request timeout (v0.4.3).

## Implementation Details

### Supported Notifications

Currently implemented:

- **`notification.message`** - Pushed when a new message is created or edited,
  matching a subscription

### Notification Format

Notifications use JSON-RPC 2.0 notification format (no `id` field, no response
expected):

```json
{
  "jsonrpc": "2.0",
  "method": "notification.message",
  "params": {
    "message_id": "msg_...",
    "author": {
      "agent_id": "furiosa",
      "name": "furiosa",
      "role": "implementer",
      "module": ""
    },
    "preview": "First 100 characters of content...",
    "scopes": [{ "type": "task", "value": "thrum-ukr" }],
    "matched_subscription": {
      "subscription_id": 1,
      "match_type": "scope"
    },
    "timestamp": "2026-02-03T10:00:00Z"
  }
}
```text

### Subscription Filtering

The dispatcher automatically filters events based on subscriptions:

- **Scope subscriptions**: Only notify if message has matching scope
- **Mention subscriptions**: Only notify if message mentions the agent's role or
  name (supports both `@reviewer` and `@furiosa`)
- **All subscriptions**: Notify for every message
- **No subscription**: No notifications (client must poll inbox)

### Client Buffer Management

Both Unix socket and WebSocket connections use buffered I/O:

- **WebSocket**: 256-message buffered channel per connection (`sendCh` in
  `internal/websocket/connection.go`)
- **Unix socket**: Direct write to `net.Conn` with newline framing
- **Slow client handling**: If WebSocket buffer is full, the send fails and the
  client is auto-unregistered

## Usage

### Daemon Initialization

When starting the daemon, create the event streaming infrastructure:

```go
// Create daemon state
st, _ := state.NewState(thrumDir, syncDir, repoID)

// Create client registries
unixClients := daemon.NewClientRegistry()

// Create WebSocket server with handler registry and optional UI filesystem
wsServer := websocket.NewServer(wsAddr, wsRegistry, uiFS)

// Set up event streaming (wires Broadcaster + Dispatcher)
eventSetup := daemon.NewEventStreamingSetupFromState(st, unixClients, wsServer)

// Create message handler with the dispatcher for push notifications
messageHandler := rpc.NewMessageHandlerWithDispatcher(st, eventSetup.Dispatcher)

// Register handlers on both Unix socket and WebSocket registries...
```go

### Client Subscription

Clients subscribe via the `subscribe` RPC method:

```json
{
  "jsonrpc": "2.0",
  "method": "subscribe",
  "params": {
    "scope": { "type": "task", "value": "thrum-ukr" }
  },
  "id": 1
}
```text

Or subscribe to mentions:

```json
{
  "jsonrpc": "2.0",
  "method": "subscribe",
  "params": {
    "mention_role": "reviewer"
  },
  "id": 1
}
```text

Or subscribe to all messages (firehose):

```json
{
  "jsonrpc": "2.0",
  "method": "subscribe",
  "params": {
    "all": true
  },
  "id": 1
}
```text

### Receiving Notifications

WebSocket clients receive notifications as JSON-RPC notifications (no response
required):

```javascript
// WebSocket client example
const ws = new WebSocket("ws://localhost:9999/ws");

ws.onmessage = (event) => {
  const notification = JSON.parse(event.data);
  if (notification.method === "notification.message") {
    console.log("New message:", notification.params.preview);
  }
};
```text

### MCP Server Integration

The MCP server (`thrum mcp serve`) uses WebSocket notifications for its
`wait_for_message` tool. It connects to the daemon's WebSocket endpoint and
subscribes to notifications, enabling blocking message waits for agent
sub-agents (like the message-listener pattern).

## Testing

Comprehensive test coverage includes:

1. **Unit Tests** (`internal/daemon/broadcaster_test.go`)
   - Broadcaster notification routing (WebSocket path)
   - Client not connected handling
   - Notification format conversion

2. **Integration Tests** (`internal/daemon/event_streaming_test.go`)
   - End-to-end message notification flow with subscriptions
   - Subscription filtering (scope matching vs. non-matching)
   - Event streaming setup wiring
   - Mock notification receiver pattern

3. **Dispatcher Tests** (`internal/subscriptions/dispatcher_test.go`)
   - Scope, mention, and "all" subscription matching
   - Name-based mention matching (@furiosa)
   - Multiple subscriptions per message
   - No subscriptions scenario

Run tests:

```bash
go test ./internal/daemon/...
go test ./internal/subscriptions/...
go test ./internal/websocket/...
```text

## Performance Characteristics

- **Latency**: Sub-millisecond notification dispatch (synchronous in
  message.send path)
- **Throughput**: Limited by subscription query (loads all subscriptions per
  message)
- **Memory**: O(clients) for WebSocket send buffers (256 messages each),
  O(subscriptions) for filtering
- **Concurrency**: Thread-safe Broadcaster, client registries, and dispatcher

## Troubleshooting

### Notifications Not Received

1. Check subscription exists: `thrum subscriptions` CLI or `subscriptions.list`
   RPC
2. Verify client is connected: Check WebSocket client registry via daemon logs
3. Confirm event matches subscription: Check scope/mention filters match message
   scopes/refs
4. Look for slow client disconnections: WebSocket buffer full (256-message
   limit)
5. Verify WebSocket endpoint: Use `ws://localhost:9999/ws` (not
   `ws://localhost:9999/`)

### High Memory Usage

- Too many buffered messages: Check for slow WebSocket consumers
- Too many subscriptions: Review per-session subscription counts

### Notification Lag

- Subscription query is synchronous in message.send path
- Check SQLite database performance
- Monitor WebSocket connection health

## References

- Subscription Details: `docs/subscriptions.md`
- WebSocket API: `docs/api/websocket.md`
- Daemon Architecture: `docs/daemon.md`
- RPC API: `docs/rpc-api.md`
- Broadcaster: `internal/daemon/broadcaster.go`
- Dispatcher: `internal/subscriptions/dispatcher.go`
- Event Streaming Setup: `internal/daemon/event_streaming.go`
- WebSocket Server: `internal/websocket/server.go`
- WebSocket Client Registry: `internal/websocket/registry.go`
- Unix Socket Client Registry: `internal/daemon/notify.go`
````
