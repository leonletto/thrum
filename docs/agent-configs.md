
# Agent Configurations

Claude Code agent definitions teach Claude how to use Thrum and Beads effectively. These `.md` files with YAML frontmatter ship in `toolkit/agents/` and load into your project's `.claude/agents/` directory.

## Install the agent configs

Copy the agent definitions to your project:

```bash
mkdir -p .claude/agents
cp toolkit/agents/*.md .claude/agents/
```

Claude Code automatically detects and loads these files when you start a session.

## Available agents

### thrum-agent

Comprehensive guide for Thrum messaging. Teaches Claude how to register agents, send/receive messages, coordinate with teammates, and choose between MCP tools and CLI.

**Use when:**
- Coordinating multi-agent workflows
- Working across worktrees or machines
- Requesting code reviews or assigning tasks
- Broadcasting status updates

**Key capabilities:**
- Agent registration with roles and intents
- Direct messaging and broadcasts
- MCP server integration with async notifications
- Message priority handling (critical/high/normal/low)
- Session management and heartbeats

### beads-agent

Guide for Beads issue tracking. Teaches Claude the task lifecycle (create, claim, close), dependency management with `blocks` relationships, and context recovery after conversation compaction.

**Use when:**
- Managing feature epics with multiple subtasks
- Tracking work across agent sessions
- Coordinating parallel work with dependency graphs
- Discovering side quests during implementation

**Key capabilities:**
- Finding ready work (unblocked tasks)
- Creating tasks with dependencies
- Epic-driven work breakdown
- Git-backed sync for multi-agent coordination
- Adding notes with documentation links

### message-listener

Lightweight background listener agent. Runs on Haiku model for cost efficiency (~$0.00003/cycle). Uses `thrum wait` for efficient blocking instead of polling loops. Returns immediately when messages arrive, covers ~30 minutes across 6 cycles.

**Use when:**
- You want async message notifications
- Working with Thrum via MCP server
- Running long sessions that need message awareness

**Key capabilities:**
- Blocking wait via `thrum wait --all --timeout 5m` (6 cycles max)
- Immediate return on message arrival
- Time-based filtering with `--after` flag (skips old messages)
- CLI-only (no MCP tools — sub-agents can't access MCP)

## Configure the message listener

The message-listener needs specific setup to run on Haiku and use your Thrum identity.

Launch it as a background task at session start:

```typescript
// In Claude Code with Thrum MCP configured
Task({
  subagent_type: "message-listener",
  model: "haiku",
  run_in_background: true,
  prompt: "Listen for Thrum messages. WAIT_CMD=cd /path/to/repo && thrum wait --all --timeout 5m --after -30s --json"
});
```

**Wait command flags:**
- `--all` — Subscribe to all messages (broadcasts + directed)
- `--timeout 5m` — Block up to 5 minutes per cycle
- `--after -30s` — Only return messages from the last 30 seconds (skips old)
- `--json` — Machine-readable output

The listener uses `thrum wait` which blocks until a message arrives or the timeout expires — no polling loops needed. Each cycle is a single Bash call. Re-arm the listener after processing messages to continue listening.

## Customize for your project

You can edit these agent definitions to match your team's workflows. Add project-specific commands, adjust priorities, or include custom context.

For the agent file format, see [Claude Code agent documentation](https://docs.anthropic.com/claude-code).

## See also

- [Quickstart](quickstart.md) — Get started with Thrum in 5 minutes
- [Agent Coordination](agent-coordination.md) — Multi-agent messaging patterns
- [Workflow Templates](../toolkit/templates/) — Complete planning and implementation workflows
