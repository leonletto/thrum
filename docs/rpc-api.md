
# Thrum Daemon RPC API

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


## Method Reference

### health

Health check and daemon status.

**Request:**

| Parameter | Type | Required | Description                 |
| --------- | ---- | -------- | --------------------------- |
| _(none)_  |      |          | Empty object or omit params |

**Response:**

| Field        | Type    | Description                           |
| ------------ | ------- | ------------------------------------- |
| `status`     | string  | `"ok"` or `"degraded"`                |
| `uptime_ms`  | integer | Daemon uptime in milliseconds         |
| `version`    | string  | Daemon version (e.g., `"0.1.0"`)      |
| `repo_id`    | string  | Repository identifier                 |
| `sync_state` | string  | `"synced"`, `"pending"`, or `"error"` |

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

| Field          | Type   | Description                          |
| -------------- | ------ | ------------------------------------ |
| `session_id`   | string | Session ID                           |
| `last_seen_at` | string | ISO 8601 updated last-seen timestamp |

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

| Parameter    | Type    | Required | Description                                              |
| ------------ | ------- | -------- | -------------------------------------------------------- |
| `content`    | string  | yes      | Message body text                                        |
| `format`     | string  | no       | `"markdown"` (default), `"plain"`, or `"json"`           |
| `structured` | object  | no       | Typed JSON payload                                       |
| `thread_id`  | string  | no       | Thread to send message in                                |
| `scopes`     | array   | no       | Message scopes (`[{"type": "...", "value": "..."}]`)     |
| `refs`       | array   | no       | Message references (`[{"type": "...", "value": "..."}]`) |
| `mentions`   | array   | no       | Mention roles (e.g., `["@reviewer"]`)                    |
| `tags`       | array   | no       | Message tags                                             |
| `priority`   | string  | no       | `"low"`, `"normal"` (default), `"high"`                  |
| `acting_as`  | string  | no       | Impersonate this agent ID (users only)                   |
| `disclose`   | boolean | no       | Show `[via user:X]` tag when impersonating               |

**Response:**

| Field        | Type   | Description                                   |
| ------------ | ------ | --------------------------------------------- |
| `message_id` | string | Generated message ID (e.g., `"msg_01HXE..."`) |
| `thread_id`  | string | Thread ID (present if message is in a thread) |
| `created_at` | string | ISO 8601 creation timestamp                   |

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
| `message.thread_id`              | string  | Thread ID (empty if not in a thread)                 |
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

| Parameter          | Type    | Required | Description                                                                                        |
| ------------------ | ------- | -------- | -------------------------------------------------------------------------------------------------- |
| `scope`            | object  | no       | Filter by scope (`{"type": "...", "value": "..."}`)                                                |
| `ref`              | object  | no       | Filter by ref (`{"type": "...", "value": "..."}`)                                                  |
| `thread_id`        | string  | no       | Filter by thread                                                                                   |
| `author_id`        | string  | no       | Filter by author agent ID                                                                          |
| `mentions`         | boolean | no       | Only messages mentioning current agent (resolved from config)                                      |
| `unread`           | boolean | no       | Only unread messages (resolved from config)                                                        |
| `mention_role`     | string  | no       | Explicit filter: messages with mention ref matching this role (for remote callers like MCP server) |
| `unread_for_agent` | string  | no       | Explicit filter: messages unread by this agent ID (for remote callers like MCP server)             |
| `page_size`        | integer | no       | Items per page (default: 10, max: 100)                                                             |
| `page`             | integer | no       | Page number (default: 1)                                                                           |
| `sort_by`          | string  | no       | `"created_at"` (default) or `"updated_at"`                                                         |
| `sort_order`       | string  | no       | `"asc"` or `"desc"` (default)                                                                      |

**Response:**

| Field                   | Type    | Description                                                |
| ----------------------- | ------- | ---------------------------------------------------------- |
| `messages`              | array   | List of message summaries                                  |
| `messages[].message_id` | string  | Message ID                                                 |
| `messages[].thread_id`  | string  | Thread ID (empty if not in a thread)                       |
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


### thread.create

Create a new conversation thread. Optionally send an initial message in the same
call.

**Request:**

| Parameter            | Type    | Required | Description                                                |
| -------------------- | ------- | -------- | ---------------------------------------------------------- |
| `title`              | string  | yes      | Thread title                                               |
| `scopes`             | array   | no       | Thread scopes (`[{"type": "...", "value": "..."}]`)        |
| `recipient`          | string  | no       | Agent ID to direct initial message to (requires `message`) |
| `message`            | object  | no       | Initial message content (requires `recipient`)             |
| `message.content`    | string  | yes      | Message body text                                          |
| `message.format`     | string  | no       | `"markdown"` (default), `"plain"`, or `"json"`             |
| `message.structured` | object  | no       | Typed JSON payload                                         |
| `acting_as`          | string  | no       | Impersonation agent ID for initial message                 |
| `disclose`           | boolean | no       | Show via tag on initial message                            |

**Response:**

| Field        | Type   | Description                                                      |
| ------------ | ------ | ---------------------------------------------------------------- |
| `thread_id`  | string | Generated thread ID (e.g., `"thr_01HXE..."`)                     |
| `created_at` | string | ISO 8601 creation timestamp                                      |
| `message_id` | string | ID of initial message (present only when `message` was provided) |

**Errors:**

- `title is required`: Missing `title` field
- `recipient and message must both be provided or both be nil`: Only one of
  `recipient`/`message` given


### thread.list

List threads with pagination. Includes unread counts, last sender, and preview.

**Request:**

| Parameter   | Type    | Required | Description                                         |
| ----------- | ------- | -------- | --------------------------------------------------- |
| `scope`     | object  | no       | Filter by scope (`{"type": "...", "value": "..."}`) |
| `page_size` | integer | no       | Items per page (default: 10, max: 100)              |
| `page`      | integer | no       | Page number (default: 1)                            |

**Response:**

| Field                     | Type    | Description                                               |
| ------------------------- | ------- | --------------------------------------------------------- |
| `threads`                 | array   | List of thread summaries                                  |
| `threads[].thread_id`     | string  | Thread ID                                                 |
| `threads[].title`         | string  | Thread title                                              |
| `threads[].message_count` | integer | Total messages in thread                                  |
| `threads[].unread_count`  | integer | Messages not read by current agent/session                |
| `threads[].last_activity` | string  | ISO 8601 timestamp of most recent message                 |
| `threads[].last_sender`   | string  | Agent ID of most recent message author                    |
| `threads[].preview`       | string  | First 100 characters of most recent message (may be null) |
| `threads[].created_by`    | string  | Agent ID of thread creator                                |
| `threads[].created_at`    | string  | ISO 8601 thread creation timestamp                        |
| `total`                   | integer | Total matching threads                                    |
| `page`                    | integer | Current page number                                       |
| `page_size`               | integer | Items per page                                            |
| `total_pages`             | integer | Total number of pages                                     |

**Errors:**

- `resolve agent and session`: Could not determine current agent (needed for
  unread counts)


### thread.get

Get a thread's details and its paginated messages.

**Request:**

| Parameter   | Type    | Required | Description                               |
| ----------- | ------- | -------- | ----------------------------------------- |
| `thread_id` | string  | yes      | Thread ID                                 |
| `page_size` | integer | no       | Messages per page (default: 10, max: 100) |
| `page`      | integer | no       | Page number (default: 1)                  |

**Response:**

| Field               | Type    | Description                                                      |
| ------------------- | ------- | ---------------------------------------------------------------- |
| `thread`            | object  | Thread detail                                                    |
| `thread.thread_id`  | string  | Thread ID                                                        |
| `thread.title`      | string  | Thread title                                                     |
| `thread.created_by` | string  | Agent ID of thread creator                                       |
| `thread.created_at` | string  | ISO 8601 creation timestamp                                      |
| `messages`          | array   | Paginated message summaries (same shape as `message.list` items) |
| `total`             | integer | Total messages in thread                                         |
| `page`              | integer | Current page number                                              |
| `page_size`         | integer | Messages per page                                                |
| `total_pages`       | integer | Total number of pages                                            |

**Errors:**

- `thread_id is required`: Missing `thread_id` field
- `thread not found`: No thread with given ID


### subscribe

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
    "thread_id": "thr_01HXE...",
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


## References

- [JSON-RPC 2.0 Specification](https://www.jsonrpc.org/specification)
- Daemon Architecture: `docs/daemon.md`
- Development Guide: `docs/development.md`
