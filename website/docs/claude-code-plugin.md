---
title: "Claude Code Plugin"
description:
  "Install and use the Thrum plugin for Claude Code — slash commands, automatic
  context injection, and multi-agent coordination without manual configuration"
category: "integrations"
order: 3
tags:
  ["claude-code", "plugin", "installation", "slash-commands", "mcp", "hooks"]
last_updated: "2026-04-24"
---

## Claude Code Plugin

> See also: [Quickstart Guide](quickstart.md) for basic Thrum setup,
> [Cursor Plugin](cursor-plugin.md) for Cursor integration,
> [Codex Plugin](codex-plugin.md) for Codex skill-based integration,
> [Agent Configurations](agent-configs.md) for manual agent definitions,
> [MCP Server](mcp-server.md) for the native MCP transport,
> [Multi-Agent Support](multi-agent.md) for coordination patterns.

## Overview

The Thrum plugin for Claude Code gives agents native access to Thrum messaging
through slash commands, automatic context injection via hooks, and progressive
disclosure resource docs. It replaces the manual agent definition approach
(copying `.md` files into `.claude/agents/`) with a single plugin install.

**What you get:**

- **11 slash commands** — `/thrum:send`, `/thrum:inbox`, `/thrum:quickstart`,
  `/thrum:load-context`, `/thrum:restart`, and more
- **Automatic context** — SessionStart, PreCompact, and PostCompact hooks keep
  your agent oriented across sessions and compaction
- **8 resource docs** — Progressive disclosure for messaging patterns, identity,
  worktrees, tmux sessions, and anti-patterns
- **Background listener** — Sub-agent template for async message monitoring

## Prerequisites

Before installing the plugin, you need Thrum installed and initialized:

```bash
# Install thrum
curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | sh
# Or: brew install leonletto/tap/thrum  (Homebrew 6.0+: run `brew trust leonletto/tap` first)
# Or: git clone https://github.com/leonletto/thrum.git && cd thrum && make install

# Initialize in your repository (v0.4.5+: init does full setup)
cd /path/to/your/repo
thrum init
```

`thrum init` handles the daemon-side setup: prompts for runtime detection,
starts the daemon, registers your agent, and starts a session.

If you install the Thrum plugin (next section), you do **not** need to run
`thrum setup claude-md` — the plugin's SessionStart hook injects messaging
instructions into the agent at session start, slash commands cover the common
operations, and skills disclose deeper docs on demand. The
`thrum setup claude-md --apply` CLI is the alternative for environments without
the plugin (other runtimes, or Claude Code without the plugin installed); see
[CLI Reference → thrum setup claude-md](cli.md#thrum-setup-claude-md). Use one
approach or the other, not both.

Verify the daemon is running:

```bash
thrum daemon status
```

## Installation

### From GitHub (self-hosted marketplace)

The plugin is distributed as a self-hosted marketplace. First add the
marketplace, then install the plugin:

```bash
# Add the Thrum marketplace
claude plugin marketplace add https://github.com/leonletto/thrum

# Install the plugin
claude plugin install thrum
```

### From local clone

If you've cloned the Thrum repository locally:

```bash
# Add as a local marketplace
claude plugin marketplace add /path/to/thrum

# Install the plugin
claude plugin install thrum
```

### Verify installation

Start a new Claude Code session. The SessionStart hook will prompt you to run
`/thrum:prime` to load your session context. Running it displays your agent
identity, team roster, unread messages, and daemon health.

## Slash Commands

All commands live under the `/thrum:` namespace.

| Command                 | Purpose                                                      |
| ----------------------- | ------------------------------------------------------------ |
| `/thrum:quickstart`     | Register agent and start session (interactive or with flags) |
| `/thrum:send`           | Send direct or broadcast messages                            |
| `/thrum:inbox`          | Check message inbox (all or unread only)                     |
| `/thrum:reply`          | Reply to a message (inherits original audience)              |
| `/thrum:wait`           | Block until a message arrives (background listener use)      |
| `/thrum:team`           | Show active team members with roles and intents              |
| `/thrum:overview`       | Combined status + team + inbox view                          |
| `/thrum:prime`          | Load full session context (identity, team, inbox, git)       |
| `/thrum:load-context`   | Restore saved agent work context after compaction            |
| `/thrum:update-project` | Guided workflow to update durable project state              |
| `/thrum:restart`        | Save conversation snapshot and prepare for session restart   |

### Common workflows

**Start a session:**

```text
/thrum:quickstart
```

Prompts for role, module, and intent — or pass flags directly:

```bash
thrum quickstart --name implementer_auth --role implementer --module auth --intent "Building login flow"
```

**Send a message:**

```text
/thrum:send
```

Guided prompt for recipient and message. Direct usage:

```bash
thrum send "Starting auth work" --to @coordinator
thrum send "Need review" --to @reviewers
thrum send "Heads up: breaking change" --to @everyone
```

**Check inbox and reply:**

```text
/thrum:inbox
/thrum:reply
```

## Hooks

The plugin configures three hooks that run automatically:

### SessionStart

Prompts the agent to run `/thrum:prime` when a Claude Code session begins.
Running prime loads:

- Agent identity (name, role, module)
- Active team members and their intents
- Unread messages
- Git branch and status
- Daemon health
- Restart snapshots (if any exist from a previous session)

Since v0.9.2 the hook also injects `thrum prime` output via the hook's
`additionalContext` field, so the full briefing reaches the model even when the
pane-side banner is truncated or scrolled off. Restart-snapshot content is
hoisted to the top of `additionalContext` and framed as a directive rather than
passive prose, so the model treats it as an actionable instruction on boot.

If Thrum isn't initialized, shows a friendly setup message instead of failing.

#### Pane-side identity banner

For runtimes whose plugin ships the SessionStart hook (Claude Code and Cursor),
the daemon also types a short identity banner directly into the tmux pane before
the runtime takes over the screen. It looks like this:

```text
Agent:     impl_auth
Role:      implementer
Worktree:  /Users/you/dev/myproject/auth-feature
Branch:    feature/auth
MUST READ: see auto-loaded briefing above and follow it before anything else
```

Two reasons for the redundancy. First, a human watching the pane gets
orientation regardless of how the runtime renders its UI. Second, the MUST-READ
line routes through the same channel as user prompts, which the model treats
more imperatively than `additionalContext` (which sometimes gets
read-and-rationalized-away). Two surfaces, the same directive.

Whether the banner fires is keyed off a `HasSessionStartHook` field on the
runtime preset — Claude Code and Cursor are the only `true` entries today. For
runtimes without a SessionStart hook (codex, opencode, kiro-cli, gemini, shell),
the launch flow falls back to the historical post-launch `/thrum:prime` send.

### PreCompact

Saves session context before context compaction. This preserves agent state
(decisions, next steps, work-in-progress) so it can be restored after
compaction.

### PostCompact (v0.7.0)

Fires after context compaction. Tells the agent to run `thrum prime` so it gets
its bearings back. In multi-agent mode, it also checks the listener heartbeat
and respawns if stale. PreCompact saves state, PostCompact recovers it.

## Resource Docs

The plugin includes 8 resource documents that Claude loads on demand for deeper
guidance:

| Resource              | When it's used                                            |
| --------------------- | --------------------------------------------------------- |
| `MESSAGING.md`        | Message lifecycle, addressing patterns                    |
| `IDENTITY.md`         | Agent naming, registration, multi-worktree identity       |
| `WORKTREES.md`        | Cross-worktree coordination, shared daemon, file tracking |
| `LISTENER_PATTERN.md` | Background message listener sub-agent template            |
| `BOUNDARIES.md`       | When to use Thrum vs TaskList/SendMessage                 |
| `ANTI_PATTERNS.md`    | Common mistakes and how to avoid them                     |
| `CLI_REFERENCE.md`    | Complete command syntax for all `thrum` commands          |
| `TMUX_SESSIONS.md`    | Tmux-managed session patterns and lifecycle               |

## MCP Server Integration

For native tool-call integration (no shell-outs), configure the Thrum MCP server
in your project's `.claude/settings.json`:

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

You get 4 core messaging tools (`send_message`, `check_messages`,
`wait_for_message`, `list_agents`) plus `broadcast_message` (deprecated — use
`send_message(to="@everyone")` instead). See [MCP Server](mcp-server.md) for the
full API.

**Plugin vs MCP:** The plugin's slash commands use the CLI (`Bash(thrum:*)`).
The MCP server provides native tool calls. Both work — the plugin is simpler to
set up, the MCP server is more efficient for high-frequency messaging. You can
use both together.

## Background Listener

When `thrum prime` runs in a Claude Code session and detects an active identity,
it outputs a ready-to-use listener spawn instruction with the correct repo path.
Copy and run it to start background message monitoring:

```text
Task(
  subagent_type="message-listener",
  model="haiku",
  run_in_background=true,
  prompt="Listen for Thrum messages.
    WAIT_CMD=cd /path/to/repo && thrum wait --timeout 8m --after -15s --json"
)
```

`--after -15s` means include messages sent up to 15 seconds ago (negative = "N
ago"; prevents stale replay on restart). The listener runs up to 30 cycles (~4
hours of coverage), blocks on `thrum wait` (no polling), returns when messages
arrive, and costs ~$0.00003/cycle on Haiku (~65% fewer tokens than the old
pattern). No manual re-arming needed — set up a cron watchdog to auto-respawn
the listener every 30 min if it stops:

```text
CronCreate(cron="*/30 * * * *",
  prompt="If there is no background message listener running, spawn one now:
    Task(subagent_type='message-listener', model='haiku', run_in_background=true,
      prompt='Listen for Thrum messages. WAIT_CMD=cd /path/to/repo && thrum wait --timeout 8m --after -15s --json')")
```

See the `LISTENER_PATTERN.md` resource for the full template.

## Plugin vs Manual Agent Definitions

| Feature        | Plugin                                      | Manual (toolkit/agents/)              |
| -------------- | ------------------------------------------- | ------------------------------------- |
| Installation   | `claude plugin marketplace add` + `install` | Copy `.md` files to `.claude/agents/` |
| Updates        | In-place via `/plugin marketplace update`   | Manual file replacement               |
| Slash commands | 11 commands included                        | None                                  |
| Hooks          | SessionStart + PreCompact + PostCompact     | Manual hook configuration             |
| Resource docs  | 8 progressive disclosure docs               | Single monolithic agent file          |
| Maintenance    | Versioned (v0.10.6)                         | Ad-hoc                                |

The manual agent definitions (`thrum-agent.md`, `message-listener.md`) still
work and are available in `toolkit/agents/` for environments that don't support
plugins.

## Troubleshooting

### "Thrum not initialized" on session start

Run `thrum init` in your repository root (it starts the daemon automatically).

### No context injected after plugin install

Verify the plugin is loaded: check that `/thrum:prime` is available as a slash
command. If not, reinstall the plugin.

### Messages not arriving

Check daemon status with `thrum daemon status`. The daemon must be running for
messaging to work. Start it with `thrum daemon start`.

### Identity conflicts in multi-worktree setups

Set `THRUM_NAME` environment variable to give each worktree a unique agent name.
See [Identity System](identity.md) for details.

## Next Steps

- [MCP Server](mcp-server.md) — the MCP server the plugin configures, including
  the full tool reference
- [Cursor Plugin](cursor-plugin.md) — the equivalent plugin for Cursor users
- [Codex Plugin](codex-plugin.md) — the equivalent skill bundle for Codex users
- [Agent Coordination](agent-coordination.md) — practical multi-agent workflows
  using the slash commands and hooks this plugin provides
- [Identity System](identity.md) — agent naming, `THRUM_NAME`, and multi-agent
  worktree setup
