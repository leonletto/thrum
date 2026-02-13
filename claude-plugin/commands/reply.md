---
description: Reply to a message
argument-hint: [msg-id "response"]
---

Reply to a message with the same audience as the original.

If arguments are provided, use them. Otherwise ask for the message ID and reply content.

```bash
thrum reply <msg-id> "response text"
```

The reply inherits the original message's audience (direct or group).
