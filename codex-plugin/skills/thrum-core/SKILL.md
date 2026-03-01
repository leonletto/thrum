---
name: thrum-core
description: Use when the user needs multi-agent coordination with durable Thrum messaging across worktrees, including routing via identities/groups, ownership checks, and cross-session message continuity.
# source: claude-plugin/skills/thrum/SKILL.md (condensed for codex)
# last-synced: 2026-03-01
---

# Thrum Core

Use this skill for durable multi-agent coordination with `thrum`.

## Use this when
- Messages must persist across context compaction or session restart.
- Coordination spans multiple worktrees or agents.
- You need identity, group routing, or ownership checks.

## When to Use Thrum vs Other Tools

| Thrum                                    | TaskList/SendMessage       | Neither                       |
| ---------------------------------------- | -------------------------- | ----------------------------- |
| Cross-worktree messaging                 | Same-session task tracking | Single-agent, no coordination |
| Persistent messages (survive compaction) | Ephemeral task lists       | Temporary scratch notes       |
| Background listener pattern              | Inline progress tracking   | Simple linear execution       |
| Multi-machine sync via git               | Local to conversation      | No persistence needed         |
| Group messaging                          | Direct teammate DMs        | No audience beyond self       |

**Decision test:** "Do messages need to survive session restart or reach agents
in other worktrees?" YES = Thrum.

## Core workflow
1. Prime context with `thrum prime` and capture identity, team, inbox, and sync health.
2. Choose audience (`@agent`, `@group`, `@everyone`) and send concise actionable messages.
3. Use reply chains for continuity (`thrum reply <id> "..."`).
4. Keep scope explicit: branch, worktree, files, and expected output.
5. Re-arm listener pattern if continuous monitoring is needed.

## Command baseline
```bash
thrum prime
thrum whoami
thrum team
thrum send "<message>" --to @<agent-or-group>
thrum reply <msg-id> "<response>"
thrum inbox --unread
thrum who-has <path>
```

## Listener Pattern (Background Message Monitoring)

When `thrum prime` detects an active identity, launch a background listener for
continuous coverage (~90 min, 6 cycles × 15 min):

```text
Task(subagent_type: "message-listener", model: "haiku", run_in_background: true)
```

The listener calls `thrum wait` (blocking) and returns when messages arrive.
Re-arm after processing. See `references/LISTENER_PATTERN.md` and
`references/MESSAGE_LISTENER_AGENT.md`.

## Context Management

- `thrum prime` gathers identity, team, inbox, git context, sync health
- **After compaction:** restore work context before continuing
- Agent identity persists in `.thrum/identities/`

## References
- `references/BOUNDARIES.md`
- `references/MESSAGING.md`
- `references/GROUPS.md`
- `references/IDENTITY.md`
- `references/WORKTREES.md`
- `references/LISTENER_PATTERN.md`
- `references/MESSAGE_LISTENER_AGENT.md`
- `references/ANTI_PATTERNS.md`
