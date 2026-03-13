---
title: "Code Review Workflow"
description:
  "Walk through the implement-then-review cycle: one agent writes code on a
  feature branch, another reviews it, Thrum handles the communication"
category: "guides"
order: 12
tags: ["review", "workflow", "worktree", "feature-branch", "coordination", "how-to"]
last_updated: "2026-03-13"
---

## Code Review Workflow

One agent writes the code. Another reviews it. Thrum handles the messages
between them so you don't have to relay status manually.

This is the core pattern from the [Philosophy](../philosophy.md) page — you do
the thinking, the agents do the typing.

### The Pattern

1. Implementer works on a feature branch (usually in a worktree)
2. Implementer notifies reviewer when ready
3. Reviewer reads the code, sends feedback
4. Implementer addresses feedback and replies
5. Reviewer approves, merge happens

### Set Up

Create a worktree for the implementer's feature branch:

```bash
git worktree add ~/.workspaces/myproject/feature/auth feature/auth
cd ~/.workspaces/myproject/feature/auth
```

Register the implementer in the worktree terminal:

```bash
thrum quickstart --role implementer --module auth --intent "Implementing JWT auth"
```

Register the reviewer in the main repo terminal:

```bash
thrum quickstart --role reviewer --module auth --intent "Reviewing feature branches"
```

Check both are visible:

```bash
thrum team
```

Note the exact agent names — you'll use them for direct messaging.

### Implementer Works and Notifies

The implementer writes the code, runs tests, commits:

```bash
# ... implement the feature ...
go test ./internal/auth/...
git commit -m "feat(auth): add JWT middleware"
```

Find the reviewer's name from `thrum team`, then send:

```bash
thrum send "Auth module complete — JWT middleware and token refresh. All tests passing. Branch: feature/auth" --to @rev_main_1
```

Use the actual agent name, not `@reviewer`. Sending to a role fans out to
every agent with that role — not what you want here.

### Reviewer Gets Notified

Poll at any time with `thrum inbox --unread`, or block until a message arrives:

```bash
thrum wait --timeout 30m --mention @rev_main_1
```

`thrum wait` returns as soon as a message lands or the timeout expires. Then
look at the code:

```bash
thrum message read --all
git fetch origin feature/auth
git diff main..feature/auth
```

### Reviewer Provides Feedback

Quick approval:

```bash
thrum reply MSG_ID "LGTM — merge it"
```

Detailed feedback:

```bash
thrum reply MSG_ID "Two things:
1. Token expiry should be configurable, not hardcoded to 24h
2. Add a test for expired token rejection
Otherwise looks solid"
```

### Implementer Responds

```bash
thrum inbox --unread
# ... address the feedback ...
git commit -m "fix(auth): make token expiry configurable, add expiry test"
thrum reply REVIEW_MSG_ID "Fixed both — expiry reads from config now, expiry test added and passing"
```

### Reviewer Approves and Merge

```bash
thrum inbox --unread
thrum reply MSG_ID "Approved. Merge when ready."
```

Implementer merges:

```bash
cd /path/to/main/repo
git merge --no-ff feature/auth
git push origin main
```

### Using with Beads

If you're tracking work in Beads, the implementer claims tasks before starting
and closes them when done:

```bash
bd update thrum-abc.1 -s in_progress
# ... work ...
bd close thrum-abc.1
thrum send "Completed thrum-abc.1, ready for review" --to @rev_main_1
```

See [Beads and Thrum](../beads-and-thrum.md) for how the two tools fit together.

### Next Steps

- [Agent Coordination](../agent-coordination.md) — patterns for larger teams
  and session templates
- [Workflow Templates](../workflow-templates.md) — pre-built skill pipelines
  for the full research → plan → implement → review cycle
- [Coordinate Two Agents](coordinate-two-agents.md) — simpler walkthrough for
  the basic send/receive pattern
