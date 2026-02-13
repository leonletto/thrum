---
description: Check message inbox
argument-hint: [--unread]
---

List messages in your inbox. Messages are auto-marked as read when displayed.

```bash
thrum inbox                  # All recent messages
thrum inbox --unread         # Unread only
thrum inbox --json           # Machine-readable
```

Handle messages by priority: critical/high first, then normal/low.
