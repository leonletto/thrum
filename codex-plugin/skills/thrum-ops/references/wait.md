---
description: Block until a message arrives
argument-hint: [--timeout 30s]
---

Block until a message arrives or timeout expires. Used by the message-listener
pattern.

```bash
thrum wait                           # Default 30s timeout
thrum wait --timeout 120             # 120 seconds
thrum wait --after -30s --json # Recent messages only
```

See LISTENER_PATTERN.md resource for the full background listener template.
