---
name: prime
description: Prime Thrum session context in the current repository.
---

Run `thrum prime` to gather identity, team, inbox, git context, and daemon
health.

```bash
thrum prime
```

Use `thrum prime --json` for structured output.

**Tip:** After context compaction or session restart, run `/thrum:load-context`
to restore your previous work context (what you were working on, decisions, next
steps).
