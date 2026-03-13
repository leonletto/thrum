---
title: "Quickstart Guide"
description:
  "Get up and running with Thrum in 5 minutes - initialization, daemon setup,
  agent registration, and first message"
category: "quickstart"
order: 1
tags: ["quickstart", "getting-started", "installation", "setup", "tutorial"]
last_updated: "2026-03-13"
---

## Thrum Quickstart Guide

Install Thrum, register an agent, send your first message. Five minutes.

## Installation

### Install Script (recommended)

Downloads the latest release binary with SHA-256 checksum verification. Falls
back to `go install` or building from source if no release is available for your
platform.

```bash
curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | sh
```

### Homebrew

```bash
brew install leonletto/tap/thrum
```

### From Source

```bash
git clone https://github.com/leonletto/thrum.git
cd thrum
make install  # Builds UI + Go binary, installs to ~/.local/bin
```

## Fast Path

One command to init, register, start a session, and set your intent:

```bash
cd your-project
thrum init
thrum quickstart --name myagent --role implementer --module auth --intent "Working on auth"
```

That's it. You're registered and ready to send messages. The sections below walk
through each step individually if you want to understand what's happening.

Quickstart also auto-creates an empty context file for session state
persistence. See [Agent Context Management](context.md) for details.

## Step-by-Step Walkthrough

### 1. Initialize Repository

Navigate to your project and initialize Thrum:

```bash
cd your-project
thrum init
```

This creates:

- `.thrum/` directory (gitignored entirely)
- `.git/thrum-sync/a-sync/` sync worktree on the `a-sync` orphan branch (for
  JSONL event logs)
- `.thrum/identities/` for agent identity files
- `.thrum/var/` for daemon runtime files
- `a-sync` orphan branch for message synchronization

`thrum init` also starts the daemon automatically (since v0.4.5). You do not
need a separate `thrum daemon start` step for first-time setup.

If you are upgrading an existing repo that has JSONL files tracked on `main`,
run `thrum migrate` instead.

### 2. Install the Plugin (Claude Code or Codex)

If you use Claude Code or Codex, install the plugin right after initialization
so agents get automatic context injection and slash commands.

**Claude Code:**

```bash
claude plugin marketplace add https://github.com/leonletto/thrum
claude plugin install thrum
```

See [Claude Code Plugin](claude-code-plugin.md) for details on what the plugin
provides (slash commands, hooks, resource docs).

**Codex:**

```bash
git clone https://github.com/leonletto/thrum.git
cd thrum
./codex-plugin/scripts/install-skills.sh
```

See [Codex Plugin](codex-plugin.md) for the full skill bundle reference.

### 3. Generate CLAUDE.md Coordination Instructions

For Claude Code and other AI agents, generate Thrum coordination instructions:

```bash
thrum setup claude-md --apply
```

This appends agent coordination instructions to your CLAUDE.md file (creates it
if missing). Agents will automatically use Thrum for coordination when working
in the repository.

### 4. Register Your Agent and Start a Session

The fastest way is the quickstart command, which registers, starts a session,
and sets your intent in one step:

```bash
thrum quickstart --name myagent --role implementer --module auth --intent "Working on auth"
```

Or register manually with individual commands:

```bash
thrum agent register --name myagent --role=implementer --module=auth
thrum session start
```

Agent names must be lowercase alphanumeric with underscores (`[a-z0-9_]+`).
Reserved names: `daemon`, `system`, `thrum`, `all`, `broadcast`.

You can also set identity via environment variables:

```bash
export THRUM_NAME=myagent     # Agent name (highest priority)
export THRUM_ROLE=implementer
export THRUM_MODULE=auth
thrum agent register
```

Priority: `THRUM_NAME` env var > `--name` flag > solo-agent auto-select.

### 5. Send Your First Message

```bash
thrum send "Started working on user authentication" \
  --scope module:auth \
  --ref issue:beads-123
```

### 6. Check Your Inbox

```bash
thrum inbox
thrum sent
thrum message read --all     # Mark all messages as read
```

View messages from other agents and humans working on the project, and inspect
the messages you sent with their recipient and read state.

## Common Commands

### Check Status

```bash
thrum status
```

Shows:

- Your agent identity
- Active session
- Inbox counts
- Sync status
- Daemon health

### Subscribe to Notifications

```bash
# Subscribe to your module
thrum subscribe --scope module:auth

# Subscribe to mentions
thrum subscribe --mention @implementer

# List active subscriptions
thrum subscriptions
```

### Sync Control

```bash
# Check sync status
thrum sync status

# Force immediate sync
thrum sync force
```

### Context Management

```bash
# Save context for session continuity
thrum context save --file continuation-notes.md

# View saved context
thrum context show

# Clear context
thrum context clear

# Share context across worktrees (manual sync)
thrum context sync
```

### Agent Management

```bash
# Delete an agent
thrum agent delete myagent

# Detect orphaned agents (preview)
thrum agent cleanup --dry-run

# Delete all orphaned agents
thrum agent cleanup --force
```

### End Session

```bash
thrum session end
```

### MCP Server (for LLM Agents)

Start an MCP server for native tool-based messaging (e.g., from Claude Code):

```bash
thrum mcp serve
thrum mcp serve --agent-id myagent  # Override agent identity
```

See [MCP Server](mcp-server.md) for configuration and the complete tools
reference (11 tools: 5 core messaging + 6 group management).

## Typical Workflow

### Morning: Start Work

```bash
# 1. Register and start session (or just start session if already registered)
#    (Use `thrum daemon start` explicitly if the daemon stopped for any reason)
thrum quickstart --name myagent --role implementer --module auth --intent "Working on auth"

# 2. Check inbox for updates
thrum inbox --unread         # does not mark messages as read

# 2b. Check sent items for delivery/read state
thrum sent --unread

# 2c. Mark all messages as read when done reviewing
thrum message read --all

# 3. Subscribe to your module
thrum subscribe --scope module:auth
```

### During Work: Send Updates

```bash
# Progress updates
thrum send "Implemented password hashing" \
  --scope module:auth \
  --ref issue:beads-123

# Request review
thrum send "Auth module ready for review" \
  --scope module:auth \
  --mention @reviewer
```

### Evening: End Work

```bash
# End session
thrum session end

# Check final status
thrum status
```

## Working Across Machines

> **Note:** `thrum init` sets `local_only: true` by default. To enable
> cross-machine sync, set `local_only: false` in `.thrum/config.json` or run
> `THRUM_LOCAL=false thrum daemon start`.

Thrum uses Git for synchronization. No cloud service, no opaque API — just
push and pull on the `a-sync` branch.

### On Machine A

```bash
# Make changes, send messages
thrum send "Completed feature X"

# Sync happens automatically every 60s
# Or force sync
thrum sync force
```

### On Machine B

```bash
# Pull latest (includes a-sync branch)
git fetch origin
git merge origin/main

# Daemon automatically syncs messages
# Or force sync
thrum sync force

# Check inbox
thrum inbox

# Check sent items
thrum sent
```

## Working with Multiple Worktrees

Feature worktrees share the main worktree's daemon and message store via a
redirect file. Use `thrum setup` to configure a feature worktree:

```bash
# Main worktree (already initialized — daemon running from thrum init)
cd ~/project

# Feature worktree -- set up redirect to main
cd ~/project-features/auth
thrum setup --main-repo ~/project
thrum session start
thrum send "Experimenting with auth approaches"
```

The `thrum setup --main-repo <path>` command creates a `.thrum/redirect` file
pointing to the main worktree's `.thrum/` directory. All worktrees then share
the same sync worktree, daemon, and message store. Messages sync across all
worktrees and machines through Git.

### Use the setup scripts for batch configuration

Two shell scripts automate redirect file creation for all your worktrees at
once:

```bash
# Set up Thrum redirects for all worktrees
./scripts/setup-worktree-thrum.sh

# Set up Beads redirects for all worktrees
./scripts/setup-worktree-beads.sh
```

Both scripts auto-detect worktrees via `git worktree list` and create the
appropriate redirect files. They skip worktrees that are already configured.

#### Set up a single worktree

```bash
# Thrum redirect for one worktree
./scripts/setup-worktree-thrum.sh ~/.workspaces/thrum/my-feature

# Beads redirect for one worktree
./scripts/setup-worktree-beads.sh ~/.workspaces/thrum/my-feature
```

#### What the scripts create

Each script creates a redirect file pointing to the main repository:

```text
# In the worktree:
.thrum/redirect    → /path/to/main/repo/.thrum
.beads/redirect    → /path/to/main/repo/.beads
```

This ensures all worktrees share the same daemon, message store, and issue
tracker. The scripts are idempotent — run them as many times as you need.

## Troubleshooting

### Daemon won't start

```bash
# Check if already running (shows repo path from JSON PID file)
thrum daemon status

# Stop and restart
thrum daemon stop
thrum daemon start

# Check PID file (JSON format: PID, RepoPath, StartedAt, SocketPath)
cat .thrum/var/thrum.pid
```

The daemon uses flock-based locking (`.thrum/var/thrum.lock`) for SIGKILL
resilience and pre-startup duplicate detection to prevent multiple daemons
serving the same repository.

### Messages not syncing

```bash
# Check sync status
thrum sync status

# Force sync
thrum sync force

# Check Git branches
git branch -a | grep a-sync
```

### Registration conflicts

```bash
# Another agent registered with same role+module
thrum agent register --force  # Override

# Or use different role/module
thrum agent register --role=implementer-2 --module=auth
```

## Key Concepts

### Messages

Persistent communication between agents, stored in Git-tracked JSONL. They're
just text — `cat` them, `grep` them, pipe them through `jq`.

### Sessions

Work periods tracked per agent. Messages require an active session.

### Scopes

Categorize messages by context (module:auth, file:src/auth.go, etc.)

### Subscriptions

Get push notifications for messages matching criteria.

### Sync

Background process (60s interval) that syncs messages via Git. Data lives on the
`a-sync` orphan branch, accessed through the sync worktree at
`.git/thrum-sync/a-sync/` with sparse checkout. No branch switching needed.

### Daemon

Background service handling RPC requests, sync operations, and serving the
embedded Web UI. WebSocket and SPA are served on the same port (default 9999).

### MCP Server

Stdio-based MCP server (`thrum mcp serve`) for native tool-based agent
messaging. Enables LLM agents to use Thrum via MCP tools instead of CLI
shell-outs.

## Tips

1. **Always start a session** before sending messages
2. **Subscribe to your module** to get relevant notifications
3. **Use scopes** to categorize messages
4. **Mention other agents** when you need their attention
5. **Check sync status** if messages aren't appearing
6. **Use `--json` flag** for scripting and automation
7. **Back up your data** regularly: `thrum backup`
8. **Enable automatic backups**: `thrum backup schedule 24h`

## Next Steps

- [Why Thrum Exists](philosophy.md) — understand the philosophy behind
  human-directed agent coordination before going deeper
- [CLI Reference](cli.md) — complete documentation for every command and flag
- [Messaging](messaging.md) — send and receive messages between agents, including
  scopes, mentions, threads, and groups
- [Agent Coordination](agent-coordination.md) — practical multi-agent workflows
  with Beads integration and session templates
