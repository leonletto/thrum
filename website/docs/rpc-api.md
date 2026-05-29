---
title: "RPC API"
description:
  "Thrum daemon JSON-RPC 2.0 API reference — all RPC methods, parameters, and
  response formats"
category: "reference"
last_updated: "2026-04-20"
---

## Thrum Daemon RPC API

> **TL;DR:** 50+ RPC methods over JSON-RPC 2.0, available on a Unix socket or
> WebSocket. Most users never need this — the CLI wraps all of it. This
> reference is for building custom integrations or understanding what's
> happening under the hood.

## Overview

The Thrum daemon exposes a JSON-RPC 2.0 API over Unix socket
(`.thrum/var/thrum.sock`) and WebSocket (`ws://localhost:9999`). This document
describes every available RPC method.

## Protocol

### JSON-RPC 2.0

All requests and responses follow the JSON-RPC 2.0 specification.

**Request format:**

```json
{
  "jsonrpc": "2.0",
  "method": "method_name",
  "params": {},
  "id": 1
}
```

**Success response:**

```json
{
  "jsonrpc": "2.0",
  "result": {
    /* method-specific result */
  },
  "id": 1
}
```

**Error response:**

```json
{
  "jsonrpc": "2.0",
  "error": {
    "code": -32000,
    "message": "Error description",
    "data": "Additional error info"
  },
  "id": 1
}
```

### Standard Error Codes

| Code   | Meaning          | Description                      |
| ------ | ---------------- | -------------------------------- |
| -32700 | Parse error      | Invalid JSON                     |
| -32600 | Invalid request  | Not a valid JSON-RPC 2.0 request |
| -32601 | Method not found | Method doesn't exist             |
| -32602 | Invalid params   | Invalid method parameters        |
| -32603 | Internal error   | Internal JSON-RPC error          |
| -32000 | Server error     | Generic server error             |
| -32001 | Transport error  | Wrong transport for this method  |

### Transports

Both Unix socket and WebSocket expose the same set of methods, with one
exception:

- `user.register` is only available over WebSocket (returns `-32001` on Unix
  socket)

### Caller Identity

Most RPC fields in this document include a `caller_agent_id` parameter. Its
semantics depend on the transport:

- **Unix socket (v0.9.0+):** The daemon resolves caller identity via kernel peer
  credentials (`SO_PEERCRED` on Linux, `LOCAL_PEERPID` on macOS). If
  `caller_agent_id` is also provided, the daemon cross-checks it against the
  kernel-verified identity. A mismatch returns
  `unauthenticated_rpc/identity_mismatch`. Forged claims cannot succeed.
  **Shared-worktree fallback:** when peercred resolves to a worktree hosting
  multiple co-located agents and the claimed `caller_agent_id` is also
  registered in that same worktree, the claim is trusted (the CLI plumbs
  `caller_agent_id` from `THRUM_NAME` / the local identity file on every
  mutating call, including `message.delete`). Cross-worktree claims still fail.
  See [Security Model](security-model.md) for the full trust stack.
- **WebSocket:** Peercred is not available. Mutating RPCs require a non-empty
  `caller_agent_id`. Strict mode returns
  `unauthenticated_rpc/no_caller_agent_id` when it is absent. See
  [Troubleshooting Identity](troubleshooting-identity.md) for remediation steps.
  (G3 note)

**Anonymous callers** (callers whose CWD does not resolve to a registered agent
worktree) may only invoke a hardcoded read-only allowlist of ~30 methods.
Mutating RPCs are rejected at the dispatcher before the handler runs. See
[Security Model](security-model.md) for the full allowlist.

## Method Reference

### health

Health check and daemon status.

**Request:**

| Parameter | Type | Required | Description                 |
| --------- | ---- | -------- | --------------------------- |
| _(none)_  |      |          | Empty object or omit params |

**Response:**

| Field                     | Type    | Description                                                       |
| ------------------------- | ------- | ----------------------------------------------------------------- |
| `status`                  | string  | `"ok"` or `"degraded"`                                            |
| `uptime_ms`               | integer | Daemon uptime in milliseconds                                     |
| `version`                 | string  | Daemon version (e.g., `"0.1.0"`)                                  |
| `repo_id`                 | string  | Repository identifier                                             |
| `sync_state`              | string  | `"synced"`, `"pending"`, or `"error"` (requires active sync loop) |
| `identity`                | object  | Daemon identity fields (omitted when identity is not initialized) |
| `identity.daemon_id`      | string  | ULID-based daemon identifier (e.g., `"d_01J..."`)                 |
| `identity.repo_name`      | string  | Repository name (e.g., `"falcon-backend"`)                        |
| `identity.hostname`       | string  | Machine hostname                                                  |
| `identity.repo_path`      | string  | Absolute path to the repository root                              |
| `identity.git_origin_url` | string  | Git remote URL (omitted when not set)                             |
| `identity.init_at`        | string  | ISO 8601 timestamp when this daemon_id was first generated        |

**Errors:**

- No method-specific errors.

### agent.register

Register or update an agent identity.

**Request:**

| Parameter     | Type    | Required | Description                                                                                                                        |
| ------------- | ------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| `name`        | string  | no       | Human-readable agent name (e.g., `"furiosa"`). Must match `[a-z0-9_]+`. Reserved: `daemon`, `system`, `thrum`, `all`, `broadcast`. |
| `role`        | string  | yes      | Agent role (e.g., `"implementer"`, `"reviewer"`)                                                                                   |
| `module`      | string  | yes      | Module/component responsibility (e.g., `"auth"`)                                                                                   |
| `display`     | string  | no       | Human-readable display name                                                                                                        |
| `force`       | boolean | no       | Override existing registration by a different agent                                                                                |
| `re_register` | boolean | no       | Same agent returning (re-register after identity loss)                                                                             |

**Response:**

| Field                        | Type   | Description                                             |
| ---------------------------- | ------ | ------------------------------------------------------- |
| `agent_id`                   | string | Generated agent ID (e.g., `"agent:implementer:ABC123"`) |
| `status`                     | string | `"registered"`, `"updated"`, or `"conflict"`            |
| `conflict`                   | object | Present only when `status` is `"conflict"`              |
| `conflict.existing_agent_id` | string | ID of the conflicting agent                             |
| `conflict.registered_at`     | string | ISO 8601 timestamp of conflicting registration          |
| `conflict.last_seen_at`      | string | ISO 8601 timestamp of last activity                     |

**Errors:**

- `invalid request`: Malformed JSON params
- `role is required`: Missing `role` field
- `module is required`: Missing `module` field

**Notes:**

- The role+module conflict check is scoped to the **local daemon only**. Agents
  that were registered on a remote daemon and synced into the local DB via
  `origin_daemon` are not treated as conflicts — two daemons in a peer mesh can
  have agents with identical role+module without triggering this error. This
  fixes the cross-daemon force-delete bug tracked as thrum-mm3l.

### agent.list

List registered agents with optional filters.

**Request:**

| Parameter | Type   | Required | Description      |
| --------- | ------ | -------- | ---------------- |
| `role`    | string | no       | Filter by role   |
| `module`  | string | no       | Filter by module |

**Response:**

| Field                    | Type   | Description                                     |
| ------------------------ | ------ | ----------------------------------------------- |
| `agents`                 | array  | List of agent objects                           |
| `agents[].agent_id`      | string | Agent ID                                        |
| `agents[].kind`          | string | `"agent"` or `"user"`                           |
| `agents[].role`          | string | Agent role                                      |
| `agents[].module`        | string | Agent module                                    |
| `agents[].display`       | string | Display name                                    |
| `agents[].registered_at` | string | ISO 8601 registration timestamp                 |
| `agents[].last_seen_at`  | string | ISO 8601 last activity timestamp (may be empty) |

**Errors:**

- `invalid request`: Malformed JSON params

### agent.whoami

Get current agent identity and active session.

**Request:**

| Parameter | Type | Required | Description                 |
| --------- | ---- | -------- | --------------------------- |
| _(none)_  |      |          | Empty object or omit params |

**Response:**

| Field           | Type   | Description                                                       |
| --------------- | ------ | ----------------------------------------------------------------- |
| `agent_id`      | string | Current agent ID                                                  |
| `role`          | string | Agent role                                                        |
| `module`        | string | Agent module                                                      |
| `display`       | string | Display name                                                      |
| `source`        | string | Identity source: `"environment"`, `"flags"`, or `"identity_file"` |
| `session_id`    | string | Active session ID (empty if no active session)                    |
| `session_start` | string | ISO 8601 session start time (empty if no active session)          |

**Errors:**

- `resolve identity`: Could not determine agent identity from environment or
  identity file

### agent.listContext

List agent work contexts (branch, commits, files, intent, task).

**Request:**

| Parameter  | Type   | Required | Description                                                |
| ---------- | ------ | -------- | ---------------------------------------------------------- |
| `agent_id` | string | no       | Filter by specific agent ID                                |
| `branch`   | string | no       | Filter by branch name                                      |
| `file`     | string | no       | Filter by file path (matches changed or uncommitted files) |

**Response:**

| Field                                   | Type   | Description                                       |
| --------------------------------------- | ------ | ------------------------------------------------- |
| `contexts`                              | array  | List of work context objects                      |
| `contexts[].session_id`                 | string | Session ID                                        |
| `contexts[].agent_id`                   | string | Agent ID                                          |
| `contexts[].branch`                     | string | Current Git branch (may be empty)                 |
| `contexts[].worktree_path`              | string | Worktree filesystem path (may be empty)           |
| `contexts[].unmerged_commits`           | array  | List of commit summaries not on main              |
| `contexts[].unmerged_commits[].hash`    | string | Commit hash                                       |
| `contexts[].unmerged_commits[].subject` | string | Commit subject line                               |
| `contexts[].uncommitted_files`          | array  | List of uncommitted file paths                    |
| `contexts[].changed_files`              | array  | List of all changed file paths                    |
| `contexts[].git_updated_at`             | string | ISO 8601 timestamp of last git context extraction |
| `contexts[].current_task`               | string | Current task identifier (may be empty)            |
| `contexts[].task_updated_at`            | string | ISO 8601 timestamp of last task update            |
| `contexts[].intent`                     | string | Free-text intent description (may be empty)       |
| `contexts[].intent_updated_at`          | string | ISO 8601 timestamp of last intent update          |

**Errors:**

- `invalid request`: Malformed JSON params

### agent.delete

Delete an agent by name. Removes the identity file, per-agent JSONL message
file, and SQLite record. Emits an `agent.cleanup` event.

**Request:**

| Parameter | Type   | Required | Description                                    |
| --------- | ------ | -------- | ---------------------------------------------- |
| `name`    | string | yes      | Agent name to delete (must match `[a-z0-9_]+`) |

**Response:**

| Field      | Type    | Description                         |
| ---------- | ------- | ----------------------------------- |
| `agent_id` | string  | Deleted agent ID                    |
| `deleted`  | boolean | `true` if the agent was deleted     |
| `message`  | string  | Human-readable confirmation message |

**Errors:**

- `agent name is required`: Missing `name` field
- `invalid agent name`: Name does not match validation regex
- `agent not found`: No agent with given name

### agent.cleanup

Detect and optionally remove orphaned agents. An agent is considered orphaned if
its identity file is missing, its worktree no longer exists, or it has been
inactive beyond the threshold.

**Request:**

| Parameter   | Type    | Required | Description                            |
| ----------- | ------- | -------- | -------------------------------------- |
| `dry_run`   | boolean | no       | Preview orphans without deleting       |
| `force`     | boolean | no       | Delete all orphans without prompting   |
| `threshold` | integer | no       | Days since last seen to consider stale |

**Response:**

| Field                            | Type    | Description                                              |
| -------------------------------- | ------- | -------------------------------------------------------- |
| `orphans`                        | array   | List of orphaned agent objects                           |
| `orphans[].agent_id`             | string  | Agent ID                                                 |
| `orphans[].role`                 | string  | Agent role                                               |
| `orphans[].module`               | string  | Agent module                                             |
| `orphans[].worktree`             | string  | Worktree name (may be empty)                             |
| `orphans[].branch`               | string  | Branch name (may be empty)                               |
| `orphans[].last_seen_at`         | string  | ISO 8601 last activity timestamp                         |
| `orphans[].worktree_missing`     | boolean | Whether the agent's worktree no longer exists            |
| `orphans[].branch_missing`       | boolean | Whether the agent's branch no longer exists              |
| `orphans[].days_since_last_seen` | integer | Days since last activity                                 |
| `orphans[].message_count`        | integer | Number of messages from this agent                       |
| `deleted`                        | array   | List of deleted agent ID strings (empty in dry-run mode) |
| `dry_run`                        | boolean | Whether this was a dry-run                               |
| `message`                        | string  | Summary message                                          |

**Errors:**

- `invalid request`: Malformed JSON params

### session.start

Start a new work session for an agent. Automatically recovers any orphaned
sessions for the same agent.

**Request:**

| Parameter  | Type   | Required | Description                                                  |
| ---------- | ------ | -------- | ------------------------------------------------------------ |
| `agent_id` | string | yes      | Which agent is starting the session                          |
| `scopes`   | array  | no       | Initial session scopes (`[{"type": "...", "value": "..."}]`) |
| `refs`     | array  | no       | Initial session refs (`[{"type": "...", "value": "..."}]`)   |

**Response:**

| Field        | Type   | Description                             |
| ------------ | ------ | --------------------------------------- |
| `session_id` | string | New session ID (e.g., `"ses_01HXE..."`) |
| `agent_id`   | string | Agent ID that owns this session         |
| `started_at` | string | ISO 8601 session start timestamp        |

**Errors:**

- `agent_id is required`: Missing `agent_id` field
- `agent not found`: Agent with given ID is not registered

### session.end

End an active work session. Syncs work contexts to JSONL on end.

**Request:**

| Parameter    | Type   | Required | Description                                                 |
| ------------ | ------ | -------- | ----------------------------------------------------------- |
| `session_id` | string | yes      | Session ID to end                                           |
| `reason`     | string | no       | End reason: `"normal"` (default), `"crash"`, `"superseded"` |

**Response:**

| Field         | Type    | Description                      |
| ------------- | ------- | -------------------------------- |
| `session_id`  | string  | Ended session ID                 |
| `ended_at`    | string  | ISO 8601 end timestamp           |
| `duration_ms` | integer | Session duration in milliseconds |

**Errors:**

- `session_id is required`: Missing `session_id` field
- `get session`: Session ID not found

### session.heartbeat

Update session activity timestamp. Optionally add/remove scopes and refs.
Extracts git work context if a `worktree` ref is set.

**Request:**

| Parameter       | Type   | Required | Description                                         |
| --------------- | ------ | -------- | --------------------------------------------------- |
| `session_id`    | string | yes      | Session ID                                          |
| `add_scopes`    | array  | no       | Scopes to add (`[{"type": "...", "value": "..."}]`) |
| `remove_scopes` | array  | no       | Scopes to remove                                    |
| `add_refs`      | array  | no       | Refs to add (`[{"type": "...", "value": "..."}]`)   |
| `remove_refs`   | array  | no       | Refs to remove                                      |

**Response:**

| Field              | Type    | Description                                                                                                                                    |
| ------------------ | ------- | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| `session_id`       | string  | Session ID                                                                                                                                     |
| `last_seen_at`     | string  | ISO 8601 updated last-seen timestamp                                                                                                           |
| `branch`           | string  | Current git branch name; omitted if no `worktree` ref is set or git extraction fails                                                           |
| `unmerged_commits` | integer | Count of commits on the current branch not yet merged to the default branch; omitted if no git context                                         |
| `file_changes`     | array   | List of changed files: `[{"path": "...", "last_modified": "...", "additions": N, "deletions": N, "status": "..."}]`; omitted if no git context |

**Errors:**

- `session_id is required`: Missing `session_id` field
- `session not found`: Session ID does not exist
- `session has already ended`: Session was previously ended

### session.setIntent

Set a free-text intent describing what the agent is working on.

**Request:**

| Parameter    | Type   | Required | Description                                                                 |
| ------------ | ------ | -------- | --------------------------------------------------------------------------- |
| `session_id` | string | yes      | Session ID                                                                  |
| `intent`     | string | yes      | Free-text intent (e.g., `"Refactoring auth flow"`). Empty string clears it. |

**Response:**

| Field               | Type   | Description                      |
| ------------------- | ------ | -------------------------------- |
| `session_id`        | string | Session ID                       |
| `intent`            | string | The intent that was set          |
| `intent_updated_at` | string | ISO 8601 timestamp of the update |

**Errors:**

- `session_id is required`: Missing `session_id` field
- `session not found`: Session ID does not exist
- `session has already ended`: Session was previously ended

### session.setTask

Set the current task identifier for the session.

**Request:**

| Parameter      | Type   | Required | Description                                                          |
| -------------- | ------ | -------- | -------------------------------------------------------------------- |
| `session_id`   | string | yes      | Session ID                                                           |
| `current_task` | string | yes      | Task identifier (e.g., `"beads:thrum-xyz"`). Empty string clears it. |

**Response:**

| Field             | Type   | Description                      |
| ----------------- | ------ | -------------------------------- |
| `session_id`      | string | Session ID                       |
| `current_task`    | string | The task that was set            |
| `task_updated_at` | string | ISO 8601 timestamp of the update |

**Errors:**

- `session_id is required`: Missing `session_id` field
- `session not found`: Session ID does not exist
- `session has already ended`: Session was previously ended

### session.list

List sessions with optional filters.

**Request:**

| Parameter     | Type    | Required | Description                             |
| ------------- | ------- | -------- | --------------------------------------- |
| `agent_id`    | string  | no       | Filter by agent ID                      |
| `active_only` | boolean | no       | Only return active (non-ended) sessions |

**Response:**

| Field                     | Type   | Description                              |
| ------------------------- | ------ | ---------------------------------------- |
| `sessions`                | array  | List of session objects                  |
| `sessions[].session_id`   | string | Session ID                               |
| `sessions[].agent_id`     | string | Agent ID                                 |
| `sessions[].started_at`   | string | ISO 8601 start timestamp                 |
| `sessions[].ended_at`     | string | ISO 8601 end timestamp (empty if active) |
| `sessions[].end_reason`   | string | End reason (empty if active)             |
| `sessions[].last_seen_at` | string | ISO 8601 last heartbeat timestamp        |
| `sessions[].intent`       | string | Free-text intent (empty if not set)      |
| `sessions[].status`       | string | `"active"` or `"ended"`                  |

**Errors:**

- `invalid request`: Malformed JSON params

### message.send

Send a message to the messaging system. Triggers subscription notifications.

**Request:**

| Parameter    | Type    | Required | Description                                                                                                                                                                                  |
| ------------ | ------- | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `content`    | string  | yes      | Message body text                                                                                                                                                                            |
| `format`     | string  | no       | `"markdown"` (default), `"plain"`, or `"json"`                                                                                                                                               |
| `structured` | object  | no       | Typed JSON payload                                                                                                                                                                           |
| `thread_id`  | string  | no       | Thread identifier (deprecated in v0.4.0). Use `reply_to` instead — replying to a message automatically creates or joins a thread, and the resulting `thread_id` is returned in the response. |
| `reply_to`   | string  | no       | Message ID to reply to. Triggers implicit auto-threading: a `thread_id` is automatically created (for the first reply) or joined (for subsequent replies), and returned in the response.     |
| `scopes`     | array   | no       | Message scopes (`[{"type": "...", "value": "..."}]`)                                                                                                                                         |
| `refs`       | array   | no       | Message references (`[{"type": "...", "value": "..."}]`)                                                                                                                                     |
| `mentions`   | array   | no       | Mention roles (e.g., `["@reviewer"]`)                                                                                                                                                        |
| `tags`       | array   | no       | Message tags                                                                                                                                                                                 |
| `acting_as`  | string  | no       | Impersonate this agent ID (users only)                                                                                                                                                       |
| `disclose`   | boolean | no       | Show `[via user:X]` tag when impersonating                                                                                                                                                   |

**Response:**

| Field         | Type    | Description                                                                                   |
| ------------- | ------- | --------------------------------------------------------------------------------------------- |
| `message_id`  | string  | Generated message ID (e.g., `"msg_01HXE..."`)                                                 |
| `thread_id`   | string  | Thread ID if the message was sent with `reply_to` (auto-created or joined); omitted otherwise |
| `created_at`  | string  | ISO 8601 creation timestamp                                                                   |
| `resolved_to` | integer | Number of `mentions` that were resolved to known agents                                       |
| `warnings`    | array   | Informational warning strings (e.g., unresolvable mentions); omitted when empty               |

**Errors:**

- `content is required`: Missing `content` field
- `invalid format`: Format not one of `markdown`, `plain`, `json`
- `no active session found`: Agent does not have an active session
- `only users can impersonate agents`: Non-user tried to use `acting_as`
- `target agent does not exist`: `acting_as` references nonexistent agent

### message.get

Retrieve a single message by ID with full details.

**Request:**

| Parameter    | Type   | Required | Description            |
| ------------ | ------ | -------- | ---------------------- |
| `message_id` | string | yes      | Message ID to retrieve |

**Response:**

| Field                            | Type    | Description                                          |
| -------------------------------- | ------- | ---------------------------------------------------- |
| `message`                        | object  | Full message detail                                  |
| `message.message_id`             | string  | Message ID                                           |
| `message.author`                 | object  | Author information                                   |
| `message.author.agent_id`        | string  | Author agent ID                                      |
| `message.author.session_id`      | string  | Session ID that created the message                  |
| `message.body`                   | object  | Message body                                         |
| `message.body.format`            | string  | `"markdown"`, `"plain"`, or `"json"`                 |
| `message.body.content`           | string  | Message text                                         |
| `message.body.structured`        | string  | JSON string of structured data (empty if none)       |
| `message.scopes`                 | array   | Message scopes                                       |
| `message.refs`                   | array   | Message refs                                         |
| `message.metadata`               | object  | Deletion metadata                                    |
| `message.metadata.deleted_at`    | string  | ISO 8601 deletion timestamp (empty if not deleted)   |
| `message.metadata.delete_reason` | string  | Deletion reason (empty if not deleted)               |
| `message.created_at`             | string  | ISO 8601 creation timestamp                          |
| `message.updated_at`             | string  | ISO 8601 last edit timestamp (empty if never edited) |
| `message.deleted`                | boolean | Whether the message is deleted                       |

**Errors:**

- `message_id is required`: Missing `message_id` field
- `message not found`: No message with given ID

### message.list

List messages with filtering, pagination, and sorting.

**Request:**

| Parameter             | Type    | Required | Description                                                                                        |
| --------------------- | ------- | -------- | -------------------------------------------------------------------------------------------------- |
| `scope`               | object  | no       | Filter by scope (`{"type": "...", "value": "..."}`)                                                |
| `ref`                 | object  | no       | Filter by ref (`{"type": "...", "value": "..."}`)                                                  |
| `thread_id`           | string  | no       | Filter by thread ID                                                                                |
| `author_id`           | string  | no       | Filter by author agent ID                                                                          |
| `mentions`            | boolean | no       | Only messages mentioning current agent (resolved from config)                                      |
| `unread`              | boolean | no       | Only unread messages (resolved from config)                                                        |
| `mention_role`        | string  | no       | Explicit filter: messages with mention ref matching this role (for remote callers like MCP server) |
| `unread_for_agent`    | string  | no       | Explicit filter: messages unread by this agent ID (for remote callers like MCP server)             |
| `exclude_self`        | boolean | no       | Exclude messages authored by current agent (inbox mode)                                            |
| `caller_agent_id`     | string  | no       | For worktree callers to pass their agent ID                                                        |
| `caller_mention_role` | string  | no       | For worktree callers to pass their role for mentions filter                                        |
| `for_agent`           | string  | no       | Filter for messages addressed to this agent name (mentions + broadcasts)                           |
| `for_agent_role`      | string  | no       | Filter for messages addressed to this agent role (mentions + broadcasts)                           |
| `page_size`           | integer | no       | Items per page (default: 10, max: 100)                                                             |
| `page`                | integer | no       | Page number (default: 1)                                                                           |
| `sort_by`             | string  | no       | `"created_at"` (default) or `"updated_at"`                                                         |
| `sort_order`          | string  | no       | `"asc"` or `"desc"` (default)                                                                      |

**Response:**

| Field                   | Type    | Description                                                |
| ----------------------- | ------- | ---------------------------------------------------------- |
| `messages`              | array   | List of message summaries                                  |
| `messages[].message_id` | string  | Message ID                                                 |
| `messages[].agent_id`   | string  | Author agent ID                                            |
| `messages[].body`       | object  | Message body (format, content, structured)                 |
| `messages[].created_at` | string  | ISO 8601 creation timestamp                                |
| `messages[].deleted`    | boolean | Whether the message is deleted                             |
| `messages[].is_read`    | boolean | Whether the message has been read by current agent/session |
| `total`                 | integer | Total matching messages                                    |
| `unread`                | integer | Count of unread messages                                   |
| `page`                  | integer | Current page number                                        |
| `page_size`             | integer | Items per page                                             |
| `total_pages`           | integer | Total number of pages                                      |

**Errors:**

- `invalid sort_by`: Must be `"created_at"` or `"updated_at"`
- `invalid sort_order`: Must be `"asc"` or `"desc"`

### message.edit

Edit a message's content or structured data. Only the original author can edit.

**Request:**

| Parameter    | Type   | Required | Description                                                              |
| ------------ | ------ | -------- | ------------------------------------------------------------------------ |
| `message_id` | string | yes      | Message ID to edit                                                       |
| `content`    | string | no       | New content (at least one of `content` or `structured` required)         |
| `structured` | object | no       | New structured data (at least one of `content` or `structured` required) |

**Response:**

| Field        | Type    | Description                                         |
| ------------ | ------- | --------------------------------------------------- |
| `message_id` | string  | Edited message ID                                   |
| `updated_at` | string  | ISO 8601 edit timestamp                             |
| `version`    | integer | Edit count (total number of edits for this message) |

**Errors:**

- `message_id is required`: Missing `message_id` field
- `at least one of content or structured must be provided`: Neither field given
- `message not found`: No message with given ID
- `cannot edit deleted message`: Message was soft-deleted
- `only message author can edit`: Current agent is not the message author
- `no active session found`: Agent does not have an active session

### message.delete

Soft-delete a message. The message remains in the database and JSONL log but is
marked as deleted.

**Request:**

| Parameter    | Type   | Required | Description          |
| ------------ | ------ | -------- | -------------------- |
| `message_id` | string | yes      | Message ID to delete |
| `reason`     | string | no       | Reason for deletion  |

**Response:**

| Field        | Type   | Description                 |
| ------------ | ------ | --------------------------- |
| `message_id` | string | Deleted message ID          |
| `deleted_at` | string | ISO 8601 deletion timestamp |

**Errors:**

- `message_id is required`: Missing `message_id` field
- `message not found`: No message with given ID
- `message already deleted`: Message was already soft-deleted
- `only message author can delete`: Caller is not the message author; only the
  agent that sent the message may delete it. Non-author callers receive this
  error regardless of transport.

### message.markRead

Batch mark messages as read for the current agent and session. Returns
collaboration info (other agents who also read the messages).

**Request:**

| Parameter     | Type  | Required | Description                |
| ------------- | ----- | -------- | -------------------------- |
| `message_ids` | array | yes      | List of message ID strings |

**Response:**

| Field          | Type    | Description                                                                       |
| -------------- | ------- | --------------------------------------------------------------------------------- |
| `marked_count` | integer | Number of messages successfully marked as read                                    |
| `also_read_by` | object  | Map of message ID to list of other agent IDs that also read it (omitted if empty) |

**Errors:**

- `message_ids is required and must not be empty`: Missing or empty
  `message_ids` array
- `no active session found`: Agent does not have an active session

### message.deleteByAgent

Hard-delete all messages authored by a specific agent. The caller must be the
same agent as the target — this method cannot be used to delete another agent's
messages.

**Unix socket only.** Not available over WebSocket (structural guard prevents
registration on the WebSocket transport).

**Request:**

| Parameter  | Type   | Required | Description                       |
| ---------- | ------ | -------- | --------------------------------- |
| `agent_id` | string | yes      | Agent ID whose messages to delete |

**Response:**

| Field           | Type    | Description                     |
| --------------- | ------- | ------------------------------- |
| `deleted_count` | integer | Number of messages hard-deleted |

**Errors:**

- `agent_id is required`: Missing `agent_id` field
- `unauthorized`: Caller identity does not match the target `agent_id`. Only the
  agent itself may invoke this method.

### message.deleteByScope

> **Daemon-internal only.** This method is not callable from external clients —
> not from the CLI, WebSocket connections, or external unix-socket callers. It
> is reserved for internal daemon housekeeping operations. The method is not
> registered on the WebSocket transport at all.

**Errors:**

- `-32601` (Method not found): Returned to any external caller attempting to
  invoke this method.

### group.create

Create a named group for targeted messaging. Groups can contain agents and
roles.

**Request:**

| Parameter     | Type   | Required | Description                      |
| ------------- | ------ | -------- | -------------------------------- |
| `name`        | string | yes      | Group name (e.g., `"reviewers"`) |
| `description` | string | no       | Human-readable description       |

**Response:**

| Field        | Type   | Description                 |
| ------------ | ------ | --------------------------- |
| `group_id`   | string | Unique group identifier     |
| `name`       | string | Group name                  |
| `created_at` | string | ISO 8601 creation timestamp |
| `created_by` | string | Agent ID of creator         |

**Errors:**

- `name is required`: Missing `name` field
- `group already exists`: Group with this name already exists

### group.delete

Delete a group by name. The `@everyone` group is protected and cannot be
deleted.

**Request:**

| Parameter | Type   | Required | Description |
| --------- | ------ | -------- | ----------- |
| `name`    | string | yes      | Group name  |

**Response:**

| Field        | Type   | Description                 |
| ------------ | ------ | --------------------------- |
| `name`       | string | Name of deleted group       |
| `deleted_at` | string | ISO 8601 deletion timestamp |

**Errors:**

- `name is required`: Missing `name` field
- `cannot delete protected group`: Attempted to delete `@everyone`
- `group not found`: No group with given name

### group.member.add

Add a member to a group. Members can be agents (by name) or roles.

**Request:**

| Parameter      | Type   | Required | Description             |
| -------------- | ------ | -------- | ----------------------- |
| `group`        | string | yes      | Group to add member to  |
| `member_type`  | string | yes      | `"agent"` or `"role"`   |
| `member_value` | string | yes      | Agent name or role name |

**Response:**

| Field          | Type   | Description          |
| -------------- | ------ | -------------------- |
| `group`        | string | Group name           |
| `member_type`  | string | Type of member added |
| `member_value` | string | ID of member added   |

**Errors:**

- `group is required`: Missing `group` field
- `member_type is required`: Missing `member_type` field
- `member_value is required`: Missing `member_value` field
- `group not found`: No group with given name
- `invalid member_type`: Must be `"agent"` or `"role"`

### group.member.remove

Remove a member from a group.

**Request:**

| Parameter      | Type   | Required | Description                 |
| -------------- | ------ | -------- | --------------------------- |
| `group`        | string | yes      | Group to remove member from |
| `member_type`  | string | yes      | `"agent"` or `"role"`       |
| `member_value` | string | yes      | Agent name or role name     |

**Response:**

| Field          | Type   | Description            |
| -------------- | ------ | ---------------------- |
| `group`        | string | Group name             |
| `member_type`  | string | Type of member removed |
| `member_value` | string | ID of member removed   |

**Errors:**

- `group is required`: Missing `group` field
- `member_type is required`: Missing `member_type` field
- `member_value is required`: Missing `member_value` field
- `group not found`: No group with given name
- `member not found`: Member is not in the group

### group.list

List all groups in the system.

**Request:**

| Parameter | Type | Required | Description                 |
| --------- | ---- | -------- | --------------------------- |
| _(none)_  |      |          | Empty object or omit params |

**Response:**

| Field                   | Type    | Description                      |
| ----------------------- | ------- | -------------------------------- |
| `groups`                | array   | List of group objects            |
| `groups[].group_id`     | string  | Unique group identifier          |
| `groups[].name`         | string  | Group name                       |
| `groups[].description`  | string  | Group description (may be empty) |
| `groups[].created_at`   | string  | ISO 8601 creation timestamp      |
| `groups[].member_count` | integer | Number of direct members         |

**Errors:**

- No method-specific errors.

### group.info

Get detailed information about a specific group.

**Request:**

| Parameter | Type   | Required | Description |
| --------- | ------ | -------- | ----------- |
| `name`    | string | yes      | Group name  |

**Response:**

| Field                    | Type   | Description                              |
| ------------------------ | ------ | ---------------------------------------- |
| `group_id`               | string | Unique group identifier                  |
| `name`                   | string | Group name                               |
| `description`            | string | Group description (may be empty)         |
| `created_at`             | string | ISO 8601 creation timestamp              |
| `created_by`             | string | Agent ID of creator                      |
| `members`                | array  | List of member objects                   |
| `members[].member_type`  | string | `"agent"` or `"role"`                    |
| `members[].member_value` | string | Agent name or role name                  |
| `members[].added_at`     | string | ISO 8601 timestamp when member was added |
| `members[].added_by`     | string | Agent ID who added this member           |

**Errors:**

- `name is required`: Missing `name` field
- `group not found`: No group with given name

### group.members

Get members of a group with optional expansion. When `expand` is `true`,
resolves roles to individual agent IDs.

**Request:**

| Parameter | Type    | Required | Description                                   |
| --------- | ------- | -------- | --------------------------------------------- |
| `name`    | string  | yes      | Group name                                    |
| `expand`  | boolean | no       | Resolve roles to agent IDs (default: `false`) |

**Response (without expand):**

| Field                    | Type   | Description                   |
| ------------------------ | ------ | ----------------------------- |
| `members`                | array  | List of direct member objects |
| `members[].member_type`  | string | `"agent"` or `"role"`         |
| `members[].member_value` | string | Agent name or role name       |

**Response (with expand=true):**

| Field      | Type  | Description                                                 |
| ---------- | ----- | ----------------------------------------------------------- |
| `members`  | array | List of direct member objects                               |
| `expanded` | array | List of resolved agent IDs (strings, only when expand=true) |

**Errors:**

- `name is required`: Missing `name` field
- `group not found`: No group with given name

### subscribe

> **Internal RPC only.** The `thrum subscribe`, `thrum unsubscribe`, and
> `thrum subscriptions` CLI commands have been removed. From the CLI, use
> `thrum wait` to block until a message arrives. These RPC methods remain
> available for custom clients (MCP server, WebSocket clients, scripts that talk
> directly to the daemon socket).

Subscribe to push notifications for messages matching a scope, mention, or all
messages.

**Request:**

| Parameter      | Type    | Required | Description                                             |
| -------------- | ------- | -------- | ------------------------------------------------------- |
| `scope`        | object  | no       | Subscribe to scope (`{"type": "...", "value": "..."}`)  |
| `mention_role` | string  | no       | Subscribe to mentions of this role (without `@` prefix) |
| `all`          | boolean | no       | Subscribe to all messages (firehose)                    |

Exactly one of `scope`, `mention_role`, or `all` must be provided.

**Response:**

| Field             | Type    | Description                            |
| ----------------- | ------- | -------------------------------------- |
| `subscription_id` | integer | Subscription ID (used for unsubscribe) |
| `session_id`      | string  | Session that owns this subscription    |
| `created_at`      | string  | ISO 8601 creation timestamp            |

**Errors:**

- `at least one of scope, mention_role, or all must be specified`: No
  subscription type given
- `no active session found`: Agent does not have an active session

### unsubscribe

Remove a subscription by ID.

**Request:**

| Parameter         | Type    | Required | Description               |
| ----------------- | ------- | -------- | ------------------------- |
| `subscription_id` | integer | yes      | Subscription ID to remove |

**Response:**

| Field     | Type    | Description                            |
| --------- | ------- | -------------------------------------- |
| `removed` | boolean | `true` if the subscription was deleted |

**Errors:**

- `subscription_id is required`: Missing or zero `subscription_id`
- `no active session found`: Agent does not have an active session

### subscriptions.list

List all subscriptions for the current session.

**Request:**

| Parameter | Type | Required | Description                 |
| --------- | ---- | -------- | --------------------------- |
| _(none)_  |      |          | Empty object or omit params |

**Response:**

| Field                          | Type    | Description                                        |
| ------------------------------ | ------- | -------------------------------------------------- |
| `subscriptions`                | array   | List of subscription objects                       |
| `subscriptions[].id`           | integer | Subscription ID                                    |
| `subscriptions[].scope_type`   | string  | Scope type (empty if not a scope subscription)     |
| `subscriptions[].scope_value`  | string  | Scope value (empty if not a scope subscription)    |
| `subscriptions[].mention_role` | string  | Mention role (empty if not a mention subscription) |
| `subscriptions[].all`          | boolean | `true` if this is an all-messages subscription     |
| `subscriptions[].created_at`   | string  | ISO 8601 creation timestamp                        |

**Errors:**

- `no active session found`: Agent does not have an active session

### user.register

Register a human user identity. Only available over WebSocket.

**Request:**

| Parameter  | Type   | Required | Description                                                |
| ---------- | ------ | -------- | ---------------------------------------------------------- |
| `username` | string | yes      | Username (alphanumeric, underscore, or hyphen; 1-32 chars) |
| `display`  | string | no       | Display name                                               |

**Response:**

| Field          | Type   | Description                                                 |
| -------------- | ------ | ----------------------------------------------------------- |
| `user_id`      | string | Generated user ID (e.g., `"user:leon"`)                     |
| `username`     | string | The registered username                                     |
| `display_name` | string | Display name (if provided)                                  |
| `token`        | string | Session token for reconnection                              |
| `status`       | string | `"registered"` or `"existing"` (idempotent re-registration) |

**Errors:**

- `-32001` (Transport error): Method called over Unix socket instead of
  WebSocket
- `username is required`: Missing `username` field
- `username cannot start with 'agent:' prefix`: Namespace conflict
- `invalid username format`: Does not match `[a-zA-Z0-9_-]{1,32}`

**Notes:**

- Registration is idempotent: if the user already exists, returns the existing
  info with a fresh token and status `"existing"`.

### user.identify

Get the current user's identity from git config. Used for browser
auto-registration.

**Request:**

| Parameter | Type | Required | Description                 |
| --------- | ---- | -------- | --------------------------- |
| _(none)_  |      |          | Empty object or omit params |

**Response:**

| Field      | Type   | Description                                                          |
| ---------- | ------ | -------------------------------------------------------------------- |
| `username` | string | Sanitized git `user.name` (lowercased, spaces replaced with hyphens) |
| `email`    | string | Git `user.email`                                                     |
| `display`  | string | Raw git `user.name`                                                  |

**Errors:**

- `git config user.name not set`: Git user.name is not configured in the
  repository

## Push Notifications

When a message matches a subscription, the daemon sends a JSON-RPC notification
(no `id` field) over the same socket connection:

**Notification format:**

```json
{
  "jsonrpc": "2.0",
  "method": "notification.message",
  "params": {
    "message_id": "msg_01HXE...",
    "author": {
      "agent_id": "agent:implementer:auth:ABC",
      "role": "implementer",
      "module": "auth"
    },
    "preview": "First 100 chars of message...",
    "scopes": [{ "type": "module", "value": "auth" }],
    "matched_subscription": {
      "subscription_id": 42,
      "match_type": "scope"
    },
    "timestamp": "2026-02-03T10:00:00Z"
  }
}
```

**Match types:**

- `scope` - Message has a scope matching your subscription
- `mention` - Message mentions your subscribed role
- `all` - Your "all messages" subscription matched

**Client implementation notes:**

1. Keep connection open to receive notifications
2. Notifications have no `id` field (one-way, no response expected)
3. Use `message.get` to fetch full message content after notification
4. Clients disconnected at notification time will see messages via
   `message.list` when reconnecting

### sync.status

Get current sync loop status and health. Available when the sync loop is active
(requires a remote origin).

**Request:**

| Parameter | Type | Required | Description                 |
| --------- | ---- | -------- | --------------------------- |
| _(none)_  |      |          | Empty object or omit params |

**Response:**

| Field          | Type    | Description                                     |
| -------------- | ------- | ----------------------------------------------- |
| `running`      | boolean | Whether the sync loop is running                |
| `last_sync_at` | string  | ISO 8601 timestamp of last successful sync      |
| `last_error`   | string  | Last error message (empty if no error)          |
| `sync_state`   | string  | `"stopped"`, `"idle"`, `"synced"`, or `"error"` |

**Notes:**

- This method is only registered when the sync loop is initialized (i.e., the
  repository has a remote origin). Returns method-not-found (`-32601`)
  otherwise.

### sync.force

Trigger an immediate sync (non-blocking). Available when the sync loop is active
(requires a remote origin).

**Request:**

| Parameter | Type | Required | Description                 |
| --------- | ---- | -------- | --------------------------- |
| _(none)_  |      |          | Empty object or omit params |

**Response:**

| Field          | Type    | Description                     |
| -------------- | ------- | ------------------------------- |
| `triggered`    | boolean | Whether sync was triggered      |
| `last_sync_at` | string  | ISO 8601 timestamp of last sync |
| `sync_state`   | string  | Current sync state              |

**Notes:**

- This method is only registered when the sync loop is initialized (i.e., the
  repository has a remote origin). Returns method-not-found (`-32601`)
  otherwise.
- The sync loop runs every 60 seconds by default (configurable via
  `--sync-interval`).

## Peer Methods (v0.7.0)

### peer.start_pairing

Start a pairing session. Returns a code and address for the joining peer.

**Request:**

| Parameter         | Type    | Required | Description                          |
| ----------------- | ------- | -------- | ------------------------------------ |
| `timeout_seconds` | integer | no       | Pairing timeout (default: 300)       |
| `auth_key`        | string  | no       | Tailscale auth key for tsnet startup |

**Response:**

| Field     | Type   | Description                                  |
| --------- | ------ | -------------------------------------------- |
| `code`    | string | Pairing code to share                        |
| `address` | string | Local tsnet address (e.g., `100.x.x.x:9100`) |

### peer.wait_pairing

Block until the active pairing session completes or times out. Long-poll method.

**Request:** _(none)_

**Response:**

| Field            | Type   | Description                               |
| ---------------- | ------ | ----------------------------------------- |
| `status`         | string | `"paired"`, `"timeout"`, or `"error"`     |
| `peer_name`      | string | Name of the paired peer (on success)      |
| `peer_address`   | string | Address of the paired peer (on success)   |
| `peer_daemon_id` | string | Daemon ID of the paired peer (on success) |
| `message`        | string | Human-readable status message             |

### pair.request

Low-level handshake RPC invoked by the joining peer against the listener during
`thrum peer join`. Both sides exchange identity metadata at pairing time so the
remote's repo name, hostname, repo path, and git origin URL are stored in
`peers.json` for future routing.

All four identity fields are `omitempty` — an older peer that does not send them
produces empty strings in the remote registry. No re-pairing is required for
existing peers.

**Request:**

| Parameter        | Type   | Required | Description                            |
| ---------------- | ------ | -------- | -------------------------------------- |
| `code`           | string | yes      | 16-digit numeric pairing code          |
| `daemon_id`      | string | yes      | Caller's daemon ID                     |
| `name`           | string | no       | Caller's repo name (used as peer name) |
| `address`        | string | no       | Caller's `ip:port`                     |
| `repo_name`      | string | no       | Caller's repository name               |
| `hostname`       | string | no       | Caller's machine hostname              |
| `repo_path`      | string | no       | Caller's absolute repo path            |
| `git_origin_url` | string | no       | Caller's git remote URL                |

**Response:**

| Field            | Type   | Description                                          |
| ---------------- | ------ | ---------------------------------------------------- |
| `status`         | string | `"paired"`                                           |
| `token`          | string | Long-lived shared auth token stored in `peers.json`  |
| `daemon_id`      | string | Listener's daemon ID                                 |
| `name`           | string | Listener's peer name                                 |
| `repo_name`      | string | Listener's repo name (omitted when not set)          |
| `hostname`       | string | Listener's machine hostname (omitted when not set)   |
| `repo_path`      | string | Listener's absolute repo path (omitted when not set) |
| `git_origin_url` | string | Listener's git remote URL (omitted when not set)     |

### peer.join

Connect to a remote peer using a pairing code.

**Request:**

| Parameter   | Type   | Required | Description                                   |
| ----------- | ------ | -------- | --------------------------------------------- |
| `address`   | string | yes      | Remote peer's tsnet address                   |
| `code`      | string | yes      | Pairing code from `peer.start_pairing`        |
| `repo_path` | string | no       | Filesystem path (sets transport to `"local"`) |

**Response:**

| Field            | Type   | Description                   |
| ---------------- | ------ | ----------------------------- |
| `status`         | string | `"paired"` or `"error"`       |
| `peer_name`      | string | Name of the paired peer       |
| `peer_daemon_id` | string | Daemon ID of the paired peer  |
| `message`        | string | Human-readable status message |

### peer.list

List all known peers.

**Request:** _(none)_

**Response:** Array of peer objects:

| Field             | Type    | Description                 |
| ----------------- | ------- | --------------------------- |
| `daemon_id`       | string  | Peer daemon ID              |
| `name`            | string  | Peer name                   |
| `address`         | string  | Peer address                |
| `last_sync`       | string  | Relative last sync time     |
| `last_synced_seq` | integer | Last synced sequence number |

### peer.status

Detailed per-peer health including authentication status.

**Request:** _(none)_

**Response:** Array of peer status objects:

| Field             | Type    | Description                      |
| ----------------- | ------- | -------------------------------- |
| `daemon_id`       | string  | Peer daemon ID                   |
| `name`            | string  | Peer name                        |
| `address`         | string  | Peer address                     |
| `has_token`       | boolean | Whether a shared token is stored |
| `paired_at`       | string  | ISO 8601 pairing timestamp       |
| `last_sync`       | string  | Relative last sync time          |
| `last_synced_seq` | integer | Last synced sequence number      |

### peer.remove

Remove a peer by name or daemon ID.

**Request:**

| Parameter   | Type   | Required | Description                              |
| ----------- | ------ | -------- | ---------------------------------------- |
| `name`      | string | no       | Peer name (one of `name` or `daemon_id`) |
| `daemon_id` | string | no       | Peer daemon ID                           |

**Response:**

| Field    | Type   | Description |
| -------- | ------ | ----------- |
| `status` | string | `"ok"`      |

### peer.configure

Add or remove proxy agents for a peer.

**Request:**

| Parameter    | Type   | Required | Description                       |
| ------------ | ------ | -------- | --------------------------------- |
| `peer_name`  | string | yes      | Name of the peer                  |
| `action`     | string | yes      | `"add-agent"` or `"remove-agent"` |
| `agent_name` | string | yes      | Agent name to add/remove as proxy |

**Response:**

| Field    | Type    | Description                      |
| -------- | ------- | -------------------------------- |
| `ok`     | boolean | Success indicator                |
| `action` | string  | `"added"` or `"removed"`         |
| `agent`  | string  | The agent name that was acted on |

### peer.address_changed

Notify this daemon that a remote peer's address has changed.

**Request:**

| Parameter    | Type   | Required | Description       |
| ------------ | ------ | -------- | ----------------- |
| `peer_token` | string | yes      | Shared peer token |
| `new_ip`     | string | yes      | New IP address    |
| `new_port`   | string | yes      | New port          |

**Response:**

| Field | Type    | Description       |
| ----- | ------- | ----------------- |
| `ok`  | boolean | Success indicator |

### peer.repair

Re-establish a known peer relationship after address drift, without re-pairing.
Uses the stored bearer token from `peers.json` as the trust anchor — no new
peercode is required. This RPC is distinct from `pair.request`: it does not mint
a new token and does not require an active pairing session.

Invoked automatically by the `reconcile` package on boot (`ReconcileAll`) and
inline on dial failure (`OnDialError`). Can also be triggered manually via
`thrum peer join --type repair <name>`.

**Auth:** Bearer token in the `token` field (from the stored `peers.json`
entry). Distinct from the peercode used in `pair.request`.

**Request:**

| Parameter        | Type   | Required | Description                           |
| ---------------- | ------ | -------- | ------------------------------------- |
| `token`          | string | yes      | Stored bearer token from `peers.json` |
| `daemon_id`      | string | yes      | Caller's current daemon ID            |
| `address`        | string | no       | Caller's current `ip:port`            |
| `repo_name`      | string | no       | Caller's repository name              |
| `hostname`       | string | no       | Caller's machine hostname             |
| `repo_path`      | string | no       | Caller's absolute repo path           |
| `git_origin_url` | string | no       | Caller's git remote URL               |

**Response** (`RepairResponse`):

| Field            | Type   | Description                                                      |
| ---------------- | ------ | ---------------------------------------------------------------- |
| `status`         | string | `"repaired"`                                                     |
| `daemon_id`      | string | Listener's current daemon ID (used to detect daemon_id rotation) |
| `name`           | string | Peer name (never rotated — stable across daemon_id changes)      |
| `repo_name`      | string | Listener's repo name (omitted when not set)                      |
| `hostname`       | string | Listener's machine hostname (omitted when not set)               |
| `repo_path`      | string | Listener's absolute repo path (omitted when not set)             |
| `git_origin_url` | string | Listener's git remote URL (omitted when not set)                 |

**Error codes:**

| Condition                     | Code               | Description                                                       |
| ----------------------------- | ------------------ | ----------------------------------------------------------------- |
| Token not found in registry   | `CatTokenRejected` | No peer entry matches the presented token; re-pair required       |
| Empty `daemon_id` in response | `CatOther`         | Response is malformed; reconcile manager does not re-key registry |

**When to use:** After a peer's address drifts (IP change, port reassignment,
daemon restart on a different port) and automatic reconciliation has failed. Use
`thrum peer join --type repair <name>` to trigger manually. This RPC is **not**
valid for `peer add` — only for `peer join`.

## Agent Methods (v0.8.0)

### agent.set-status

Set an agent's operational status. The daemon locates the agent's identity file
(including across worktrees) and writes the status directly.

**Request:**

| Parameter | Type   | Required | Description                                      |
| --------- | ------ | -------- | ------------------------------------------------ |
| `agent`   | string | yes      | Agent name                                       |
| `status`  | string | yes      | `"working"`, `"idle"`, `"blocked"`, or `"stuck"` |

**Response:**

| Field    | Type   | Description             |
| -------- | ------ | ----------------------- |
| `agent`  | string | Agent name              |
| `status` | string | The status that was set |

**Errors:**

- `invalid status "<value>": must be working, idle, blocked, or stuck`: Invalid
  status value

## Tmux Methods (v0.7.1)

### tmux.create

Create a tmux session for an agent with a clean environment and
`monitor-silence` hooks. Also accepts quickstart fields to register an agent
identity in the same call (equivalent to running `thrum tmux quickstart`).

**Request:**

| Parameter    | Type    | Required | Description                                                                                     |
| ------------ | ------- | -------- | ----------------------------------------------------------------------------------------------- |
| `name`       | string  | yes      | Session name                                                                                    |
| `cwd`        | string  | yes      | Working directory for the session                                                               |
| `agent_name` | string  | no       | Register agent with this name (quickstart: sets `--name`)                                       |
| `role`       | string  | no       | Agent role (quickstart: sets `--role`)                                                          |
| `module`     | string  | no       | Agent module (quickstart: sets `--module`)                                                      |
| `intent`     | string  | no       | Initial agent intent (quickstart: sets `--intent`)                                              |
| `runtime`    | string  | no       | Runtime to launch after creation (`claude`, `opencode`, `shell`) — skips launch step if omitted |
| `no_agent`   | boolean | no       | Create the session without registering an agent identity                                        |
| `force`      | boolean | no       | Override an existing identity at this path                                                      |

**Response:**

| Field      | Type   | Description                                    |
| ---------- | ------ | ---------------------------------------------- |
| `session`  | string | Created session name                           |
| `identity` | object | Agent identity file if found at cwd (nullable) |

### tmux.launch

Start an AI tool inside an existing tmux session. Writes `tmux_session` and
`runtime` to the agent's identity file.

**Request:**

| Parameter | Type   | Required | Description                                                           |
| --------- | ------ | -------- | --------------------------------------------------------------------- |
| `name`    | string | yes      | Session name                                                          |
| `runtime` | string | no       | Runtime to launch (`claude`, `opencode`, `shell`) — default: `claude` |

**Response:**

| Field     | Type   | Description           |
| --------- | ------ | --------------------- |
| `session` | string | Session name          |
| `runtime` | string | Runtime that launched |

### tmux.status

List all tmux-managed sessions with agent info and liveness state. Scans
identity files in `.thrum/identities/` for agents with `tmux_session` set, then
checks session existence and PID liveness.

**Request:** _(none)_

**Response:**

| Field                | Type   | Description                          |
| -------------------- | ------ | ------------------------------------ |
| `sessions`           | array  | List of session info objects         |
| `sessions[].name`    | string | Tmux session name                    |
| `sessions[].agent`   | string | Agent name                           |
| `sessions[].role`    | string | Agent role                           |
| `sessions[].module`  | string | Agent module                         |
| `sessions[].state`   | string | `alive`, `stale`, `dead`             |
| `sessions[].runtime` | string | Runtime (`claude`, `opencode`, etc.) |
| `sessions[].branch`  | string | Current git branch                   |

### tmux.kill

Tear down a tmux session and clear `tmux_session` from all matching identity
files.

**Request:**

| Parameter | Type   | Required | Description  |
| --------- | ------ | -------- | ------------ |
| `name`    | string | yes      | Session name |

**Response:** `null`

### tmux.send

Send text into a tmux session via `send-keys`.

**Request:**

| Parameter | Type   | Required | Description  |
| --------- | ------ | -------- | ------------ |
| `name`    | string | yes      | Session name |
| `text`    | string | yes      | Text to send |

**Response:** `null`

### tmux.capture

Capture the visible content of a tmux pane.

**Request:**

| Parameter | Type    | Required | Description                    |
| --------- | ------- | -------- | ------------------------------ |
| `name`    | string  | yes      | Session name                   |
| `lines`   | integer | no       | Lines to capture (default: 50) |

**Response:**

| Field     | Type   | Description          |
| --------- | ------ | -------------------- |
| `content` | string | Captured pane output |

### tmux.check-pane

Check a tmux pane state. The daemon's `SessionPoller` calls this internally
every ~10 seconds (2-hash stability window) to classify pane state. Direct
invocation is supported but unusual — most callers rely on the poller result
rather than invoking this RPC manually.

**Request:**

| Parameter | Type   | Required | Description                                                                       |
| --------- | ------ | -------- | --------------------------------------------------------------------------------- |
| `session` | string | yes      | Session name                                                                      |
| `content` | string | no       | Captured pane content (last 5 lines). When omitted the daemon captures it itself. |

**Note:** The `reason` field accepted by older clients is now daemon-computed,
not client-supplied. Clients that send `reason` will have it ignored.

**Response:**

| Field     | Type   | Description                                                                                 |
| --------- | ------ | ------------------------------------------------------------------------------------------- |
| `session` | string | Session name                                                                                |
| `state`   | string | Detected state: `idle`, `permission`, `working`, `command_completed`, or `working_but_idle` |
| `reason`  | string | Human-readable reason string computed by the daemon                                         |

**State values:**

| State               | Meaning                                                          |
| ------------------- | ---------------------------------------------------------------- |
| `idle`              | Pane is at a shell prompt, no active process                     |
| `permission`        | A permission prompt was detected (awaiting user approval)        |
| `working`           | AI runtime is actively generating output                         |
| `command_completed` | A queued command has completed                                   |
| `working_but_idle`  | Pane is technically idle but heuristics suggest work in progress |

### tmux.restart

Restart a tmux-managed agent session with a context snapshot. The daemon picks
between two flows based on `force`:

- **Graceful (default)** — sends an `@system` message asking the agent to save
  its own snapshot, nudges the pane, and polls for the snapshot file up to
  `restart.graceful_timeout` seconds. Falls back to JSONL extraction on timeout.
- **Force (`force: true`)** — skips the graceful prompt and extracts directly
  from the JSONL conversation transcript. Only works for Claude Code (other
  runtimes don't use the same JSONL format).

Either way, the daemon kills the session, creates a new one, and relaunches.

**Request:**

| Parameter | Type    | Required | Description                                                      |
| --------- | ------- | -------- | ---------------------------------------------------------------- |
| `name`    | string  | yes      | Session name                                                     |
| `force`   | boolean | no       | Skip graceful save prompt, extract from JSONL (default: `false`) |
| `runtime` | string  | no       | Runtime override (default: same as before)                       |

**Response:**

| Field            | Type    | Description                 |
| ---------------- | ------- | --------------------------- |
| `session`        | string  | New session name            |
| `snapshot_lines` | integer | Lines in the saved snapshot |

## Queue Methods (v0.8.0)

### tmux.queue

Submit a command to a tmux session's queue. Commands are dispatched FIFO — one
at a time. The daemon sends the command to the pane when it goes silent, detects
completion via the next silence event, captures output, and optionally sends an
`@system` inbox message to the requester.

**Request:**

| Parameter            | Type    | Required | Description                                                  |
| -------------------- | ------- | -------- | ------------------------------------------------------------ |
| `session`            | string  | yes      | Tmux session name                                            |
| `text`               | string  | yes      | Command text to send                                         |
| `requester`          | string  | yes      | Agent name of the requester (for `@system` notifications)    |
| `timeout_ms`         | integer | no       | Command timeout in ms (default: 120000)                      |
| `silence_ms`         | integer | no       | Silence threshold in ms (default: 5000)                      |
| `notify_on_complete` | boolean | no       | Send `@system` inbox message on completion (default: `true`) |

**Response:**

| Field        | Type    | Description                       |
| ------------ | ------- | --------------------------------- |
| `command_id` | string  | Command ID (`cmd_` prefix + ULID) |
| `position`   | integer | Queue position                    |

**Errors:**

- `session is required`: Missing `session` field
- `text is required`: Missing `text` field
- `requester is required`: Missing `requester` field

**`@system` messages:** When `notify_on_complete` is `true`, the daemon sends
inbox messages from agent `"system"` for completion, timeout, cancellation, and
interruption events. The message includes the command ID, session name, elapsed
time, and captured output (last 500 lines). Timeout warnings are sent when the
command exceeds its timeout but is still running — the message includes a
`thrum tmux cancel` hint. On daemon restart, interrupted commands always send
notifications regardless of `notify_on_complete`.

### tmux.queue-wait

Long-poll until a queued command reaches a terminal state (`completed`,
`cancelled`, or `interrupted`). Polls the database every 500ms.

**Request:**

| Parameter    | Type    | Required | Description                           |
| ------------ | ------- | -------- | ------------------------------------- |
| `command_id` | string  | yes      | Command ID to wait on                 |
| `timeout_ms` | integer | no       | Max wait time in ms (default: 120000) |

**Response:**

| Field        | Type    | Description                               |
| ------------ | ------- | ----------------------------------------- |
| `command_id` | string  | Command ID                                |
| `state`      | string  | Terminal state                            |
| `output`     | string  | Captured output (only on terminal states) |
| `elapsed_ms` | integer | Elapsed time from send to completion      |

**States:** `queued`, `sent`, `completed`, `timeout_waiting`, `cancelled`,
`interrupted`. Terminal states: `completed`, `cancelled`, `interrupted`.

### tmux.queue-status

Show the active command and queued commands for a session.

**Request:**

| Parameter | Type   | Required | Description       |
| --------- | ------ | -------- | ----------------- |
| `session` | string | yes      | Tmux session name |

**Response:**

| Field                       | Type    | Description                                             |
| --------------------------- | ------- | ------------------------------------------------------- |
| `session`                   | string  | Session name                                            |
| `active`                    | object  | Active command (nullable)                               |
| `active.id`                 | string  | Command ID                                              |
| `active.text`               | string  | Command text                                            |
| `active.requester_agent`    | string  | Requester agent name                                    |
| `active.state`              | string  | Current state                                           |
| `active.silence_ms`         | integer | Silence threshold                                       |
| `active.notify_on_complete` | boolean | Whether notifications are enabled                       |
| `active.submitted_at`       | string  | ISO 8601 submission timestamp                           |
| `active.sent_at`            | string  | ISO 8601 send timestamp                                 |
| `queued`                    | array   | List of queued command objects (same shape as `active`) |

### tmux.cancel

Cancel a queued or active command.

**Request:**

| Parameter    | Type   | Required | Description |
| ------------ | ------ | -------- | ----------- |
| `command_id` | string | yes      | Command ID  |

**Response:**

| Field        | Type   | Description               |
| ------------ | ------ | ------------------------- |
| `command_id` | string | Cancelled command ID      |
| `state`      | string | Final state (`cancelled`) |
| `output`     | string | Partial captured output   |

## Monitor Methods (v0.9.0)

Monitor methods are **Unix socket only** — they're not available over WebSocket
or via peer/Telegram dispatchers.

### monitor.start

Start a new monitor job. Spawns the child process immediately and persists the
spec to the `monitors` table so it survives daemon restarts.

**Request:**

| Parameter  | Type   | Required | Description                                                                                   |
| ---------- | ------ | -------- | --------------------------------------------------------------------------------------------- |
| `name`     | string | yes      | Short identifier (used as the synthetic sender: `monitor:<name>`)                             |
| `match`    | string | yes      | Go RE2 regex. Matching lines are delivered as messages.                                       |
| `to`       | string | yes      | Recipient agent name (without `@` prefix)                                                     |
| `argv`     | array  | yes      | Command and arguments as a string array (e.g., `["node", "server.js", "--watch"]`). No shell. |
| `cwd`      | string | yes      | Working directory for the child process                                                       |
| `debounce` | string | no       | Debounce window as a Go duration string (e.g., `"60s"`). Min `"30s"`. Default `"60s"`.        |
| `env`      | object | no       | Extra environment variables for the child process (key/value map). Stored redacted in the DB. |

**Response:**

| Field  | Type    | Description                      |
| ------ | ------- | -------------------------------- |
| `id`   | string  | Monitor ID (`m_` prefix + ULID)  |
| `name` | string  | Monitor name                     |
| `pid`  | integer | PID of the spawned child process |

**Errors:**

- `name is required`: Missing `name` field
- `match is required`: Missing `match` field
- `to is required`: Missing `to` field
- `argv is required and must not be empty`: Missing or empty `argv`
- `cwd is required`: Missing `cwd` field
- `invalid regex`: `match` is not a valid RE2 expression
- `debounce below minimum (30s)`: Debounce window shorter than 30 seconds
- `max monitors reached`: Already at the 100-monitor limit
- `agent not found`: Recipient agent name is not registered

---

### monitor.list

List monitor jobs.

**Request:**

| Parameter | Type    | Required | Description                                      |
| --------- | ------- | -------- | ------------------------------------------------ |
| `all`     | boolean | no       | Include stopped monitors (default: running only) |

**Response:**

| Field               | Type    | Description                              |
| ------------------- | ------- | ---------------------------------------- |
| `monitors`          | array   | List of monitor summary objects          |
| `monitors[].id`     | string  | Monitor ID                               |
| `monitors[].name`   | string  | Monitor name                             |
| `monitors[].status` | string  | `"running"` or `"stopped"`               |
| `monitors[].pid`    | integer | Child PID (0 if stopped)                 |
| `monitors[].uptime` | string  | Human-readable uptime (empty if stopped) |
| `monitors[].match`  | string  | Regex pattern                            |
| `monitors[].to`     | string  | Recipient agent name                     |

---

### monitor.show

Full detail on a single monitor job.

**Request:**

| Parameter | Type   | Required | Description |
| --------- | ------ | -------- | ----------- |
| `id`      | string | yes      | Monitor ID  |

**Response:**

| Field            | Type    | Description                                     |
| ---------------- | ------- | ----------------------------------------------- |
| `id`             | string  | Monitor ID                                      |
| `name`           | string  | Monitor name                                    |
| `status`         | string  | `"running"` or `"stopped"`                      |
| `pid`            | integer | Child PID (0 if stopped)                        |
| `uptime`         | string  | Human-readable uptime (empty if stopped)        |
| `match`          | string  | Regex pattern                                   |
| `to`             | string  | Recipient agent name                            |
| `argv`           | array   | Full command argv                               |
| `cwd`            | string  | Working directory                               |
| `debounce`       | string  | Debounce window duration string                 |
| `env`            | object  | Env vars with values redacted as `"[redacted]"` |
| `match_count`    | integer | Total lines matched since last start            |
| `recent_matches` | array   | Last N matched lines (strings)                  |
| `created_at`     | string  | ISO 8601 creation timestamp                     |

---

### monitor.stop

Stop a running monitor. Sends SIGTERM, waits 5 seconds, sends SIGKILL if still
running. Removes the spec from persistence — the monitor won't respawn.

**Request:**

| Parameter | Type   | Required | Description |
| --------- | ------ | -------- | ----------- |
| `id`      | string | yes      | Monitor ID  |

**Response:**

| Field     | Type    | Description       |
| --------- | ------- | ----------------- |
| `id`      | string  | Monitor ID        |
| `stopped` | boolean | `true` on success |

**Errors:**

- `monitor not found`: No monitor with given ID
- `monitor already stopped`: Monitor is not running

---

### monitor.logs

Return the last N bytes of captured stdout+stderr from the child process.

**Request:**

| Parameter | Type    | Required | Description                          |
| --------- | ------- | -------- | ------------------------------------ |
| `id`      | string  | yes      | Monitor ID                           |
| `bytes`   | integer | no       | Max bytes to return (default: 10000) |

**Response:**

| Field  | Type   | Description                    |
| ------ | ------ | ------------------------------ |
| `id`   | string | Monitor ID                     |
| `logs` | string | Captured output (last N bytes) |

**Errors:**

- `monitor not found`: No monitor with given ID

---

### monitor.restart

Restart a stopped or dead monitor by respawning its child process from the saved
spec.

**Request:**

| Parameter | Type   | Required | Description |
| --------- | ------ | -------- | ----------- |
| `id`      | string | yes      | Monitor ID  |

**Response:**

| Field | Type    | Description                    |
| ----- | ------- | ------------------------------ |
| `id`  | string  | Monitor ID                     |
| `pid` | integer | PID of the newly spawned child |

**Errors:**

- `monitor not found`: No monitor with given ID
- `monitor already running`: Monitor is currently active

---

## Using the API

### From Command Line

```bash
# Using netcat (nc)
echo '{"jsonrpc":"2.0","method":"health","id":1}' | nc -U .thrum/var/thrum.sock

# Using curl (requires HTTP-to-Unix-socket proxy)
# Not directly supported without proxy
```

### From Go Code

```go
import "github.com/leonletto/thrum/internal/daemon"

// Connect to daemon
client, err := daemon.NewClient(".thrum/var/thrum.sock")
if err != nil {
    log.Fatal(err)
}
defer client.Close()

// Call health method
result, err := client.Call("health", map[string]any{})
if err != nil {
    log.Fatal(err)
}

var healthResp daemon.HealthResponse
if err := json.Unmarshal(result, &healthResp); err != nil {
    log.Fatal(err)
}

fmt.Printf("Status: %s\n", healthResp.Status)
```

### From Other Languages

#### Python Example

```python
import socket
import json

def call_rpc(method, params={}):
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    sock.connect('.thrum/var/thrum.sock')

    request = {
        'jsonrpc': '2.0',
        'method': method,
        'params': params,
        'id': 1
    }

    sock.sendall(json.dumps(request).encode() + b'\n')
    response = sock.recv(4096)
    sock.close()

    return json.loads(response)

# Call health method
result = call_rpc('health')
print(result['result']['status'])
```

#### Node.js Example

```javascript
const net = require("net");

function callRPC(method, params = {}) {
  return new Promise((resolve, reject) => {
    const client = net.createConnection(".thrum/var/thrum.sock");

    const request = {
      jsonrpc: "2.0",
      method: method,
      params: params,
      id: 1,
    };

    client.on("connect", () => {
      client.write(JSON.stringify(request) + "\n");
    });

    client.on("data", (data) => {
      const response = JSON.parse(data.toString());
      client.end();
      resolve(response.result);
    });

    client.on("error", reject);
  });
}

// Call health method
callRPC("health").then((result) => {
  console.log("Status:", result.status);
});
```

## Best Practices

### Connection Management

1. **Reuse connections** - Keep the connection open for multiple requests
2. **Handle disconnections** - Implement retry logic for connection failures
3. **Set timeouts** - Don't wait forever for responses

### Error Handling

1. **Check error field** - Always check for `error` in response
2. **Handle specific error codes** - Different codes require different actions
3. **Retry on transient errors** - Socket errors, timeouts

### Request IDs

1. **Use unique IDs** - Helps correlate requests and responses
2. **Can be strings or numbers** - Both are valid per JSON-RPC 2.0
3. **Optional for notifications** - Omit ID if you don't need a response

## Troubleshooting

### Connection Refused

```bash
# Check if daemon is running
ps aux | grep thrum

# Check socket exists
ls -l .thrum/var/thrum.sock

# Check socket permissions
# Should be: srw------- (0600)
```

### Parse Errors

Common causes:

- Missing newline (`\n`) at end of request
- Invalid JSON syntax
- Wrong encoding (must be UTF-8)

### Method Not Found

- Check method name spelling (case-sensitive)
- Verify daemon version supports the method
- See available methods section above

## Next Steps

- [WebSocket API](api/websocket.md) — use these methods over WebSocket for
  real-time browser and agent connections
- [Daemon Architecture](daemon.md) — how the daemon registers and dispatches
  these RPC handlers
- [Inbox Query Methods](inbox-query-methods.md) — deeper coverage of
  `message.list` filtering, pagination, and read-state tracking
- [Event Streaming](event-streaming.md) — push notifications via the internal
  `subscribe` RPC method and Broadcaster

## References

- [JSON-RPC 2.0 Specification](https://www.jsonrpc.org/specification)
- Daemon Architecture: `docs/daemon.md`
- Development Guide: `docs/development.md`
