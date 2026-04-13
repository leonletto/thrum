---
name: thrum-group
description: Manage messaging groups
# source: claude-plugin/commands/group.md
# generated-by: scripts/sync-skills.sh
---

# Thrum Group

Use this skill when the user explicitly wants the `group` Thrum
workflow. Prefer the umbrella `thrum` skill when the request spans multiple
commands or needs broader coordination judgment.


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
