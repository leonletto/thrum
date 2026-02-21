
# Agent Configurations

> **Recommended:** Install the [Thrum plugin](claude-code-plugin.md) instead of
> manual agent definitions. The plugin provides 10 slash commands, automatic
> context hooks, and 8 resource docs — all in a single install.

Claude Code agent definitions teach Claude how to use Thrum effectively. These
`.md` files with YAML frontmatter ship in `toolkit/agents/` and load into your
project's `.claude/agents/` directory. For Beads task tracking, we recommend
installing the [Beads plugin](#beads-plugin-recommended) instead.

## Install the agent configs

Copy the agent definitions to your project:

```bash
mkdir -p .claude/agents
cp toolkit/agents/thrum-agent.md .claude/agents/
cp toolkit/agents/message-listener.md .claude/agents/
```

Claude Code automatically detects and loads these files when you start a
session.

## Available agents

### thrum-agent

Comprehensive guide for Thrum messaging. Teaches Claude how to register agents,
send/receive messages, coordinate with teammates, and choose between MCP tools
and CLI.

**Use when:**

- Coordinating multi-agent workflows
- Working across worktrees or machines
- Requesting code reviews or assigning tasks
- Broadcasting status updates

**Key capabilities:**

- Agent registration with roles and intents
- Direct messaging and broadcasts
- MCP server integration with async notifications
- Session management and heartbeats

### Beads Plugin (recommended)

For Beads issue tracking, **install the Beads plugin** instead of using a local
agent file. The plugin provides richer functionality:

- **30+ slash commands** (`/beads:ready`, `/beads:create`, `/beads:close`,
  `/beads:sync`, etc.)
- **15+ resource files** covering dependencies, workflows, troubleshooting, and
  more
- **Hooks** that auto-run `bd prime` on session start for workflow context
- **Session protocol** with CLI reference and resource links

Install the plugin in Claude Code:

```
/install-plugin beads
```

Or visit the [Beads project](https://github.com/steveyegge/beads) for details.

### message-listener

Lightweight background listener that watches for incoming Thrum messages so you
don't have to manually check your inbox. Runs on Haiku for cost efficiency
(~$0.00003/cycle). Uses `thrum wait` for efficient blocking instead of polling
loops — returns immediately when messages arrive, covers ~90 minutes across 6
cycles.

**Use when:**

- You're running multiple agents and want to know when they message you
- Working long sessions where agents on other worktrees may send updates
- You want incoming messages surfaced without manually running `thrum inbox`

**Key capabilities:**

- Blocking wait via `thrum wait --timeout 15m` (6 cycles max, filters by agent identity)
- Immediate return on message arrival
- Time-based filtering with `--after` flag (skips old messages)
- CLI-only (no MCP tools — sub-agents can't access MCP)

## Configure the message listener

The message-listener runs as a background task so your main agent session stays
focused on work while messages are watched for you.

Launch it at session start:

```typescript
// In Claude Code with Thrum MCP configured
Task({
  subagent_type: "message-listener",
  model: "haiku",
  run_in_background: true,
  prompt:
    "Listen for Thrum messages. WAIT_CMD=cd /path/to/repo && thrum wait --timeout 15m --after -30s --json",
});
```

**Wait command flags:**

- `--timeout 15m` — Block up to 15 minutes per cycle
- `--after -30s` — Only return messages from the last 30 seconds (skips old)
- `--json` — Machine-readable output

The listener uses `thrum wait` which blocks until a message arrives or the
timeout expires — no polling loops needed. Each cycle is a single Bash call.
Re-arm the listener after processing messages to continue listening.

## Customize for your project

You can edit these agent definitions to match your team's workflows. Add
project-specific commands, adjust priorities, or include custom context.

For the agent file format, see
[Claude Code agent documentation](https://docs.anthropic.com/claude-code).

## See also

- [Claude Code Plugin](claude-code-plugin.md) — Recommended: plugin with slash
  commands and hooks
- [Quickstart](quickstart.md) — Get started with Thrum in 5 minutes
- [Agent Coordination](agent-coordination.md) — Multi-agent messaging patterns
- [Workflow Templates](../toolkit/templates/) — Complete planning and
  implementation workflows
