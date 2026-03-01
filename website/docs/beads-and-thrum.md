---
title: "Beads and Thrum"
description:
  "How persistent task tracking and agent messaging work together for
  multi-agent development"
category: "guides"
order: 4
tags: ["beads", "thrum", "agents", "coordination", "memory", "messaging"]
last_updated: "2026-02-10"
---

## Beads and Thrum

## The Context Loss Problem

AI agents lose context. When a conversation becomes too long, the context window
compacts and early messages disappear. When a session ends, the agent forgets
what it was working on. When an agent teammate sends a message, that message
vanishes if the receiving agent isn't actively listening.

In single-agent workflows, this is annoying. In multi-agent workflows, it's
catastrophic. Agents duplicate work, miss dependencies, and lose coordination.
The traditional solution — stuffing everything into the prompt — doesn't scale
beyond trivial tasks.

## Two Complementary Tools

Beads and Thrum solve different halves of the same problem: how to give agents
persistent memory and communication that survives session boundaries.

**Beads** is a git-backed issue tracker. It handles task state, dependencies,
and work discovery. When an agent needs to know "what should I work on?" or
"what was I doing?", Beads provides the answer.

**Thrum** is a git-backed messaging system. It handles coordination,
notifications, and presence. When an agent needs to know "what did my teammates
tell me?" or "who's working on what?", Thrum provides the answer.

Both use Git as the persistence layer. No external services, no databases, no
state that lives outside the repository. Everything survives session boundaries
because everything is committed to version control.

## What Each Tool Covers

| Aspect                | Beads                                          | Thrum                                           |
| --------------------- | ---------------------------------------------- | ----------------------------------------------- |
| **Purpose**           | Task tracking and dependency management        | Agent messaging and coordination                |
| **Primary Use**       | Persistent work state across sessions          | Communication across agents and sessions        |
| **Key Commands**      | `bd create`, `bd ready`, `bd close`, `bd show` | `thrum send`, `thrum inbox`, `thrum agent list` |
| **Recovery Scenario** | "What tasks am I responsible for?"             | "What messages did I miss while offline?"       |
| **State Storage**     | `.beads/` directory in Git                     | `.thrum/` directory in Git                      |
| **Daemon**            | None (purely CLI)                              | Optional daemon for real-time notifications     |

Beads answers: **What should I work on?** Thrum answers: **What did others tell
me?**

## A Combined Workflow

Here's how an agent uses both tools together across multiple sessions.

### Session 1: Starting work

```bash
# Check for assigned work
$ bd ready --assigned-to @implementer_ui
- thrum-a1b2: Implement search UI component

# Check for messages from coordinator
$ thrum inbox --unread
From: @planner
Message: "Search UI should use the MiniSearch interface we built in thrum-c3d4"

# Claim the task
$ bd update thrum-a1b2 --status in_progress

# Acknowledge and ask question
$ thrum send "Starting on search UI. Should it support dark mode?" --to @planner
```

### Session 2: After context window compaction

The agent's context has been compacted. Early messages are gone. But the state
persists in Git.

```bash
# Recover task context
$ bd show thrum-a1b2
Title: Implement search UI component
Status: in_progress
Assigned: @implementer_ui
Depends on: thrum-c3d4 (closed)

# Check for planner's response
$ thrum inbox --unread
From: @planner
Message: "Yes, dark mode required. See design tokens in styles/theme.css"

# Continue work with full context recovered
```

### Session 3: Finishing up

```bash
# Mark task complete
$ bd close thrum-a1b2 --comment "Implemented with dark mode support"

# Notify coordinator
$ thrum send "Search UI complete (thrum-a1b2). Ready for integration." --to @planner
```

### Session 4: New agent picks up follow-up work

A different agent needs to build on this work.

```bash
# Discover what's ready to work on
$ bd ready
- thrum-e5f6: Integrate search UI into main app (depends on thrum-a1b2)

# Check message history for context
$ thrum inbox --from @implementer_ui
From: @implementer_ui
Message: "Search UI complete (thrum-a1b2). Ready for integration."

# Full context recovered without any conversation history
```

## Why Git-Backed Persistence Matters

Both Beads and Thrum store state in Git, not in memory, databases, or external
services. This design choice provides several guarantees:

**Offline operation.** No network dependency. Agents work in air-gapped
environments, on planes, or when external services are down.

**State travels with the repository.** Clone the repo, get all task history and
message history. No separate database to back up or migrate.

**Survives any session boundary.** Context window compaction, agent restart,
machine crash, or branch switch — the state persists because it's committed to
version control.

**Auditable history.** Every task state change and every message is a Git
commit. You can see who changed what, when, and why using standard Git tools.

This architecture trades real-time performance for persistence and simplicity.
For agent workflows — where context loss is the primary failure mode — that's
the right tradeoff.

## Beads Command Reference for Sub-Agents

Sub-agents (spawned via the Task tool in Claude Code, or similar delegation in
other frameworks) typically **don't receive plugin context**. They can run `bd`
commands via Bash, but they don't know the correct syntax unless you tell them.

If you use Beads for task tracking, add the block below to your project's
`CLAUDE.md` (for Claude Code / Auggie) or `AGENTS.md` (for other agent
frameworks). This prevents sub-agents from guessing wrong commands like
`bd start` or `bd progress`.

### Recommended Block for CLAUDE.md / AGENTS.md

````markdown
## Beads (bd) Quick Reference

Task tracking uses Beads (`bd`). Sub-agents don't get the beads plugin context,
so this section ensures all agents know the correct commands.

### Essential Commands

```bash
bd ready                           # Show unblocked tasks
bd list                            # All open issues
bd show <id>                       # Issue details
bd blocked                         # Show blocked issues
bd create "title" -t task -p 2     # Create task (P2)
bd update <id> -s in_progress      # Start working on issue
bd update <id> --claim             # Atomically claim (assign + in_progress)
bd close <id>                      # Mark done
bd close <id> --suggest-next       # Mark done + show newly unblocked
bd comments <id> add "note"        # Add comment
bd dep <blocker> --blocks <blocked>  # Add dependency
```

**Common mistakes:**
- `bd start` does not exist — use `bd update <id> -s in_progress`
- `bd progress` does not exist — use `bd update <id> -s in_progress`
- `bd set-state` is for operational dimensions (patrol, health), NOT workflow status

### Creating an Epic with Tasks

```bash
# 1. Create the epic
bd create "Epic title" -t epic -p 1 -d "Description"
# Output: Created thrum-abc

# 2. Create child tasks under the epic
bd create "Task 1" -t task --parent thrum-abc -d "Details"
# Output: Created thrum-abc.1
bd create "Task 2" -t task --parent thrum-abc -d "Details"
# Output: Created thrum-abc.2
bd create "Task 3" -t task --parent thrum-abc -d "Details"
# Output: Created thrum-abc.3

# 3. Add dependencies BETWEEN tasks (not epic→task)
# "task 2 depends on task 1" = task 1 blocks task 2
bd dep thrum-abc.1 --blocks thrum-abc.2

# 4. Verify structure
bd dep tree thrum-abc
bd epic status thrum-abc
```

**Dependency direction:** `bd dep <blocker> --blocks <blocked>` means the
blocker must close before the blocked task becomes ready. The alternative
syntax is `bd dep add <blocked> <blocker>` (reversed order).
````

You can also generate a minimal starter block with `bd onboard`, but the block
above is more comprehensive and covers the epic/dependency patterns that
sub-agents most commonly get wrong.

## See Also

- [Quickstart](./quickstart.md) — Get started with Beads and Thrum in 5 minutes
- [Agent Coordination](./agent-coordination.md) — Patterns for multi-agent
  workflows
- [CLI Reference](./cli-reference.md) — Complete command documentation
