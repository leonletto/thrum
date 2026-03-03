# Thrum Registration

**STOP. Register before doing any other work.** Do not skip this step.

## Registration Commands

```bash
thrum quickstart --role implementer --module <branch-name> --intent "What you are working on"
thrum inbox --unread
thrum send "Starting work on <task>" --to @<coordinator-name>
```

Replace the placeholders with values appropriate for your session:

- `--role` — your function: `implementer`, `coordinator`, `reviewer`, `planner`, `tester`
- `--module` — the branch or area of work (e.g., the current git branch name)
- `--intent` — a brief description of what you are doing right now

**Verify registration succeeded** — you must see your agent name in the output
of `thrum quickstart`. If it fails, check that the daemon is running:

```bash
thrum daemon status
```

## Finding Agent Names

Run `thrum team` to see all active agents and their names before sending any
messages. Agent names look like `coord_main`, `impl_feature_a`, etc.

```bash
thrum team
```

## @name vs @role Addressing

**This is critical.** The two addressing modes behave very differently:

| Address form    | Behavior                                          | Example                     |
| --------------- | ------------------------------------------------- | --------------------------- |
| `--to @name`    | Direct message to one specific agent              | `--to @coord_main`          |
| `--to @role`    | Group fanout — ALL agents with that role receive  | `--to @coordinator`         |
| `--to @everyone`| Broadcast to all active agents                    | `--to @everyone`            |

Always use agent names for direct communication. Only use role addressing when
you intentionally want every agent with that role to receive the message.

## Message Listener Pattern

Spawn a background listener so you receive async notifications without polling:

```bash
thrum wait --timeout 15m --after -1s --json
```

Re-arm the listener every time it returns — both when messages arrive and when
it times out. This keeps you reachable throughout your session.

## Completion and Blocker Messages

When your work is complete:

```bash
thrum send "Completed <task>. All tasks done, tests passing." --to @<coordinator-name>
```

If you hit a blocker:

```bash
thrum send "Blocked on <task-id>: <description of blocker>" --to @<coordinator-name>
```

## Inbox Management

Check your inbox at session start and periodically during work:

```bash
thrum inbox --unread        # show unread messages only
thrum message read --all    # mark all messages as read
thrum reply <MSG_ID> "..."  # reply to a specific message
```
