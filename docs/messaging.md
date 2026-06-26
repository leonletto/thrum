
## Thrum Messaging System

> **TL;DR:** Send messages with `thrum send`, check them with `thrum inbox`,
> reply with `thrum reply`. Messages support scopes and mentions for targeting
> specific recipients. The Quick Reference tables below have every command.
> Implementation details (storage schemas, indexes) are at the bottom — you
> don't need them for normal use.

## Overview

Thrum messaging connects agents with direct messages, read tracking, scopes,
references, and structured content. Messages go into a Git-backed event log and
project into SQLite for fast queries.

**Message delivery:** For agents in [tmux-managed sessions](tmux-sessions.md),
the daemon delivers notifications instantly via `tmux send-keys` — no background
listener needed. Agents without tmux fall back to the listener/hook-based pull
mechanism.

This document covers the CLI commands for sending, receiving, replying to, and
managing messages.

## Messaging across repos and machines

Same-repo messaging is the default. When you want agents in different repos, or
on different machines, to talk to each other, you pair their daemons as peers.
The messaging API is the same — `thrum send`, `thrum inbox`, threads — but
messages route through peers instead of staying local.

For pairing, address handling, and the two transports (same-machine and
Tailscale), see [Peers](peers.md).

## Quick Reference

### Top-Level Commands

| Command                   | Description                                                            |
| ------------------------- | ---------------------------------------------------------------------- |
| `thrum send MESSAGE`      | Send a message (with optional `--to`, `--scope`, `--ref`, `--mention`) |
| `thrum reply MSG_ID TEXT` | Reply to a message, creating a reply-to reference                      |
| `thrum inbox`             | List messages with read/unread indicators                              |
| `thrum sent`              | List messages you sent with recipient read status                      |
| `thrum message read`      | Mark messages as read (single, multiple, or --all)                     |

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
thrum send --to @coordinator_main "Test suite is green, ready for review"
```

The daemon resolves your current agent identity and session automatically. It
writes the message as a `message.create` event to the agent's sharded JSONL log
(`.git/thrum-sync/a-sync/messages/{agent_name}.jsonl`) and projects it into
SQLite.

### Flags

| Flag           | Format                  | Description                                                                      | Default |
| -------------- | ----------------------- | -------------------------------------------------------------------------------- | ------- |
| `--to`         | `@name`                 | Recipient — `@agent_name` or `@everyone` (mutually exclusive with `--broadcast`) |         |
| `--broadcast`  | —                       | Fan out to the entire team (mutually exclusive with `--to`)                      | `false` |
| `--scope`      | `type:value`            | Attach scope context (repeatable)                                                |         |
| `--ref`        | `type:value`            | Attach reference (repeatable)                                                    |         |
| `--mention`    | `@role`                 | Mention an agent role (repeatable)                                               |         |
| `--format`     | `markdown\|plain\|json` | Content format (default: `markdown`)                                             |         |
| `--structured` | JSON string             | Typed payload for machine-readable data                                          |         |

### Direct Messaging with --to

`--to` is shorthand for directing a message to a specific agent, role, or group.
Under the hood, `--to @reviewer` appends `@reviewer` to the mentions list,
stored as a `mention` ref on the message.

```bash
# These are equivalent:
thrum send "Please review PR #42" --to @reviewer
thrum send "Please review PR #42" --mention @reviewer

# Broadcast to all agents
thrum send "Deploy complete" --to @everyone
```

The `@` prefix is optional -- `--to reviewer` and `--to @reviewer` both work.

### Mention Routing

When a message has mentions (via `--to`, `--mention`, or `@role` in the mentions
list), Thrum stores each one as a ref with type `mention` and the role name as
the value:

```json
{ "type": "mention", "value": "reviewer" }
```

Agents filter their inbox to only messages that mention them:

```bash
thrum inbox --mentions
thrum sent --unread
```

This queries for messages where a `mention` ref matches the current agent's
role.

### Name vs Role Routing (v0.4.5+)

Thrum routes `@mentions` differently depending on whether the target is a name
or a role:

| Target            | Routing behaviour                                                                 |
| ----------------- | --------------------------------------------------------------------------------- |
| `@furiosa`        | Routes directly to agent named `furiosa`                                          |
| `@reviewer`       | Routes via the auto-created `reviewer` role group (all agents with role reviewer) |
| `@everyone`       | Broadcasts to all agents                                                          |
| `@sf:coordinator` | Routes to proxy agent `sf:coordinator` (cross-repo via peer transport, v0.7.0)    |

**Important:** Sending to an unknown name or group is a **hard error** — the
message is rejected and not stored. Create unknown recipients as agents or
groups first.

**Registration rule:** Agent names must differ from their role. Use
`--name coord_main --role coordinator`, not
`--name coordinator --role coordinator`.

### Example: Agent-to-Agent Coordination

```bash
# Implementer finishes a task and notifies the reviewer
thrum send "Auth module complete, all tests passing" \
  --to @reviewer \
  --scope module:auth \
  --ref issue:beads-42

# Reviewer checks inbox for messages directed at them
thrum inbox --mentions

# Implementer verifies the outgoing message recipients and receipts
thrum sent --to @reviewer

# Reviewer replies to the message
thrum reply msg_01HXE... "Looks good, merging now"
```

## Replying to Messages

`reply` links your message back to the original with a `reply_to` reference:

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

```text
> Reply sent: msg_01HXG...
  In reply to: msg_01HXE...
```

### Auto-Threading (v0.5.0+)

When you reply, Thrum automatically assigns a shared `thread_id` to both the
reply and the original message. Threads are implicit — you don't create them
explicitly.

**How it works:**

1. If the parent message already has a `thread_id`, the reply inherits it
   (joining the existing thread).
2. If the parent has no `thread_id`, a new one is generated (`thr_...`) and set
   on both the parent and the reply.
3. All subsequent replies in the chain share the same `thread_id`.

**Example:**

```bash
# Send a message (no thread_id yet)
thrum send "Auth module ready for review" --to @reviewer1

# Reply creates a thread — both messages now share thread_id
thrum reply msg_01HXE... "Looking at it now"

# Further replies join the same thread
thrum reply msg_01HXE... "Approved, merging"
```

The UI groups conversations by `thread_id`. Messages without a `thread_id` fall
back to `reply_to` chain-walking for backward compatibility.

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
| `--limit N`          | Alias for `--page-size`                    |
| `--page N`           | Page number (default: 1)                   |

### Read/Unread Indicators

Each message in the inbox shows a read-state indicator:

- `●` (filled circle) -- unread
- `○` (open circle) -- read

The header line for each message follows this format:

```text
│ ● msg_01HXE...  @implementer  5m ago                     │
│ ○ msg_01HXF...  @reviewer     1h ago  (edited)           │
│ ↳ msg_01HXG...  @implementer  10m ago                    │
```

Messages that have been edited show an `(edited)` tag in the header. Replies are
displayed with a `↳` prefix and grouped with the original message.

### Auto Mark-as-Read

Viewing the inbox marks all displayed messages as read (best-effort; failure
doesn't block the command). The `--unread` filter skips this — so you can check
what's unread without immediately clearing it.

The footer shows pagination and unread count:

```text
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

Pull a single message with its full details: author, timestamps, scopes, refs,
edit and delete status.

```bash
thrum message get msg_01HXE...
```

**Output:**

```yaml
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

Replace a message's content entirely. Only the original author (matched by
`agent_id`) can edit. Deleted messages can't be edited.

```bash
thrum message edit msg_01HXE... "Updated: auth module complete with rate limiting"
```

**Output:**

```text
> Message edited: msg_01HXE... (version 2)
```

Each edit lands in the `message_edits` table with before/after content, the
editor's session, and a timestamp. The version number is the total number of
edits on that message. Edits trigger subscription notifications just like new
messages.

### Delete

Soft-delete a message. Requires the `--force` flag as a safety confirmation.

```bash
thrum message delete msg_01HXE... --force
```

**Output:**

```text
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

Explicitly mark one or more messages as read — useful when auto mark-as-read was
skipped or when you're processing messages programmatically. Use `--all` to
clear everything at once.

```bash
thrum message read msg_01HXE...
thrum message read msg_01 msg_02 msg_03
thrum message read --all
```

**Output:**

```text
> Marked 3 messages as read
```

Read state is tracked per session and per agent in `message_reads`. A message is
"read" if any session or agent matching your current identity has a read record
for it.

### Auto Mark-as-Read Summary

Several commands mark messages as read automatically:

| Command                    | Behavior                                                  |
| -------------------------- | --------------------------------------------------------- |
| `thrum inbox`              | Marks all displayed messages as read                      |
| `thrum inbox --unread`     | Peeks at unread messages **without** marking them as read |
| `thrum reply MSG_ID ...`   | Marks the replied-to message as read                      |
| `thrum message get MSG_ID` | Marks the retrieved message as read                       |
| `thrum message read --all` | Explicitly marks all unread messages as read              |

All auto mark-as-read operations are best-effort — if they fail, the parent
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

The `structured` field lets agents parse machine-readable payloads, build
dashboards, trigger automated workflows, and index by structured fields.

## Scopes

Scopes tag the context of a message. They answer "What is this message about?"

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

## Broadcast

Use `--to @everyone` to send to all agents:

```bash
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

---

_The sections below cover storage internals. You don't need these for normal
use._

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

The MCP server (`thrum mcp serve`) gives AI agents running in Claude Code or
similar environments native messaging tools. It exposes 4 MCP tools:
`send_message`, `check_messages`, `wait_for_message`, and `list_agents`. MCP
tools run the same underlying RPC methods but add `@role` addressing and
real-time WebSocket push notifications.

See [MCP Server](mcp-server.md) for the complete tools reference and
configuration.

## Agent Identity

Each agent has an identity file at `.thrum/identities/{name}.json`. Identity
resolves in this priority order:

1. `THRUM_NAME` environment variable (selects which identity file to load)
2. `THRUM_ROLE` and `THRUM_MODULE` environment variables
3. CLI flags (`--role`, `--module`, `--name`)
4. Single identity file auto-selection (solo-agent worktrees)

Agent names must match `[a-z0-9_]+`. Reserved names: `daemon`, `system`,
`thrum`, `all`, `broadcast`.

In multi-agent worktrees, each agent gets its own identity file and JSONL shard.

## Next Steps

- [Peers](peers.md) — pair daemons to route messages across repos and machines
- [Subscriptions & Notifications](subscriptions.md) — subscribe to scopes and
  mentions so messages arrive as push notifications instead of requiring polling
- [MCP Server](mcp-server.md) — optional native tool integration for
  environments that support MCP
- [Agent Coordination](agent-coordination.md) — practical multi-agent workflows
  combining Thrum messaging with Beads task tracking
- [RPC API Reference](rpc-api.md) — full JSON-RPC method reference if you're
  building a custom integration
