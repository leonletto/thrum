---
name: thrum-inbox
description: Check message inbox
# source: claude-plugin/commands/inbox.md
# generated-by: scripts/sync-skills.sh
---

# Thrum Inbox

Use this skill when the user explicitly wants the `inbox` Thrum
workflow. Prefer the umbrella `thrum` skill when the request spans multiple
commands or needs broader coordination judgment.


List messages in your inbox. Messages are auto-marked as read when displayed.

```bash
thrum inbox                  # All recent messages (auto-marks as read)
thrum inbox --unread         # Unread only (does not mark as read)
thrum inbox --json           # Machine-readable
thrum sent --unread          # Check sent items with unread recipients
thrum message read --all     # Mark all messages as read
```
