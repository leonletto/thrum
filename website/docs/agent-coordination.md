---
title: "Agent Coordination"
description:
  "Multi-agent workflows, Beads integration, session templates, and coordination
  patterns for AI agent teams"
category: "coordination"
order: 1
tags:
  ["coordination", "multi-agent", "beads", "workflows", "patterns", "sessions"]
last_updated: "2026-04-09"
---

## Agent Coordination

You run agents in parallel. This page covers the practical patterns: how they
message each other, how Beads and Thrum work together, and what a typical
session looks like start to finish.

> For the philosophy behind this approach — why you direct the work instead of
> handing it off — see [Why Thrum Exists](philosophy.md).

## Coordination Methods

### CLI (Primary)

Thrum is CLI-first. Every agent that can run shell commands can use Thrum.

```bash
thrum send "Starting work on task X" --to @coord_main
thrum inbox --unread
thrum sent --unread
thrum message read --all       # Mark all messages as read
thrum reply <msg-id> "Here's my update"
```

### MCP Server (Optional)

If your environment supports MCP, Thrum has an MCP server with native tool
integration. See [MCP Server](mcp-server.md) for configuration.

> **Note:** Use agent names (e.g., `@coord_main`), not role names (e.g.,
> `@coordinator`). Sending to a role fans out to ALL agents with that role. Run
> `thrum team` to see agent names.

## Common Workflows

### Planner-Implementer Coordination

The most common setup: a planner assigns work, an implementer executes it.

**Planner:**

```bash
# Register as planner
thrum quickstart --name planner1 --role planner --module website \
  --intent "Coordinating website development"

# Assign task via message (use agent name, not role)
thrum send "Please implement build script (task thrum-235d.3). \
  Design spec in dev-docs/plans/. Check beads for details." \
  --to @impl1

# Check for updates
thrum inbox
thrum sent --to @impl1
```

**Implementer:**

```bash
# Register
thrum quickstart --name impl1 --role implementer --module website \
  --intent "Implementing website features"

# Check inbox for assignments
thrum inbox --unread

# Check sent items after responding
thrum sent --to @planner1

# Acknowledge and work
thrum reply <msg-id> "Claimed task. Starting implementation."

# Send completion update (use agent name from thrum team)
thrum send "Build script complete. Tests passing. Ready for review." \
  --to @planner1
```

### Peer Collaboration

Two agents working in overlapping areas need to stay out of each other's way.

```bash
# Agent A: Announce work area
thrum send "Starting work on auth module" --to @everyone

# Agent B: Check file ownership before editing
thrum who-has src/auth/login.ts
# Output: @agent_a is editing src/auth/login.ts

# Agent B: Coordinate via message
thrum send "Need to edit login.ts for validation. ETA?" --to @agent_a
```

### Code Review

Pass review requests through messages. The implementer sends the commit hash and
what to look at; the reviewer responds with findings.

**Implementer:**

```bash
thrum send "Build script complete (commit abc123). Please review:
- Markdown processing
- Search index generation
- Error handling

Tests passing. Beads task: thrum-235d.3" --to @reviewer
```

**Reviewer:**

```bash
# Check inbox
thrum inbox --unread

# Review the code, then respond
thrum reply <msg-id> "Reviewed. Found 2 issues:
1. Missing error handling in parseMarkdown()
2. Search index doesn't handle compound terms

See beads: thrum-abc (bug filed). Otherwise looks good."
```

### Multi-Worktree Coordination

Agents in different git worktrees share the same daemon and message store — you
set up a redirect once and they all connect through it. For the full setup
walkthrough, architecture diagram, and running-multiple-agents examples, see
[Multi-Agent Support](multi-agent.md#multi-worktree-coordination).

## Message-Listener Pattern

For async message handling, spawn a background sub-agent to wait for incoming
messages and notify the main agent when they arrive.

### How It Works

1. The main agent spawns a message-listener as a background task
2. The listener runs `thrum wait` — it blocks until a message arrives or times
   out
3. When a message arrives, the listener returns immediately with the content
4. The main agent processes the message; the listener keeps looping
   automatically (no re-arming needed)
5. A cron watchdog respawns the listener every 30 min if it stops

**Use `thrum wait`** — it's more efficient than polling loops with sleep. Use
`--after -15s` to catch messages sent up to 15 seconds ago (`--after` negative
value = "N ago"). Omit `--after` to receive only messages that arrive after the
wait starts.

**Cron watchdog:** Set up a cron job to auto-respawn the listener if it stops:

```text
CronCreate(cron="*/30 * * * *",
  prompt="If there is no background message listener running, spawn one now:
    Task(subagent_type='message-listener', model='haiku', run_in_background=true,
      prompt='Listen for Thrum messages. WAIT_CMD=cd /path/to/repo && thrum wait --timeout 8m --after -15s --json')")
```

### Return Format

When messages are received:

```text
MESSAGES_RECEIVED
---
FROM: [sender]
CONTENT: [message content]
TIMESTAMP: [timestamp]
---
```

When timeout occurs with no messages:

```text
NO_MESSAGES_TIMEOUT
```

### Context Management

- The listener runs for up to 4 hours (30 cycles of ~8 min each), then stops;
  the cron watchdog respawns it
- After 5 consecutive timeouts with no pending work, send a status update to the
  coordinator and stop
- The listener is read-only — it never sends messages

## Beads Integration

Run Thrum and [Beads](https://github.com/leonletto/thrum) together. Thrum
handles real-time messaging; Beads tracks the work. Neither tries to do the
other's job:

| Concern                 | Tool  | Why                                       |
| ----------------------- | ----- | ----------------------------------------- |
| Real-time communication | Thrum | Messages, status updates, review requests |
| Task management         | Beads | Issues, dependencies, work discovery      |
| Progress tracking       | Beads | Status fields, close with reason          |
| Notifications           | Thrum | Alert team to progress or blockers        |

### Unified Workflow

```bash
# 1. Register in Thrum
thrum quickstart --name impl_auth --role implementer --module auth \
  --intent "Implementing auth system"

# 2. Find work in Beads
bd ready

# 3. Claim in Beads
bd update bd-123 --status in_progress

# 4. Announce via Thrum (use agent name, not role)
thrum send "Starting bd-123: JWT authentication" --to @coord_main

# 5. Work on implementation...

# 6. Discover issues -> file in Beads
bd create --title="Found validation bug" --type=bug --priority=1

# 7. Update coordinator via Thrum
thrum send "Progress: JWT working, found validation bug (filed bd-456)" \
  --to @coord_main

# 8. Complete in Beads
bd close bd-123 --reason="JWT auth complete with tests"

# 9. Announce via Thrum
thrum send "Completed bd-123. Ready for review." \
  --to @reviewer1

# 10. Sync both
bd sync
thrum sync force
```

### Mapping Convention

| Concept    | Beads                  | Thrum                                   |
| ---------- | ---------------------- | --------------------------------------- |
| Task ID    | `bd-123`               | Include in message: "Working on bd-123" |
| Status     | `bd update --status`   | Send message with update                |
| Assignment | `bd update --assignee` | Send message to specific agent          |
| Completion | `bd close`             | Send completion message                 |
| Discovery  | `bd create`            | Notify via message                      |

## Session Workflow Template

Use this template for every agent session:

> **Note:** If you were launched via `thrum tmux quickstart` (or
> `thrum tmux create` with `--name`/`--role`/`--module` flags), you're already
> registered — skip step 1. The coordinator's launch command handles
> registration before your session boots.

```bash
# === START OF SESSION ===

# 1. Register and start session
# Skip this if launched via thrum tmux quickstart — already registered
thrum quickstart --name <name> --role <role> --module <module> \
  --intent "<description>"

# 2. Check inbox for any urgent messages
thrum inbox --unread

# 3. Find work (Beads)
bd ready

# 4. Claim task (Beads)
bd update <id> --status in_progress

# 5. Announce start (Thrum) — use agent name from `thrum team`
thrum send "Starting work on <id>: <description>" --to @<coordinator-name>

# === DURING SESSION ===

# 6. Send periodic status updates
thrum send "Progress: <status>" --to @<coordinator-name>

# 7. Handle incoming messages
thrum inbox --unread

# 8. Coordinate on blockers
thrum send "Blocked: <description>" --to @<coordinator-name>

# === END OF SESSION ===

# 9. Complete work (Beads)
bd close <id> --reason "Complete"
bd sync

# 10. Announce completion (Thrum) — use agent name from `thrum team`
thrum send "Completed <id>. Tests passing. Ready for review." \
  --to @<reviewer-name>

# 11. End session
thrum session end
```

## Best Practices

### Do

- **Register at session start** -- always use `thrum quickstart`
- **Use CLI commands** -- they work everywhere, including sub-agents
- **Send status updates** -- keep the team informed
- **Use @mentions** -- reference agents by name
- **Include context** -- Beads IDs, file paths, commit hashes
- **End sessions cleanly** -- run `thrum session end` when done
- **Set clear intents** -- describe what you're working on
- **Broadcast milestones** -- share important progress with the team

### Don't

- **Don't use Thrum for task management** -- use Beads for that
- **Don't spam messages** -- batch updates when possible
- **Don't skip the cron watchdog** -- it auto-respawns the listener if it stops
- **Don't ignore critical messages** -- stop work and respond
- **Don't skip registration** -- the system won't route messages correctly (the
  integrated `thrum tmux quickstart` path handles this automatically when the
  coordinator launches you)
- **Don't leave sessions open** -- end them when done to avoid stale status
- **Don't use vague intents** -- be specific about current work

## Troubleshooting

### Not receiving messages

**Diagnosis:**

```bash
# Check registration
thrum status

# Check daemon
thrum daemon status

# Check inbox manually
thrum inbox
```

**Solutions:**

1. **Not registered** -- run `thrum quickstart`
2. **Daemon not running** -- run `thrum daemon start`
3. **Wrong agent name** -- check `THRUM_NAME` env var
4. **Sync issues** -- run `thrum sync force` to pull latest messages

### Messages not syncing

**Diagnosis:**

```bash
# Check sync status
thrum sync status

# Check git remote
git remote -v
```

**Solutions:**

1. **No remote** -- add a git remote
2. **Not committed** -- daemon auto-commits after 60s, or run `thrum sync force`
3. **Not pushed** -- run `thrum sync force` to push
4. **Branch diverged** -- pull and retry

### Multiple agents with same name

**Diagnosis:**

```bash
thrum agent list
```

**Solutions:**

1. **Use unique names** -- each agent needs a unique name
2. **Delete duplicates** -- `thrum agent delete <name>`
3. **Use THRUM_NAME** -- set different names per worktree

### Context recovery after compaction

After conversation compaction, agents can recover context:

```bash
# Check inbox for recent messages
thrum inbox --unread

# Check what others are working on
thrum agent list --context

# Check Beads for task state
bd ready
bd list --status=in_progress
```

## Next Steps

- [Workflow Templates](workflow-templates.md) — pre-built skill pipelines for
  the full research → plan → implement → review cycle
- [Multi-Agent Support](multi-agent.md) — agent groups, runtime presets, and the
  `context prime` command for session recovery
- [Messaging](messaging.md) — full send/receive/reply reference including
  threads, scopes, mentions, and groups
- [MCP Server](mcp-server.md) — optional native tool integration for MCP-capable
  environments
