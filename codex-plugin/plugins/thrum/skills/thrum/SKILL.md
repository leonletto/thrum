---
name: thrum
description:
  Main Thrum entry point for multi-agent coordination, messaging, session
  context, and command routing in Codex.
# source: claude-plugin/skills/thrum/SKILL.md (adapted for codex)
---

# Thrum

Use this as the main Thrum skill in Codex. It is the umbrella entry point for
multi-agent coordination, command selection, and message-routing decisions.

## Use this when

- Work must coordinate across agents, worktrees, or sessions.
- You need to choose the right Thrum workflow before acting.
- The request spans multiple Thrum commands rather than one narrow operation.

## Command Skills

Codex registers the Claude plugin command surface as top-level skills:

- `thrum-prime`
- `thrum-overview`
- `thrum-update-project`
- `thrum-team`
- `thrum-inbox`
- `thrum-send`
- `thrum-reply`
- `thrum-wait`
- `thrum-restart`
- `thrum-load-context`
- `thrum-quickstart`
- `thrum-configure-roles`
- `thrum-project-setup`

Use those when the user explicitly asks for one command. Use `thrum` when you
need judgment across commands.

## Core workflow

1. Prime context with `thrum prime` and capture identity, team, inbox, and sync
   health.
2. Decide whether the task is messaging, triage, session lifecycle, role setup,
   or project decomposition.
3. Route into the matching `thrum-*` command skill when the next step is
   command-specific.
4. Keep scope explicit: branch, worktree, files, intended audience, and expected
   output.
5. Re-arm a listener or tmux session pattern when continuous monitoring matters.

## Command baseline

```bash
thrum prime
thrum overview
thrum team
thrum inbox --unread
thrum send "<message>" --to @<agent-or-group>
thrum reply <msg-id> "<response>"
thrum wait --timeout 120s
thrum who-has <path>
```

## Tmux vs Listener

- Prefer tmux-managed sessions when available. They get daemon nudges directly
  with no background listener cost.
- When tmux is not available, use the background listener pattern and re-arm it
  after each completion.

## Context Management

- `thrum prime` gathers identity, team, inbox, git context, and sync health.
- Save durable project state before compaction or handoff.
- Restore saved work context after restart with `thrum-load-context`.

## References

- `resources/TMUX_SESSIONS.md`
- `resources/BOUNDARIES.md`
- `resources/MESSAGING.md`
- `resources/ANTI_PATTERNS.md`
- `resources/LISTENER_PATTERN.md`
- `resources/CLI_REFERENCE.md`
- `resources/IDENTITY.md`
- `resources/WORKTREES.md`
