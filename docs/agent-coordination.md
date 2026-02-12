
# Agent Coordination

Thrum enables multiple AI agents and humans to coordinate work across sessions,
worktrees, and machines. This guide covers practical coordination patterns,
integration with the Beads issue tracker, and session workflow templates.

## Coordination Methods

Thrum supports two integration methods for agent coordination:

### MCP Server (Recommended)

Native tool integration with async message notifications. Best for Claude Code
agents.

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

MCP tools: `send_message`, `check_messages`, `wait_for_message`, `list_agents`,
`broadcast_message`.

### CLI (Fallback)

Shell commands for basic messaging. Works everywhere.

```bash
thrum send "Starting work on task X" --to @coordinator
thrum inbox --unread
thrum reply <msg-id> "Here's my update"
```

## Common Workflows

### Planner-Implementer Coordination

The most common pattern: a planner agent assigns work and an implementer
executes it.

**Planner:**

```bash
# Register as planner
thrum quickstart --name planner --role planner --module website \
  --intent "Coordinating website development"

# Assign task via message
thrum send "Please implement build script (task thrum-235d.3). \
  Design spec in docs/plans/. Check beads for details." \
  --to @implementer --priority high

# Check for updates
thrum inbox
```

**Implementer:**

```bash
# Register
thrum quickstart --name implementer --role implementer --module website \
  --intent "Implementing website features"

# Check inbox for assignments
thrum inbox --unread

# Acknowledge and work
thrum reply <msg-id> "Claimed task. Starting implementation."

# Send completion update
thrum send "Build script complete. Tests passing. Ready for review." \
  --to @planner
```

### Peer Collaboration

Agents working on related areas coordinate to avoid conflicts.

```bash
# Agent A: Announce work area
thrum send "Starting work on auth module" --broadcast

# Agent B: Check file ownership before editing
thrum who-has src/auth/login.ts
# Output: @agent_a is editing src/auth/login.ts

# Agent B: Coordinate via message
thrum send "Need to edit login.ts for validation. ETA?" --to @agent_a
```

### Code Review

Request and respond to code reviews via messaging.

**Implementer:**

```bash
thrum send "Build script complete (commit abc123). Please review:
- Markdown processing
- Search index generation
- Error handling

Tests passing. Beads task: thrum-235d.3" --to @reviewer --priority high
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

Agents in different git worktrees share the same daemon and message store.

**Agent in main worktree:**

```bash
cd /path/to/repo
export THRUM_NAME=main_agent
thrum quickstart --name main_agent --role coordinator --module main \
  --intent "Main branch coordination"

thrum send "Feature branch ready for integration testing" --to @feature_agent
```

**Agent in feature worktree:**

```bash
cd ~/.workspaces/repo/feature
thrum setup --main-repo /path/to/repo
export THRUM_NAME=feature_agent
thrum quickstart --name feature_agent --role implementer --module feature \
  --intent "Feature implementation"

# Check inbox (sees messages from main_agent)
thrum inbox
```

## Message-Listener Pattern

For async message handling, spawn a background sub-agent that listens for
incoming messages and notifies the main agent when they arrive.

### How It Works

1. The main agent spawns a message-listener as a background task
2. The listener polls `thrum inbox --unread` on a loop with sleep intervals
3. When a message arrives, the listener returns immediately with the message
   content
4. The main agent processes the message and re-arms the listener

### Return Format

When messages are received:

```
MESSAGES_RECEIVED
FROM: [sender]
PRIORITY: [priority]
CONTENT: [message content]
TIMESTAMP: [timestamp]
```

When timeout occurs with no messages:

```
NO_MESSAGES_TIMEOUT
```

### Priority Handling

When the main agent receives messages from the listener:

| Priority   | Action                                  |
| ---------- | --------------------------------------- |
| `critical` | Stop current work immediately           |
| `high`     | Process at next breakpoint              |
| `normal`   | Process when current sub-task completes |
| `low`      | Queue, process when convenient          |

### Context Management

- Re-arm the listener after processing messages (the listener exits after
  returning)
- After 5 consecutive timeouts with no pending work, send status to the
  coordinator and stop the listener
- The listener is read-only; it never sends messages

## Beads Integration

When both Thrum and [Beads](https://github.com/leonletto/thrum) are available,
use them together for full coordination:

| Concern                 | Tool  | Why                                       |
| ----------------------- | ----- | ----------------------------------------- |
| Real-time communication | Thrum | Messages, status updates, review requests |
| Task management         | Beads | Issues, dependencies, work discovery      |
| Progress tracking       | Beads | Status fields, close with reason          |
| Notifications           | Thrum | Alert team to progress or blockers        |

### Unified Workflow

```bash
# 1. Register in Thrum
thrum quickstart --name implementer --role implementer --module auth \
  --intent "Implementing auth system"

# 2. Find work in Beads
bd ready

# 3. Claim in Beads
bd update bd-123 --status in_progress

# 4. Announce via Thrum
thrum send "Starting bd-123: JWT authentication" --to @coordinator

# 5. Work on implementation...

# 6. Discover issues -> file in Beads
bd create --title="Found validation bug" --type=bug --priority=1

# 7. Update coordinator via Thrum
thrum send "Progress: JWT working, found validation bug (filed bd-456)" \
  --to @coordinator

# 8. Complete in Beads
bd close bd-123 --reason="JWT auth complete with tests"

# 9. Announce via Thrum
thrum send "Completed bd-123. Ready for review." \
  --to @reviewer --priority high

# 10. Sync both
bd sync
thrum sync force
```

### Mapping Convention

| Concept    | Beads                  | Thrum                                   |
| ---------- | ---------------------- | --------------------------------------- |
| Task ID    | `bd-123`               | Include in message: "Working on bd-123" |
| Status     | `bd update --status`   | Send message with update                |
| Priority   | `bd update --priority` | `--priority` flag on messages           |
| Assignment | `bd update --assignee` | Send message to specific agent          |
| Completion | `bd close`             | Send completion message                 |
| Discovery  | `bd create`            | Notify via message                      |

## Session Workflow Template

Use this template for every agent session:

```bash
# === START OF SESSION ===

# 1. Register and start session
thrum quickstart --name <name> --role <role> --module <module> \
  --intent "<description>"

# 2. Check inbox for any urgent messages
thrum inbox --unread

# 3. Find work (Beads)
bd ready

# 4. Claim task (Beads)
bd update <id> --status in_progress

# 5. Announce start (Thrum)
thrum send "Starting work on <id>: <description>" --to @coordinator

# === DURING SESSION ===

# 6. Send periodic status updates
thrum send "Progress: <status>" --to @coordinator

# 7. Handle incoming messages
thrum inbox --unread

# 8. Coordinate on blockers
thrum send "Blocked: <description>" --to @coordinator --priority high

# === END OF SESSION ===

# 9. Complete work (Beads)
bd close <id> --reason "Complete"
bd sync

# 10. Announce completion (Thrum)
thrum send "Completed <id>. Tests passing. Ready for review." \
  --to @reviewer

# 11. End session
thrum session end
```

## Best Practices

### Do

- **Register at session start** -- always use `thrum quickstart`
- **Use MCP server when available** -- better than CLI polling
- **Handle priorities** -- respect critical/high/normal/low
- **Send status updates** -- keep the team informed
- **Use @mentions** -- reference agents by name
- **Include context** -- Beads IDs, file paths, commit hashes
- **End sessions cleanly** -- run `thrum session end` when done
- **Set clear intents** -- describe what you're working on
- **Broadcast milestones** -- share important progress with the team

### Don't

- **Don't use Thrum for task management** -- use Beads for that
- **Don't spam messages** -- batch updates when possible
- **Don't forget to re-arm the listener** -- after processing messages
- **Don't ignore critical messages** -- stop work and respond
- **Don't skip registration** -- the system won't route messages correctly
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

# Review thread history
thrum thread list

# Check Beads for task state
bd ready
bd list --status=in_progress
```

## See Also

- [Multi-Agent Support](multi-agent.md) -- groups, runtime presets, and team coordination
- [Tailscale Sync](tailscale-sync.md) -- cross-machine sync via Tailscale
- [CLI Reference](cli.md) -- complete command documentation
- [MCP Server](mcp-server.md) -- MCP tools and message-listener details
- [Messaging System](messaging.md) -- message structure and threading
- [Quickstart Guide](quickstart.md) -- getting started in 5 minutes
- [Identity System](identity.md) -- agent names and registration
