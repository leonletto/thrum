---
title: "Claude Code Plugin"
description:
  "Install and use the Thrum plugin for Claude Code — slash commands, automatic
  context injection, and multi-agent coordination without manual configuration"
category: "guides"
order: 3
tags:
  [
    "claude-code",
    "plugin",
    "installation",
    "slash-commands",
    "mcp",
    "hooks",
  ]
last_updated: "2026-02-13"
---

# Claude Code Plugin

> See also: [Quickstart Guide](quickstart.md) for basic Thrum setup,
> [Agent Configurations](agent-configs.md) for manual agent definitions,
> [MCP Server](mcp-server.md) for the native MCP transport,
> [Multi-Agent Support](multi-agent.md) for coordination patterns.

## Overview

The Thrum plugin for Claude Code gives agents native access to Thrum messaging
through slash commands, automatic context injection via hooks, and progressive
disclosure resource docs. It replaces the manual agent definition approach
(copying `.md` files into `.claude/agents/`) with a single plugin install.

**What you get:**

- **10 slash commands** — `/thrum:send`, `/thrum:inbox`, `/thrum:quickstart`, and more
- **Automatic context** — SessionStart and PreCompact hooks run `thrum prime` to
  inject identity, team roster, inbox, and git state
- **8 resource docs** — Progressive disclosure for messaging patterns, groups,
  identity, worktrees, and anti-patterns
- **Background listener** — Sub-agent template for async message monitoring

## Prerequisites

Before installing the plugin, you need Thrum installed and initialized:

```bash
# Install thrum (Go 1.23+)
go install github.com/leonletto/thrum@latest

# Or build from source
git clone https://github.com/leonletto/thrum.git
cd thrum && make install

# Initialize in your repository
cd /path/to/your/repo
thrum init
thrum daemon start
```

Verify the daemon is running:

```bash
thrum daemon status
```

## Installation

### From GitHub (self-hosted)

The plugin is distributed as a self-hosted marketplace via the repository:

```bash
claude plugin add --from https://github.com/leonletto/thrum
```

### From local clone

If you've cloned the Thrum repository:

```bash
claude plugin add --from /path/to/thrum
```

### Verify installation

Start a new Claude Code session. The SessionStart hook should automatically run
`thrum prime` and inject context. You'll see your agent identity, team roster,
and unread messages in the session preamble.

## Slash Commands

All commands live under the `/thrum:` namespace.

| Command | Purpose |
|---------|---------|
| `/thrum:quickstart` | Register agent and start session (interactive or with flags) |
| `/thrum:send` | Send direct, group, or broadcast messages with priority |
| `/thrum:inbox` | Check message inbox (all or unread only) |
| `/thrum:reply` | Reply to a message (inherits original audience) |
| `/thrum:wait` | Block until a message arrives (background listener use) |
| `/thrum:team` | Show active team members with roles and intents |
| `/thrum:group` | Create, manage, and message agent groups |
| `/thrum:overview` | Combined status + team + inbox view |
| `/thrum:prime` | Load full session context (identity, team, inbox, git) |
| `/thrum:update-context` | Guided workflow to save session narrative + state |

### Common workflows

**Start a session:**

```
/thrum:quickstart
```

Prompts for role, module, and intent — or pass flags directly:

```bash
thrum quickstart --role implementer --module auth --intent "Building login flow"
```

**Send a message:**

```
/thrum:send
```

Guided prompt for recipient, message, and priority. Direct usage:

```bash
thrum send "Starting auth work" --to @coordinator
thrum send "Need review" --to @reviewers -p high
thrum send "Heads up: breaking change" --to @everyone
```

**Check inbox and reply:**

```
/thrum:inbox
/thrum:reply
```

## Hooks

The plugin configures two hooks that run automatically:

### SessionStart

Runs `thrum prime` when a Claude Code session begins. Injects:

- Agent identity (name, role, module)
- Active team members and their intents
- Unread messages
- Git branch and status
- Daemon health

If Thrum isn't initialized, shows a friendly setup message instead of failing.

### PreCompact

Runs `thrum prime` before context compaction. This ensures agent identity and
session state survive compaction — the agent can continue working without losing
track of who it is or what messages are pending.

## Resource Docs

The plugin includes 8 resource documents that Claude loads on demand for deeper
guidance:

| Resource | When it's used |
|----------|---------------|
| `MESSAGING.md` | Message lifecycle, priority handling, addressing patterns |
| `GROUPS.md` | Creating groups, adding members, group messaging |
| `IDENTITY.md` | Agent naming, registration, multi-worktree identity |
| `WORKTREES.md` | Cross-worktree coordination, shared daemon, file tracking |
| `LISTENER_PATTERN.md` | Background message listener sub-agent template |
| `BOUNDARIES.md` | When to use Thrum vs TaskList/SendMessage |
| `ANTI_PATTERNS.md` | 12 common mistakes and how to avoid them |
| `CLI_REFERENCE.md` | Complete command syntax for all `thrum` commands |

## MCP Server Integration

For native tool-call integration (no shell-outs), configure the Thrum MCP
server in your project's `.claude/settings.json`:

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

This provides MCP tools like `send_message`, `check_messages`,
`wait_for_message`, `list_agents`, and group management tools. See
[MCP Server](mcp-server.md) for the full API.

**Plugin vs MCP:** The plugin's slash commands use the CLI (`Bash(thrum:*)`).
The MCP server provides native tool calls. Both work — the plugin is simpler to
set up, the MCP server is more efficient for high-frequency messaging. You can
use both together.

## Background Listener

When `thrum prime` runs in a Claude Code session and detects an active identity,
it outputs a ready-to-use listener spawn instruction with the correct repo path.
Copy and run it to start background message monitoring:

```
Task(
  subagent_type="message-listener",
  model="haiku",
  run_in_background=true,
  prompt="Listen for Thrum messages.
    WAIT_CMD=cd /path/to/repo && thrum wait --all --timeout 15m --after -30s --json"
)
```

The listener runs 6 cycles of 15 minutes each (~90 min coverage), blocks on
`thrum wait` (no polling), returns when messages arrive, and costs
~$0.00003/cycle on Haiku. Re-arm after processing each batch. See the
`LISTENER_PATTERN.md` resource for the full template.

## Plugin vs Manual Agent Definitions

| Feature | Plugin | Manual (toolkit/agents/) |
|---------|--------|-------------------------|
| Installation | `claude plugin add` | Copy `.md` files to `.claude/agents/` |
| Updates | Re-install from source | Manual file replacement |
| Slash commands | 10 commands included | None |
| Hooks | SessionStart + PreCompact | Manual hook configuration |
| Resource docs | 8 progressive disclosure docs | Single monolithic agent file |
| Maintenance | Versioned (v0.4.0) | Ad-hoc |

The manual agent definitions (`thrum-agent.md`, `message-listener.md`) still
work and are available in `toolkit/agents/` for environments that don't support
plugins.

## Troubleshooting

**"Thrum not initialized" on session start**

Run `thrum init && thrum daemon start` in your repository root.

**No context injected after plugin install**

Verify the plugin is loaded: check that `/thrum:prime` is available as a slash
command. If not, reinstall the plugin.

**Messages not arriving**

Check daemon status with `thrum daemon status`. The daemon must be running for
messaging to work. Start it with `thrum daemon start`.

**Identity conflicts in multi-worktree setups**

Set `THRUM_NAME` environment variable to give each worktree a unique agent name.
See [Identity System](identity.md) for details.
