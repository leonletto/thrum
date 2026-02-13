---
description: Block until a message arrives
argument-hint: [--timeout 5m]
---

Block until a message arrives or timeout expires. Used by the message-listener pattern.

```bash
thrum wait                           # Default 5 min timeout
thrum wait --timeout 120             # 120 seconds
thrum wait --all --after -30s --json # All messages, recent only
```

See LISTENER_PATTERN.md resource for the full background listener template.
