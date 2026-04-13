# Tmux-Managed Sessions

Tmux-managed sessions are the recommended way to run Thrum agents. The daemon
detects the tmux pane and delivers message notifications directly — zero token
cost, no background listener needed.

## Why Tmux Sessions

- **Zero-cost message delivery** — daemon nudges the tmux pane directly
- **No background listener** — eliminates the message-listener sub-agent
- **Session lifecycle** — create, launch, status, connect, restart, kill
- **Runtime detection** — reads configured runtime from `.thrum/config.json`

## Quick Start

```bash
thrum tmux start                    # One-command: create + launch + prime + attach
```

This creates a tmux session named after the current directory, launches the
configured runtime (from `config.Runtime.Primary`, default `claude`), runs
`/thrum:prime` for registration, and attaches.

## Setting Up a New Agent Worktree

Full sequence for a coordinator setting up an agent in a new worktree:

```bash
# 1. Initialize thrum + beads redirects in the worktree
cd <worktree-path>
thrum init                              # Sets up .thrum redirect to main repo
# Beads: bd init doesn't auto-detect worktrees, so set up the redirect manually:
mkdir -p .beads && echo /path/to/main/repo/.beads > .beads/redirect

# 2. Create tmux session + register agent identity in one step
thrum tmux create <name> --cwd <path> \
  --name <agent> --role <role> --module <mod> --intent '...'
# alias: thrum tmux quickstart <name> --cwd <path> --name <agent> --role <role> --module <mod>

# 3. Launch the runtime (reads configured runtime from .thrum/config.json)
thrum tmux launch <name>

# 4. Agent runs /thrum:prime on startup — loads identity + full context
# 5. Communicate via: thrum send "message" --to @<agent>
```

`thrum tmux create` requires either quickstart flags (`--name`, `--role`,
`--module`) or `--no-agent`. Bare `thrum tmux create <name> --cwd <path>` errors
out.

**Single identity per worktree:** quickstart cleans up any old identity files in
the worktree. Each worktree has exactly one identity.

**`--no-agent`** skips identity registration — for bare sessions, debugging, or
non-agent processes:

```bash
thrum tmux create debug-session --cwd /path --no-agent
```

## Manual Setup (Quick Reference)

```bash
thrum tmux create <name> --cwd <path> --name <agent> --role <role> --module <mod>
thrum tmux launch <name>                # Start runtime in session
thrum tmux connect <name>               # Attach to session
```

## Session Management

```bash
thrum tmux status                   # Show all managed sessions with state
thrum tmux connect                  # Interactive picker for alive sessions
thrum tmux connect <name>           # Attach directly by name
thrum tmux kill <name>              # Tear down a session
```

## Message Delivery

When a message arrives for your agent, the daemon sends a nudge directly to your
tmux pane. You'll see a notification appear — check your inbox:

```bash
thrum inbox --unread
```

No listener sub-agent, no polling, no CronCreate watchdog. Messages just arrive.

## Session Restart

If your context window is running low, you can restart with a conversation
snapshot preserved:

- **Self-initiated:** Run `/thrum:restart` — saves snapshot, notifies
  coordinator
- **Coordinator-initiated:** `thrum tmux restart <name> --force`
- **Automatic:** Configure `restart.auto_threshold` in `.thrum/config.json`

The snapshot is automatically included in `thrum prime` on the next session
start.

## When Tmux Isn't Available

If tmux is not installed or not practical for your setup, use the background
message listener pattern instead. See
[LISTENER_PATTERN.md](LISTENER_PATTERN.md).
