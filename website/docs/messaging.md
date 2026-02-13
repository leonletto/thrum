
# Thrum Messaging System

## Overview

The Thrum messaging system provides structured communication between agents with
support for direct messaging, read tracking, scoping, references, and rich content
formats. Messages are persisted in a Git-backed event log and projected into SQLite
for fast queries.

This document covers the CLI commands and behaviors for sending, receiving,
replying to, and managing messages.

## Quick Reference

### Top-Level Commands

| Command                   | Description                                                            |
| ------------------------- | ---------------------------------------------------------------------- |
| `thrum send MESSAGE`      | Send a message (with optional `--to`, `--scope`, `--ref`, `--mention`) |
| `thrum reply MSG_ID TEXT` | Reply to a message, creating a reply-to reference                      |
| `thrum inbox`             | List messages with read/unread indicators                              |

### Message Subcommands

| Command                               | Description                                 |
| ------------------------------------- | ------------------------------------------- |
| `thrum message get MSG_ID`            | Retrieve a single message with full details |
| `thrum message edit MSG_ID TEXT`      | Replace a message's content (author only)   |
| `thrum message delete MSG_ID --force` | Soft-delete a message                       |
| `thrum message read MSG_ID [...]`     | Manually mark messages as read              |

## Sending Messages

### Basic Send

```bash
thrum send "Test suite is green, ready for review"
```

The daemon resolves the current agent identity and session automatically. The
message is written as a `message.create` event to the agent's sharded JSONL log
(`.git/thrum-sync/a-sync/messages/{agent_name}.jsonl`) and projected into
SQLite.

### Flags

| Flag           | Format                  | Description                             |
| -------------- | ----------------------- | --------------------------------------- |
| `--to`         | `@role`                 | Direct recipient (adds a mention ref)   |
| `--scope`      | `type:value`            | Attach scope context (repeatable)       |
| `--ref`        | `type:value`            | Attach reference (repeatable)           |
| `--mention`    | `@role`                 | Mention an agent role (repeatable)      |
| `--format`     | `markdown\|plain\|json` | Content format (default: `markdown`)    |
| `--priority`   | `low\|normal\|high`     | Message priority (default: `normal`)    |
| `--structured` | JSON string             | Typed payload for machine-readable data |

### Direct Messaging with --to

The `--to` flag provides a shorthand for directing a message to a specific agent,
role, or group. Under the hood, `--to @reviewer` appends `@reviewer` to the mentions
list, which is stored as a `mention` ref on the message.

```bash
# These are equivalent:
thrum send "Please review PR #42" --to @reviewer
thrum send "Please review PR #42" --mention @reviewer

# Send to a group
thrum send "Deploy complete" --to @everyone

# Send to a custom group
thrum send "Backend review needed" --to @backend
```

The `@` prefix is optional -- `--to reviewer` and `--to @reviewer` both work.

### Mention Routing

When a message includes mentions (via `--to`, `--mention`, or `@role` in the
mentions list), Thrum stores them as refs with type `mention` and the role name
as the value:

```json
{ "type": "mention", "value": "reviewer" }
```

Agents can then filter their inbox to only messages that mention them:

```bash
thrum inbox --mentions
```

This queries for messages where a `mention` ref matches the current agent's
role.

### Example: Agent-to-Agent Coordination

```bash
# Implementer finishes a task and notifies the reviewer
thrum send "Auth module complete, all tests passing" \
  --to @reviewer \
  --scope module:auth \
  --ref issue:beads-42

# Reviewer checks inbox for messages directed at them
thrum inbox --mentions

# Reviewer replies to the message
thrum reply msg_01HXE... "Looks good, merging now"
```

## Replying to Messages

The `reply` command creates a reply-to reference linking your message to the
original:

```bash
thrum reply MSG_ID "Your reply text"
```

**What happens internally:**

1. Creates a new message with a `reply_to` ref pointing to the original message.
2. The inbox groups replies with the original message using a `↳` prefix.
3. Marks the original message as read (auto mark-as-read).

### Flags

| Flag       | Format                  | Description                          |
| ---------- | ----------------------- | ------------------------------------ |
| `--format` | `markdown\|plain\|json` | Content format (default: `markdown`) |

### Example

```bash
# Reply to a message
thrum reply msg_01HXE... "Good idea, let's do that"

# Reply with plain text format
thrum reply msg_01HXF... "Acknowledged" --format plain
```

**Output:**

```
> Reply sent: msg_01HXG...
  In reply to: msg_01HXE...
```

## Inbox

The inbox displays messages with read/unread indicators, relative timestamps,
and pagination.

```bash
thrum inbox
```

### Flags

| Flag                 | Description                                |
| -------------------- | ------------------------------------------ |
| `--scope type:value` | Filter by scope                            |
| `--mentions`         | Only messages mentioning the current agent |
| `--unread`           | Only unread messages                       |
| `--page-size N`      | Results per page (default: 10)             |
| `--page N`           | Page number (default: 1)                   |

### Read/Unread Indicators

Each message in the inbox shows a read-state indicator:

- `●` (filled circle) -- unread
- `○` (open circle) -- read

The header line for each message follows this format:

```
│ ● msg_01HXE...  @implementer  5m ago                     │
│ ○ msg_01HXF...  @reviewer     1h ago  (edited)           │
│ ↳ msg_01HXG...  @implementer  10m ago                    │
```

Messages that have been edited show an `(edited)` tag in the header. Replies are
displayed with a `↳` prefix and grouped with the original message.

### Auto Mark-as-Read

Viewing the inbox automatically marks all displayed messages as read
(best-effort; failure does not block the command). This behavior is skipped when
using the `--unread` filter, so that repeatedly checking unread messages does
not clear them before you act on them.

The footer shows pagination and unread count:

```
Showing 1-10 of 23 messages (3 unread)
```

### Empty States

When no messages match the current filters, the inbox shows contextual feedback:

```bash
# No messages at all
thrum inbox
# Output: No messages in inbox.

# No messages matching a scope filter
thrum inbox --scope module:auth
# Output: No messages matching filter --scope module:auth
#         Showing 0 of 15 total messages (filter: scope=module:auth)
```

## Message Operations

### Get

Retrieve a single message with its full details: author, timestamps, scopes,
refs, edit and delete status.

```bash
thrum message get msg_01HXE...
```

**Output:**

```
Message: msg_01HXE...
  From:    @implementer
  Time:    5m ago
  Scopes:  module:auth
  Refs:    issue:beads-42, mention:reviewer
  Edited:  2m ago

Auth module complete, all tests passing
```

The `get` command automatically marks the message as read.

### Edit

Replace a message's content entirely. Only the original author (matching
`agent_id`) can edit their own messages. Deleted messages cannot be edited.

```bash
thrum message edit msg_01HXE... "Updated: auth module complete with rate limiting"
```

**Output:**

```
> Message edited: msg_01HXE... (version 2)
```

Each edit is recorded in the `message_edits` table with before/after content,
the editor's session, and a timestamp. The version number reflects the total
number of edits applied to the message. Edits trigger subscription notifications
just like new messages.

### Delete

Soft-delete a message. Requires the `--force` flag as a safety confirmation.

```bash
thrum message delete msg_01HXE... --force
```

**Output:**

```
> Message deleted: msg_01HXE...
```

Deleted messages:

- Are flagged with `deleted=1` in SQLite and a `message.delete` event is
  appended to the JSONL log.
- Still appear in `message get` (with a `DELETED` status label) but are excluded
  from inbox listings by default.
- Cannot be un-deleted (the JSONL log is append-only).
- Can include an optional deletion reason in the RPC request.

### Mark Read

Explicitly mark one or more messages as read. This is useful when auto
mark-as-read was skipped or when processing messages programmatically. Use
`--all` to mark every unread message as read at once.

```bash
thrum message read msg_01HXE...
thrum message read msg_01 msg_02 msg_03
thrum message read --all
```

**Output:**

```
> Marked 3 messages as read
```

Read state is tracked per session and per agent in the `message_reads` table. A
message is considered "read" if any session or agent matching the current
identity has a read record for it.

### Auto Mark-as-Read Summary

Several commands mark messages as read automatically:

| Command                    | Behavior                                                       |
| -------------------------- | -------------------------------------------------------------- |
| `thrum inbox`              | Marks all displayed messages as read (skipped with `--unread`) |
| `thrum reply MSG_ID ...`   | Marks the replied-to message as read                           |
| `thrum message get MSG_ID` | Marks the retrieved message as read                            |

All auto mark-as-read operations are best-effort: if they fail, the parent
command still succeeds.

## Message Structure

### Core Fields

Every message has these core fields:

```json
{
  "message_id": "msg_01HXE...",
  "author": {
    "agent_id": "agent:role:HASH",
    "session_id": "ses_01HXE..."
  },
  "body": {
    "format": "markdown",
    "content": "Message content",
    "structured": "{...}"
  },
  "scopes": [],
  "refs": [],
  "metadata": {},
  "created_at": "2026-02-03T10:00:00Z",
  "updated_at": "2026-02-03T11:00:00Z",
  "deleted": false
}
```

### Body Formats

Messages support three content formats:

**Markdown** (default) -- best for human-readable documentation, notes, and
status updates:

```json
{ "format": "markdown", "content": "# Heading\n\nWith **formatting**" }
```

**Plain text** -- best for log messages, simple status, and system
notifications:

```json
{ "format": "plain", "content": "Simple unformatted text" }
```

**JSON** -- best for machine-readable data, API responses, and structured
status:

```json
{ "format": "json", "content": "{\"type\":\"status\",\"value\":\"complete\"}" }
```

### Structured Data

In addition to the main content, messages can include typed structured data via
the `--structured` flag:

```bash
thrum send "Test results for feature X" \
  --structured '{"type":"test_result","passed":45,"failed":2,"coverage":85.9}'
```

The `structured` field allows agents to parse machine-readable payloads, build
dashboards, trigger automated workflows, and index by structured fields.

## Scopes

Scopes define the context and visibility of a message. They answer "What is this
message about?"

### Scope Structure

```json
{ "type": "scope_type", "value": "scope_value" }
```

### Common Scope Types

| Type        | Example                       | Use                                     |
| ----------- | ----------------------------- | --------------------------------------- |
| `repo`      | `repo:github.com/user/repo`   | Messages about a specific repository    |
| `file`      | `file:src/main.go`            | Messages related to a specific file     |
| `dir`       | `dir:src/components`          | Messages about a directory and contents |
| `feature`   | `feature:user-authentication` | Messages about a feature or epic        |
| `component` | `component:api-server`        | Messages about a system component       |
| `module`    | `module:auth`                 | Messages about a code module            |

### Multiple Scopes

A message can have multiple scopes:

```bash
thrum send "Fixed authentication bug" \
  --scope repo:github.com/user/repo \
  --scope file:src/auth.go \
  --scope feature:user-authentication
```

### Filtering by Scope

```bash
thrum inbox --scope file:src/main.go
thrum inbox --scope module:auth
```

## References (Refs)

References link messages to external entities. They answer "What does this
message reference?"

### Ref Structure

```json
{ "type": "ref_type", "value": "ref_value" }
```

### Common Ref Types

| Type       | Example                             | Use                                                     |
| ---------- | ----------------------------------- | ------------------------------------------------------- |
| `issue`    | `issue:beads-123`                   | Links to a Beads issue                                  |
| `commit`   | `commit:abc123def456`               | Links to a Git commit                                   |
| `pr`       | `pr:42`                             | Links to a pull request                                 |
| `ticket`   | `ticket:JIRA-456`                   | Links to an external ticket                             |
| `url`      | `url:https://docs.example.com/page` | Links to a web page                                     |
| `mention`  | `mention:reviewer`                  | Created automatically from `--to` and `--mention` flags |
| `reply_to` | `reply_to:msg_01HXE...`             | Created by `thrum reply` to link to parent message      |

### Multiple Refs

```bash
thrum send "Implemented feature from design doc, closes issue" \
  --ref issue:beads-123 \
  --ref commit:abc123def \
  --ref url:https://docs.example.com/design
```

## Groups

Groups allow you to send messages to collections of agents and roles using a
single `--to @groupname` address.

### Built-in Groups

**`@everyone`** — Automatically created on daemon startup. All registered agents are
implicit members. This group cannot be deleted.

```bash
# Send to all agents
thrum send "Deploy complete" --to @everyone
```

### Creating Custom Groups

```bash
# Create a group
thrum group create reviewers --description "Code review team"

# Add members (agents or roles)
thrum group add reviewers @alice
thrum group add reviewers --role reviewer

# Send to the group
thrum send "PR ready for review" --to @reviewers
```

### Group Operations

| Command                           | Description                                     |
| --------------------------------- | ----------------------------------------------- |
| `thrum group create NAME`         | Create a new group                              |
| `thrum group delete NAME`         | Delete a group (cannot delete `@everyone`)      |
| `thrum group add GROUP MEMBER`    | Add agent or role to group                      |
| `thrum group remove GROUP MEMBER` | Remove a member                                 |
| `thrum group list`                | List all groups                                 |
| `thrum group info NAME`           | Show group details                              |
| `thrum group members NAME`        | List members (`--expand` resolves to agent IDs) |

### Message Resolution

When a message is sent to a group, the daemon resolves group membership at **read time**.
This means:

- New agents added to a group automatically receive messages sent to that group
- The `@everyone` group dynamically includes all registered agents
- Role-based members are resolved to all agents with that role at query time

### Broadcast Deprecation

The `--broadcast` flag is deprecated. Use `--to @everyone` instead:

```bash
# Old (deprecated):
thrum send "Deploy complete" --broadcast

# New (recommended):
thrum send "Deploy complete" --to @everyone
```

## Global Flags

These flags are available on all commands:

| Flag        | Description                                   |
| ----------- | --------------------------------------------- |
| `--json`    | Output as JSON (for scripting and automation) |
| `--quiet`   | Suppress non-essential output (hints, tips)   |
| `--verbose` | Debug output                                  |
| `--role`    | Agent role (or `THRUM_ROLE` env var)          |
| `--module`  | Agent module (or `THRUM_MODULE` env var)      |
| `--repo`    | Repository path (default: `.`)                |

## Implementation Details

### Storage

Messages are stored in two places:

1. **Sharded JSONL Event Logs** (in the `a-sync` Git worktree at
   `.git/thrum-sync/a-sync/`) -- the append-only source of truth. Events are
   sharded per agent:
   - `.git/thrum-sync/a-sync/events.jsonl` -- agent lifecycle events
     (`agent.register`, `agent.session.start`, `agent.session.end`,
     `agent.cleanup`)
   - `.git/thrum-sync/a-sync/messages/{agent_name}.jsonl` -- per-agent message
     events (`message.create`, `message.edit`, `message.delete`, `agent.update`)

2. **SQLite Projection** (`.thrum/var/messages.db`) -- derived from the JSONL
   logs for query performance. Can be rebuilt from the logs. Contains the tables
   described below.

### Tables

#### messages

```sql
CREATE TABLE messages (
  message_id TEXT PRIMARY KEY,
  agent_id TEXT NOT NULL,
  session_id TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT,
  deleted INTEGER DEFAULT 0,
  deleted_at TEXT,
  delete_reason TEXT,
  body_format TEXT NOT NULL,
  body_content TEXT NOT NULL,
  body_structured TEXT
)
```

#### message_scopes

```sql
CREATE TABLE message_scopes (
  message_id TEXT NOT NULL,
  scope_type TEXT NOT NULL,
  scope_value TEXT NOT NULL,
  PRIMARY KEY (message_id, scope_type, scope_value)
)
```

#### message_refs

```sql
CREATE TABLE message_refs (
  message_id TEXT NOT NULL,
  ref_type TEXT NOT NULL,
  ref_value TEXT NOT NULL,
  PRIMARY KEY (message_id, ref_type, ref_value)
)
```

#### message_edits

```sql
CREATE TABLE message_edits (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  message_id TEXT NOT NULL,
  edited_at TEXT NOT NULL,
  edited_by TEXT NOT NULL,
  old_content TEXT,
  new_content TEXT,
  old_structured TEXT,
  new_structured TEXT
)
```

Each row represents one edit operation with before/after values for both content
and structured data.

#### message_reads

```sql
CREATE TABLE message_reads (
  message_id TEXT NOT NULL,
  session_id TEXT NOT NULL,
  agent_id TEXT NOT NULL,
  read_at TEXT NOT NULL,
  PRIMARY KEY (message_id, session_id),
  FOREIGN KEY (message_id) REFERENCES messages(message_id) ON DELETE CASCADE
)
```

Read state is tracked per message per session. A message is considered read for
an agent if any matching `session_id` or `agent_id` has a record. This means
read state persists across session restarts for the same agent.

### Indexes

Optimized for common query patterns:

- `idx_messages_time` -- time-based sorting
- `idx_messages_agent` -- author filtering
- `idx_messages_session` -- session filtering
- `idx_scopes_lookup` -- scope filtering
- `idx_refs_lookup` -- ref filtering (including reply_to references)
- `idx_edits_message` -- edit history lookup by message and timestamp
- `idx_message_reads_agent` -- read state by agent
- `idx_message_reads_message` -- read state by message

## MCP Server Integration

The MCP server (`thrum mcp serve`) provides native messaging tools for AI agents
running in Claude Code or similar environments:

| MCP Tool            | Description                                                                                        |
| ------------------- | -------------------------------------------------------------------------------------------------- |
| `send_message`      | Send a message to `@role` or agent name (supports `priority`: `critical`, `high`, `normal`, `low`) |
| `check_messages`    | Poll for unread messages mentioning this agent, auto-marks read                                    |
| `wait_for_message`  | Block until a message arrives via WebSocket push or timeout (max 600s)                             |
| `list_agents`       | List registered agents with active/offline status                                                  |
| `broadcast_message` | Send to all active agents (with optional exclude filter)                                           |

MCP tools use the same underlying RPC methods (`message.send`, `message.list`,
`message.markRead`) but add convenience features like `@role` addressing and
real-time WebSocket push notifications.

**Priority levels at the MCP layer**: `critical`, `high`, `normal` (default),
`low`. The `critical` level is available through MCP tools and is stored on the
message but does not have special handling at the RPC layer.

## Agent Identity

Agents are identified by name using identity files stored at
`.thrum/identities/{name}.json`. Identity is resolved in this priority order:

1. `THRUM_NAME` environment variable (selects which identity file to load)
2. `THRUM_ROLE` and `THRUM_MODULE` environment variables
3. CLI flags (`--role`, `--module`, `--name`)
4. Single identity file auto-selection (solo-agent worktrees)

Agent names must match `[a-z0-9_]+`. Reserved names: `daemon`, `system`,
`thrum`, `all`, `broadcast`.

For multi-agent worktrees, each agent gets its own identity file and JSONL
shard.

## See Also

- RPC API Reference: `docs/rpc-api.md`
- Daemon Architecture: `docs/daemon.md`
- Development Guide: `docs/development.md`
