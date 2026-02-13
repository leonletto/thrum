---
description: Send a message to an agent or group
argument-hint: ["message" --to @name]
---

Send a direct message, group message, or broadcast.

If arguments are provided, use them. Otherwise ask for recipient and message content.

```bash
thrum send "message" --to @name                  # Direct
thrum send "message" --to @group-name            # Group
thrum send "message" --to @everyone              # Broadcast
thrum send "message" --to @name -p high          # With priority
```

Priorities: `critical`, `high`, `normal` (default), `low`.
