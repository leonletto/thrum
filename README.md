# Thrum

**Persistent messaging for AI agents.**

[![License](https://img.shields.io/github/license/leonletto/thrum)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/leonletto/thrum?v=2)](https://goreportcard.com/report/github.com/leonletto/thrum)
[![CI](https://github.com/leonletto/thrum/actions/workflows/ci.yml/badge.svg)](https://github.com/leonletto/thrum/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/leonletto/thrum)](https://github.com/leonletto/thrum/releases)
[![Go Version](https://img.shields.io/github/go-mod/go-version/leonletto/thrum)](go.mod)

Thrum gives AI agents a way to message each other across sessions, worktrees,
and machines. You direct the work. The agents coordinate through Thrum. Messages
persist through context compaction, session restarts, and machine changes —
nothing gets lost.

**v0.9.2 highlights:**

- **Role configuration system (thrum-z2et)** — `/thrum:configure-roles` now
  persists answers under `role_config` in `.thrum/config.json`, and
  `thrum roles refresh` regenerates `.thrum/role_templates/<role>.md` from the
  saved answers + the shipped templates embedded in the binary. `thrum prime`
  surfaces drift hints when shipped templates change (`migration`,
  `schema-bump`, `body-diff`). User overlays at `.thrum/context/<agent>.md`
  compose into the rendered preamble.
- **SessionStart identity banner + auto-load directive** — Claude Code sessions
  launched via `thrum tmux create` and restarted via `thrum tmux restart` show a
  pane-side identity banner and a size-aware MUST-READ directive. The plugin
  SessionStart hook injects `thrum prime` output via `additionalContext` so the
  briefing reaches the model even when the pane is small.
- **tmux session scoping (thrum-zuz5)** — `thrum tmux status` /
  `thrum tmux connect` now scope to the current daemon via a `@thrum-thrum-dir`
  tag, no longer leaking sessions across worktrees and projects. Migration:
  pre-0.9.2 sessions need to be recreated to appear.
- **tmux pty leak fix (thrum-x6e8.5)** — tmux-exec moved from `respawn-pane` to
  a persistent-session pool, eliminating fd-leak crashes on long-running
  daemons.

## Quick Start

```bash
# Install
curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | sh

# Initialize (starts the daemon automatically)
cd your-project
thrum init

# Register and send your first message
thrum quickstart --name myagent --role planner --module auth
thrum send "Starting work on auth module" --to @implementer
thrum inbox
```

## How It Works

Thrum is a single binary: CLI, daemon, web UI, and optional MCP server.

![Thrum architecture](website/img/architecture.svg)

- **CLI-first.** Every agent that can run shell commands can use Thrum. No SDK,
  no framework, no protocol to implement.
- **Offline-first.** Everything works locally. Git push/pull syncs when ready.
- **Zero-conflict.** Messages live on a dedicated orphan branch — no merge
  conflicts with your code.
- **Inspectable.** Messages are JSONL files. State is a SQLite database. Sync is
  plain Git. If something goes wrong, you look at files.

## What You Can Do

- **Send and receive messages** — `thrum send`, `thrum inbox`, `thrum reply`
- **See what everyone is working on** — `thrum team`, `thrum who-has`
- **Coordinate agents across worktrees** — each worktree gets its own identity
- **Create groups** — `@everyone`, `@reviewers`, or any custom group
- **Subscribe to events** — get push notifications for scopes and mentions
- **Monitor in real time** — embedded web UI with live feed, threaded inbox,
  agent list
- **Get messages on your phone** — Telegram bridge with bidirectional threading
- **Sync across machines** — automatic Git sync, or Tailscale for real-time
  peer-to-peer

## Installation

### Install Script (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | sh
```

Downloads the prebuilt binary for your platform with SHA-256 checksum
verification.

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

## Daily Commands

You only need about 8 commands for daily use:

| Command                                    | What it does                     |
| ------------------------------------------ | -------------------------------- |
| `thrum quickstart --name NAME --role ROLE` | Register agent and start session |
| `thrum send "message" --to @name`          | Send a message                   |
| `thrum inbox`                              | Check your messages              |
| `thrum reply MSG_ID "response"`            | Reply to a message               |
| `thrum team`                               | See what everyone is working on  |
| `thrum who-has FILE`                       | Check who's editing a file       |
| `thrum overview`                           | Status, team, inbox in one view  |
| `thrum status`                             | Your current state               |

Everything else — agent lifecycle, sessions, subscriptions, groups, context
management — is infrastructure that agents use programmatically. See the
[CLI Reference](https://leonletto.github.io/thrum/docs/cli.html) for the full
list.

## Agent Setup

### Install the Thrum Skill (All Agents)

```bash
thrum init --skills
```

Auto-detects your agent (Claude Code, Cursor, Codex, Gemini, Augment, Amp) and
installs the thrum skill to the right location. If multiple agents are detected,
you'll be prompted to choose. Works with any agent that supports the `SKILL.md`
format.

### Claude Code Plugin (Full Integration)

For Claude Code users who want the complete experience — slash commands,
automatic context injection, hooks, and startup scripts:

```bash
claude plugin marketplace add https://github.com/leonletto/thrum
claude plugin install thrum
```

See
[Claude Code Plugin](https://leonletto.github.io/thrum/docs/claude-code-plugin.html).
If the plugin is already installed, `thrum init --skills` will detect it and
skip the install.

### Any Agent via CLI

Any agent that can run shell commands works with Thrum. No plugin or skill
required — just call `thrum` from the command line.

## Documentation

Full documentation:
**[leonletto.github.io/thrum](https://leonletto.github.io/thrum)**

- [Overview](https://leonletto.github.io/thrum/docs/overview.html) |
  [Quickstart](https://leonletto.github.io/thrum/docs/quickstart.html) |
  [CLI Reference](https://leonletto.github.io/thrum/docs/cli.html) |
  [Architecture](https://leonletto.github.io/thrum/docs/architecture.html)
- [Messaging](https://leonletto.github.io/thrum/docs/messaging.html) |
  [Agent Coordination](https://leonletto.github.io/thrum/docs/agent-coordination.html)
  | [Multi-Agent](https://leonletto.github.io/thrum/docs/multi-agent.html) |
  [Sync](https://leonletto.github.io/thrum/docs/sync.html)
- [Telegram Bridge](https://leonletto.github.io/thrum/docs/telegram-bridge.html)
  | [Tailscale Sync](https://leonletto.github.io/thrum/docs/tailscale-sync.html)
  | [Web UI](https://leonletto.github.io/thrum/docs/web-ui.html)

## License

[MIT](LICENSE)
