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

Agent context provides a simple, persistent storage mechanism for agents to save
and retrieve volatile project state that doesn't belong in git commits but needs
to survive session boundaries. Each agent gets a dedicated markdown file at
`.thrum/context/{agent-name}.md` for storing notes, work-in-progress findings,
temporary decisions, or any other context that helps continuity across sessions.

**Use cases:**

- Documenting architectural decisions still under consideration
- Tracking partial investigation results
- Recording discovered patterns or anti-patterns in the codebase
- Maintaining a running list of TODOs or questions
- Preserving context when handing off work to another agent or session

**Not for:**

- Permanent documentation (use git-tracked docs)
- Message-based coordination (use `thrum send`)
- Code or configuration (use proper git-tracked files)

## Storage Location

Context files are stored in `.thrum/context/`:

```
.thrum/
├── context/
│   ├── furiosa.md                  # Agent context (volatile, per-session)
│   ├── furiosa_preamble.md         # Agent preamble (stable, user-editable)
│   ├── maximus.md                  # Another agent's context
│   ├── maximus_preamble.md         # Another agent's preamble
│   └── coordinator_1B9K33T6RK.md   # Hash-based agent ID
├── identities/
└── var/
```

The `.thrum/` directory is gitignored, so context files are local by default.
Use `thrum context sync` to manually share context across worktrees via the
a-sync branch.

## Context Layers

Thrum provides a three-layer context model for managing agent state at different levels of persistence:

| Layer | File | Persistence | Content | Maintained By |
|-------|------|-------------|---------|---------------|
| Prompt | `dev-docs/prompts/{epic}.md` | Given at session start | Full task instructions, phases, quality commands | Planning agent |
| Preamble | `.thrum/context/{name}_preamble.md` | Persists across all sessions | Agent identity, project rules, architectural constraints | Planning agent (initial), human (updates) |
| Context | `.thrum/context/{name}.md` | Updated each session | Current task, decisions made, blockers hit | `/update-context` skill |

The **preamble** is the stable middle layer. It is automatically prepended when showing context via `thrum context show`, providing persistent project-specific instructions. The default thrum quick-reference is always included; custom content from `--preamble-file` is appended below.

The **context** file is volatile — rewritten each session by the `/update-context` skill with current task state, decisions, and blockers.

See [Workflow Templates](workflow-templates.md) for how these layers work together in the planning → prepare → implement workflow.

## Preamble

Each agent can have a **preamble** — a stable, user-editable header stored at
`.thrum/context/{agent}_preamble.md`. The preamble is automatically prepended
when showing context via `thrum context show`, providing a persistent reference
(like a system prompt) that survives context saves.

**Key properties:**

- **Not touched by `thrum context save`** — the preamble persists across saves
- **Auto-created on first save** — a default preamble with thrum quick reference
  commands is created when you first save context
- **User-editable** — customize via `thrum context preamble --file` or edit directly
- **Not removed by `thrum context clear`** — clear only removes session context
- **Removed on agent delete** — cleaned up alongside context when agent is deleted

**Default preamble content:**

```markdown
## Thrum Quick Reference

**Check messages:** `thrum inbox --unread`
**Send message:** `thrum send "message" --to @role`
**Reply:** `thrum reply <MSG_ID> "response"`
**Status:** `thrum status`
**Who's online:** `thrum agent list --context`
**Save context:** `thrum context save`
**Wait for messages:** `thrum wait --all --after -30s --timeout 5m`
```

### Suggestions & Examples

The preamble acts as a persistent system prompt that survives session resets. Custom preambles can encode project conventions, role-specific instructions, team rosters, or boot sequences.

**Use cases:**

- **Project-specific instructions** — Encode coding standards, preferred tools, repo structure hints
- **Role-based preambles** — Different instructions for coordinator vs implementer vs reviewer agents
- **Team coordination** — Team roster, communication protocols, escalation paths
- **Session recovery** — Boot sequence that tells the agent what to check on startup

#### Example 1: Project-Specific Preamble

```markdown
## Project Conventions

**Architecture:** Hexagonal (ports/adapters)
**Preferred tools:** `rg` for search, `fd` for files, `jq` for JSON
**Repo structure:**
- `cmd/` — CLI entrypoints
- `internal/` — Private application code
- `pkg/` — Public libraries

**Testing:** Always run `make test` before committing. Use table-driven tests.
**Commits:** Follow Conventional Commits (feat:, fix:, docs:, etc.)

## Thrum Quick Reference

**Check messages:** `thrum inbox --unread`
**Send message:** `thrum send "message" --to @role`
**Who's online:** `thrum agent list --context`
```

**Setup:**

```bash
# Save to file
cat > project-preamble.md <<'EOF'
## Project Conventions
...
EOF

# Install for all agents in this repo
thrum context preamble --file project-preamble.md
```

#### Example 2: Role-Based Preamble (Coordinator)

```markdown
## Coordinator Boot Sequence

On session start:
1. `thrum inbox --unread` — Check pending messages
2. `thrum agent list --context` — See who's online
3. `thrum overview` — Get full team status
4. Review context from last session

**Responsibilities:**
- Triage incoming work
- Assign tasks to implementers and reviewers
- Resolve blockers
- Escalate critical issues to @lead

**Communication:**
- Daily status to @lead at 9am, 5pm
- Broadcast critical blockers immediately
- Reply to all agent messages within 15min

## Team Roster

- @lead (Alice) — Overall direction
- @implementer (Bob, Carol) — Feature work
- @reviewer (Dave) — Code review, security
- @tester (Eve) — Integration tests

## Thrum Quick Reference

**Send to team:** `thrum send "message" --to @implementer`
**Broadcast:** `thrum send "urgent message" --to @all --priority high`
**Wait for replies:** `thrum wait --mention @coordinator --timeout 5m`
```

**Setup (coordinator-specific):**

```bash
# Coordinator gets this preamble
export THRUM_NAME=coordinator
thrum context preamble --file coordinator-preamble.md

# Implementers get a different preamble
export THRUM_NAME=implementer
thrum context preamble --file implementer-preamble.md
```

#### Example 3: Session Recovery Preamble

```markdown
## Session Recovery Checklist

Run these commands at the start of every session:

```bash
# 1. Check for messages
thrum inbox --unread

# 2. See who's online
thrum agent list --context

# 3. Review last session's context
thrum context show

# 4. Check git status
git status

# 5. See recent commits
git log --oneline -5

# 6. Check for failing tests
make test || echo "Tests failing - investigate"
```

If continuing work:
- Reply to any pending messages first
- Update intent: `thrum agent set-intent "..."`
- Send status to @coordinator

If starting new work:
- Check backlog: `bd list --status open`
- Coordinate with @coordinator before claiming
- Set intent when starting

## Thrum Quick Reference

**Update intent:** `thrum agent set-intent "Working on auth module"`
**Save context:** `thrum context save --file notes.md`
**Heartbeat:** `thrum agent heartbeat`
```

#### Version-Controlled Preamble Templates

Store preamble templates in the repo and load them dynamically:

```bash
# Directory structure
.thrum-templates/
├── preamble-coordinator.md
├── preamble-implementer.md
└── preamble-reviewer.md

# Load from template
thrum context preamble --file .thrum-templates/preamble-coordinator.md
```

**Tip:** Add preamble setup to onboarding scripts:

```bash
#!/bin/bash
# scripts/setup-agent.sh

ROLE="${1:-implementer}"
PREAMBLE_FILE=".thrum-templates/preamble-${ROLE}.md"

if [[ ! -f "$PREAMBLE_FILE" ]]; then
  echo "No template for role: $ROLE"
  exit 1
fi

thrum context preamble --file "$PREAMBLE_FILE"
echo "Preamble installed for $ROLE"
```

## CLI Commands

### thrum context save

Save context content from a file or stdin.

```bash
thrum context save [flags]
```

| Flag       | Description                                         | Default  |
| ---------- | --------------------------------------------------- | -------- |
| `--file`   | Path to markdown file to save as context           |          |
| `--agent`  | Override agent name (defaults to current identity) |          |

**Examples:**

```bash
# Save from a file
thrum context save --file dev-docs/Continuation_Prompt.md

# Save from stdin
echo "Working on auth module" | thrum context save

# Save for a different agent
thrum context save --agent coordinator --file notes.md
```

**File permissions:** Context directory is created with `0750`, files are saved
with `0644`.

---

### thrum context show

Display the saved context for the current agent.

```bash
thrum context show [flags]
```

| Flag              | Description                                         | Default |
| ----------------- | --------------------------------------------------- | ------- |
| `--agent`         | Override agent name (defaults to current identity)  |         |
| `--raw`           | No header, file boundary markers for piping         | `false` |
| `--no-preamble`   | Exclude preamble from output                        | `false` |

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

# Raw context only (no header, no preamble)
thrum context show --raw --no-preamble > backup.md
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

| Flag      | Description                                         | Default |
| --------- | --------------------------------------------------- | ------- |
| `--agent` | Override agent name (defaults to current identity) |         |

**Examples:**

```bash
# Clear context for current agent
thrum context clear

# Clear context for a different agent
thrum context clear --agent furiosa
```

**Behavior:** Idempotent — running clear when no context exists is a no-op.

---

### thrum context sync

Copy the context file to the a-sync branch for sharing across worktrees and
machines.

```bash
thrum context sync [flags]
```

| Flag      | Description                                         | Default |
| --------- | --------------------------------------------------- | ------- |
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
- **Manual only** — context is never synced automatically. You must explicitly
  run `thrum context sync` when you want to share.

---

### thrum context preamble

Show or manage the preamble for the current agent.

```bash
thrum context preamble [flags]
```

| Flag       | Description                                         | Default  |
| ---------- | --------------------------------------------------- | -------- |
| `--agent`  | Override agent name (defaults to current identity)  |          |
| `--init`   | Create or reset to default preamble                |          |
| `--file`   | Set preamble from file                              |          |

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

**Behavior:** The preamble is auto-created with default content on the first
`thrum context save`. Use `--init` to reset to the default, or `--file` to set
custom content.

**Note:** Preambles can also be set during agent bootstrapping with `thrum quickstart --preamble-file <path>`. This composes the default preamble (thrum quick-reference) with custom content from the specified file.

---

### thrum context update

Install or update the `/update-context` skill for Claude Code agents.

```bash
thrum context update
```

**What it does:**

Detects the `/update-context` skill in these locations (in priority order):

1. **Project-level**: `.claude/commands/update-context.md` (relative to repo
   root)
2. **Global**: `~/.claude/commands/update-context.md`

If the skill is found, prints its location and status. If not found, provides
installation instructions.

**Example output:**

```
/update-context skill installed at:
  /path/to/repo/.claude/commands/update-context.md

Restart Claude Code to load the skill.
```

---

## The /update-context Skill

The `/update-context` skill is a Claude Code command that provides a guided
workflow for updating agent context. It prompts for continuation context,
formats it as markdown, and saves it via `thrum context save`.

**Installation:**

1. Create `.claude/commands/update-context.md` in your repo or globally at
   `~/.claude/commands/update-context.md`
2. Restart Claude Code
3. Use `/update-context` from the chat to invoke the skill

**Workflow:**

1. Skill prompts: "What context should I preserve for the next session?"
2. You provide notes, decisions, TODOs, or findings
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

## Integration with thrum status

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

When no context file exists, the `Context:` line is omitted.

---

## Integration with Agent Cleanup

When you delete an agent with `thrum agent delete`, the context and preamble
files are removed alongside the identity and message files:

```bash
$ thrum agent delete furiosa
Delete agent 'furiosa' and all associated data? [y/N] y
✓ Agent deleted: furiosa
  - Removed identity file
  - Removed message file
  - Removed context file
  - Removed preamble file
```

---

## Workflow Examples

### Single-Agent Session Continuity

```bash
# At the end of a work session
echo "# Next Steps
- Finish JWT implementation
- Add rate limiting tests
- Review security considerations" | thrum context save

# Next session
thrum context show
# Output:
# # Next Steps
# - Finish JWT implementation
# - Add rate limiting tests
# - Review security considerations
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

### Automated Context Updates

```bash
# Update context after significant changes
git log -1 --pretty=format:"Last commit: %h %s" | thrum context save

# Append investigation notes
thrum context show --raw >> investigation.md
# ... edit investigation.md ...
thrum context save --file investigation.md
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

When no context exists:

```json
{
  "result": {
    "agent_name": "furiosa",
    "has_context": false,
    "has_preamble": false
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

- `context.save` and `context.clear` acquire a **write lock** (`Lock()`)
- `context.show` acquires a **read lock** (`RLock()`)

This ensures thread-safe access when multiple clients interact with context
files.

### File Format

Context files are plain markdown (`.md`). No special format or structure is
enforced — agents are free to use any markdown convention that suits their
workflow.

**Common patterns:**

- Heading-based sections (`# Next Steps`, `# Decisions`, `# Questions`)
- Bulleted lists for TODOs
- Code blocks for snippets or commands
- Fenced blocks for structured data (JSON, YAML)

### Sync Workflow (Manual Only)

Context sync is **manual-only** to avoid noise and respect agent autonomy:

- The daemon **never** auto-syncs context files
- Agents must explicitly run `thrum context sync` to share
- Sync respects local-only mode (no-op when `--local` is set on the daemon)

**Rationale:** Context is volatile and session-specific. Auto-syncing would
create unnecessary churn. Manual sync gives agents control over when and what to
share.

---

## Troubleshooting

### Context file not found

**Cause:** Agent has never saved context, or it was cleared.

**Solution:** Save new context with `thrum context save`.

### Permission denied

**Cause:** `.thrum/context/` directory has incorrect permissions.

**Solution:** Re-create the directory:

```bash
rm -rf .thrum/context
thrum context save --file /dev/null  # Creates directory
```

### Context sync fails

**Cause:** No remote configured, or local-only mode is active.

**Solution:** Check sync status and remote configuration:

```bash
thrum sync status
git remote -v
```

If local-only mode is active, sync will be a no-op. If no remote is configured,
add one:

```bash
git remote add origin <repo-url>
```

### Wrong agent's context shown

**Cause:** Multiple agents in the same worktree, and `THRUM_NAME` is not set.

**Solution:** Set `THRUM_NAME` to select the correct agent:

```bash
export THRUM_NAME=furiosa
thrum context show
```

---

## Best Practices

### Keep Context Concise

Context files should be **high-signal** summaries, not exhaustive logs. Prefer
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
