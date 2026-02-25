---
name: thrum-core
description: Use when the user needs multi-agent coordination with durable Thrum messaging across worktrees, including routing via identities/groups, ownership checks, and cross-session message continuity.
---

# Thrum Core

Use this skill for durable multi-agent coordination with `thrum`.

## Use this when
- Messages must persist across context compaction or session restart.
- Coordination spans multiple worktrees or agents.
- You need identity, group routing, or ownership checks.

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

## References
- `references/BOUNDARIES.md`
- `references/MESSAGING.md`
- `references/GROUPS.md`
- `references/IDENTITY.md`
- `references/WORKTREES.md`
- `references/LISTENER_PATTERN.md`
- `references/MESSAGE_LISTENER_AGENT.md`
- `references/ANTI_PATTERNS.md`
