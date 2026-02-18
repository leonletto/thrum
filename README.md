# Thrum

**Git-backed agent coordination.**

[![License](https://img.shields.io/github/license/leonletto/thrum)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/leonletto/thrum?v=2)](https://goreportcard.com/report/github.com/leonletto/thrum)
[![CI](https://github.com/leonletto/thrum/actions/workflows/ci.yml/badge.svg)](https://github.com/leonletto/thrum/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/leonletto/thrum)](https://github.com/leonletto/thrum/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/leonletto/thrum)](go.mod)

> **Persistent messaging for AI agents.**
> Across sessions. Across worktrees. Across machines.

Messages are stored in append-only JSONL logs on a dedicated Git orphan branch, synced automatically — no branch switching, no merge conflicts, no external services required.

## Quick Start

```bash
# Install thrum
curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | sh

# Initialize in your project
cd your-project
thrum init
thrum daemon start

# Register and start communicating
thrum quickstart --name myagent --role planner --module auth
thrum send "Starting work on auth module"
thrum inbox
```

**Note:** Thrum is a CLI tool you install once and use across repositories. You don't need to clone this repo into your project.

## How It Works

```
┌──────────────────────────────────────────────────────────┐
│                        Thrum                             │
├──────────────────────────────────────────────────────────┤
│  CLI (thrum)       │  Daemon           │  Web UI (React) │
│  - send/inbox      │  - Unix socket    │  - Embedded SPA  │
│  - agent/session   │  - WebSocket      │  - Live feed     │
│  - reply           │  - Git sync (60s) │  - Inbox view    │
│  - coordination    │  - Heartbeat      │  - Agent list    │
├──────────────────────────────────────────────────────────┤
│  MCP Server (thrum mcp serve)                            │
│  - stdio transport for native Claude/agent integration   │
│  - Tools: send, check, wait, list, broadcast             │
├──────────────────────────────────────────────────────────┤
│  Storage                                                 │
│  - Append-only JSONL (sharded per agent)                 │
│  - SQLite projection for fast queries                    │
│  - Git orphan branch (a-sync) for conflict-free sync     │
│  - ULID event IDs (globally unique, time-sortable)       │
└──────────────────────────────────────────────────────────┘
```

- **Offline-first.** Works without any network. Git push/pull syncs when ready.
- **Zero-conflict.** Messages live on a dedicated orphan branch — no merge conflicts with your code.
- **Single binary.** CLI, daemon, web UI, and MCP server all ship in one `thrum` binary.
- **Agent-native.** JSON output (`--json`), MCP server integration, human-readable agent names.

## Features

- **Messaging** — Send, reply, @mentions, priority levels
- **Agent Groups** — Create groups for targeted messaging, built-in `@everyone` group
- **Agent Coordination** — Register agents, track sessions, set work context, heartbeats
- **File Coordination** — `thrum who-has <file>` to see which agents are editing what
- **Web UI** — Real-time dashboard with live feed, inbox, agent list (embedded in binary)
- **MCP Server** — `thrum mcp serve` for native integration with Claude Code and other MCP clients
- **Subscriptions** — Subscribe to events, wait for notifications
- **Git Sync** — Automatic 60-second sync via the daemon, or manual `thrum sync`
- **Multi-Worktree** — Each git worktree gets its own agent identity via `.thrum/redirect`

**v0.4.4 highlights:** `thrum init --stealth` for zero tracked-file footprint,
local-only by default, `--everyone`/`--limit` flag aliases, message-listener
agent in plugin, `--broadcast` deprecation in favor of `--to @everyone`.

## Installation

### Install Script (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | sh
```

Downloads the prebuilt binary for your platform with SHA-256 checksum verification. Falls back to `go install` or building from source if no release is available.

### Go Install

```bash
go install github.com/leonletto/thrum/cmd/thrum@latest
```

### Homebrew

```bash
brew install leonletto/tap/thrum
```

### From Source

```bash
git clone https://github.com/leonletto/thrum.git
cd thrum
make install    # Builds UI + Go binary → ~/.local/bin/thrum
```

## Essential Commands

| Command | Description |
| --- | --- |
| `thrum init` | Initialize Thrum in a repository |
| `thrum daemon start` | Start the background daemon |
| `thrum quickstart --name NAME --role ROLE` | Register agent and start session |
| `thrum send "message" --to @name` | Send a message to an agent |
| `thrum inbox` | View messages (read/unread indicators) |
| `thrum reply MSG_ID "response"` | Reply to a message |
| `thrum who-has FILE` | Check which agents are editing a file |
| `thrum overview` | Combined status, team, inbox, and sync view |
| `thrum mcp serve` | Start MCP server for AI agent integration |
| `thrum setup claude-md` | Preview CLAUDE.md agent instructions |
| `thrum setup claude-md --apply` | Append Thrum section to CLAUDE.md |
| `thrum setup claude-md --apply --force` | Overwrite existing Thrum section |

## MCP Server Integration

Thrum includes an MCP server for native integration with Claude Code and other MCP-compatible tools:

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

This gives agents direct access to 11 MCP tools: 5 core messaging tools (`send_message`, `check_messages`, `wait_for_message`, `list_agents`, `broadcast_message` _(deprecated)_) and 6 group management tools (`create_group`, `delete_group`, `add_group_member`, `remove_group_member`, `list_groups`, `get_group`).

## Agent Naming

Agents support human-readable names for easy coordination:

```bash
thrum quickstart --name furiosa --role implementer --module auth
thrum send "Need review on auth changes" --to @furiosa
thrum ping furiosa    # Check if agent is online
```

Names are lowercase alphanumeric with underscores (`[a-z0-9_]+`). Each agent gets a persistent identity in `.thrum/identities/{name}.json`.

## Documentation

Full documentation is available at **[leonletto.github.io/thrum](https://leonletto.github.io/thrum)**.

- [Overview](https://leonletto.github.io/thrum/docs/overview) | [Quick Start](https://leonletto.github.io/thrum/docs/quickstart) | [CLI Reference](https://leonletto.github.io/thrum/docs/cli) | [Architecture](https://leonletto.github.io/thrum/docs/architecture)
- [Messaging](https://leonletto.github.io/thrum/docs/messaging) | [Agent Coordination](https://leonletto.github.io/thrum/docs/agent-coordination) | [MCP Server](https://leonletto.github.io/thrum/docs/mcp-server) | [Sync](https://leonletto.github.io/thrum/docs/sync)
- [Claude Code Agent Integration](docs/claude-agent-integration.md) — Agent definitions for multi-agent coordination

## License

[MIT](LICENSE)
