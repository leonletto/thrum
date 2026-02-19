---
title: "CLI Reference"
description:
  "Complete reference for the thrum command-line interface — all commands,
  flags, and usage examples"
category: "reference"
---

# Thrum CLI Reference

Complete reference for the `thrum` command-line interface -- a Git-backed
messaging system for AI agent coordination.

## Quick Reference

| Command                    | Description                                          |
| -------------------------- | ---------------------------------------------------- |
| `thrum init`               | Initialize Thrum in the current repository           |
| `thrum setup`              | Configure a feature worktree with `.thrum/redirect`  |
| `thrum migrate`            | Migrate old-layout repos to worktree architecture    |
| `thrum quickstart`         | Register, start session, and set intent in one step  |
| `thrum overview`           | Show combined status, team, and inbox view           |
| `thrum status`             | Show current agent status, session, and work context |
| `thrum send`               | Send a message (direct or broadcast)                 |
| `thrum reply`              | Reply to a message                                   |
| `thrum inbox`              | List messages in your inbox                          |
| `thrum message get`        | Get a single message with full details               |
| `thrum message edit`       | Edit a message (full replacement)                    |
| `thrum message delete`     | Delete a message                                     |
| `thrum message read`       | Mark messages as read                                |
| `thrum agent register`     | Register this agent with the daemon                  |
| `thrum agent list`         | List registered agents                               |
| `thrum agent whoami`       | Show current agent identity                          |
| `thrum agent context`      | Show agent work context                              |
| `thrum agent delete`       | Delete an agent and all associated data              |
| `thrum agent cleanup`      | Detect and remove orphaned agents                    |
| `thrum agent start`        | Start a new session (alias)                          |
| `thrum agent end`          | End current session (alias)                          |
| `thrum agent set-intent`   | Set work intent (alias)                              |
| `thrum agent set-task`     | Set current task (alias)                             |
| `thrum agent heartbeat`    | Send heartbeat (alias)                               |
| `thrum session start`      | Start a new work session                             |
| `thrum session end`        | End the current session                              |
| `thrum session list`       | List sessions (active and ended)                     |
| `thrum session heartbeat`  | Send a session heartbeat                             |
| `thrum session set-intent` | Set session work intent                              |
| `thrum session set-task`   | Set current task identifier                          |
| `thrum context save`       | Save agent context from file or stdin                |
| `thrum context show`       | Show agent context                                   |
| `thrum context clear`      | Clear agent context                                  |
| `thrum context sync`       | Sync context to a-sync branch                        |
| `thrum context prime`      | Collect all context for session initialization       |
| `thrum context update`     | Install/update the /update-context skill             |
| `thrum runtime`            | Manage runtime presets (list, show, edit)            |
| `thrum peer`               | Manage Tailscale peers                               |
| `thrum config`             | Manage configuration (show, init)                    |
| `thrum group create`       | Create a named group                                 |
| `thrum group delete`       | Delete a group                                       |
| `thrum group add`          | Add member to a group                                |
| `thrum group remove`       | Remove member from a group                           |
| `thrum group list`         | List all groups                                      |
| `thrum group info`         | Show group details                                   |
| `thrum group members`      | List group members                                   |
| `thrum who-has`            | Check which agents are editing a file                |
| `thrum ping`               | Check if an agent is online                          |
| `thrum subscribe`          | Subscribe to push notifications                      |
| `thrum unsubscribe`        | Remove a subscription                                |
| `thrum subscriptions`      | List active subscriptions                            |
| `thrum wait`               | Wait for notifications                               |
| `thrum daemon start`       | Start the daemon in the background                   |
| `thrum daemon stop`        | Stop the daemon gracefully                           |
| `thrum daemon status`      | Show daemon status                                   |
| `thrum daemon restart`     | Restart the daemon                                   |
| `thrum sync status`        | Show sync loop status                                |
| `thrum sync force`         | Trigger an immediate sync                            |
| `thrum mcp serve`          | Start MCP stdio server for agent messaging           |

## Global Flags

Available on all commands:

| Flag        | Description                              | Default |
| ----------- | ---------------------------------------- | ------- |
| `--role`    | Agent role (or `THRUM_ROLE` env var)     |         |
| `--module`  | Agent module (or `THRUM_MODULE` env var) |         |
| `--repo`    | Repository path                          | `.`     |
| `--json`    | JSON output for scripting                | `false` |
| `--quiet`   | Suppress non-essential output            | `false` |
| `--verbose` | Debug output                             | `false` |

## Core Commands

### thrum init

Initialize Thrum in the current repository. Creates the `.thrum/` directory
structure, sets up the `a-sync` branch for message synchronization, and updates
`.gitignore`. Detects installed AI runtimes and prompts you to select one.

    thrum init [--stealth] [flags]

| Flag             | Description                                                                                   | Default |
| ---------------- | --------------------------------------------------------------------------------------------- | ------- |
| `--force`        | Force reinitialization if already initialized                                                 | `false` |
| `--runtime`      | Specify runtime directly (skip detection prompt)                                              | (auto)  |
| `--dry-run`      | Preview changes without writing files                                                         | `false` |
| `--stealth`      | Write exclusions to `.git/info/exclude` instead of `.gitignore` (zero tracked-file footprint) | `false` |
| `--agent-name`   | Agent name for templates                                                                      |         |
| `--agent-role`   | Agent role for templates                                                                      |         |
| `--agent-module` | Agent module for templates                                                                    |         |

Example:

    $ thrum init
    ✓ Thrum initialized successfully
      Repository: .
      Created: .thrum/ directory structure
      Created: a-sync branch for message sync
      Updated: .gitignore

    Detected AI runtimes:
      1. Claude Code    (found .claude/settings.json)

    Which is your primary runtime? [1]:
    ✓ Runtime saved to .thrum/config.json (primary: claude)

### thrum config show

Show effective configuration resolved from all sources. Displays each value and
where it came from (config.json, environment variable, default).

    thrum config show [flags]

| Flag     | Description             | Default |
| -------- | ----------------------- | ------- |
| `--json` | Machine-readable output | `false` |

Example:

    $ thrum config show
    Thrum Configuration
      Config file: .thrum/config.json

    Runtime
      Primary:     claude (config.json)
      Detected:    claude, augment

    Daemon
      Local-only:    true (config.json)
      Sync interval: 60s (default)
      WS port:       auto (default)
      Status:        running (PID 7718)

### thrum setup

Set up Thrum in a feature worktree so it shares the daemon, database, and sync
state with the main repository. Creates a `.thrum/redirect` file pointing to the
main repo's `.thrum/` directory and a local `.thrum/identities/` directory for
per-worktree agent identities.

    thrum setup [flags]

| Flag          | Description                                     | Default |
| ------------- | ----------------------------------------------- | ------- |
| `--main-repo` | Path to the main repository (where daemon runs) | `.`     |

Example:

    $ thrum setup --main-repo /path/to/main/repo
    Connected to daemon
    ✓ Thrum worktree setup complete

### thrum setup claude-md

Generate Thrum agent coordination instructions for CLAUDE.md.

```bash
thrum setup claude-md              # Print to stdout
thrum setup claude-md --apply      # Append to CLAUDE.md (creates if missing)
thrum setup claude-md --apply --force  # Replace existing Thrum section
```

Flags:

- `--apply` — Append generated content to CLAUDE.md (with duplicate detection)
- `--force` — Replace existing Thrum section instead of skipping (used with
  --apply)

This command generates comprehensive agent coordination instructions including:

- Registration and session management
- Message protocols
- MCP server configuration
- Background listener setup
- Group management

The instructions are automatically injected by `thrum prime` when agents start
sessions, providing immediate context on how to use Thrum for coordination.

### thrum migrate

Migrate an existing Thrum repository from the old layout (JSONL files tracked on
main branch) to the new worktree architecture (JSONL files on `a-sync` branch
via `.git/thrum-sync/a-sync/` worktree). Safe to run multiple times -- it
detects what needs migration and skips steps that are already done.

    thrum migrate

### thrum quickstart

Register, start a session, and set an initial intent in one step. If the agent
is already registered, it re-registers automatically. Supports agent naming via
the `--name` flag or `THRUM_NAME` environment variable.

    thrum quickstart --role ROLE --module MODULE [flags]

| Flag        | Description                                                   | Default |
| ----------- | ------------------------------------------------------------- | ------- |
| `--name`    | Human-readable agent name (optional, defaults to `role_hash`) |         |
| `--display` | Display name for the agent                                    |         |
| `--intent`  | Initial work intent                                           |         |

Requires `--role` and `--module` (via flags or `THRUM_ROLE`/`THRUM_MODULE` env
vars).

The `THRUM_NAME` environment variable takes priority over the `--name` flag.

Example:

    $ thrum quickstart --role implementer --module auth --intent "Fixing token refresh"
    ✓ Registered as @implementer (implementer_35HV62T9B9)
    ✓ Session started: ses_01HXF2A9...
    ✓ Intent set: Fixing token refresh

    # With a human-readable name
    $ thrum quickstart --name furiosa --role implementer --module auth --intent "Fixing token refresh"
    ✓ Registered as @furiosa (furiosa)
    ✓ Session started: ses_01HXF2A9...
    ✓ Intent set: Fixing token refresh

### thrum overview

Show a comprehensive orientation view combining identity, work context, team
activity, inbox counts, and sync status.

    thrum overview

Example:

    $ thrum overview
    You: @implementer (implementer_35HV62T9B9)
    Session: active for 2h15m
    Intent: Fixing token refresh
    Branch: feature/auth (3 commits, 5 files)

    Team:
      @reviewer     feature/auth   Reviewing PR #42          15m ago
      @planner      main           Planning next sprint      1h ago

    Inbox: 3 unread (12 total)
    Sync: ✓ synced

### thrum status

Show current agent identity, session, work context, inbox counts, and daemon
health.

    thrum status

Example:

    $ thrum status
    Agent:    implementer_35HV62T9B9 (@implementer)
    Module:   auth
    Display:  Auth Developer
    Session:  ses_01HXF2A9... (duration: 2h15m)
    Intent:   Fixing token refresh
    Branch:   feature/auth (3 commits ahead)
    Files:    5 changed, 2 uncommitted
    Inbox:    47 messages (12 unread)
    Sync:     ✓ synced
    Daemon:   running (2h15m uptime, v0.1.0)
    WebSocket: ws://localhost:9999

## Messaging

### thrum send

Send a message to the messaging system. The daemon must be running and you must
have an active session.

    thrum send MESSAGE [flags]

| Flag                | Description                                                  | Default    |
| ------------------- | ------------------------------------------------------------ | ---------- |
| `--to`              | Direct recipient (format: `@role`, `@name`, or `@groupname`) |            |
| `--everyone`        | Alias for `--to @everyone` (send to all agents)              |            |
| `--broadcast`, `-b` | Send to all agents (alias for `--to @everyone`)              | `false`    |
| `--scope`           | Add scope (repeatable, format: `type:value`)                 |            |
| `--ref`             | Add reference (repeatable, format: `type:value`)             |            |
| `--mention`         | Mention a role (repeatable, format: `@role`)                 |            |
| `--structured`      | Structured payload (JSON string)                             |            |
| `--priority`        | Message priority (`low`, `normal`, `high`)                   | `normal`   |
| `--format`          | Message format (`markdown`, `plain`, `json`)                 | `markdown` |

The `--to` flag adds the recipient as a mention, making it a directed message.
Recipients can be agents (`@alice`), roles (`@reviewer`), or groups
(`@everyone`, `@backend`). The `--broadcast` and `--to` flags are mutually
exclusive.

The `--broadcast` flag is an alias for `--to @everyone`.

Example:

    $ thrum send "PR ready for review" --to @reviewer --scope module:auth --ref pr:42
    ✓ Message sent: msg_01HXE8Z7...
      Created: 2026-02-03T10:00:00Z

    # Send to all agents via @everyone group
    $ thrum send "Deploy complete" --to @everyone
    ✓ Message sent: msg_01HXE8Z8...

    # Send to a custom group
    $ thrum send "Backend review needed" --to @backend
    ✓ Message sent: msg_01HXE8Z9...

### thrum reply

Reply to a message by ID. Creates a reply-to reference so replies are grouped
with the original message in your inbox. The replied-to message is marked as
read.

    thrum reply MSG_ID TEXT [flags]

| Flag       | Description                                  | Default    |
| ---------- | -------------------------------------------- | ---------- |
| `--format` | Message format (`markdown`, `plain`, `json`) | `markdown` |

Example:

    $ thrum reply msg_01HXE8Z7 "Good idea, let's do that"
    ✓ Reply sent: msg_01HXE9A3...
      In reply to: msg_01HXE8Z7

### thrum inbox

List messages in your inbox with filtering and pagination. Displayed messages
are automatically marked as read (unless filtering with `--unread`).

    thrum inbox [flags]

| Flag          | Description                            | Default |
| ------------- | -------------------------------------- | ------- |
| `--scope`     | Filter by scope (format: `type:value`) |         |
| `--mentions`  | Only messages mentioning me            | `false` |
| `--unread`    | Only unread messages                   | `false` |
| `--page-size` | Results per page                       | `10`    |
| `--limit N`   | Alias for `--page-size`                | `10`    |
| `--page`      | Page number                            | `1`     |

The output adapts to terminal width and shows read/unread indicators.

Example:

    $ thrum inbox --unread
    ┌──────────────────────────────────────────────────────────┐
    │ ● msg_01HXE8Z7  @planner  2m ago                       │
    │ We should refactor the sync daemon before adding embeds. │
    ├──────────────────────────────────────────────────────────┤
    │ ● msg_01HXE8A2  @reviewer  15m ago                      │
    │ LGTM on the auth changes. Ready to merge.               │
    └──────────────────────────────────────────────────────────┘
    Showing 1-2 of 12 messages (5 unread)

### thrum message get

Get a single message with full details. The message is automatically marked as
read.

    thrum message get MSG_ID

Example:

    $ thrum message get msg_01HXE8Z7
    Message: msg_01HXE8Z7
      From:    @planner
      Time:    2m ago
      Scopes:  module:auth
      Refs:    issue:thrum-42

    We should refactor the sync daemon before adding embeddings.

### thrum message edit

Edit a message by replacing its content entirely. Only the message author can
edit their own messages.

    thrum message edit MSG_ID TEXT

Example:

    $ thrum message edit msg_01HXE8Z7 "Updated: refactor sync daemon first"
    ✓ Message edited: msg_01HXE8Z7 (version 2)

### thrum message delete

Delete a message by ID. Requires the `--force` flag to confirm.

    thrum message delete MSG_ID --force

| Flag      | Description      | Default |
| --------- | ---------------- | ------- |
| `--force` | Confirm deletion | `false` |

Example:

    $ thrum message delete msg_01HXE8Z7 --force
    ✓ Message deleted: msg_01HXE8Z7

### thrum message read

Mark one or more messages as read, or all unread messages at once.

    thrum message read MSG_ID [MSG_ID...]
    thrum message read --all

| Flag    | Description                      | Default |
| ------- | -------------------------------- | ------- |
| `--all` | Mark all unread messages as read | `false` |

Example:

    $ thrum message read msg_01 msg_02 msg_03
    ✓ Marked 3 messages as read

    $ thrum message read --all
    ✓ Marked 7 messages as read

## Identity & Sessions

### Agent Naming

Agents support human-readable names that become their canonical identifier for
display, messaging (`@name`), and file paths.

**Naming rules:**

- Allowed characters: lowercase letters (`a-z`), digits (`0-9`), underscores
  (`_`)
- Reserved names: `daemon`, `system`, `thrum`, `all`, `broadcast`
- Cannot be empty

**Name resolution priority (highest to lowest):**

1. `THRUM_NAME` environment variable (highest -- used for multi-agent worktrees)
2. `--name` CLI flag
3. Scan `.thrum/identities/` for a single file (backward compat for solo-agent
   worktrees)

When no name is provided, agent IDs default to `{role}_{hash10}` format (e.g.,
`implementer_35HV62T9B9`).

### thrum agent register

Register this agent with the daemon. The agent identity is resolved from: (1)
`THRUM_NAME` env var, (2) `--name` flag, (3) environment variables
(`THRUM_ROLE`, `THRUM_MODULE`), (4) identity file in `.thrum/identities/`
directory.

    thrum agent register [flags]

| Flag            | Description                                                   | Default |
| --------------- | ------------------------------------------------------------- | ------- |
| `--name`        | Human-readable agent name (optional, defaults to `role_hash`) |         |
| `--force`       | Force registration (override existing)                        | `false` |
| `--re-register` | Re-register same agent (update)                               | `false` |
| `--display`     | Display name for the agent                                    |         |

Requires `--role` and `--module` (via global flags or env vars). On successful
registration, saves an identity file to `.thrum/identities/{name}.json`.

Example:

    $ thrum --role=implementer --module=auth agent register --display "Auth Developer"
    ✓ Agent registered: implementer_35HV62T9B9

    # With a human-readable name
    $ thrum --role=implementer --module=auth agent register --name furiosa --display "Auth Developer"
    ✓ Agent registered: furiosa

### thrum agent list

List all registered agents with session status and work context.

    thrum agent list [flags]

| Flag        | Description                                       | Default |
| ----------- | ------------------------------------------------- | ------- |
| `--role`    | Filter by role                                    |         |
| `--module`  | Filter by module                                  |         |
| `--context` | Show work context table (branch, commits, intent) | `false` |

Without `--context`, shows a detailed card view per agent with active/offline
status. With `--context`, shows a compact table of work contexts.

Example (default view):

    $ thrum agent list
    Registered agents (2):

    ┌─ ● @implementer (active)
    │  Module:  auth
    │  Intent:  Fixing token refresh
    │  Branch:  feature/auth (3 commits)
    │  Active:  5m ago
    └─

    ┌─ ○ @reviewer (offline)
    │  Module:  auth
    │  Last seen: 2h ago
    └─

Example (context table):

    $ thrum agent list --context
    AGENT          SESSION      BRANCH               COMMITS  FILES INTENT                         UPDATED
    ────────────────────────────────────────────────────────────────────────────────────────────────────────
    @implementer   ses_01HXF... feature/auth               3      5 Fixing token refresh           5m ago

### thrum agent whoami

Show the current agent identity and active session.

    thrum agent whoami

Identity is resolved from: (1) command-line flags (`--role`, `--module`), (2)
environment variables (`THRUM_ROLE`, `THRUM_MODULE`, `THRUM_NAME`), (3) identity
files in `.thrum/identities/` directory.

Example:

    $ thrum agent whoami
    Agent ID:  implementer_35HV62T9B9
    Role:      @implementer
    Module:    auth
    Display:   Auth Developer
    Source:    environment
    Session:   ses_01HXF2A9... (2h ago)

### thrum agent context

Show detailed work context for agents. Without arguments, lists all active work
contexts. With an agent argument, shows detailed context for that agent.

    thrum agent context [AGENT] [flags]

| Flag       | Description            | Default |
| ---------- | ---------------------- | ------- |
| `--agent`  | Filter by agent role   |         |
| `--branch` | Filter by branch       |         |
| `--file`   | Filter by changed file |         |

Example (single agent detail):

    $ thrum agent context @implementer
    Agent: @implementer (ses_01HXF...)
    Branch: feature/auth
    Intent: Fixing token refresh (set 5m ago)
    Task: beads:thrum-42 (set 1h ago)

    Unmerged Commits (2):
      abc1234 Add token refresh logic [auth.go, token.go]
      def5678 Fix expiry check [auth.go]

    Changed Files (vs main): 3
      internal/auth/auth.go
      internal/auth/token.go
      internal/auth/token_test.go

    Uncommitted: 1
      internal/auth/refresh.go

### thrum agent delete

Delete an agent and all its associated data. This removes the identity file
(`identities/{name}.json`), message file (`messages/{name}.jsonl`), and the
agent record from the database. Prompts for confirmation before deletion.

    thrum agent delete <name>

Example:

    $ thrum agent delete furiosa
    Delete agent 'furiosa' and all associated data? [y/N] y
    ✓ Agent deleted: furiosa

### thrum agent cleanup

Detect and remove orphaned agents whose worktrees or branches no longer exist.
Scans all registered agents and identifies orphans based on missing worktree,
missing branch, or staleness (not seen in a long time).

    thrum agent cleanup [flags]

| Flag          | Description                                  | Default |
| ------------- | -------------------------------------------- | ------- |
| `--dry-run`   | List orphans without deleting                | `false` |
| `--force`     | Delete all orphans without prompting         | `false` |
| `--threshold` | Days since last seen to consider agent stale | `30`    |

The `--dry-run` and `--force` flags are mutually exclusive.

Example:

    $ thrum agent cleanup --dry-run
    Orphaned agents (2):
      implementer_35HV... — missing worktree
      reviewer_8KBN...    — not seen in 45 days

    $ thrum agent cleanup --force
    ✓ Deleted implementer_35HV...
    ✓ Deleted reviewer_8KBN...
    ✓ Deleted 2 orphaned agent(s)

### thrum agent start

Start a new work session. This is an alias for `thrum session start`. The agent
must be registered first.

    thrum agent start

### thrum agent end

End the current session. This is an alias for `thrum session end`.

    thrum agent end [flags]

| Flag           | Description                             | Default  |
| -------------- | --------------------------------------- | -------- |
| `--reason`     | End reason (`normal`, `crash`)          | `normal` |
| `--session-id` | Session ID to end (defaults to current) |          |

### thrum agent set-intent

Set the work intent for the current session. This is an alias for
`thrum session set-intent`. Pass an empty string to clear.

    thrum agent set-intent TEXT

Example:

    $ thrum agent set-intent "Fixing memory leak in connection pool"
    ✓ Intent set: Fixing memory leak in connection pool

### thrum agent set-task

Set the current task identifier for the session. This is an alias for
`thrum session set-task`. Pass an empty string to clear.

    thrum agent set-task TASK

Example:

    $ thrum agent set-task beads:thrum-42
    ✓ Task set: beads:thrum-42

### thrum agent heartbeat

Send a heartbeat for the current session. This is an alias for
`thrum session heartbeat`. Triggers git context extraction and updates the
agent's last-seen time.

    thrum agent heartbeat [flags]

| Flag             | Description                                     | Default |
| ---------------- | ----------------------------------------------- | ------- |
| `--add-scope`    | Add scope (repeatable, format: `type:value`)    |         |
| `--remove-scope` | Remove scope (repeatable, format: `type:value`) |         |
| `--add-ref`      | Add ref (repeatable, format: `type:value`)      |         |
| `--remove-ref`   | Remove ref (repeatable, format: `type:value`)   |         |

### thrum session start

Start a new work session. Automatically detects the current agent from whoami
and recovers any orphaned sessions.

    thrum session start

Example:

    $ thrum session start
    ✓ Session started: ses_01HXF2A9...
      Agent:      implementer_35HV62T9B9
      Started:    2026-02-03 10:00:00

### thrum session end

End the current or specified session.

    thrum session end [flags]

| Flag           | Description                             | Default  |
| -------------- | --------------------------------------- | -------- |
| `--reason`     | End reason (`normal`, `crash`)          | `normal` |
| `--session-id` | Session ID to end (defaults to current) |          |

Example:

    $ thrum session end
    ✓ Session ended: ses_01HXF2A9...
      Ended:      2026-02-03 12:00:00
      Duration:   2h

### thrum session list

List all sessions (active and ended) with optional filtering.

    thrum session list [flags]

| Flag       | Description               | Default |
| ---------- | ------------------------- | ------- |
| `--active` | Show only active sessions | `false` |
| `--agent`  | Filter by agent ID        |         |

Example:

    $ thrum session list
    Sessions (3):
      ses_01HXF2A9  implementer_35HV  active  2h ago   Fixing token refresh
      ses_01HXF1B8  reviewer_8KBN     ended   4h ago   Reviewing PR #42
      ses_01HXF0C7  planner_9QRM      ended   1d ago   Sprint planning

    $ thrum session list --active
    Sessions (1):
      ses_01HXF2A9  implementer_35HV  active  2h ago   Fixing token refresh

### thrum session heartbeat

Send a heartbeat for the current session. Triggers git context extraction and
updates the agent's last-seen time. Optionally add or remove scopes and refs.

    thrum session heartbeat [flags]

| Flag             | Description                                     | Default |
| ---------------- | ----------------------------------------------- | ------- |
| `--add-scope`    | Add scope (repeatable, format: `type:value`)    |         |
| `--remove-scope` | Remove scope (repeatable, format: `type:value`) |         |
| `--add-ref`      | Add ref (repeatable, format: `type:value`)      |         |
| `--remove-ref`   | Remove ref (repeatable, format: `type:value`)   |         |

Example:

    $ thrum session heartbeat --add-scope module:auth
    ✓ Heartbeat sent: ses_01HXF2A9...
      Context: branch: feature/auth, 3 commits, 5 files

### thrum session set-intent

Set a free-text description of what the agent is currently working on. Appears
in `thrum agent list --context` and `thrum agent context`. Pass an empty string
to clear.

    thrum session set-intent TEXT

Example:

    $ thrum session set-intent "Refactoring login flow"
    ✓ Intent set: Refactoring login flow

### thrum session set-task

Set the current task identifier for the session (e.g., a beads issue ID).
Appears in `thrum agent list --context` and `thrum agent context`. Pass an empty
string to clear.

    thrum session set-task TASK

Example:

    $ thrum session set-task beads:thrum-42
    ✓ Task set: beads:thrum-42

## Groups

### thrum group create

Create a named group for targeted messaging. Groups contain agents and roles.

    thrum group create NAME [flags]

| Flag            | Description                      | Default |
| --------------- | -------------------------------- | ------- |
| `--description` | Human-readable group description |         |

The `@everyone` group is created automatically on daemon startup and includes
all agents.

Example:

    $ thrum group create reviewers --description "Code review team"
    ✓ Group created: reviewers

    $ thrum group create backend --description "Backend developers"
    ✓ Group created: backend

### thrum group delete

Delete a group by name. The `@everyone` group is protected and cannot be
deleted.

    thrum group delete NAME

Example:

    $ thrum group delete reviewers
    ✓ Group deleted: reviewers

    $ thrum group delete @everyone
    ✗ Cannot delete protected group: @everyone

### thrum group add

Add a member to a group. Members can be agents or roles.

    thrum group add GROUP MEMBER

**Member types:**

- `@alice` or `alice` — Specific agent by name
- `--role planner` — All agents with role "planner"

Example:

    # Add specific agent
    $ thrum group add reviewers @alice
    ✓ Added agent alice to group reviewers

    # Add all agents with a role
    $ thrum group add reviewers --role reviewer
    ✓ Added role reviewer to group reviewers

### thrum group remove

Remove a member from a group.

    thrum group remove GROUP MEMBER

Uses the same member detection as `group add`.

Example:

    $ thrum group remove reviewers @alice
    ✓ Removed agent alice from group reviewers

### thrum group list

List all groups in the system.

    thrum group list

Example:

    $ thrum group list
    Groups (3):

    @everyone
      Description: All registered agents
      Members:     (implicit - all agents)
      Created:     2026-02-09 10:00:00

    reviewers
      Description: Code review team
      Members:     2
      Created:     2026-02-09 10:15:00

    backend
      Description: Backend developers
      Members:     3
      Created:     2026-02-09 10:20:00

### thrum group info

Show detailed information about a specific group.

    thrum group info NAME

Example:

    $ thrum group info reviewers
    Group: reviewers
      Description: Code review team
      Created:     2026-02-09 10:15:00
      Created by:  alice
      Members:     2

      Members:
        - @alice (agent)
        - reviewer (role)

### thrum group members

List members of a group.

    thrum group members NAME

Example:

    $ thrum group members reviewers
    Members of reviewers (2):
      - @alice (agent)
      - reviewer (role)

## Coordination

### thrum who-has

Check which agents are currently editing a file. Shows agents with the file in
their uncommitted changes or changed files, along with branch and change count
information.

    thrum who-has FILE

Example:

    $ thrum who-has internal/auth/auth.go
    @implementer is editing internal/auth/auth.go (2 uncommitted changes, branch: feature/auth)

    $ thrum who-has unknown.go
    No agents are currently editing unknown.go

### thrum ping

Check the presence status of an agent. Shows whether the agent is active or
offline, along with their current intent, task, and branch if active. The agent
can be specified with or without the `@` prefix.

    thrum ping AGENT

Example:

    $ thrum ping @reviewer
    @reviewer: active, last heartbeat 5m ago
      Intent: Reviewing PR #42
      Task: beads:thrum-55
      Branch: feature/auth

    $ thrum ping planner
    @planner: offline (last seen 3h ago)

## Context Management

### thrum context save

Save agent context from a file or stdin. Context is stored in
`.thrum/context/{agent-name}.md` for persistence across sessions.

    thrum context save [flags]

| Flag      | Description                                        | Default |
| --------- | -------------------------------------------------- | ------- |
| `--file`  | Path to markdown file to save as context           |         |
| `--agent` | Override agent name (defaults to current identity) |         |

Example:

    $ thrum context save --file dev-docs/Continuation_Prompt.md
    ✓ Context saved for furiosa (1234 bytes)

    # Save from stdin
    $ echo "Working on auth module" | thrum context save
    ✓ Context saved for furiosa (24 bytes)

### thrum context show

Display the saved context for the current agent.

    thrum context show [flags]

| Flag      | Description                                        | Default |
| --------- | -------------------------------------------------- | ------- |
| `--agent` | Override agent name (defaults to current identity) |         |
| `--raw`   | Output raw content without decoration              | `false` |

Example:

    $ thrum context show
    Context for furiosa (1.2 KB, updated 5m ago):

    # Current Work
    - Implementing JWT token refresh
    - Investigating rate limiting bug

    # Get raw output
    $ thrum context show --raw > backup.md

### thrum context clear

Remove the context file for the current agent. Idempotent — running clear when
no context exists is a no-op.

    thrum context clear [flags]

| Flag      | Description                                        | Default |
| --------- | -------------------------------------------------- | ------- |
| `--agent` | Override agent name (defaults to current identity) |         |

Example:

    $ thrum context clear
    ✓ Context cleared for furiosa

### thrum context sync

Copy the context file to the a-sync branch for sharing across worktrees and
machines. This is a manual operation — context is never synced automatically.

    thrum context sync [flags]

| Flag      | Description                                        | Default |
| --------- | -------------------------------------------------- | ------- |
| `--agent` | Override agent name (defaults to current identity) |         |

What it does:

1. Copies `.thrum/context/{agent}.md` to the sync worktree
2. Commits the change with message `"context: update {agent}"`
3. Pushes to the remote a-sync branch

No-op when no remote is configured (local-only mode) or when the `--local`
daemon flag is set.

Example:

    $ thrum context sync
    ✓ Context synced for furiosa
      Committed and pushed to a-sync branch

### thrum context update

Install or update the `/update-context` skill for Claude Code agents. Detects
the skill in project-level (`.claude/commands/update-context.md`) or global
(`~/.claude/commands/update-context.md`) locations.

    thrum context update

Example:

    $ thrum context update
    /update-context skill installed at:
      /path/to/repo/.claude/commands/update-context.md

    Restart Claude Code to load the skill.

## Notifications

### thrum subscribe

Subscribe to push notifications. Subscription types are mutually exclusive:
specify exactly one of `--scope`, `--mention`, or `--all`.

    thrum subscribe [flags]

| Flag        | Description                                     | Default |
| ----------- | ----------------------------------------------- | ------- |
| `--scope`   | Subscribe to scope (format: `type:value`)       |         |
| `--mention` | Subscribe to mentions of role (format: `@role`) |         |
| `--all`     | Subscribe to all messages (firehose)            | `false` |

Example:

    $ thrum subscribe --scope module:auth
    ✓ Subscription created: #42
      Session:    ses_01HXF2A9...
      Created:    2026-02-03 10:00:00

### thrum unsubscribe

Remove a subscription by ID.

    thrum unsubscribe SUBSCRIPTION_ID

Example:

    $ thrum unsubscribe 42
    ✓ Subscription #42 removed

### thrum subscriptions

List all active subscriptions for the current session.

    thrum subscriptions

Example:

    $ thrum subscriptions
    Active subscriptions (2):

    ┌─ Subscription #42
    │  Type:       Scope (module:auth)
    │  Created:    2026-02-03 10:00:00 (2h ago)
    └─

    ┌─ Subscription #43
    │  Type:       Mention (@reviewer)
    │  Created:    2026-02-03 10:05:00 (1h55m ago)
    └─

### thrum wait

Block until a matching message arrives or timeout occurs. Useful for automation
and hooks.

    thrum wait [flags]

| Flag        | Description                                 | Default |
| ----------- | ------------------------------------------- | ------- |
| `--timeout` | Max wait time (e.g., `30s`, `5m`, `1h`)     | `30s`   |
| `--scope`   | Filter by scope (format: `type:value`)      |         |
| `--mention` | Wait for mentions of role (format: `@role`) |         |

Exit codes: `0` = message received, `1` = timeout, `2` = error.

Example:

    $ thrum wait --scope module:auth --timeout=5m
    ✓ Message received: msg_01HXE8Z7 from @planner
      We should refactor the sync daemon before adding embeddings.

    # Use in scripts
    if thrum wait --timeout=30s; then
      echo "New message received"
    else
      echo "Timeout"
    fi

## Infrastructure

### thrum daemon start

Start the daemon in the background. Uses `thrum daemon run` internally to run
the daemon in a detached process. The daemon serves both a Unix socket (for CLI
RPC) and a combined WebSocket + SPA server on port 9999.

    thrum daemon start [flags]

| Flag      | Description                               | Default |
| --------- | ----------------------------------------- | ------- |
| `--local` | Disable remote git sync (local-only mode) | `false` |

The daemon performs pre-startup duplicate detection by checking if another
daemon is already serving this repository (via JSON PID files and `flock()`).

Example:

    # Start in local-only mode (no git push/fetch)
    thrum daemon start --local

### thrum daemon stop

Stop the daemon gracefully by sending SIGTERM.

    thrum daemon stop

### thrum daemon status

Show daemon status including PID, uptime, version, and the repository path being
served.

    thrum daemon status

### thrum daemon restart

Restart the daemon (stop + start).

    thrum daemon restart

### thrum sync status

Show sync loop status, last sync time, and any errors. When local-only mode is
active, displays "Mode: local-only" instead of "Mode: normal".

    thrum sync status

Sync states: `stopped`, `idle`, `synced`, `error`.

### thrum sync force

Trigger an immediate sync (non-blocking). Fetches new messages from the remote
and pushes local messages. The default sync interval is 60 seconds. When
local-only mode is active, displays "local-only (remote sync disabled)".

    thrum sync force

## MCP Server

### thrum mcp serve

Start an MCP (Model Context Protocol) server on stdin/stdout for native
tool-based agent messaging. This allows Claude Code agents to communicate via
MCP tools instead of shelling out to the CLI.

    thrum mcp serve [flags]

| Flag         | Description                                                       | Default |
| ------------ | ----------------------------------------------------------------- | ------- |
| `--agent-id` | Override agent identity (selects `.thrum/identities/{name}.json`) |         |

Requires the Thrum daemon to be running. The `--agent-id` flag sets `THRUM_NAME`
internally for identity resolution.

**MCP Tools provided (11 total):**

**Core messaging (5):**

| Tool                | Description                                                                   |
| ------------------- | ----------------------------------------------------------------------------- |
| `send_message`      | Send a message to another agent via `@role` addressing                        |
| `check_messages`    | Poll for unread messages mentioning this agent (auto-marks read)              |
| `wait_for_message`  | Block until a message arrives (WebSocket push) or timeout                     |
| `list_agents`       | List registered agents with active/offline status                             |
| `broadcast_message` | Send to all agents (convenience wrapper around `send_message` to `@everyone`) |

**Group management (6):**

| Tool                  | Description                                                      |
| --------------------- | ---------------------------------------------------------------- |
| `create_group`        | Create a named messaging group                                   |
| `delete_group`        | Delete a messaging group                                         |
| `add_group_member`    | Add an agent or role as a member of a group                      |
| `remove_group_member` | Remove a member from a group                                     |
| `list_groups`         | List all messaging groups                                        |
| `get_group`           | Get group details including members (expand=true resolves roles) |

**Configuration in Claude Code's `.claude/settings.json`:**

```json
{
  "mcpServers": {
    "thrum": {
      "type": "stdio",
      "command": "thrum",
      "args": ["mcp", "serve"]
    }
  }
}
```

For multi-agent worktrees, use `--agent-id` or set `THRUM_NAME`:

```json
{
  "mcpServers": {
    "thrum": {
      "type": "stdio",
      "command": "thrum",
      "args": ["mcp", "serve", "--agent-id", "furiosa"]
    }
  }
}
```

## Environment Variables

| Variable        | Description                                           | Example                      |
| --------------- | ----------------------------------------------------- | ---------------------------- |
| `THRUM_NAME`    | Agent name (highest priority for identity resolution) | `furiosa`                    |
| `THRUM_ROLE`    | Agent role (overrides identity file)                  | `implementer`                |
| `THRUM_MODULE`  | Agent module (overrides identity file)                | `auth`                       |
| `THRUM_DISPLAY` | Display name (overrides identity file)                | `Auth Developer`             |
| `THRUM_WS_PORT` | WebSocket and SPA server port (daemon)                | `9999`                       |
| `THRUM_UI_DEV`  | Path to dev UI dist for hot reload (daemon)           | `./ui/packages/web-app/dist` |
| `THRUM_LOCAL`   | Enable local-only mode (disables remote sync)         | `1`                          |

## Identity Resolution

Identity is resolved using the following priority (highest to lowest):

1. `THRUM_NAME` environment variable (selects specific identity file)
2. `--name` CLI flag
3. Environment variables (`THRUM_ROLE`, `THRUM_MODULE`)
4. CLI flags (`--role`, `--module`)
5. Identity files in `.thrum/identities/` directory (auto-selects if exactly one
   file exists)

For multi-agent worktrees with multiple identity files, set `THRUM_NAME` to
select the correct one.

## Configuration Files

### .thrum/identities/{name}.json

Per-agent identity files stored in the `.thrum/identities/` directory. Created
automatically on successful registration. The filename is derived from the agent
name (e.g., `furiosa.json` or `implementer_35HV62T9B9.json`).

```json
{
  "version": 1,
  "repo_id": "r_0123456789AB",
  "agent": {
    "kind": "agent",
    "name": "furiosa",
    "role": "implementer",
    "module": "auth",
    "display": "Auth Developer"
  },
  "worktree": "main",
  "confirmed_by": "",
  "updated_at": "2026-02-03T10:00:00Z"
}
```

### Storage Layout

Messages and events are stored on the `a-sync` Git branch in a worktree at
`.git/thrum-sync/a-sync/`:

| Path                                            | Description                                          |
| ----------------------------------------------- | ---------------------------------------------------- |
| `.git/thrum-sync/a-sync/events.jsonl`           | Agent lifecycle events (register, session start/end) |
| `.git/thrum-sync/a-sync/messages/{agent}.jsonl` | Per-agent sharded message files                      |
| `.thrum/var/messages.db`                        | SQLite projection cache (derived from JSONL)         |
| `.thrum/identities/{name}.json`                 | Per-worktree agent identity files                    |
| `.thrum/var/thrum.sock`                         | Unix socket for CLI-daemon RPC                       |
| `.thrum/var/thrum.pid`                          | JSON PID file with daemon metadata                   |
| `.thrum/var/ws.port`                            | WebSocket port file                                  |
| `.thrum/var/thrum.lock`                         | flock() lock file for SIGKILL resilience             |
| `.thrum/redirect`                               | Redirect pointer for feature worktrees               |
