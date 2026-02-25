---
description: Manage messaging groups
argument-hint: [create|add|list|info] [args...]
---

Create and manage groups for team messaging.

```bash
thrum group create <name>                    # Create group
thrum group add <name> @agent                # Add agent
thrum group add <name> --role <role>         # Add by role
thrum group list                             # List all groups
thrum group info <name>                      # Group details
thrum group members <name> --expand          # Resolved members
thrum group remove <name> @agent             # Remove member
thrum group delete <name>                    # Delete group
```

Send to a group: `thrum send "msg" --to @group-name`
