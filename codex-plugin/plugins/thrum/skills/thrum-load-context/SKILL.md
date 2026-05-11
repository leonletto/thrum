---
name: thrum-load-context
description:
  Restore saved agent work context after compaction or session restart
# source: claude-plugin/commands/load-context.md
# generated-by: scripts/sync-skills.sh
---

# Thrum Load Context

Use this skill when the user explicitly wants the `load-context` Thrum workflow.
Prefer the umbrella `thrum` skill when the request spans multiple commands or
needs broader coordination judgment.

Run `thrum prime` to restore your complete session context. Prime now includes
project state and session context inline — no separate load step needed.

```bash
thrum prime
```

If you need only the session context (without the full briefing):

```bash
thrum context show --session
```
