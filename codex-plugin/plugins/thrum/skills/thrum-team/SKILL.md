---
name: thrum-team
description: Show active team members
# source: claude-plugin/commands/team.md
# generated-by: scripts/sync-skills.sh
---

# Thrum Team

Use this skill when the user explicitly wants the `team` Thrum workflow. Prefer
the umbrella `thrum` skill when the request spans multiple commands or needs
broader coordination judgment.

List all active agents with their roles, modules, and current intents.

```bash
thrum team                   # Human-readable
thrum team --json            # Machine-readable
```

Use `thrum ping @name` to check if a specific agent is online.
