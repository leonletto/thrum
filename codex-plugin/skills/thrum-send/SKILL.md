---
name: thrum-send
description: Send a message to an agent
# source: claude-plugin/commands/send.md
# generated-by: scripts/sync-skills.sh
---

# Thrum Send

Use this skill when the user explicitly wants the `send` Thrum workflow. Prefer
the umbrella `thrum` skill when the request spans multiple commands or needs
broader coordination judgment.

Send a direct message or broadcast.

If arguments are provided, use them. Otherwise ask for recipient and message
content.

```bash
thrum send "message" --to @agent_name             # Direct message
thrum send "message" --to @everyone               # Broadcast to all agents
```

Unknown recipients are a hard error. Use `thrum team` to verify agent names
before sending.
