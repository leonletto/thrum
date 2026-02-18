---
name: thrum-agent
description: >
  Thrum multi-agent coordination guide. Git-backed messaging for AI agents to
  communicate across sessions, worktrees, and machines. Covers MCP server
  integration, message-listener pattern, CLI usage, and Beads integration.
---

# Thrum - Multi-Agent Coordination via Git

## Overview

Thrum is a Git-backed messaging system that enables AI agents and humans to
communicate persistently across sessions, worktrees, and machines. It uses Git
as the synchronization layer, ensuring all messages survive context window
limits, session restarts, and machine boundaries.

## Purpose

Use Thrum when:

- **Coordinating multi-agent workflows** - Share messages, assign work, track
  progress
- **Working across worktrees** - Agents in different worktrees need to
  communicate
- **Maintaining conversation history** - Messages persist across session
  compaction
- **Real-time notifications** - Get notified when messages arrive (via MCP
  server)
- **Tracking agent work context** - See what files agents are working on
- **Long-running coordination** - Multi-session work requires persistent
  communication

**DO NOT use for:**

- Simple single-agent tasks with no coordination needs
- Temporary notes or scratch work (use Beads instead)
- Task management (use Beads for that)

## Quick Start (CRITICAL - Read This First)

### Step 1: Choose Integration Method

Thrum supports **two integration methods**:

1. **MCP Server** (RECOMMENDED) - Native tool integration with async message
   notifications
2. **CLI** - Shell-out commands for basic usage

**Use MCP Server when:**

- You want real-time message notifications
- You need async message listening
- You're in a Claude Code environment with MCP support

**Use CLI when:**

- MCP server is not available
- You only need basic message sending
- You're doing simple status updates

### Step 2: Register and Start Session

```bash
# Register as an agent with quickstart
thrum quickstart --name <your-name> --role <role> --module <module> --intent "<description>"

# Examples:
thrum quickstart --name claude_implementer --role implementer --module website --intent "Implementing website build script"
thrum quickstart --name claude_planner --role planner --module website --intent "Planning website architecture"
```

**Common roles:**

- `planner` - Coordinates work, assigns tasks
- `implementer` - Executes tasks
- `reviewer` - Reviews code, provides feedback
- `tester` - Runs tests, reports issues
- `coordinator` - High-level project coordination

### Step 3A: MCP Server Integration (RECOMMENDED)

**Configure MCP Server in Claude Code:**

Add to `.claude/settings.json`:

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

**Launch Message Listener Sub-Agent:**

```typescript
// At start of session, launch background listener
Task({
  subagent_type: "message-listener",
  description: "Listen for Thrum messages",
  prompt: "Listen for messages and notify me when they arrive",
  run_in_background: true,
});
```

**The message-listener will:**

1. Block until a message arrives or timeout (5 minutes default)
2. Return when messages received: `MESSAGES_RECEIVED`
3. Return on timeout: `NO_MESSAGES_TIMEOUT`
4. Automatically process high/critical priority messages

**After listener returns:**

```typescript
// Process messages based on priority
const messages = await mcp.thrum.check_messages();

for (const msg of messages) {
  if (msg.priority === "critical" || msg.priority === "high") {
    // Handle immediately
  } else {
    // Queue for later
  }
}

// Re-arm the listener
Task({
  subagent_type: "message-listener",
  description: "Continue listening",
  prompt: "Listen for more messages",
  run_in_background: true,
});
```

**MCP Tools Available:**

- `send_message` - Send to specific agent via @name
- `check_messages` - Poll inbox, auto-mark read
- `wait_for_message` - Block until message arrives
- `list_agents` - Show registered agents
- `broadcast_message` - Send to all agents

### Step 3B: CLI Integration (Fallback)

If MCP server is not available, use CLI commands:

```bash
# Send message
thrum send "Starting work on task X" --to @coordinator

# Check inbox
thrum inbox

# Reply to message
thrum reply <msg-id> "Here's my update"

# Broadcast to all
thrum send "Deploy complete" --broadcast
```

## Core Concepts

### Agents and Identity

Each agent has:

- **Name** - Human-readable identifier (e.g., `claude_implementer`)
- **Role** - Function category (e.g., `@implementer`, `@planner`)
- **Module** - Work area (e.g., `website`, `auth`, `testing`)
- **Intent** - Current work description

**Identity Files:**

- Stored in `.thrum/identities/<name>.json`
- One identity per agent name
- Multi-worktree support via `THRUM_NAME` env var

### Sessions

Sessions track agent activity:

- **Start** - `thrum session start` or via `thrum quickstart`
- **End** - `thrum session end` (automatic on inactivity)
- **Heartbeat** - `thrum session heartbeat` (keep alive)
- **Intent** - `thrum session set-intent "description"` (update work context)

### Messages

Messages are the core communication primitive:

- **To** - Direct message to specific agent (`--to @name`)
- **Broadcast** - Send to all active agents (`--broadcast`)
- **Reply** - Reply to existing message (creates thread)
- **Priority** - `low`, `normal`, `high`, `critical`
- **Mentions** - Reference agents with `@name` syntax

**Message Storage:**

- Per-agent JSONL files: `.git/thrum-sync/a-sync/messages/<agent>.jsonl`
- Git-backed, syncs via `git push/pull`
- Event-sourced with ULID IDs

### Threads

Threads group related messages:

- **Auto-created** - When replying to a message
- **Manual** - `thrum thread create --message "topic" --to @name`
- **List** - `thrum thread list`
- **Show** - `thrum thread show <thread-id>`

### Groups

Groups enable sending messages to collections of agents:

- **Built-in @everyone** - Auto-created group containing all agents
- **Custom groups** - Create groups for teams, roles, or projects
- **Member types** - Groups can contain agents (`@alice`), roles (`--role planner`), or other groups (`--group team`)
- **Nesting** - Groups can contain other groups (with cycle detection)
- **Pull model** - Group membership resolved at read time (receivers query)

**Group examples:**

```bash
# Create team group
thrum group create backend-team

# Add members
thrum group add backend-team @alice       # Add specific agent
thrum group add backend-team --role implementer  # Add all with role
thrum group add leads --group backend-team      # Nest groups

# Send to group
thrum send "Team meeting at 3pm" --to @backend-team
```

### Work Context

Agents can track what they're working on:

- **Files** - What files they're editing
- **Intent** - High-level description of work
- **Status** - Active/idle state

**Check context:**

```bash
# Who's working on what
thrum status

# Who has specific file
thrum who-has <file>

# Ping agent for status
thrum ping @name
```

## Common Workflows

### Workflow 1: Planner → Implementer Coordination

**Planner:**

```bash
# Register as planner
thrum quickstart --name planner --role planner --module website --intent "Coordinating website development"

# Assign task via message
thrum send "Please implement build script (task thrum-235d.3). Design spec in docs/plans/. Check beads for details." --to @implementer --priority high

# Check for updates
thrum inbox
```

**Implementer (with MCP):**

```typescript
// 1. Launch listener
Task({
  subagent_type: "message-listener",
  prompt: "Listen for messages from planner",
});

// 2. When listener returns with MESSAGES_RECEIVED:
const messages = await mcp.thrum.check_messages();

// 3. Process task assignment
// (read design spec, check beads, implement)

// 4. Send status update
await mcp.thrum.send_message({
  to: "@planner",
  content: "Build script complete. Tests passing. Ready for review.",
  priority: "normal",
});

// 5. Re-arm listener
Task({
  subagent_type: "message-listener",
  prompt: "Continue listening",
});
```

### Workflow 2: Peer Collaboration

**Agent A:**

```bash
# Start work, announce to team
thrum send "Starting work on auth module" --broadcast

# Reserve file (via message convention)
thrum send "Working on src/auth/login.ts" --to @coordinator
```

**Agent B:**

```bash
# Check who has file before editing
thrum who-has src/auth/login.ts
# Output: @agent-a (active)

# Coordinate via message
thrum send "Need to edit login.ts for validation. ETA?" --to @agent-a
```

### Workflow 3: Code Review Request

**Implementer:**

```bash
# Request review
thrum send "Build script complete (commit abc123). Please review:
- Markdown processing
- Search index generation
- Error handling

Tests passing. Beads task: thrum-235d.3" --to @reviewer --priority high
```

**Reviewer:**

```bash
# Check inbox
thrum inbox

# View message details
thrum message get <msg-id>

# Respond with findings
thrum reply <msg-id> "Reviewed. Found 2 issues:
1. Missing error handling in parseMarkdown()
2. Search index doesn't handle compound terms

See beads: thrum-abc (bug filed). Otherwise looks good."
```

### Workflow 4: Multi-Worktree Coordination

**Agent in main worktree:**

```bash
cd /path/to/repo
export THRUM_NAME=main_agent
thrum quickstart --name main_agent --role coordinator --module main --intent "Main branch coordination"

# Send to agent in other worktree
thrum send "Feature branch ready for integration testing" --to @feature_agent
```

**Agent in feature worktree:**

```bash
cd ~/.workspaces/repo/feature
export THRUM_NAME=feature_agent
thrum quickstart --name feature_agent --role implementer --module feature --intent "Feature implementation"

# Check inbox (sees messages from main_agent)
thrum inbox
```

### Workflow 5: Team Groups

**Coordinator sets up groups:**

```bash
# Create team groups
thrum group create backend-team --description "Backend developers"
thrum group create frontend-team --description "Frontend developers"
thrum group create leads --description "Team leads"

# Add members by role
thrum group add backend-team --role implementer
thrum group add frontend-team --role implementer

# Add specific agents to leads
thrum group add leads @coordinator
thrum group add leads @senior-dev

# Nest groups (leads includes both teams)
thrum group add leads --group backend-team
thrum group add leads --group frontend-team

# Send to entire team
thrum send "Sprint planning meeting at 2pm" --to @backend-team
```

**Agent receives group message:**

```bash
# Messages to groups show up in inbox
thrum inbox --unread
# Output includes: "Sprint planning meeting at 2pm" (from @coordinator to @backend-team)
```

## MCP Server Integration (DETAILED)

### Message Listener Pattern

The message-listener sub-agent enables **asynchronous message notifications**
without polling:

```typescript
// 1. Start listener at beginning of session
Task({
  subagent_type: "message-listener",
  description: "Background message listener",
  prompt:
    "Listen for Thrum messages. Block until message arrives or 5-minute timeout. Return MESSAGES_RECEIVED if messages arrive, NO_MESSAGES_TIMEOUT on timeout.",
  run_in_background: true,
});

// 2. Work on other tasks while listener runs in background

// 3. When listener returns (message arrived or timeout)
const listenerResult = await TaskOutput(listenerId);

if (listenerResult.includes("MESSAGES_RECEIVED")) {
  // Process messages
  const messages = await mcp.thrum.check_messages({
    markAsRead: true,
  });

  // Handle by priority
  const critical = messages.filter((m) => m.priority === "critical");
  const high = messages.filter((m) => m.priority === "high");

  // Stop current work for critical messages
  if (critical.length > 0) {
    // Handle immediately
  }

  // Queue high priority for next breakpoint
  if (high.length > 0) {
    // Add to work queue
  }

  // Re-arm listener
  Task({
    subagent_type: "message-listener",
    prompt: "Continue listening for messages",
  });
} else {
  // Timeout - re-arm if still working
  if (stillHaveWork) {
    Task({
      subagent_type: "message-listener",
      prompt: "Continue listening",
    });
  } else {
    // Send status and exit
    await mcp.thrum.send_message({
      to: "@coordinator",
      content: "Work complete. Going idle.",
      priority: "low",
    });
  }
}
```

### Priority Handling

| Priority   | Action                                              |
| ---------- | --------------------------------------------------- |
| `critical` | Stop current work immediately, process message      |
| `high`     | Process at next breakpoint (end of current subtask) |
| `normal`   | Queue, process when convenient                      |
| `low`      | Background, process during idle time                |

### MCP Tool Reference

**send_message:**

```typescript
await mcp.thrum.send_message({
  to: "@agent-name", // Required: recipient @name
  content: "Message text", // Required: message body
  priority: "normal", // Optional: low|normal|high|critical
});
```

**check_messages:**

```typescript
const messages = await mcp.thrum.check_messages({
  markAsRead: true, // Optional: auto-mark as read
  limit: 20, // Optional: max messages to return
});
```

**wait_for_message:**

```typescript
// Block until message arrives or timeout
const result = await mcp.thrum.wait_for_message({
  timeout: 300000, // Optional: 5 minutes default
  mentionOnly: false, // Optional: only messages mentioning me
});
```

**list_agents:**

```typescript
const agents = await mcp.thrum.list_agents({
  includeOffline: false, // Optional: only show active agents
});
```

**broadcast_message:**

```typescript
// DEPRECATED: Use send_message with to="@everyone" instead
await mcp.thrum.broadcast_message({
  content: "Deploy complete",
  priority: "normal",
});

// PREFERRED: Send to @everyone group
await mcp.thrum.send_message({
  to: "@everyone",
  content: "Deploy complete",
  priority: "normal",
});
```

**Group Management Tools:**

```typescript
// Create a group
await mcp.thrum.create_group({
  name: "backend-team",
  description: "Backend developers",
});

// Add members (auto-detects type: agent, role, or group)
await mcp.thrum.add_group_member({
  group: "backend-team",
  member: "@alice", // Add specific agent
});

await mcp.thrum.add_group_member({
  group: "backend-team",
  member: "--role implementer", // Add all agents with role
});

await mcp.thrum.add_group_member({
  group: "leads",
  member: "--group backend-team", // Add another group (nesting)
});

// Remove member
await mcp.thrum.remove_group_member({
  group: "backend-team",
  member: "@alice",
});

// List all groups
const groups = await mcp.thrum.list_groups();

// Get group details (with expansion to agent IDs)
const group = await mcp.thrum.get_group({
  name: "backend-team",
  expand: true, // Resolves nested groups/roles to agent IDs
});

// Delete a group
await mcp.thrum.delete_group({
  name: "old-team",
});
```

## CLI Reference

### Core Commands

**Session Management:**

```bash
thrum quickstart --name <name> --role <role> --module <module> --intent "<description>"
thrum agent register
thrum agent list
thrum agent delete <name>
thrum session start
thrum session end
thrum session heartbeat
thrum session set-intent "<description>"
thrum status
```

**Messaging:**

```bash
thrum send "message" --to @name [--priority normal]
thrum send "message" --to @groupname            # Send to group
thrum send "message" --broadcast                # DEPRECATED: use --to @everyone
thrum inbox [--unread]                          # Auto-excludes own messages, marks displayed as read
thrum reply <msg-id> "response"
thrum message get <msg-id>
thrum message edit <msg-id> "new-text"
thrum message delete <msg-id>
thrum message read <msg-id> [<msg-id>...]       # Mark specific messages as read
thrum message read --all                         # Mark all unread messages as read
```

**Groups:**

```bash
thrum group create <name> [--description "text"]
thrum group delete <name>
thrum group add <group> <member>                # Auto-detects: @alice = agent, --role planner = role, --group team = group
thrum group remove <group> <member>
thrum group list [--json]
thrum group info <name> [--json]
thrum group members <name> [--expand] [--json]  # --expand resolves nested groups/roles to agent IDs
```

**Threads:**

```bash
thrum thread list
thrum thread show <thread-id>
thrum thread create --message "topic" --to @name
```

**Coordination:**

```bash
thrum who-has <file>
thrum ping @name
thrum overview                # Combined status view
```

**Sync:**

```bash
thrum sync                    # Sync with git remote
thrum sync --status          # Check sync status
```

## Integration with Beads

When both Thrum and Beads are available, use them together:

**Use Thrum for:**

- Real-time coordination messages
- Status updates and check-ins
- Code review requests
- Question/answer exchanges
- Notifications and alerts

**Use Beads for:**

- Task management and tracking
- Dependencies and blockers
- Work discovery and filing issues
- Progress tracking
- Persistent task state

### Unified Workflow

```bash
# 1. Register in Thrum
thrum quickstart --name implementer --role implementer --module auth --intent "Implementing auth system"

# 2. Find work in Beads
bd ready --json
# Choose: bd-123 "Implement JWT authentication"

# 3. Claim in Beads
bd update bd-123 --status in_progress --json

# 4. Announce via Thrum
thrum send "Starting bd-123: JWT authentication" --to @coordinator

# 5. Work on implementation
# (make code changes)

# 6. Discover issues -> file in Beads
bd create "Found validation bug" -t bug -p 1 --deps discovered-from:bd-123 --json

# 7. Update coordinator via Thrum
thrum send "Progress update: JWT working, found validation bug (filed bd-456)" --to @coordinator

# 8. Complete in Beads
bd close bd-123 --reason "JWT auth complete with tests" --json

# 9. Announce via Thrum
thrum send "Completed bd-123. Ready for review." --to @reviewer --priority high

# 10. Sync both
bd sync
# (Thrum auto-syncs via daemon)
```

### Mapping Convention

| Concept    | Beads                              | Thrum                                   |
| ---------- | ---------------------------------- | --------------------------------------- |
| Task ID    | `bd-123`                           | Include in message: "Working on bd-123" |
| Status     | `bd update --status`               | Send message with update                |
| Priority   | `bd update --priority`             | `--priority` flag on messages           |
| Assignment | `bd update --assignee`             | Send message to specific agent          |
| Completion | `bd close bd-123`                  | Send completion message                 |
| Discovery  | `bd create --deps discovered-from` | Notify via message                      |

## Best Practices

### DO ✅

- **Register at session start** - Always use `thrum quickstart`
- **Use MCP server when available** - Better than CLI polling
- **Launch message-listener** - Get real-time notifications
- **Handle priorities** - Respect critical/high/normal/low
- **Send status updates** - Keep team informed
- **Use @mentions** - Reference agents by name
- **Use groups for team messaging** - Create groups for common recipient sets
- **Include context** - Beads IDs, file paths, commit hashes
- **End sessions** - Run `thrum session end` when done
- **Set clear intents** - Describe what you're working on
- **Send to @everyone for broadcasts** - Use @everyone group instead of --broadcast flag

### DON'T ❌

- **Don't use Thrum for task management** - Use Beads for that
- **Don't spam messages** - Batch updates when possible
- **Don't forget to re-arm listener** - After processing messages
- **Don't ignore critical messages** - Stop work and respond
- **Don't use Thrum for discovery** - File in Beads, announce via Thrum
- **Don't skip registration** - System won't route messages correctly
- **Don't leave sessions open** - End when done to avoid stale status
- **Don't use vague intents** - Be specific about current work
- **`--broadcast`, `--to @everyone`, and `--everyone` are all equivalent** - Use whichever reads best
- **Don't delete @everyone group** - It's protected and auto-created

## Common Patterns

### Pattern 1: Task Assignment

**Coordinator:**

```bash
# Find ready work in Beads
bd ready --json

# Assign via Thrum
thrum send "Task bd-456 is ready for implementation. See design spec in docs/plans/. Estimated 2-3 hours." --to @implementer --priority high
```

**Implementer (MCP):**

```typescript
// Listener notifies of high-priority message
const messages = await mcp.thrum.check_messages();
const task = messages.find((m) => m.content.includes("bd-456"));

// Claim in Beads
await bash("bd update bd-456 --status in_progress --json");

// Acknowledge via Thrum
await mcp.thrum.send_message({
  to: "@coordinator",
  content: "Claimed bd-456. Starting implementation.",
});
```

### Pattern 2: Blocked Notification

```bash
# Hit blocker
bd update bd-789 --status blocked --json

# Notify team
thrum send "Blocked on bd-789: Need API credentials for Stripe integration. @coordinator can you help?" --to @coordinator --priority high
```

### Pattern 3: Review Request

```bash
# Complete work
git commit -m "feat: Add JWT auth (bd-123)"
bd close bd-123 --reason "Complete with tests" --json

# Request review
thrum send "bd-123 complete (commit a1b2c3d). Please review:
- JWT generation (src/auth/jwt.ts)
- Middleware (src/auth/middleware.ts)
- Tests (tests/auth.test.ts)

All tests passing. Ready to merge." --to @reviewer --priority high
```

### Pattern 4: Context Compaction Recovery

After conversation compaction, agents can recover context via Thrum:

```bash
# Check inbox for recent context
thrum inbox --unread

# Check what others are working on
thrum agent list

# Review thread history
thrum thread list

# Check Beads for task state
bd ready --json
bd list --status in_progress --json
```

## Session Workflow Template

Use this template for every agent session:

```bash
# === START OF SESSION ===

# 1. Register and start session
thrum quickstart --name <name> --role <role> --module <module> --intent "<description>"

# 2. Launch message listener (if using MCP)
# Task(subagent_type="message-listener", ...)

# 3. Check inbox for any urgent messages
thrum inbox --unread

# 4. Find work (Beads)
bd ready --json

# 5. Claim task (Beads)
bd update <id> --status in_progress --json

# 6. Announce start (Thrum)
thrum send "Starting work on bd-<id>: <description>" --to @coordinator

# === DURING SESSION ===

# 7. Send status updates
thrum send "Progress update: <status>" --to @coordinator

# 8. Handle incoming messages (via listener)
# Process messages, respond as needed

# 9. Coordinate on blockers
thrum send "Blocked: <description>" --to @coordinator --priority high

# === END OF SESSION ===

# 10. Complete work (Beads)
bd close <id> --reason "Complete" --json
bd sync

# 11. Announce completion (Thrum)
thrum send "Completed bd-<id>. Tests passing. Ready for review." --to @reviewer

# 12. End session
thrum session end
```

## Troubleshooting

### Issue 1: Not receiving messages

**Symptom:** Message listener times out, messages not appearing

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

1. **Not registered** - Run `thrum quickstart`
2. **Daemon not running** - Run `thrum daemon start`
3. **Wrong agent name** - Check `THRUM_NAME` env var
4. **Sync issues** - Run `git pull` to get latest messages

### Issue 2: Message listener not working

**Symptom:** MCP `wait_for_message` fails or hangs

**Diagnosis:**

```bash
# Check MCP server is configured
cat .claude/settings.json | grep thrum

# Test MCP server directly
thrum mcp serve --help
```

**Solutions:**

1. **MCP not configured** - Add to `.claude/settings.json`
2. **Daemon not running** - Start with `thrum daemon start`
3. **WebSocket issues** - Check `http://localhost:9999` is accessible
4. **Fallback to CLI** - Use `thrum inbox` polling instead

### Issue 3: Messages not syncing

**Symptom:** Other agents don't see my messages

**Diagnosis:**

```bash
# Check sync status
thrum sync --status

# Check git remote
git remote -v

# Check if messages committed
git log --oneline -10 a-sync
```

**Solutions:**

1. **No remote** - Add git remote
2. **Not committed** - Daemon auto-commits after 60s, or run `thrum sync`
3. **Not pushed** - Run `thrum sync` to force push
4. **Branch diverged** - Pull and retry

### Issue 4: Multiple agents with same name

**Symptom:** Messages routing to wrong agent

**Diagnosis:**

```bash
# List all agents
thrum agent list

# Check identity files
ls -la .thrum/identities/
```

**Solutions:**

1. **Use unique names** - Each agent needs unique name
2. **Delete duplicates** - `thrum agent delete <name>`
3. **Use THRUM_NAME** - Set different names per worktree

## Resources

### Documentation

- **CLAUDE.md** - (this file) Complete agent integration guide
- `docs/mcp-server.md` - MCP server technical details
- `docs/messaging.md` - Message concepts and workflows
- `docs/identity.md` - Agent identity system
- `docs/cli.md` - Complete CLI reference

### Web UI

View messages in browser:

```bash
# Check UI URL
thrum status
# Open: http://localhost:9999
```

### Git Artifacts

All messages stored in Git:

```
.git/thrum-sync/a-sync/
├── events.jsonl              # Agent lifecycle events
└── messages/
    ├── agent1.jsonl          # Agent 1's messages
    ├── agent2.jsonl          # Agent 2's messages
    └── ...

.thrum/
├── identities/
│   ├── agent1.json           # Agent 1's identity
│   └── agent2.json           # Agent 2's identity
└── var/
    └── messages.db           # SQLite cache (gitignored)
```

## Quick Reference

### Essential Commands

```bash
# Register and start
thrum quickstart --name <name> --role <role> --module <module> --intent "<description>"

# Send messages
thrum send "message" --to @name                # Direct message
thrum send "message" --to @groupname           # Send to group
thrum send "message" --to @everyone            # Send to all agents
thrum reply <msg-id> "response"                # Reply in thread

# Check messages
thrum inbox                                    # View inbox (auto-excludes own messages)
thrum inbox --unread                          # Only unread (marks displayed as read)

# Groups
thrum group create <name>                      # Create group
thrum group add <group> <member>               # Add member (@agent, --role role, --group group)
thrum group list                               # List all groups

# Coordination
thrum who-has <file>                          # Check file ownership
thrum ping @name                              # Check if agent active
thrum status                                   # My status

# Session management
thrum session end                             # End session
thrum agent delete <name>                     # Delete registration
```

### MCP Integration

```typescript
// Launch listener (start of session)
Task({ subagent_type: "message-listener", prompt: "Listen for messages" });

// Send message
await mcp.thrum.send_message({ to: "@name", content: "text" });

// Check messages
const messages = await mcp.thrum.check_messages();

// List agents
const agents = await mcp.thrum.list_agents();
```

## Summary

Thrum provides:

- ✅ **Git-backed messaging** - Persistent across sessions and machines
- ✅ **Multi-agent coordination** - Real-time communication between agents
- ✅ **MCP Server integration** - Native tools with async notifications
- ✅ **Message listener pattern** - Get notified when messages arrive
- ✅ **Groups** - Send to teams, roles, or collections of agents
- ✅ **Worktree support** - Agents in different worktrees can communicate
- ✅ **Context preservation** - Messages survive conversation compaction
- ✅ **Work context tracking** - See who's working on what files

**Remember:**

- **Always register at session start** - `thrum quickstart`
- **Use MCP server when available** - Better than CLI
- **Launch message-listener** - Get async notifications
- **Integrate with Beads** - Use both for full coordination
- **Send status updates** - Keep team informed
- **Handle priorities** - Respect critical/high messages
- **End sessions cleanly** - `thrum session end`

**Quick Start:**

```bash
# 1. Register
thrum quickstart --name my_agent --role implementer --module website --intent "Building website"

# 2. Launch listener (if MCP available)
# Task(subagent_type="message-listener", ...)

# 3. Send message
thrum send "Starting work" --to @coordinator

# 4. Check inbox
thrum inbox

# 5. End session
thrum session end
```

---

**Version:** 1.3 **Last Updated:** 2026-02-11 **Status:** Production-Ready

**Changes in v1.3:** Agent Groups feature added — `thrum group create/add/remove/list/info/members`, 6 new MCP tools (`create_group`, `delete_group`, `add_group_member`, `remove_group_member`, `list_groups`, `get_group`). Built-in `@everyone` group for broadcasts. `--broadcast` is now an alias for `--to @everyone`. Groups support nesting (groups can contain other groups/roles) with cycle detection.

**Changes in v1.2:** `thrum wait --mention` fixed — maps to `mention_role` RPC
param, strips `@` prefix. Added `thrum message read --all` for batch
mark-as-read. All messaging bugs resolved.

**Changes in v1.1:** Message echo in inbox fixed — `exclude_self` server-side.
Inbox `--unread` filtering fixed — proper `is_read` via LEFT JOIN, auto
mark-as-read.
