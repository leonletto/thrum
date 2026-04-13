---
name: thrum-overview
description: Combines identity, team, inbox, and sync status
# source: claude-plugin/commands/overview.md
# generated-by: scripts/sync-skills.sh
---

# Thrum Overview

Use this skill when the user explicitly wants the `overview` Thrum
workflow. Prefer the umbrella `thrum` skill when the request spans multiple
commands or needs broader coordination judgment.


Show a combined view of agent identity, active team members, inbox messages, and
sync status.

```bash
thrum overview               # Human-readable
thrum overview --json        # Machine-readable
```

This is equivalent to running `thrum status`, `thrum team`, `thrum inbox`,
`thrum sent`, and `thrum sync status` together.
