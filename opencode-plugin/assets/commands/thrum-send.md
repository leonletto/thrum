---
description: Send a message to an agent
---

Send a direct message or broadcast.

If arguments are provided, use them. Otherwise ask for recipient and message
content.

```bash
thrum send "message" --to @agent_name             # Direct message
thrum send "message" --to @everyone               # Broadcast to all agents
```

Unknown recipients are a hard error. Use `thrum team` to verify agent names
before sending.
