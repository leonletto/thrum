---
title: "Coordinate Two Agents"
description:
  "Walk through registering two agents, sending messages between them, and
  seeing what everyone is working on — the most common Thrum use case"
category: "guides"
order: 10
tags:
  [
    "coordination",
    "two-agents",
    "messaging",
    "quickstart",
    "worktree",
    "how-to",
  ]
last_updated: "2026-03-13"
---

## Coordinate Two Agents

The most common Thrum setup: two agents working on the same project. One
implements, one reviews. They communicate directly without you relaying messages
between terminal windows.

This walkthrough goes from zero to two agents talking to each other.

### What You Need

- A git repo with `thrum init` already done
- Two terminals open (or two worktrees — the pattern is the same)
- Thrum daemon running (`thrum init` starts it automatically)

### Register Both Agents

In the first terminal, register the implementer:

```bash
thrum quickstart --role implementer --module auth --intent "Implementing auth module"
```

Thrum picks a name for you — something like `impl_auth_1`. Check it:

```bash
thrum whoami
```

In the second terminal, register the reviewer:

```bash
thrum quickstart --role reviewer --module auth --intent "Reviewing auth changes"
```

This agent gets its own name, like `rev_main_1`. Check who's online:

```bash
thrum team
```

You should see both agents listed with their roles, intent, and which worktree
they're on.

### Send a Message

The implementer finishes a piece of work and wants to notify the reviewer.
First, find the reviewer's exact name:

```bash
thrum team
```

Then send:

```bash
thrum send "Auth module ready for review — JWT middleware and tests passing" --to @rev_main_1
```

Use the actual agent name from `thrum team`, not the role. Sending
`--to @reviewer` would fan out to every agent with a reviewer role — not what
you want here.

### Check Inbox from the Other Agent

In the reviewer's terminal:

```bash
thrum inbox --unread
```

The message shows up with its ID, sender, and content. Mark it read:

```bash
thrum message read --all
```

### Reply

The reviewer looks at the code and replies:

```bash
thrum reply MSG_ID "LGTM, merging now"
```

Replace `MSG_ID` with the actual ID from `thrum inbox`. Replies thread
automatically — the implementer sees it in context.

Back in the implementer's terminal:

```bash
thrum inbox --unread
```

The reply appears. Both agents now have a record of the exchange, persisted in
git.

### See What Everyone Is Working On

From either terminal:

```bash
thrum team
```

Shows each agent's name, role, intent, and worktree. If you want to know which
agent has a particular file open:

```bash
thrum who-has internal/auth/jwt.go
```

Useful when you're about to touch a file and want to check if another agent is
already working on it.

### Update Your Intent

When you move to the next task, update what you're doing so `thrum team` stays
accurate:

```bash
thrum agent set-intent "Writing integration tests for auth flow"
```

Agents auto-heartbeat while the daemon is running. When a session ends, they
become inactive automatically — no explicit cleanup needed.

### Next Steps

- [Messaging](../messaging.md) — full send/receive/reply reference including
  scopes, priorities, and group messaging
- [Multi-Agent Support](../multi-agent.md) — groups, runtime presets, and
  patterns for larger teams
- [Review Workflow](review-workflow.md) — a complete implement-then-review
  walkthrough with worktrees
