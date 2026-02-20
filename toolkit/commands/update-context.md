# Update Agent Context

Guide for composing and saving structured agent context via
`thrum context save`.

## Instructions

### Step 1: Compose Your Session Summary

Before delegating, write a brief narrative from your own memory covering:

- **What you worked on** - tasks, features, bugs, investigations
- **Key decisions** - approach changes, trade-offs, rejected alternatives
- **Current state** - what's in-progress, what's blocked, what's done
- **What a future session needs to know** - gotchas, incomplete work, important
  context

Keep it concise. The subagent will gather mechanical state automatically.

### Step 2: Spawn the Update Agent

Delegate to a **general-purpose subagent** that will:

1. **Gather mechanical state** using available tools:
   - `git log --oneline -10` (recent commits)
   - `git status --short` (uncommitted changes)
   - `git branch --show-current` (current branch)
   - `bd stats` (project statistics, if beads is available)
   - `bd list --status=in_progress` (active tasks, if beads is available)
   - `bd ready` (next available work, if beads is available)
   - Skip any command that fails - not all tools are installed everywhere

2. **Read existing context** via `thrum context show` to understand what's
   already saved

3. **Merge** your narrative summary (from Step 1) with the gathered state into a
   structured markdown document:

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
   - **Stats:** ...

   ## Open Questions / Blockers

   <!-- Anything unresolved -->

   ## Next Steps

   <!-- What the next session should pick up -->
   ```

4. **Save** the composed document by piping it to `thrum context save`:

   ```bash
   echo "$CONTENT" | thrum context save
   ```

5. **Return** a brief summary of what was updated (1-2 sentences)

### Subagent Prompt Template

Pass your narrative summary to the subagent in its prompt. Example:

```
Task(
  subagent_type: "general-purpose",
  description: "Update agent context",
  prompt: """
    You are updating agent context for a thrum-managed project.

    ## Agent's Session Summary
    <paste your narrative from Step 1 here>

    ## Your Job
    1. Run git commands to gather repo state (git log, git status, git branch)
    2. Run beads commands if available (bd stats, bd list --status=in_progress, bd ready) - skip if bd not found
    3. Run `thrum context show` to read existing context
    4. Compose a structured markdown document merging the session summary above with gathered state
    5. Pipe the result to `thrum context save`
    6. Return a brief summary of what was saved
  """
)
```

## Why This Approach

- **Subagent handles investigation** - main context window stays clean
- **Narrative + mechanical** - agent insight combined with ground truth
- **Reads before writing** - merges with existing context, doesn't blindly
  replace
- **Graceful degradation** - skips unavailable tools (no beads? no problem)
- **Storage-agnostic** - uses `thrum context save` as the primitive, doesn't
  care how it's stored
