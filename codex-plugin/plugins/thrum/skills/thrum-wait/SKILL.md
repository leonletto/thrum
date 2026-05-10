---
name: thrum-wait
description: Block until a message arrives
# source: claude-plugin/commands/wait.md
# generated-by: scripts/sync-skills.sh
---

# Thrum Wait

Use this skill when the user explicitly wants the `wait` Thrum
workflow. Prefer the umbrella `thrum` skill when the request spans multiple
commands or needs broader coordination judgment.


Block until a message arrives or timeout expires. Used by the message-listener
pattern.

```bash
thrum wait                           # Default 30s timeout
thrum wait --timeout 120s            # 120 seconds
thrum wait --after -15s --json # Include messages sent up to 15s ago; JSON output
```

See LISTENER_PATTERN.md resource for the full background listener template.
