---
title: "Claude Code Agent Integration"
description:
  "Agent definitions for multi-agent coordination in Claude Code — thrum-agent
  and message-listener setup"
category: "identity"
---

## Claude Code Agent Integration

Thrum ships two Claude Code agent definitions for multi-agent coordination.
Place these in `.claude/agents/` in any project that uses Thrum.

## thrum-agent

A comprehensive coordination guide that teaches Claude Code how to use Thrum for
multi-agent workflows. Covers:

- MCP server integration and configuration
- CLI commands for messaging, sessions, and coordination
- Message listener pattern for async notifications
- Common workflows: task assignment, peer collaboration, code review
- Beads integration for combined task + messaging coordination
- Troubleshooting guide

**Usage:** Reference this agent when an AI agent needs to coordinate with other
agents via Thrum messaging.

<details>
<summary>thrum-agent.md</summary>

```markdown
---
name: thrum-agent
description: >
  Thrum multi-agent coordination guide. Persistent messaging for AI agents to
  communicate across sessions, worktrees, and machines. Covers MCP server
  integration, message-listener pattern, CLI usage, and Beads integration.
---

# Thrum - Multi-Agent Coordination via Git

## Overview

Thrum is a messaging system that enables AI agents and humans to communicate
persistently across sessions, worktrees, and machines. It uses Git as the
synchronization layer, ensuring all messages survive context window limits,
session restarts, and machine boundaries.

## Quick Start

### 1. Register and Start Session

thrum quickstart --name <your-name> --role <role> --module <module> --intent
"<description>"

Common roles: planner, implementer, reviewer, tester, coordinator

### 2. Configure MCP Server (recommended)

Add to .claude/settings.json:

{ "mcpServers": { "thrum": { "type": "stdio", "command": "thrum", "args":
["mcp", "serve"] } } }

### 3. Launch Background Listener

Spawn a message-listener sub-agent to get async notifications. The listener
loops automatically for up to 4 hours — no manual re-arming needed. Set up a
cron watchdog to auto-respawn it every 30 min if it stops.

### 4. Communicate

thrum send "message" --to @name # Direct message thrum send "message" --to
@everyone # Broadcast to all thrum inbox --unread # Check for new messages thrum
sent --unread # Check sent items and receipts thrum reply <msg-id> "response" #
Reply (creates a reply-to reference)

## MCP Tools (5 total)

**Core messaging (4):**

- send_message — Send to specific agent via @name
- check_messages — Poll inbox, auto-mark read
- wait_for_message — Block until message arrives
- list_agents — Show registered agents

**Deprecated (1):**

- broadcast_message — Use `send_message` with `to="@everyone"` instead

## Session Template

# Start

thrum quickstart --name <name> --role <role> --module <module> --intent "<desc>"
thrum inbox --unread thrum sent --unread thrum message read --all # Mark all
messages as read after reviewing

# During work

thrum send "status update" --to @coordinator

# End

thrum session end
```

</details>

## message-listener

A lightweight background agent (runs on Haiku for ~$0.00003/cycle) that uses
`thrum wait` for efficient blocking until messages arrive — no polling loops or
sleep intervals needed. It loops up to 30 cycles (~4 hours of coverage),
returning immediately when new messages arrive. A cron watchdog auto-respawns it
every 30 min if it stops — no manual re-arming needed.

**Usage:** Spawn at the start of every coordination session. Set up a cron
watchdog to keep it running automatically.

<details>
<summary>message-listener.md</summary>

```markdown
---
name: message-listener
description: >
  Background listener for incoming Thrum messages. Runs on Haiku for cost
  efficiency (~$0.00003/cycle). Uses `thrum wait` for efficient blocking instead
  of polling loops. Returns immediately when new messages arrive.
model: haiku
allowed-tools:
  - Bash
---

You are a background message listener for the Thrum agent messaging system.

## CRITICAL: Tool Constraints

You do NOT have access to MCP tools. You MUST use the Bash tool exclusively to
run thrum CLI commands.

## Instructions

You use `thrum wait` to block efficiently until a message arrives. No polling
loops needed — `thrum wait` uses the daemon's WebSocket push internally.

Your prompt contains the EXACT command to use after "WAIT_CMD=". Use that
command verbatim.

**LOOP** (repeat up to 30 cycles, ~4 hour coverage):

1. Run the WAIT_CMD from your prompt EXACTLY as given (Bash).
2. If messages found (exit code 0), return them immediately.
3. If timeout (exit code 1), go back to step 1.
4. If error (exit code 2), return the error.

After exhausting all cycles with no messages, return NO_MESSAGES_TIMEOUT.

## Return Format

When messages received:

## MESSAGES_RECEIVED

FROM: [sender] CONTENT: [message content]

---

When timeout:

NO_MESSAGES_TIMEOUT

Always end with:

LISTENER_STOPPED: This listener has exhausted its cycles. The cron watchdog will
respawn it automatically within 30 minutes.
```

</details>

## Setup

Copy both files into your project:

```bash
mkdir -p .claude/agents
cp docs/claude-agent-integration.md .  # For reference

# Create the agent files (or copy from this doc)
# .claude/agents/thrum-agent.md
# .claude/agents/message-listener.md
```

Or see the full agent definitions in the
[documentation site](https://leonletto.github.io/thrum).

## Next Steps

- [Claude Code Plugin](claude-code-plugin.md) — slash commands, hooks, and
  resource docs for Claude Code
- [MCP Server](mcp-server.md) — optional native tool integration for MCP-capable
  environments
- [Multi-Agent Support](multi-agent.md) — runtime presets and coordination
  patterns
