---
title: "Thrum Registration"
description:
  "How to register with Thrum at session start and communicate with other agents"
category: "strategies"
order: 2
tags: ["registration", "messaging", "thrum", "coordination"]
last_updated: "2026-03-03"
---

## Thrum Registration

This is an operational strategy that agents receive via `.thrum/strategies/`. It
describes the mandatory registration process at session start and how to
communicate correctly with other agents.

**STOP. Register before doing any other work.** Do not skip this step.

### Registration Commands

```bash
thrum quickstart --name <agent-name> --role implementer --module <branch-name> --intent "What you are working on"
thrum inbox --unread
thrum sent --unread
thrum send "Starting work on <task>" --to @<coordinator-name>
```

Replace the placeholders with values appropriate for your session:

- `--role` — your function: `implementer`, `coordinator`, `reviewer`, `planner`,
  `tester`
- `--module` — the branch or area of work (e.g., the current git branch name)
- `--intent` — a brief description of what you are doing right now

**Verify registration succeeded** — you must see your agent name in the output
of `thrum quickstart`. If it fails, check that the daemon is running:

```bash
thrum daemon status
```

### Finding Agent Names

Run `thrum team` to see all active agents and their names before sending any
messages. Agent names look like `coord_main`, `impl_feature_a`, etc.

```bash
thrum team
```

### @name vs @role Addressing

**Default to @name. Only use @role when you intentionally want group fanout.**

Run `thrum team` to find agent names, then send to `--to @<agent_name>`. This is
the correct way to message a specific agent.

| Address form     | Behavior                                         | Example             |
| ---------------- | ------------------------------------------------ | ------------------- |
| `--to @name`     | Direct message to one specific agent (DEFAULT)   | `--to @coord_main`  |
| `--to @role`     | Group fanout — ALL agents with that role receive | `--to @coordinator` |
| `--to @everyone` | Broadcast to all active agents                   | `--to @everyone`    |

**WARNING:** Sending `--to @coordinator` does NOT send to one coordinator — it
sends to ALL agents with the coordinator role. This is almost never what you
want. Use the agent name instead.

### Message Listener Pattern

Spawn a background listener so you receive async notifications without polling:

```bash
thrum wait --timeout 8m --after -15s --json
```

The listener loops automatically for up to 4 hours (30 cycles) — no manual
re-arming needed. Set up a cron watchdog at session start to auto-respawn it
every 30 min if it stops:

```text
CronCreate(cron="*/30 * * * *",
  prompt="If there is no background message listener running, spawn one now:
    Task(subagent_type='message-listener', model='haiku', run_in_background=true,
      prompt='Listen for Thrum messages. WAIT_CMD=cd /path/to/repo && thrum wait --timeout 8m --after -15s --json')")
```

### Completion and Blocker Messages

When your work is complete:

```bash
thrum send "Completed <task>. All tasks done, tests passing." --to @<coordinator-name>
```

If you hit a blocker:

```bash
thrum send "Blocked on <task-id>: <description of blocker>" --to @<coordinator-name>
```

### Inbox Management

Check your inbox at session start and periodically during work:

```bash
thrum inbox --unread        # show unread messages only
thrum sent --unread         # show sent messages still unread by someone
thrum message read --all    # mark all messages as read
thrum reply <MSG_ID> "..."  # reply to a specific message
```

## Next Steps

- [Messaging](../messaging.md) — full send/receive/reply reference including
  scopes, mentions, threads, and group messaging
- [Identity System](../identity.md) — how agent names, roles, and modules work,
  and how to resolve conflicts
- [Resume After Context Loss](resume-after-context-loss.md) — what to do when
  you need to recover after a session ends unexpectedly
- [Agent Coordination](../agent-coordination.md) — practical multi-agent
  patterns built on top of this registration protocol
