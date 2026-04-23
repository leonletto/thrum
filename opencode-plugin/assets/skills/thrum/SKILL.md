---
name: thrum
description: >
  Multi-agent coordination via messaging, groups, and shared context. Use when
  agents need to communicate, delegate work, or coordinate across worktrees.
allowed-tools: "Bash(thrum:*)"
version: "0.9.0"
author: "Leon Letto <https://github.com/leonletto>"
license: "MIT"
---

# Thrum - Git-Backed Agent Messaging

Run `thrum prime` for full session context (auto-injected by hooks on
SessionStart and PreCompact).

## Quick Command Reference

### Messaging

```bash
thrum send "msg" --to @name              Direct message
thrum send "msg" --to @everyone          Broadcast to all agents
thrum reply <msg-id> "response"          Reply (same audience)
thrum inbox                              List messages (auto-marks displayed as read)
thrum inbox --unread                     Unread only (does not mark as read)
thrum sent                               List messages you sent
thrum sent --unread                      Sent messages with unread recipients
thrum message read --all                 Mark all messages as read
thrum wait                               Block until message arrives (30s timeout)
thrum wait --timeout 120s                Custom timeout (duration)
```

### Agents

```bash
thrum quickstart --name <agent-name> --role R --module M --intent "..."   Register + start session
thrum worktree create <name> --name <agent> --role R --module M    Create worktree + register agent
                                                                   (then run `thrum tmux launch <name>` to start runtime)
thrum worktree setup <name> --name <agent> --role R --module M     Alias for worktree create
thrum whoami                                          Show identity
thrum team                                            List active agents
thrum ping @name                                      Check if agent online
thrum who-has <file>                                  Who's editing a file
```

### Role Templates

```bash
thrum roles list                         List templates + matching agents
thrum roles deploy                       Re-render preambles from templates
thrum roles deploy --agent foo           Deploy for specific agent
thrum roles deploy --dry-run             Preview without writing
```

### Sessions & Context

```bash
thrum session start                      Start session
thrum session end                        End session
thrum session set-intent "..."           Update work description
thrum context show                       Show saved work context
thrum context save --file <path>         Save context from file
thrum overview                           Combined status + team + inbox
/thrum:update-project                    Guided project state update (narrative + state)
/thrum:load-context                      Restore work context after compaction
```

### Tmux Sessions (Recommended)

```bash
thrum tmux start                     Launch agent session (create+launch+prime+attach)
thrum tmux status                    Show managed sessions with state
thrum tmux connect                   Attach to running session (interactive picker)
thrum tmux restart <name>            Restart session with context snapshot
thrum tmux kill <name>               Tear down session
thrum tmux create <session> --name <n> --role <r> --module <m> --cwd <path>   Create + register agent
thrum tmux quickstart <session> --name <n> --role <r> --module <m> --cwd <path>   Alias for tmux create
thrum tmux launch <session> [--runtime <r>]   Start runtime in session (REQUIRED after `tmux create`)
```

### Monitor Jobs

```bash
thrum monitor add <name> --cmd "..." --on-match "msg"   Add a monitored process
thrum monitor list                   List all monitor jobs
thrum monitor show <name>            Show job details and recent matches
thrum monitor stop <name>            Stop a monitor job
thrum monitor logs <name>            Tail job output
thrum monitor restart <name>         Restart a stopped job
```

### Daemon & Sync

```bash
thrum daemon start                       Start daemon
thrum daemon stop                        Stop daemon
thrum daemon status                      Daemon health
thrum sync force                         Force immediate sync
thrum sync status                        Sync state
```

### Utility

```bash
thrum init                               Initialize thrum in repo (also starts the daemon)
thrum prime                              Full session context
thrum prime --json                       Machine-readable output
thrum <cmd> --help                       Detailed command usage
```

## When to Use Thrum

| Thrum                                    | TaskList/SendMessage       | Neither                       |
| ---------------------------------------- | -------------------------- | ----------------------------- |
| Cross-worktree messaging                 | Same-session task tracking | Single-agent, no coordination |
| Persistent messages (survive compaction) | Ephemeral task lists       | Temporary scratch notes       |
| Background listener pattern              | Inline progress tracking   | Simple linear execution       |
| Multi-machine sync via git               | Local to conversation      | No persistence needed         |

**Decision test:** "Do messages need to survive session restart or reach agents
in other worktrees?" YES = Thrum.

**Thrum + TaskList coexist:** Use TaskList for immediate session work. Use Thrum
for cross-session/cross-worktree coordination messages.

## Message Delivery

### Tmux Sessions (Recommended)

When running in a tmux-managed session, messages are delivered directly to your
pane via daemon nudge — zero token cost, no background sub-agent needed. See
[TMUX_SESSIONS.md](resources/TMUX_SESSIONS.md).

### Listener Pattern (Fallback)

When tmux is not available, use the background message listener. It calls
`thrum wait` (blocking) and returns when messages arrive. Re-arm after
processing. See [LISTENER_PATTERN.md](resources/LISTENER_PATTERN.md).

### Context Management

- `thrum prime` gathers identity, team, inbox, git context, sync health
- SessionStart hook auto-runs `thrum prime` on session start
- PreCompact hook saves state to thrum context + `/tmp` backup before compaction
- **After compaction:** run `/thrum:load-context` to restore your work context
- Agent identity persists in `.thrum/identities/`

## Resources

| Resource                                             | Content                                      |
| ---------------------------------------------------- | -------------------------------------------- |
| [TMUX_SESSIONS.md](resources/TMUX_SESSIONS.md)       | Tmux-managed session setup and commands      |
| [BOUNDARIES.md](resources/BOUNDARIES.md)             | Thrum vs TaskList/SendMessage decision guide |
| [MESSAGING.md](resources/MESSAGING.md)               | Protocol patterns, context management        |
| [ANTI_PATTERNS.md](resources/ANTI_PATTERNS.md)       | Common mistakes and how to avoid them        |
| [LISTENER_PATTERN.md](resources/LISTENER_PATTERN.md) | Background message listener template         |
| [CLI_REFERENCE.md](resources/CLI_REFERENCE.md)       | Complete command syntax reference            |
| [IDENTITY.md](resources/IDENTITY.md)                 | Agent identity and multi-worktree patterns   |
| [WORKTREES.md](resources/WORKTREES.md)               | Multi-worktree coordination                  |

Run `thrum <command> --help` for any command's full usage.
