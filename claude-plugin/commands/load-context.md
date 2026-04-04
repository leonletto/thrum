---
description: Restore saved agent work context after compaction or session restart
---

Run `thrum prime` to restore your complete session context. Prime now includes
project state and session context inline — no separate load step needed.

```bash
thrum prime
```

If you need only the session context (without the full briefing):

```bash
thrum context show --session
```
