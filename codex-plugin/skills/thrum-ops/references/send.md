---
description: Send a message to an agent or group
argument-hint: ["message" --to @name]
---

Send a direct message, group message, or broadcast.

If arguments are provided, use them. Otherwise ask for recipient and message
content.

```bash
thrum send "message" --to @name                  # Direct (routes to named agent)
thrum send "message" --to @group-name            # Group
thrum send "message" --to @everyone              # Broadcast
```

Unknown recipients are a hard error. Use `thrum team` to verify agent names
before sending. Sending `--to @role` fans out to all agents with that role (with
a warning) — use `--to @name` for direct messages.
