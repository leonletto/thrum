
## Cursor Plugin

> See also: [Quickstart Guide](quickstart.md) for basic Thrum setup,
> [Claude Code Plugin](claude-code-plugin.md) for the Claude Code integration,
> [Codex Plugin](codex-plugin.md) for the Codex skill bundle,
> [MCP Server](mcp-server.md) for the native MCP transport.

## Overview

The Thrum plugin for Cursor deploys coordination infrastructure into your
project's `.cursor/` directory. It gives Cursor agents native access to Thrum
messaging through slash commands, lifecycle hooks, safety rules, and MCP
configuration — without manual setup.

**What you get:**

- **11 slash commands** — `prime`, `send`, `inbox`, `quickstart`, `restart`, and
  more
- **5 hooks** — SessionStart, beforeShellExecution, Stop, PreCompact, and
  PostCompact keep your agent oriented across sessions and compaction
- **2 rules** — `.mdc` files for sync worktree safety and session lifecycle
- **4 skills** — Thrum coordination, orchestration, project setup, and role
  configuration
- **1 agent** — Background message listener sub-agent template
- **MCP config** — Thrum MCP server wired up automatically

## Prerequisites

Before installing the plugin, you need Thrum installed and initialized:

```bash
# Install thrum
curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | sh
# Or: brew install leonletto/tap/thrum
# Or: git clone https://github.com/leonletto/thrum.git && cd thrum && make install

# Initialize in your repository
cd /path/to/your/repo
thrum init
```

`thrum init` handles the full setup: prompts for runtime detection, generates
coordination instructions, starts the daemon, registers your agent, and starts a
session.

Verify the daemon is running:

```bash
thrum daemon status
```

## Installation

The Cursor plugin is installed via a local install script. It deploys files
directly into your project's `.cursor/` directory.

### From local clone

```bash
# Clone the Thrum repository (if you haven't already)
git clone https://github.com/leonletto/thrum.git
cd thrum

# Install into the current repo (or specify --target)
cursor-plugin/local-install.sh
cursor-plugin/local-install.sh --target /path/to/project
```

The installer:

1. Creates `.cursor/rules/`, `.cursor/skills/`, `.cursor/commands/`,
   `.cursor/agents/`
2. Copies `.mdc` rule files
3. Copies skills, commands, and agent definitions (if synced)
4. Writes `hooks.json` with resolved absolute paths to hook scripts
5. Writes `mcp.json` with the Thrum MCP server configuration
6. Adds `.cursor/` to `.gitignore`

### Verify installation

Start a new Cursor session. The SessionStart hook will prompt you to run
`thrum prime` to load your session context. Running it displays your agent
identity, team roster, unread messages, and daemon health.

### Updating

After upstream changes to the Thrum repository, re-sync and re-install:

```bash
scripts/sync-skills.sh        # Sync skills/commands from claude-plugin source
cursor-plugin/local-install.sh # Re-deploy to .cursor/
```

## Slash Commands

All commands are available as `.md` files in `.cursor/commands/`.

| Command          | Purpose                                             |
| ---------------- | --------------------------------------------------- |
| `quickstart`     | Register agent and start session                    |
| `send`           | Send direct or broadcast messages                   |
| `inbox`          | Check message inbox (all or unread only)            |
| `reply`          | Reply to a message (inherits original audience)     |
| `wait`           | Block until a message arrives (background listener) |
| `team`           | Show active team members with roles and intents     |
| `overview`       | Combined status + team + inbox view                 |
| `prime`          | Load full session context (identity, team, inbox)   |
| `update-project` | Guided workflow to update durable project state     |
| `restart`        | Save conversation snapshot and prepare for restart  |
| `load-context`   | Restore saved agent work context after compaction   |

## Hooks

The plugin configures five hooks in `.cursor/hooks.json`:

### SessionStart

Prompts the agent to run `thrum prime` when a Cursor session begins. Running
prime loads agent identity, team roster, unread messages, git branch, and daemon
health.

### beforeShellExecution

Runs `block-sync-worktree-cd.sh` before any shell command. Blocks commands that
would `cd` into Thrum's internal `.git/thrum-sync/a-sync` worktree — this
prevents accidental writes to the sync branch.

### Stop

Runs `stop-check-messages.sh` when the agent stops. Checks for unread messages
and reminds the agent to process them before ending. Has a 15-second timeout.

### PreCompact

Runs `pre-compact-save-context.sh` before context compaction. Saves session
state (decisions, next steps, work-in-progress) so it can be restored afterward.

### PostCompact

Runs `post-compact-recover.sh` after context compaction. Tells the agent to run
`thrum prime` to recover its bearings. In multi-agent mode, it also checks the
listener heartbeat and respawns if stale.

## Rules

Two `.mdc` rule files are deployed to `.cursor/rules/`:

| Rule                | Purpose                                                                                    |
| ------------------- | ------------------------------------------------------------------------------------------ |
| `thrum-safety.mdc`  | Blocks writes to `.git/thrum-sync/a-sync` (read-only sync worktree)                        |
| `thrum-session.mdc` | Session lifecycle: run `thrum prime` on start, check inbox, save context before compaction |

Both rules have `alwaysApply: true` — they're active in every conversation.

## Skills

Four skills are deployed to `.cursor/skills/`:

| Skill             | Purpose                                                                                                                    |
| ----------------- | -------------------------------------------------------------------------------------------------------------------------- |
| `thrum`           | Core coordination skill with 8 resource docs for messaging patterns, identity, worktrees, anti-patterns, and CLI reference |
| `orchestrate`     | Execute plans epic-by-epic with review gates, agent lifecycle, and merge reports                                           |
| `project-setup`   | Convert plans into beads epics, tasks, implementation prompts, and worktrees                                               |
| `configure-roles` | Detect environment and generate role-based preamble templates                                                              |

The `thrum` skill includes the same 8 resource documents as the Claude Code
plugin:

| Resource              | When it's used                                            |
| --------------------- | --------------------------------------------------------- |
| `MESSAGING.md`        | Message lifecycle, addressing patterns                    |
| `IDENTITY.md`         | Agent naming, registration, multi-worktree identity       |
| `WORKTREES.md`        | Cross-worktree coordination, shared daemon, file tracking |
| `LISTENER_PATTERN.md` | Background message listener sub-agent template            |
| `BOUNDARIES.md`       | When to use Thrum vs built-in task tools                  |
| `ANTI_PATTERNS.md`    | Common mistakes and how to avoid them                     |
| `CLI_REFERENCE.md`    | Complete command syntax for all `thrum` commands          |
| `TMUX_SESSIONS.md`    | Tmux-managed session patterns and lifecycle               |

## MCP Server Integration

The installer writes `.cursor/mcp.json` with the Thrum MCP server configuration:

```json
{
  "mcpServers": {
    "thrum": {
      "type": "command",
      "command": "thrum",
      "args": ["mcp", "serve"]
    }
  }
}
```

This provides 4 core messaging tools (`send_message`, `check_messages`,
`wait_for_message`, `list_agents`) plus `broadcast_message`. See
[MCP Server](mcp-server.md) for the full API.

## Background Listener

The plugin includes a `message-listener.md` agent definition in
`.cursor/agents/`. This is a lightweight sub-agent template that blocks on
`thrum wait` for incoming messages — useful for async coordination in
multi-agent setups.

## Cursor Plugin vs Claude Code Plugin

| Feature        | Cursor Plugin                           | Claude Code Plugin                          |
| -------------- | --------------------------------------- | ------------------------------------------- |
| Packaging      | Local install script                    | Marketplace plugin                          |
| Installation   | `local-install.sh`                      | `claude plugin marketplace add` + `install` |
| Updates        | `sync-skills.sh` + re-install           | Re-install from source                      |
| Hooks          | 5 (session, shell guard, stop, compact) | 5 (tool guard, session, stop, compact)      |
| Rules          | 2 `.mdc` files (always-on)              | N/A (uses hooks instead)                    |
| Skills         | 4 skills + 8 resource docs              | Via plugin progressive disclosure           |
| MCP            | Auto-configured `mcp.json`              | Manual `.claude/settings.json`              |
| Slash commands | 11 command files                        | 11 slash commands                           |

Both plugins share the same underlying skill content — `scripts/sync-skills.sh`
keeps them in sync from the Claude plugin source.

## Troubleshooting

### "Thrum not initialized" on session start

Run `thrum init` in your repository root (it starts the daemon automatically).

### No context injected after install

Verify the hook is present: check that `.cursor/hooks.json` exists and contains
the sessionStart entry. Re-run `local-install.sh` if missing.

### Messages not arriving

Check daemon status with `thrum daemon status`. The daemon must be running for
messaging to work. Start it with `thrum daemon start`.

### Skills or commands missing

Run `scripts/sync-skills.sh` from the Thrum repository root to sync the latest
skills and commands from the Claude plugin source, then re-run
`local-install.sh`.

## Next Steps

- [MCP Server](mcp-server.md) — the MCP server the plugin configures
- [Claude Code Plugin](claude-code-plugin.md) — the equivalent plugin for Claude
  Code users
- [Codex Plugin](codex-plugin.md) — the equivalent skill bundle for Codex users
- [Multi-Agent Support](multi-agent.md) — coordination patterns across agents
- [Identity System](identity.md) — agent naming, `THRUM_NAME`, and multi-agent
  worktree setup
