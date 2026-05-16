---
name: thrum-quickstart
description: Register agent and start session
# source: claude-plugin/commands/quickstart.md
# generated-by: scripts/sync-skills.sh
---

# Thrum Quickstart

Use this skill when the user explicitly wants the `quickstart` Thrum workflow.
Prefer the umbrella `thrum` skill when the request spans multiple commands or
needs broader coordination judgment.

Register as an agent, start a session, and set intent in one step.

If arguments are provided, use them. Otherwise ask the user for role, module,
and intent.

```bash
thrum quickstart --name <agent-name> --role <role> --module <module> --intent "<description>"
```

Common roles: `implementer`, `planner`, `reviewer`, `tester`, `coordinator`.
