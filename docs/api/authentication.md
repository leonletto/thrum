
# Authentication Guide

This document explains authentication and authorization in the Thrum API.

## Overview

Thrum supports two types of identity:

1. **Agents**: Autonomous processes (bots, AI agents, services) that send and
   receive messages
2. **Users**: Human users accessing the system via WebSocket (typically through
   the embedded web UI)

Both agents and users are registered with the daemon and stored in the same
`agents` table in SQLite (distinguished by the `kind` field: `"agent"` or
`"user"`).

## Agent Authentication

### Registration

Agents register using the `agent.register` RPC method, available over both Unix
socket and WebSocket.

**Identity Components**:

- `name`: Human-readable agent name (optional; e.g., `"furiosa"`, `"nux"`)
- `role`: The agent's function (e.g., `"implementer"`, `"reviewer"`,
  `"coordinator"`)
- `module`: The area of responsibility (e.g., `"auth"`, `"sync"`, `"ui"`)
- `repo_id`: Automatically determined from the repository's git origin URL

**Agent ID Format**:

- **Named**: `{name}` (e.g., `furiosa`)
- **Unnamed**: `{role}_{hash10}` (e.g., `implementer_35HV62T9B9`)
- **Legacy** (backward compatible): `agent:{role}:{hash}` -- no longer generated
  but still recognized

The hash is computed as
`crockford_base32(sha256(repo_id + "|" + role + "|" + module))[:10]`.

**Example** (named agent):

```json
{
  "jsonrpc": "2.0",
  "method": "agent.register",
  "params": {
    "name": "furiosa",
    "role": "implementer",
    "module": "auth",
    "display": "Auth Implementer"
  },
  "id": 1
}
```

**Response**:

```json
{
  "jsonrpc": "2.0",
  "result": {
    "agent_id": "furiosa",
    "status": "registered"
  },
  "id": 1
}
```

**Example** (unnamed agent):

```json
{
  "jsonrpc": "2.0",
  "method": "agent.register",
  "params": {
    "role": "implementer",
    "module": "auth"
  },
  "id": 1
}
```

**Response**:

```json
{
  "jsonrpc": "2.0",
  "result": {
    "agent_id": "implementer_35HV62T9B9",
    "status": "registered"
  },
  "id": 1
}
```

### Re-Registration

Agents can re-register with the same role/module/name. The same agent ID is
returned without writing a new event (idempotent). To force an update event, use
the `re_register` flag.

**Use cases**:

- Agent restart
- Daemon restart
- Network reconnection
- Updating display name

### Quickstart (CLI Shortcut)

The `quickstart` command combines registration, session start, and optional
intent setting:

```bash
thrum quickstart --name furiosa --role implementer --module auth --intent "Working on auth"
```

### Session Management

After registration, agents must start a session to send messages:

1. **Register**: `agent.register` -> get `agent_id`
2. **Start session**: `session.start` with `agent_id` -> get `session_id`
3. **Heartbeat**: `session.heartbeat` to update last-seen time and extract git
   work context
4. **Send messages**: Use `agent_id` in requests
5. **End session**: `session.end` with `session_id`

**Why sessions?**

- Track agent activity periods
- Detect crashes (orphaned sessions)
- Attribute messages to specific runs
- Track git work context (branch, changed files, uncommitted work)

**Session IDs** use ULID format: `ses_` + ULID (e.g.,
`ses_01HXE8Z7R9K3Q6M2W8F4VY`).

### Identity Resolution for CLI

The CLI resolves agent identity using a priority chain (see `docs/identity.md`
for full details):

1. `THRUM_NAME` env var (selects identity file; highest priority)
2. CLI flags (`--name`, `--role`, `--module`)
3. Environment variables (`THRUM_ROLE`, `THRUM_MODULE`, `THRUM_DISPLAY`)
4. Identity file in `.thrum/identities/` directory
5. Error if required fields missing

### MCP Server Identity

The MCP server (`thrum mcp serve`) loads agent identity at startup from
`.thrum/identities/{name}.json`. It requires a named agent. Use `--agent-id`
flag or `THRUM_NAME` env var for multi-agent worktrees:

```bash
THRUM_NAME=furiosa thrum mcp serve
thrum mcp serve --agent-id furiosa
```

## User Authentication

### WebSocket-Only

Users can **only** register via WebSocket (not Unix socket). This is enforced by
the daemon:

- Unix socket is for local agents (same machine, no UI)
- WebSocket is for browser connections (UI at `http://localhost:9999`)

Attempting `user.register` over Unix socket returns error code `-32001`.

### Browser Auto-Registration

When a user opens the web UI at `http://localhost:9999`, the browser
automatically registers:

1. **Connect**: WebSocket connects to `ws://localhost:9999/ws`
2. **Identify**: Call `user.identify` RPC to get git user info from the
   repository's `git config`
3. **Register**: Call `user.register` with the sanitized username
4. **Persist**: Store user info and token in `localStorage` for reconnection

This flow is handled by the `AuthProvider` React component. No manual login is
required.

**Fallback**: If `git config user.name` is not set, the UI checks `localStorage`
for a previously stored username. If neither source is available, an error is
displayed.

### user.identify RPC

Returns git user info from the repository's git config. No authentication needed
-- this is a read-only query. Available over both Unix socket and WebSocket.

**Response**:

```json
{
  "jsonrpc": "2.0",
  "result": {
    "username": "leon-letto",
    "email": "leon@example.com",
    "display": "Leon Letto"
  },
  "id": 1
}
```

The `username` field is sanitized from `git config user.name`: lowercased,
spaces/hyphens/underscores normalized to hyphens, non-alphanumeric characters
stripped, truncated to 32 characters.

### user.register RPC

Registers a user with the daemon. **Idempotent**: if the user already exists,
returns existing info with a fresh session token.

**Username Rules**:

- Alphanumeric characters, underscores, and hyphens
- Length 1-32 characters
- Must not start with `agent:` prefix
- Regex: `^[a-zA-Z0-9_-]{1,32}$`

**User ID Format**: `user:{username}` (e.g., `user:leon`)

**Example**:

```json
{
  "jsonrpc": "2.0",
  "method": "user.register",
  "params": {
    "username": "leon",
    "display": "Leon Letto"
  },
  "id": 1
}
```

**Response**:

```json
{
  "jsonrpc": "2.0",
  "result": {
    "user_id": "user:leon",
    "username": "leon",
    "display_name": "Leon Letto",
    "token": "tok_01HXE8Z7R9K3Q6M2W8F4VY",
    "status": "registered"
  },
  "id": 1
}
```

When re-registering an existing user, the response has `"status": "existing"`
and a fresh token.

**Storage**: Users are stored internally as `agent.register` events with
`kind: "user"`, `role: {username}`, and `module: "ui"`.

### localStorage Persistence

The browser stores user identity in `localStorage` under the `thrum_user` key:

```json
{
  "user_id": "user:leon",
  "token": "tok_...",
  "username": "leon",
  "display_name": "Leon Letto"
}
```

On subsequent page loads, the stored username is used as a fallback if
`user.identify` fails.

## Authorization

### Impersonation

Users can impersonate agents to send messages "as" an agent.

**Use case**: UI allowing users to send messages from an agent's perspective.

**Restrictions**:

1. **Only users can impersonate**: Agents cannot impersonate other agents or
   users
2. **Only agents can be impersonated**: Users cannot impersonate other users
3. **Target must exist**: The impersonated agent must be registered

**Example**:

```json
{
  "jsonrpc": "2.0",
  "method": "message.send",
  "params": {
    "content": "Hello from Claude!",
    "acting_as": "furiosa"
  },
  "id": 1
}
```

**Audit Trail**:

- `authored_by`: Original user ID (e.g., `"user:leon"`)
- `disclosed`: Whether impersonation is revealed in UI (default: false)
- `agent_id`: Impersonated agent ID (appears as message author)

**Validation**:

- Caller must be `user:*`
- `acting_as` must reference an existing agent
- `acting_as` cannot be a `user:*` ID

**Error codes**:

- `-32001`: Only users can impersonate
- `-32002`: Target agent does not exist
- `-32003`: Users can only impersonate agents, not other users

### Message Ownership

**Editing**: Only the message author can edit their own messages.

**Deletion**: Only the message author can delete their own messages.

**Author determination**:

- If `authored_by` is set (impersonation): `authored_by` is the owner
- Otherwise: `agent_id` is the owner

**Example**: User "leon" impersonates agent "furiosa" to send a message:

- `agent_id`: `"furiosa"` (appears as author)
- `authored_by`: `"user:leon"` (actual owner)
- Only `"user:leon"` can edit/delete this message

### Session-Based Authorization

All RPC requests include transport context with the caller's identity.

**Transport context**:

- Unix socket: Agent identity (from environment or registration)
- WebSocket: User/agent identity (from registration)

**Implicit authorization**: The daemon knows which connection is making the
request.

**Use cases**:

- Prevent cross-session operations
- Attribute actions to sessions
- Audit trail

## Security Considerations

### Current (MVP)

1. **No encryption**: WebSocket traffic is unencrypted (`ws://`, not `wss://`)
2. **No authentication tokens**: No password or token verification for initial
   connection
3. **Local-only**: Daemon binds to `127.0.0.1` (localhost only)
4. **Trust-based**: Clients on the same machine are trusted
5. **Single port**: WebSocket and embedded SPA share port 9999

**Acceptable for**:

- Local development
- Single-user machines
- Trusted environments

**Not acceptable for**:

- Multi-user machines
- Production deployments
- Untrusted networks

### Future Enhancements

1. **TLS/SSL**: Support `wss://` for encrypted WebSocket connections
2. **Token-based auth**: JWT or similar for user authentication
3. **Role-based access control (RBAC)**: Permission system for operations
4. **API keys**: For programmatic agent access
5. **Audit logging**: Comprehensive audit trail for all operations

## Session Lifecycle

### Agent Sessions

```
1. Agent starts
   |
2. Connect to daemon (Unix socket or WebSocket)
   |
3. Register: agent.register (with optional name)
   |
4. Start session: session.start
   |
5. Send/receive messages, heartbeat periodically
   |
6. End session: session.end (or crash)
   |
7. Disconnect
```

Or use `quickstart` to combine steps 3-4 (and optionally set intent):

```
1. Agent starts
   |
2. Connect to daemon
   |
3. Quickstart: agent.register + session.start + set-intent
   |
4. Send/receive messages, heartbeat periodically
   |
5. End session: session.end (or crash)
   |
6. Disconnect
```

### User Sessions

```
1. User opens UI at http://localhost:9999
   |
2. WebSocket connects to ws://localhost:9999/ws
   |
3. Auto-identify: user.identify (reads git config)
   |
4. Auto-register: user.register (idempotent, returns token)
   |
5. Send/receive messages, subscribe to events
   |
6. Disconnect (session persists for reconnection)
```

### Orphaned Sessions

**Problem**: Agent crashes without calling `session.end`.

**Detection**: When agent re-registers and starts a new session, the daemon
detects existing open sessions for that agent.

**Recovery**: Orphaned sessions are automatically ended with `reason: "crash"`
during the next `session.start` call.

### Daemon Lifecycle Hardening

The daemon has several safety mechanisms for robust session management:

- **Defer cleanup**: Safety net catches panics/early returns, cleans up PID
  file, socket, and port files on any exit
- **JSON PID file**: Contains `{PID, RepoPath, StartedAt, SocketPath}` for
  repo-affinity validation
- **Socket flock**: OS-level file lock (`flock`) on the socket file,
  auto-released on process death (even SIGKILL)
- **Pre-startup validation**: Detects duplicate daemons serving the same
  repository before starting

## Connection Security

### Unix Socket

**Path**: `$REPO/.thrum/daemon.sock` (follows `.thrum/redirect` in worktrees)

**Access control**: File system permissions (0600)

**Security**: Only processes with read/write access to the socket file can
connect.

**Use cases**:

- Local agents (same machine as daemon)
- CLI tools
- MCP server (`thrum mcp serve`)
- Trusted local services

### WebSocket

**Endpoint**: `ws://localhost:9999/ws` (when UI is embedded)

**Fallback**: `ws://localhost:9999/` (when no UI is embedded, backward
compatible)

**Bind address**: `127.0.0.1` (localhost only)

**Security**: Only processes on the same machine can connect.

**Use cases**:

- Web UI (browser on same machine, served as embedded SPA)
- Desktop applications
- Remote tunneling (SSH port forwarding)

**Warning**: Do not expose WebSocket port to untrusted networks without TLS and
authentication.

## Best Practices

### Agent Development

1. **Use quickstart**: Prefer `thrum quickstart` over manual register + session
   start
2. **Use named agents**: Provide `--name` for human-readable identification
3. **Graceful shutdown**: Always call `session.end` before exiting
4. **Error handling**: Catch crashes, end session in cleanup
5. **Heartbeat regularly**: Call `session.heartbeat` to update work context and
   stay visible
6. **Idempotent registration**: Safe to call `agent.register` multiple times

### User Clients

1. **Auto-registration**: Let the `AuthProvider` handle identity automatically
2. **Reconnection**: Re-register with same username after disconnect
   (idempotent)
3. **Session management**: Let daemon manage session lifecycle
4. **Impersonation disclosure**: Set `disclosed: true` for transparency
5. **Error handling**: Handle authorization errors gracefully

### Security

1. **Local only**: Do not expose daemon to network without authentication
2. **Principle of least privilege**: Subscribe only to necessary events
3. **Audit logging**: Log all user actions for accountability
4. **Input validation**: Validate all user input before sending to daemon

## Examples

### Agent Registration and Session (CLI)

```bash
# One-step quickstart (recommended)
thrum quickstart --name furiosa --role implementer --module auth \
  --intent "Implementing JWT authentication"

# Or step-by-step
thrum agent register --name=furiosa --role=implementer --module=auth
thrum session start
thrum send "Starting work on auth module" --to @coordinator
```

### Agent Registration and Session (WebSocket)

```typescript
import WebSocket from "ws";

const ws = new WebSocket("ws://localhost:9999/ws");

let agentId: string;
let sessionId: string;

ws.on("open", async () => {
  // Step 1: Register agent
  ws.send(
    JSON.stringify({
      jsonrpc: "2.0",
      method: "agent.register",
      params: {
        name: "furiosa",
        role: "implementer",
        module: "auth",
      },
      id: 1,
    }),
  );
});

ws.on("message", (data: string) => {
  const msg = JSON.parse(data);

  if (msg.id === 1) {
    // Registration response
    agentId = msg.result.agent_id;

    // Step 2: Start session
    ws.send(
      JSON.stringify({
        jsonrpc: "2.0",
        method: "session.start",
        params: { agent_id: agentId },
        id: 2,
      }),
    );
  }

  if (msg.id === 2) {
    // Session start response
    sessionId = msg.result.session_id;
    console.log(`Agent registered: ${agentId}, Session: ${sessionId}`);

    // Now ready to send messages
  }
});
```

### MCP Server (Native Agent Messaging)

```bash
# Start MCP server for Claude Code integration
THRUM_NAME=furiosa thrum mcp serve

# Or with explicit agent-id override
thrum mcp serve --agent-id furiosa
```

The MCP server provides 5 tools: `send_message`, `check_messages`,
`wait_for_message`, `list_agents`, `broadcast_message`. Identity is resolved
once at startup.

### User Registration (Browser Auto-Registration)

The browser handles registration automatically via `AuthProvider`. No manual
code is needed. The flow is:

```
Page loads -> AuthProvider mounts -> user.identify -> user.register -> UI shows identity
```

For programmatic WebSocket user registration:

```typescript
const ws = new WebSocket("ws://localhost:9999/ws");

ws.on("open", () => {
  // User registration (idempotent)
  ws.send(
    JSON.stringify({
      jsonrpc: "2.0",
      method: "user.register",
      params: {
        username: "leon",
        display: "Leon Letto",
      },
      id: 1,
    }),
  );
});

ws.on("message", (data: string) => {
  const msg = JSON.parse(data);

  if (msg.id === 1) {
    // User registered
    console.log(`User: ${msg.result.user_id}, Token: ${msg.result.token}`);
  }
});
```

## See Also

- [Identity & Registration](../identity.md) - Full agent identity system
  documentation
- [WebSocket API](./websocket.md) - Full API reference
- [Event Reference](./events.md) - Event types and payloads
