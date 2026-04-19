---
name: thrum
description: >
  Multi-agent coordination via messaging, groups, and shared context. Use when
  agents need to communicate, delegate work, or coordinate across worktrees and
  sessions.
---

# Thrum - Git-Backed Agent Messaging

Run `thrum prime` for full session context.

## Quick Command Reference

### Messaging

```bash
thrum send "msg" --to @name              Direct message
thrum send "msg" --to @everyone          Broadcast to all agents
thrum reply <msg-id> "response"          Reply (same audience)
thrum inbox                              List messages (auto-marks displayed as read)
thrum inbox --unread                     Unread only (does not mark as read)
thrum sent                               List messages you sent
thrum message read --all                 Mark all messages as read
thrum wait                               Block until message arrives (30s timeout)
thrum wait --timeout 120s                Custom timeout (duration)
```

### Agents

```bash
thrum quickstart --name <name> --role R --module M --intent "..."   Register + start session
thrum whoami                                          Show identity
thrum team                                            List active agents
thrum ping @name                                      Check if agent online
thrum who-has <file>                                  Who's editing a file
```

### Groups

```bash
thrum group create <name>                Create group
thrum group add <name> @agent            Add agent to group
thrum group list                         List groups
thrum send "msg" --to @group-name        Message a group
```

### Sessions & Context

```bash
thrum session start                      Start session
thrum session end                        End session
thrum session set-intent "..."           Update work description
thrum context show                       Show saved work context
thrum overview                           Combined status + team + inbox
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
thrum init                               Initialize thrum in repo
thrum prime                              Full session context
thrum <cmd> --help                       Detailed command usage
```

## Addressing Protocol

| Target               | Routing                                     | When to use                          |
| -------------------- | ------------------------------------------- | ------------------------------------ |
| `--to @agent_name`   | **Direct** — routes to the named agent      | Default for all task messages        |
| `--to @coordinator`  | **Role fanout** — ALL agents with that role | Only when you want every coordinator |
| `--to @backend-team` | **Group** — all members of the named group  | Team-wide announcements              |
| `--to @everyone`     | **Broadcast** — all registered agents       | Critical alerts                      |

**Critical:** `@coordinator` is a role, not an agent name. Use `thrum team` to
find agent names, then send `--to @<name>` for direct messages.

## Notification Delivery

Claude-runtime agents get message nudges via three converging paths — you
don't need to run a background listener. All three paths lead you back to
the same action: run `thrum inbox --unread` when you see a nudge.

1. **Tmux nudge** — if you're running in a tmux-managed session (e.g.,
   `thrum tmux start`), the daemon types a notification directly into your
   pane.
2. **Hook injection** — `thrum init --runtime claude` installs runtime
   lifecycle hooks (stop, post-tool, prompt-submit, and context-compact events)
   that read a per-agent spool dir and surface the nudge as context when
   lifecycle events fire.
3. **Cron backstop** — a 15-minute `CronCreate` cron runs
   `thrum inbox --unread` directly while the REPL is idle, catching
   anything the hook missed.

All three paths emit the same nudge text: `New message from @sender -- run
`thrum inbox --unread` to read`. Always act by running `thrum inbox --unread`.
The previous `thrum wait` listener pattern (see
`references/LISTENER_PATTERN.md`) is deprecated for new Claude agents —
it still works but burns context tokens for the entire session.

## Background Listener Pattern

Launch a background listener to monitor for messages:

1. Spawn a lightweight background process that calls `thrum wait --timeout 8m`
2. When a message arrives, the listener returns immediately
3. Process the message, then re-arm the listener

See [LISTENER_PATTERN.md](references/LISTENER_PATTERN.md) for setup details.

## References

| Reference                                             | Content                            |
| ----------------------------------------------------- | ---------------------------------- |
| [MESSAGING.md](references/MESSAGING.md)               | Protocol, addressing, context mgmt |
| [LISTENER_PATTERN.md](references/LISTENER_PATTERN.md) | Background listener setup          |
| [CLI_REFERENCE.md](references/CLI_REFERENCE.md)       | Complete command syntax reference  |

Run `thrum <command> --help` for any command's full usage.
