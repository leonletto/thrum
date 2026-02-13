---
description: Update agent context with session summary and repo state
---

Guide for composing and saving structured agent context via `thrum context save`.

### Step 1: Compose Your Session Summary

Before delegating, write a brief narrative from your own memory covering:

- **What you worked on** — tasks, features, bugs, investigations
- **Key decisions** — approach changes, trade-offs, rejected alternatives
- **Current state** — what's in-progress, what's blocked, what's done
- **What a future session needs to know** — gotchas, incomplete work, important context

### Step 2: Spawn the Update Agent

Delegate to a **general-purpose subagent** that will:

1. Gather mechanical state: `git log --oneline -10`, `git status --short`, `git branch --show-current`, `bd stats`, `bd list --status=in_progress`, `bd ready` (skip any that fail)
2. Read existing context via `thrum context show`
3. Merge your narrative with gathered state into structured markdown:

```markdown
# Agent Context

## Session Summary
<!-- Your narrative from Step 1 -->

## Git State
- **Branch:** ...
- **Recent commits:** ...
- **Uncommitted changes:** ...

## Task State
- **In-progress:** ...
- **Ready:** ...

## Open Questions / Blockers

## Next Steps
```

4. Save via: `echo "$CONTENT" | thrum context save`
5. Return a brief summary of what was updated

### Subagent Prompt Template

```
Task(
  subagent_type: "general-purpose",
  description: "Update agent context",
  prompt: """
    You are updating agent context for a thrum-managed project.

    ## Agent's Session Summary
    <paste your narrative from Step 1 here>

    ## Your Job
    1. Run git commands to gather repo state
    2. Run beads commands if available (skip if bd not found)
    3. Run `thrum context show` to read existing context
    4. Compose structured markdown merging session summary with gathered state
    5. Pipe result to `thrum context save`
    6. Return brief summary of what was saved
  """
)
```
