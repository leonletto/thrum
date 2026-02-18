---
name: thrum
description: >
  Multi-agent coordination via messaging, groups, and shared context. Use when
  agents need to communicate, delegate work, or coordinate across worktrees.
allowed-tools: "Bash(thrum:*)"
version: "0.4.3"
author: "Leon Letto <https://github.com/leonletto>"
license: "MIT"
---

# Thrum - Git-Backed Agent Messaging

Run `thrum prime` for full session context (auto-injected by hooks on SessionStart and PreCompact).

## Quick Command Reference

### Messaging

```
thrum send "msg" --to @name              Direct message
thrum send "msg" --to @name -p high      High priority (critical|high|normal|low)
thrum send "msg" --to @everyone          Broadcast to all agents
thrum reply <msg-id> "response"          Reply (same audience)
thrum inbox                              List messages (unread first)
thrum inbox --unread                     Unread only
thrum wait                               Block until message arrives (30s timeout)
thrum wait --timeout 120                 Custom timeout (seconds)
```

### Agents

```
thrum quickstart --role R --module M --intent "..."   Register + start session
thrum whoami                                          Show identity
thrum status                                          Agent + daemon status
thrum team                                            List active agents
thrum ping @name                                      Check if agent online
thrum who-has <file>                                  Who's editing a file
```

### Groups

```
thrum group create <name>                Create group
thrum group add <name> @agent            Add agent to group
thrum group add <name> --role <role>     Add all agents with role
thrum group list                         List groups
thrum send "msg" --to @group-name        Message a group
```

### Sessions & Context

```
thrum session start                      Start session
thrum session end                        End session
thrum session set-intent "..."           Update work description
thrum context prime                      Same as thrum prime
thrum context show                       Show saved work context
thrum context save --file <path>         Save context from file
thrum overview                           Combined status + team + inbox
/thrum:update-context                    Guided context save (narrative + state)
/thrum:load-context                      Restore work context after compaction
```

### Daemon & Sync

```
thrum daemon start                       Start daemon
thrum daemon stop                        Stop daemon
thrum daemon status                      Daemon health
thrum sync force                         Force immediate sync
thrum sync status                        Sync state
```

### Utility

```
thrum init                               Initialize thrum in repo
thrum prime                              Full session context
thrum prime --json                       Machine-readable output
thrum <cmd> --help                       Detailed command usage
```

## When to Use Thrum

| Thrum | TaskList/SendMessage | Neither |
|-------|---------------------|---------|
| Cross-worktree messaging | Same-session task tracking | Single-agent, no coordination |
| Persistent messages (survive compaction) | Ephemeral task lists | Temporary scratch notes |
| Background listener pattern | Inline progress tracking | Simple linear execution |
| Multi-machine sync via git | Local to conversation | No persistence needed |
| Group messaging | Direct teammate DMs | No audience beyond self |

**Decision test:** "Do messages need to survive session restart or reach agents in other worktrees?" YES = Thrum.

**Thrum + TaskList coexist:** Use TaskList for immediate session work. Use Thrum for cross-session/cross-worktree coordination messages.

## Message Protocol

### Priority Handling

- **critical/high**: Process immediately when received
- **normal**: Process at natural breakpoints
- **low**: Batch and process when idle

### Listener Pattern (Background Message Monitoring)

When `thrum prime` detects a Claude Code session with an active identity, it
outputs a ready-to-use listener spawn instruction. Launch it to monitor for
messages in the background (~90 min coverage, 6 cycles × 15 min):

```
Task(subagent_type: "message-listener", model: "haiku", run_in_background: true)
```

The listener calls `thrum wait` (blocking), then returns when messages arrive.
Re-arm after processing. See [LISTENER_PATTERN.md](resources/LISTENER_PATTERN.md).

### Context Management

- `thrum prime` gathers identity, team, inbox, git context, sync health
- SessionStart hook auto-runs `thrum prime` on session start
- PreCompact hook saves state to thrum context + `/tmp` backup before compaction
- **After compaction:** run `/thrum:load-context` to restore your work context
- Agent identity persists in `.thrum/identities/`

## Resources

| Resource | Content |
|----------|---------|
| [BOUNDARIES.md](resources/BOUNDARIES.md) | Thrum vs TaskList/SendMessage decision guide |
| [MESSAGING.md](resources/MESSAGING.md) | Protocol patterns, priority, context management |
| [ANTI_PATTERNS.md](resources/ANTI_PATTERNS.md) | Common mistakes and how to avoid them |
| [LISTENER_PATTERN.md](resources/LISTENER_PATTERN.md) | Background message listener template |
| [CLI_REFERENCE.md](resources/CLI_REFERENCE.md) | Complete command syntax reference |
| [GROUPS.md](resources/GROUPS.md) | Group management patterns |
| [IDENTITY.md](resources/IDENTITY.md) | Agent identity and multi-worktree patterns |
| [WORKTREES.md](resources/WORKTREES.md) | Multi-worktree coordination |

Run `thrum <command> --help` for any command's full usage.
