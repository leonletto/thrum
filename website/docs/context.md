---
title: "Agent Context Management"
description:
  "Per-agent context storage for persisting volatile project state across
  sessions and machines"
category: "context"
order: 1
tags: ["context", "agents", "persistence", "markdown", "state"]
last_updated: "2026-02-12"
---

# Agent Context Management

## Overview

Agents lose state between sessions due to context window compaction, session
resets, or switching machines. The context system preserves volatile session
state so agents can pick up where they left off.

**Why this exists:** When an agent session ends (context compacted, Claude Code
restarted, new worktree), the work-in-progress state vanishes. Context files
capture that state so the next session can continue seamlessly.

**Primary use case:** The `/update-context` skill in Claude Code uses this
system to save session summaries before compaction or session end.

**Storage:** Context files live in `.thrum/context/` (gitignored by default).
Each agent gets two files:

- `.thrum/context/{name}.md` - Volatile session state (updated frequently)
- `.thrum/context/{name}_preamble.md` - Stable reference (rarely changes)

## Context Files

Context files hold volatile session state that doesn't belong in git commits but
needs to survive session boundaries.

**What to put in context:**

- Current task and progress
- Architectural decisions under consideration
- Partial investigation results
- Discovered patterns or anti-patterns
- TODOs or questions for the next session
- Work-in-progress findings

**What NOT to put in context:**

- Permanent documentation (use git-tracked docs)
- Message-based coordination (use `thrum send`)
- Code or configuration (use proper git-tracked files)

**Location:**

```
.thrum/
├── context/
│   ├── furiosa.md                  # Agent context (volatile)
│   ├── furiosa_preamble.md         # Agent preamble (stable)
│   ├── maximus.md
│   ├── maximus_preamble.md
│   └── coordinator_1B9K33T6RK.md   # Hash-based agent ID
```

Context files are local by default. Use `thrum context sync` to manually share
across worktrees via the a-sync branch.

## Preamble

Each agent can have a preamble - a stable, user-editable header stored at
`.thrum/context/{agent}_preamble.md`. The preamble is automatically prepended
when showing context, providing a persistent reference that survives context
saves.

**Default preamble:** When you run `thrum quickstart`, a default preamble is
created automatically with thrum quick-reference commands.

**User-editable:** The preamble is just a markdown file. You can edit it
directly or replace it with `thrum context preamble --file custom.md`.

**Key properties:**

- Not touched by `thrum context save` - the preamble persists across saves
- Auto-created on first quickstart with default thrum quick-reference
- Not removed by `thrum context clear` - clear only removes session context
- Removed on agent delete - cleaned up alongside context

**Default content:**

```markdown
## Thrum Quick Reference

**Check messages:** `thrum inbox --unread` **Send message:**
`thrum send "message" --to @role` **Reply:** `thrum reply <MSG_ID> "response"`
**Status:** `thrum status` **Who's online:** `thrum agent list --context` **Save
context:** `thrum context save` **Wait for messages:**
`thrum wait --after -30s --timeout 5m`
```

**Customization examples:**

You can edit the preamble to add project conventions, role-specific
instructions, team rosters, or boot sequences:

```bash
# Edit the preamble directly
vim .thrum/context/furiosa_preamble.md

# Or replace it from a file
cat > my-preamble.md <<'EOF'
## Project Conventions

**Architecture:** Hexagonal (ports/adapters)
**Testing:** Always run `make test` before committing
**Commits:** Follow Conventional Commits (feat:, fix:, docs:)

## Thrum Quick Reference
... (default commands) ...
EOF

thrum context preamble --file my-preamble.md
```

## CLI Commands

### thrum context save

Save context content from a file or stdin.

```bash
thrum context save [flags]
```

| Flag      | Description                                        | Default |
| --------- | -------------------------------------------------- | ------- |
| `--file`  | Path to markdown file to save as context           |         |
| `--agent` | Override agent name (defaults to current identity) |         |

**Examples:**

```bash
# Save from a file
thrum context save --file notes.md

# Save from stdin
echo "Working on auth module" | thrum context save

# Save for a different agent
thrum context save --agent coordinator --file notes.md
```

---

### thrum context show

Display the saved context for the current agent.

```bash
thrum context show [flags]
```

| Flag            | Description                                        | Default |
| --------------- | -------------------------------------------------- | ------- |
| `--agent`       | Override agent name (defaults to current identity) |         |
| `--raw`         | No header, file boundary markers for piping        | `false` |
| `--no-preamble` | Exclude preamble from output                       | `false` |

**Examples:**

```bash
# Show preamble + context (default)
thrum context show

# Show context for a different agent
thrum context show --agent furiosa

# Raw output with file boundary markers
thrum context show --raw

# Context only, no preamble
thrum context show --no-preamble
```

**Output modes:**

Default (preamble + context with header):

```
# Context for furiosa (1234 bytes, updated 2026-02-11T10:00:00Z)

## Thrum Quick Reference
...

# Current Work
- Implementing JWT token refresh
```

Raw (`--raw`, shows file boundaries):

```
<!-- preamble: .thrum/context/furiosa_preamble.md -->
## Thrum Quick Reference
...
<!-- end preamble -->

# Current Work
- Implementing JWT token refresh
```

---

### thrum context clear

Remove the context file for the current agent.

```bash
thrum context clear [flags]
```

| Flag      | Description                                        | Default |
| --------- | -------------------------------------------------- | ------- |
| `--agent` | Override agent name (defaults to current identity) |         |

**Examples:**

```bash
# Clear context for current agent
thrum context clear

# Clear context for a different agent
thrum context clear --agent furiosa
```

Note: Idempotent - running clear when no context exists is a no-op.

---

### thrum context sync

Copy the context file to the a-sync branch for sharing across worktrees and
machines.

```bash
thrum context sync [flags]
```

| Flag      | Description                                        | Default |
| --------- | -------------------------------------------------- | ------- |
| `--agent` | Override agent name (defaults to current identity) |         |

**Examples:**

```bash
# Sync context for current agent
thrum context sync

# Sync context for a different agent
thrum context sync --agent furiosa
```

**What it does:**

1. Copies `.thrum/context/{agent}.md` to the sync worktree at
   `.git/thrum-sync/a-sync/context/{agent}.md`
2. Commits the change with message `"context: update {agent}"`
3. Pushes to the remote a-sync branch

**Notes:**

- No-op when no remote is configured (local-only mode)
- Respects the `--local` daemon flag
- Manual only - context is never synced automatically

---

### thrum context preamble

Show or manage the preamble for the current agent.

```bash
thrum context preamble [flags]
```

| Flag      | Description                                        | Default |
| --------- | -------------------------------------------------- | ------- |
| `--agent` | Override agent name (defaults to current identity) |         |
| `--init`  | Create or reset to default preamble                |         |
| `--file`  | Set preamble from file                             |         |

**Examples:**

```bash
# Show current preamble
thrum context preamble

# Create/reset to default preamble
thrum context preamble --init

# Set preamble from a custom file
thrum context preamble --file my-preamble.md

# Show preamble for a different agent
thrum context preamble --agent furiosa
```

---

### thrum context prime

Collect all context needed for agent session initialization or recovery. This is
a comprehensive context collection command that gathers identity, session info,
agent list, unread messages, git context, and daemon health into a single
output.

```bash
thrum context prime [flags]
```

| Flag     | Description                          | Default |
| -------- | ------------------------------------ | ------- |
| `--json` | Structured JSON output for scripting | `false` |

**Examples:**

```bash
# Human-readable summary
thrum context prime

# Structured JSON output
thrum context prime --json
```

**What it includes:**

- Agent identity (name, role, module)
- Active session information
- List of registered agents and their status
- Unread messages count
- Git context (branch, commits, files)
- Daemon health status

**Use cases:**

- Session initialization - quickly orient a new session
- Session recovery - restore context after crash or compaction
- Debugging - gather all relevant state in one command
- Agent onboarding - provide comprehensive context to new agents

**Note:** This command is an alias for `thrum prime`. The PreCompact hook
automatically saves context before compaction to `.thrum/context/{name}.md` and
`/tmp/thrum-pre-compact-{name}-{role}-{module}-{epoch}.md`, but the
agent-initiated `/update-context` skill captures richer context including
decisions and rationale.

---

### thrum context update

The `/update-context` skill is now integrated with the Thrum MCP server. Use the
MCP server for guided context updates:

```bash
thrum mcp serve
```

**In Claude Code:**

Configure the MCP server in `.claude/settings.json` and use the
`wait_for_message` and `send_message` tools for context coordination.

---

## The /update-context Skill

The `/update-context` skill is now integrated into the Thrum MCP server. The MCP
server provides message-based context coordination through the `send_message`
and `wait_for_message` tools.

**Usage:**

Configure the MCP server in `.claude/settings.json` and agents can coordinate
context updates via messages.

**Workflow:**

1. Agent sends context via `send_message`
2. Other agents receive via `wait_for_message` or `check_messages`
3. Skill formats your input as markdown and saves it via
   `thrum context save --file /tmp/context.md`

**Example:**

```
User: /update-context
Agent: What context should I preserve for the next session?
User: We're refactoring the auth module. Decided to use JWT with
      refresh tokens. Need to add rate limiting tests.
Agent: [Saves formatted context]
      ✓ Context saved (248 bytes)
```

The skill reduces the friction of updating context and ensures consistent
formatting.

---

## Use Cases and Patterns

### Single-Agent Session Continuity

```bash
# At the end of a work session
echo "# Next Steps
- Finish JWT implementation
- Add rate limiting tests
- Review security considerations" | thrum context save

# Next session
thrum context show
```

### Multi-Agent Handoff

```bash
# Agent A saves context before handing off
thrum context save --file handoff-notes.md
thrum context sync  # Share across worktrees

# Agent B (in another worktree) pulls and reads
git fetch origin
thrum context show --agent furiosa
```

### Context Updates at Decision Points

Save context when you make a significant decision, discover something important,
or reach a natural breakpoint:

```bash
# After architectural decision
echo "# Decision: Using JWT with refresh tokens
- Token expiry: 15 min (access), 7 days (refresh)
- Storage: Redis for refresh tokens
- Rate limit: 100 req/min per IP" | thrum context save
```

### Integration with thrum status

The `thrum status` command shows context file size and age when context exists:

```bash
$ thrum status
Agent:    furiosa (@implementer)
Module:   auth
Session:  ses_01HXF2A9... (active 2h15m)
Intent:   Implementing JWT refresh
Context:  1.2 KB (updated 5m ago)    # ← Context indicator
Inbox:    3 unread (12 total)
```

---

## RPC API

Context operations are available via the daemon's RPC API:

### context.save

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "context.save",
  "params": {
    "agent_name": "furiosa",
    "content": "base64-encoded-content"
  }
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "agent_name": "furiosa",
    "message": "Context saved for furiosa (248 bytes)"
  }
}
```

---

### context.show

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "context.show",
  "params": {
    "agent_name": "furiosa",
    "include_preamble": true
  }
}
```

The `include_preamble` field is optional and defaults to `true` when omitted.

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "agent_name": "furiosa",
    "preamble": "base64-encoded-preamble",
    "content": "base64-encoded-content",
    "has_context": true,
    "has_preamble": true,
    "preamble_size": 256,
    "size": 1234,
    "updated_at": "2026-02-11T10:00:00Z"
  }
}
```

---

### context.preamble.show

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "context.preamble.show",
  "params": {
    "agent_name": "furiosa"
  }
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "agent_name": "furiosa",
    "content": "base64-encoded-preamble",
    "has_preamble": true
  }
}
```

---

### context.preamble.save

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "context.preamble.save",
  "params": {
    "agent_name": "furiosa",
    "content": "base64-encoded-content"
  }
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "agent_name": "furiosa",
    "message": "Preamble saved for furiosa (256 bytes)"
  }
}
```

---

### context.clear

**Request:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "context.clear",
  "params": {
    "agent_name": "furiosa"
  }
}
```

**Response:**

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "agent_name": "furiosa",
    "message": "Context cleared for furiosa"
  }
}
```

---

## Implementation Notes

### Locking Strategy

Context RPC handlers follow the daemon's standard locking patterns:

- `context.save` and `context.clear` acquire a write lock (`Lock()`)
- `context.show` acquires a read lock (`RLock()`)

This ensures thread-safe access when multiple clients interact with context
files.

### File Format

Context files are plain markdown (`.md`). No special format or structure is
enforced - agents are free to use any markdown convention that suits their
workflow.

**Common patterns:**

- Heading-based sections (`# Next Steps`, `# Decisions`, `# Questions`)
- Bulleted lists for TODOs
- Code blocks for snippets or commands
- Fenced blocks for structured data (JSON, YAML)

### Sync Workflow (Manual Only)

Context sync is manual-only to avoid noise and respect agent autonomy:

- The daemon never auto-syncs context files
- Agents must explicitly run `thrum context sync` to share
- Sync respects local-only mode (no-op when `--local` is set on the daemon)

**Rationale:** Context is volatile and session-specific. Auto-syncing would
create unnecessary churn. Manual sync gives agents control over when and what to
share.

---

## Best Practices

### Keep Context Concise

Context files should be high-signal summaries, not exhaustive logs. Prefer
bullet points over paragraphs.

**Good:**

```markdown
# Next Steps

- Finish JWT implementation (see src/auth/jwt.go)
- Add rate limiting (decided on 100 req/min per IP)
- Review security: check token expiry edge cases
```

**Bad:**

```markdown
# What I Did Today

I started by looking at the auth module and then I realized that we need JWT so
I implemented a basic version but it's not complete yet and I still need to add
rate limiting which I discussed with the team and we decided on 100 requests per
minute...
```

### Update Context at Decision Points

Save context when you make a significant decision, discover something important,
or reach a natural breakpoint.

**When to update:**

- After choosing between architectural alternatives
- When discovering a bug or anti-pattern
- Before switching to a different task
- At the end of a work session

### Use the /update-context Skill

The skill provides a consistent, low-friction workflow for updating context.
Install it and use it regularly.

### Sync Selectively

Only sync context that is useful to other agents or future sessions on different
machines. Local notes and WIP context can stay local.

---

## See Also

- [Identity System](identity.md) - Agent identity and registration
- [CLI Reference](cli.md) - All CLI commands
- [RPC API Reference](rpc-api.md) - Complete RPC method documentation
- [Agent Coordination](agent-coordination.md) - Multi-agent workflows
