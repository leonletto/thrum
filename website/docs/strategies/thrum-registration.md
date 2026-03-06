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
thrum quickstart --role implementer --module <branch-name> --intent "What you are working on"
thrum inbox --unread
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
thrum wait --timeout 15m --after -1s --json
```

Re-arm the listener every time it returns — both when messages arrive and when
it times out. This keeps you reachable throughout your session.

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
thrum message read --all    # mark all messages as read
thrum reply <MSG_ID> "..."  # reply to a specific message
```
