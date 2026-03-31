---
description: Check message inbox
argument-hint: [--unread]
---

List messages in your inbox. Messages are auto-marked as read when displayed.

```bash
thrum inbox                  # All recent messages (auto-marks as read)
thrum inbox --unread         # Unread only (does not mark as read)
thrum inbox --json           # Machine-readable
thrum sent --unread          # Check sent items with unread recipients
thrum message read --all     # Mark all messages as read
```
