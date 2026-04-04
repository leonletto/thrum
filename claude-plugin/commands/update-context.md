---
description: Save ephemeral session context (survives compaction)
---

Save your current work state so it survives context compaction. This is the
ephemeral session layer — for durable project state updates, use
`/thrum:update-project` instead.

### Quick Save

Compose a brief summary of your in-progress work, then pipe to context save:

```bash
echo "Working on X. Blocked by Y. Next: Z." | thrum context save
```

### Detailed Save (subagent)

For a thorough save with git state, delegate to a subagent following the same
pattern as `/thrum:update-context` (existing behavior preserved).
