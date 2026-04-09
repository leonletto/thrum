---
name: orchestrate
description: Execute a plan by launching agents in tmux sessions, managing epic-by-epic execution with review gates, and preparing merge reports. Use when the orchestrator receives a plan handoff from the coordinator.
---

# Orchestrate: Managed Plan Execution

This is the full execution playbook. Follow each phase in order. Do not skip
phases or steps.

---

## Phase 1 — Validate Handoff

Before executing anything, verify the handoff is complete.

### Step 1: Read the plan

```bash
# The plan file path comes from the coordinator's message
cat <plan-file-path>
```

Read the plan file and the associated design doc (if referenced). Understand the
epic structure, task dependencies, and worktree assignments.

### Step 2: Validate completeness

Check each item. If ANY check fails, stop and send specific feedback to the
coordinator.

```bash
# 1. Beads epics exist with tasks
bd show <epic-1-id>
bd show <epic-2-id>
# ... for each epic

# 2. Implementation prompt exists
cat <prompt-file-path>

# 3. Review gates exist in the prompt (structural marker)
grep "## Review Gate:" <prompt-file-path>
# Must have at least one review gate for multi-epic prompts

# 4. Dependencies configured
bd dep tree <epic-id>

# 5. Merge target in config
cat .thrum/config.json | grep merge_target
```

### Step 3: Report validation result

If valid, proceed to Phase 2. If invalid, report what's missing:

```bash
thrum send "Plan validation failed. Missing: <specific list>" --to @<coordinator>
```

Then STOP. Wait for the coordinator to fix and re-send.

---

## Phase 2 — Configure Execution

### Step 1: Autonomy negotiation

Read the default from config:

```bash
cat .thrum/config.json | grep default_autonomy
```

Present to the human:

> "Autonomy level is `<default>`. Options:
> - `per_epic`: I'll pause after each epic for your review and approval
> - `end_only`: I'll run all epics and present a final report before merge
>
> Proceed with `<default>`?"

Wait for confirmation. If the human changes the level, use the new one.

### Step 2: Runtime selection

```bash
thrum team
```

- If all agents use the same runtime → use it silently, no question needed
- If multiple runtimes detected → ask the human which to use per work stream

### Step 3: Worktree planning

Analyze epic dependencies to determine parallelism:

```bash
bd dep tree <epic-id>
bd show <epic-id>  # for each epic
```

Present the plan alongside autonomy/runtime (one configuration gate, not three):

> "Plan: N worktrees needed. Epics A+B parallel (no dependencies), Epic C after
> both complete. Autonomy: `<level>`. Runtime: `<rt>`. Proceed?"

Wait for confirmation.

---

## Phase 3 — Launch Agents

For each agent needed:

### Step 1: Create worktree

```bash
thrum worktree create <name>
```

### Step 2: Create and launch tmux session

```bash
thrum tmux create <name> --cwd <worktree-path>
thrum tmux launch <name> --runtime <runtime>
```

### Step 3: Verify alive

```bash
thrum tmux status
```

All sessions must show as active. If any session fails to launch, retry once.
If it fails again, escalate to the human.

### Step 4: Set initial status

```bash
thrum agent set-status idle --agent <name>
```

### Step 5: Wait for agent registration

Agents auto-prime on session start, which takes ~10-15 seconds. Before sending
work, verify each agent has registered:

```bash
# Wait for agents to register (poll every 5s, max 30s)
thrum team
```

Confirm each launched agent appears in `thrum team` output. If an agent doesn't
register within 30 seconds, check its session with `thrum tmux status` and
retry the launch if needed.

### Step 6: Report to human

```bash
thrum send "All agents launched and registered: <agent-list>. Starting execution." --to @<human_or_coordinator>
```

---

## Phase 4 — Execute (Epic Loop)

For each epic batch (parallel group of independent epics):

### Step 1: Send prompts

For each agent in the batch:

```bash
thrum send "Start work on <epic-id>. Implementation prompt: <absolute-path-to-prompt>. Your section starts at '<epic heading>'. Report completion when done." --to @<agent_name>
thrum agent set-status working --agent <agent_name>
```

### Step 2: Set own status

```bash
thrum agent set-status idle
```

You are now waiting. Monitor your inbox.

### Step 3: Process agent messages

Check inbox at every breakpoint:

```bash
thrum inbox --unread
```

Handle each message type:

**Completion report:**
1. Acknowledge: `thrum reply <msg-id> "Received. Running review."`
2. Dispatch code review sub-agent (use `feature-dev:code-reviewer`)
3. If review passes → close the task: `bd close <task-id>`
4. If review has findings → send findings to agent, wait for fixes (max 3
   rounds, then escalate)

**Blocker:**
1. Assess if you can unblock (dependency issue, config problem)
2. If yes → fix and reply
3. If no → escalate to human with full context

**Question:**
1. If coordination-level (which file to modify, task priority) → answer
2. If judgment call (design decision, architecture choice) → escalate to human

### Step 4: Checkpoint (if per_epic autonomy)

When all agents in the batch complete and reviews pass:

```bash
thrum send "Epic <id> complete. Summary: <changes>. Review: passed. Proceed to next?" --to @<human>
```

Wait for approval before continuing.

### Step 5: Advance to next batch

```bash
bd close <completed-epic-ids>
bd ready  # Check what's unblocked
```

Loop to Step 1 with the next batch.

---

## Phase 5 — Finalize

### Step 1: Cross-branch review

Spawn a sub-agent to check for conflicts:

- Diff each agent branch against the merge target
- Diff branches against each other if they touch overlapping files
- Report conflicts or integration issues

### Step 2: Prepare merge report

Compile:
- Changes per branch (summary of commits)
- All review results (per-epic + cross-branch)
- Test results as reported by agents during their review gates
- Merge target: `<configured branch>`

### Step 3: Present to human

```bash
thrum send "All epics complete. Merge report: <report>. Ready to merge to <target>?" --to @<human>
```

### Step 4: Merge on approval

Only after human approves. The orchestrator runs on detached HEAD and cannot
merge directly. Spawn a sub-agent in the main repo worktree to perform the
merge:

```text
Agent(subagent_type="general-purpose", model="haiku",
  prompt="Merge agent branches to the merge target.

  Working directory: <main-repo-path>

  Steps:
  1. git checkout <merge-target>
  2. git pull origin <merge-target>
  3. For each branch: git merge <branch> --no-ff
  4. git push origin <merge-target>
  5. Report: which branches merged, any conflicts

  Branches to merge: <list-of-agent-branches>
  Merge target: <target-branch>")
```

If the sub-agent reports merge conflicts, escalate to the human with the
conflict details. Do not attempt to resolve conflicts yourself.

### Step 5: Cleanup

With human confirmation:

```bash
# Kill agent sessions
thrum tmux kill <name>

# Optionally remove worktrees
thrum worktree teardown <name>
```

### Step 6: Final status

```bash
thrum agent set-status idle
thrum send "Plan complete. Merged to <target>. All sessions cleaned up." --to @<human>
```

---

## Error Handling

### Silent agent timeout

While waiting for agents, poll session health every 5 minutes:

```bash
thrum tmux status
```

If an agent's session is dead or stuck (no message received and pane idle for
two consecutive checks):

1. Attempt restart: `thrum tmux restart <name>`
2. If restart fails → escalate to human

### Agent session dies

```bash
thrum tmux restart <name>
```

If restart fails after one retry, escalate to human with the session name and
error details.

### Review finds blockers

Send findings to the agent. Wait for fixes. Re-review. Maximum 3 rounds. If
issues persist after 3 rounds, escalate to human with the full review history.

### Human doesn't respond at review gate

Wait. Send a reminder after 5 minutes:

```bash
thrum send "Reminder: waiting for approval on <epic-id> review gate." --to @<human>
```

Do not proceed without approval when autonomy is `per_epic`.
