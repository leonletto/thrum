---
description: Update project state with session summary and mechanical state
---

Guide for composing and saving durable project state via the
`/thrum:update-project` skill.

### Step 1: Compose Your Session Summary

Before delegating, write a brief narrative from your own memory covering:

- **What you worked on** — tasks, features, bugs, investigations
- **Key decisions** — approach changes, trade-offs, rejected alternatives
- **Current state** — what's in-progress, what's blocked, what's done
- **What a future session needs to know** — gotchas, incomplete work, important
  context

### Step 2: Run the Skill

In Claude Code, run `/thrum:update-project`. The skill will:

1. Use your narrative summary from Step 1
2. Gather mechanical state: `git log --oneline -15`, `git status --short`,
   `git branch --show-current`, `bd stats`, open epics, ready issues
3. Read the existing `project_state.md`
4. Merge your narrative with gathered state using targeted edits

### Alternatively: Use thrum context save directly

For session-scoped ephemeral context (not durable project state):

```bash
echo "$CONTEXT" | thrum context save
```

See `thrum context save --help` for full options.
