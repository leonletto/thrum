---
description: Register agent and start session
argument-hint: [--role R --module M --intent "..."]
---

Register as an agent, start a session, and set intent in one step.

If arguments are provided, use them. Otherwise ask the user for role, module,
and intent.

```bash
thrum quickstart --role <role> --module <module> --intent "<description>"
```

Common roles: `implementer`, `planner`, `reviewer`, `tester`, `coordinator`.
