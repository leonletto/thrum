---
title: "Quickstart Guide"
description:
  "Get up and running with Thrum in 5 minutes - initialization, daemon setup,
  agent registration, and first message"
category: "quickstart"
order: 1
tags: ["quickstart", "getting-started", "installation", "setup", "tutorial"]
last_updated: "2026-02-12"
---

# Thrum Quickstart Guide

Get up and running with Thrum in 5 minutes.

## What is Thrum?

Thrum is a Git-backed messaging system that helps you coordinate AI agents
across sessions, worktrees, and machines using Git as the sync layer.

> **New here?** Read [Why Thrum Exists](philosophy.md) to understand the
> philosophy: you direct the work, everything is inspectable, and you stay in
> control.
>
> **Using Claude Code?** Install the [Thrum plugin](claude-code-plugin.md) for
> slash commands, automatic context injection, and zero-config agent setup.

## Fast Path

If you want to skip the manual setup, use the quickstart command:

    $ thrum quickstart --name myagent --role implementer --module auth
    $ thrum quickstart --name planner1 --role planner --module api --intent "Designing REST endpoints"

This registers your agent with a human-readable name (re-registering
automatically on conflict), starts a session, and optionally sets your work
intent in one step. The sections below walk through each step individually.

Quickstart also auto-creates an empty context file for session state persistence.
See [Agent Context Management](context.md) for details.

## Prerequisites

- Git repository
- Go 1.21 or later (for building)
- Unix-like system (macOS, Linux)

## Installation

### From Source

```bash
git clone https://github.com/leonletto/thrum.git
cd thrum
make install  # Builds UI + Go binary, installs to ~/.local/bin
```

### Or Build Go Binary Only

```bash
go build -o thrum ./cmd/thrum
```

Note: Building without `make install` will not include the embedded Web UI SPA.
The daemon will still work, but the web interface at `http://localhost:9999`
will not be available.

## Quick Start

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

**Git worktree auto-detection:** Since v0.4.1, `thrum init` automatically detects if you're in a git worktree and sets up a `.thrum/redirect` file pointing to the main repo's `.thrum/` directory. All worktrees share the same daemon and message history — no manual worktree configuration needed.

If you are upgrading an existing repo that has JSONL files tracked on `main`,
run `thrum migrate` instead.

### 2. Start the Daemon

```bash
thrum daemon start
```

The daemon handles:

- RPC requests from CLI and MCP server via Unix socket
- WebSocket + embedded Web UI SPA on port 9999 (configurable via
  `THRUM_WS_PORT`)
- Background sync every 60s
- Push notifications for subscriptions
- Browser auto-registration via git config

### 3. Register Your Agent and Start a Session

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

### 4. Send Your First Message

```bash
thrum send "Started working on user authentication" \
  --scope module:auth \
  --ref issue:beads-123
```

### 5. Check Your Inbox

```bash
thrum inbox
```

View messages from other agents and humans working on the project.

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

Configure in `.claude/settings.json`:

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

## Typical Workflow

### Morning: Start Work

```bash
# 1. Start daemon (if not running)
thrum daemon start

# 2. Register and start session (or just start session if already registered)
thrum quickstart --name myagent --role implementer --module auth --intent "Working on auth"

# 3. Check inbox for updates
thrum inbox --unread

# 4. Subscribe to your module
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

Thrum uses Git for synchronization:

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
```

## Working with Multiple Worktrees

Feature worktrees share the main worktree's daemon and message store via a
redirect file. Use `thrum setup` to configure a feature worktree:

```bash
# Main worktree (already initialized)
cd ~/project
thrum daemon start

# Feature worktree -- set up redirect to main
cd ~/project-features/auth
thrum setup
thrum session start
thrum send "Experimenting with auth approaches"
```

The `thrum setup` command creates a `.thrum/redirect` file pointing to the main
worktree's `.thrum/` directory. All worktrees then share the same sync worktree,
daemon, and message store. Messages sync across all worktrees and machines
through Git.

### Use the setup scripts for batch configuration

Two shell scripts automate redirect file creation for all your worktrees at once:

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

```
# In the worktree:
.thrum/redirect    → /path/to/main/repo/.thrum
.beads/redirect    → /path/to/main/repo/.beads
```

This ensures all worktrees share the same daemon, message store, and issue
tracker. The scripts are idempotent — run them as many times as you need.

## Automation with Hooks

### Git Post-Commit Hook

```bash
#!/bin/bash
# .git/hooks/post-commit

thrum send "Committed: $(git log -1 --pretty=%B)" \
  --scope repo:$(basename $(git rev-parse --show-toplevel)) \
  --ref commit:$(git rev-parse SHORT HEAD)
```

### Wait for Notifications

```bash
#!/bin/bash
# Wait for CI results

thrum subscribe --mention @ci

if thrum wait --mention @ci --timeout=5m; then
  echo "CI feedback received"
  thrum inbox --mentions --unread
else
  echo "Timeout - no CI feedback"
fi
```

## Multi-Agent Collaboration

### Agent Roles

Different agents can have different names and roles. Multiple agents can coexist
in the same worktree:

```bash
# Implementer agent
thrum quickstart --name furiosa --role implementer --module auth

# Reviewer agent (in another terminal/session)
THRUM_NAME=maximus thrum quickstart --role reviewer --module auth
```

### Communication Pattern

```bash
# Implementer: Request review
thrum send "Auth module ready for review" \
  --scope module:auth \
  --mention @reviewer

# Reviewer: Subscribe and wait
thrum subscribe --mention @reviewer
thrum wait --mention @reviewer

# Reviewer: Provide feedback
thrum send "LGTM - looks good to merge" \
  --scope module:auth \
  --mention @implementer
```

## Configuration

### Identity Files

Agent identities are stored in `.thrum/identities/{name}.json` (one JSON file
per agent, created automatically by `thrum agent register` or
`thrum quickstart`). Multiple agents can coexist in a single worktree, each with
their own identity file.

### Environment Variables

Add to your shell profile:

```bash
# ~/.bashrc or ~/.zshrc
export THRUM_NAME=myagent       # Selects identity file (highest priority)
export THRUM_ROLE=implementer
export THRUM_MODULE=auth
```

Priority: `THRUM_NAME` env var > `--name` flag > solo-agent auto-select. For
role/module: CLI flags > Environment variables > Identity file.

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

## Next Steps

- Read the [CLI Reference](cli.md) for complete command documentation
- Explore the [RPC API](rpc-api.md) for programmatic access
- Check [Architecture](architecture.md) to understand how Thrum works
- See [Development Guide](development.md) to contribute

## Getting Help

```bash
# Command help
thrum --help
thrum send --help

# Check status
thrum status

# View daemon logs
# (Daemon logs to stdout when run in foreground)
```

## Key Concepts

### Messages

Persistent communication between agents, stored in Git-tracked JSONL.

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

Happy collaborating!
