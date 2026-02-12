# Implementation Agent Template

> **Note:** This is a distributable template. Fill in the `{{PLACEHOLDER}}` values with your project-specific information before using.

## Purpose

Guide an agent through implementing an epic's tasks in a designated worktree.
Supports both cold-start and resume scenarios. The agent works through beads
tasks in order, runs quality gates, and pushes completed work.

## Inputs Required

- `{{EPIC_ID}}` — The beads epic ID to implement
- `{{WORKTREE_PATH}}` — Absolute path to the working worktree
- `{{BRANCH_NAME}}` — Git branch for this work (e.g., `feature/auth`)
- `{{DESIGN_DOC}}` — Path to the design spec (if applicable)
- `{{REFERENCE_CODE}}` — Paths to reference implementations (if any)
- `{{QUALITY_COMMANDS}}` — Commands for test/lint (e.g.,
  `make test && make lint`)
- `{{COVERAGE_TARGET}}` — Minimum coverage threshold (e.g., >80%)
- `{{AGENT_NAME}}` — Unique name for this agent (e.g., `impl-daemon`,
  `impl-cli`)

---

## MANDATORY: Register Before Any Work (If Using Thrum)

**If your project uses Thrum for agent coordination, run these commands before doing anything else.** If you're not using Thrum, skip this section.

```bash
cd {{WORKTREE_PATH}}
thrum quickstart --name {{AGENT_NAME}} --role implementer --module {{BRANCH_NAME}} --intent "Implementing {{EPIC_ID}}"
thrum inbox --unread
thrum send "Starting work on {{EPIC_ID}}" --to @coordinator
```

When your work is complete (Phase 4), send a completion message:

```bash
thrum send "Completed {{EPIC_ID}}. All tasks done, tests passing." --to @coordinator
```

---

## Phase 1: Orient

Whether starting fresh or resuming after context loss, always begin here. This
phase is idempotent — running it multiple times is safe and expected.

### Step 1: Check Epic & Task Status

```bash
# Navigate to worktree
cd {{WORKTREE_PATH}}

# Check epic status — see which tasks are done, in_progress, or pending
bd show {{EPIC_ID}}

# Check for any in-progress tasks (may be from a prior session)
bd list --status=in_progress

# Check completed tasks
bd list --status=completed
```

### Step 2: Check Git State

```bash
# Verify you're on the right branch
git branch --show-current

# Check for uncommitted work from a prior session
git status

# Review recent commits to understand what's been done
git --no-pager log --oneline -10

# Pull latest changes (if remote tracking is set up)
git pull --rebase
```

### Step 3: Explore Existing Code

Use `auggie-mcp codebase-retrieval` to understand what already exists:

1. Check which files/packages have been created
2. Review existing implementations for patterns to follow
3. Identify the current state of any in-progress tasks

If a design doc exists, read it: `{{DESIGN_DOC}}`

If reference code exists, explore it: `{{REFERENCE_CODE}}`

### Step 4: Determine Starting Point

- If **all tasks are complete**: Skip to Phase 3 (Verify Quality) then Phase 4
  (Complete)
- If **a task is `in_progress`**: Review its state — check what's partially
  implemented, check git diff, then continue from where it left off
- If **tasks are pending**: Start with the first unblocked pending task (lowest
  dependency order)

**CRITICAL: DO NOT redo completed work.** Trust the beads status and git
history. Pick up from exactly where the previous session stopped.

---

## Phase 2: Implement Tasks

### Task Execution Loop

For each task (in dependency order, foundations first):

#### 1. Claim the Task

```bash
bd update {{TASK_ID}} --status=in_progress
```

#### 2. Read the Task Details

```bash
bd show {{TASK_ID}}
```

The task description is the **source of truth** for what to build. It contains
file paths, signatures, code examples, and acceptance criteria. Follow it
precisely.

#### 3. Implement the Task

- Follow the task description
- Write tests alongside implementation (TDD when appropriate)
- Follow existing code patterns and conventions in the codebase
- Reference `{{DESIGN_DOC}}` for architectural context when needed

#### 4. Verify the Task

```bash
# Run project quality gates
{{QUALITY_COMMANDS}}
```

All tests must pass before proceeding. If tests fail, fix them before moving on.
Do not skip failing tests or mark them as known failures without explicit
instruction.

#### 5. Commit the Work

```bash
git add <specific-files>
git commit -m "{{EPIC_TITLE}}: <concise task description>"
```

Commit after each task, not in bulk at the end. Use descriptive commit messages
that explain what was built.

#### 6. Close the Task

```bash
bd close {{TASK_ID}}
```

#### 7. Move to Next Task

Repeat from step 1 with the next unblocked pending task.

If multiple tasks are complete, close them in batch:

```bash
bd close {{TASK_ID_1}} {{TASK_ID_2}} {{TASK_ID_3}}
```

---

### Tool Usage

#### auggie-mcp (Code Retrieval)

Use `auggie-mcp codebase-retrieval` for exploring existing code. It's
context-efficient — prefer it over reading files manually when you need to
understand how existing code works, find patterns, or explore unfamiliar
packages.

#### Subagents

Use subagents to parallelize independent work and manage context:

| Agent                 | Use For                                                                                |
| --------------------- | -------------------------------------------------------------------------------------- |
| **Claude Sonnet**     | Complex coding: implementation, refactoring, intricate logic, multi-file changes       |
| **Claude Haiku**      | Simple tasks: documentation updates, formatting, straightforward edits, config changes |
| **Verifier subagent** | After implementing a task, spawn a subagent to independently verify (see below)        |

**Verifier subagent pattern:** After completing a task, spawn a subagent to:

- Run the test suite and confirm all tests pass
- Review the implementation against the task description
- Check that code follows project conventions
- Verify no regressions were introduced
- Confirm acceptance criteria from the task description are met

#### When to Use Subagents vs. Direct Work

- **Direct work:** Sequential tasks, tasks that modify shared state, tasks
  requiring deep context of prior changes in the same session
- **Subagent:** Independent tasks, verification, documentation, tasks in
  separate files/packages that don't need shared context

---

### Handling Blockers

If a task is blocked:

1. Check what's blocking it: `bd show {{BLOCKER_ID}}`
2. If the blocker is another task **within your epic**, work on the blocker
   first
3. If the blocker is **external** (different epic, different agent), skip the
   blocked task and work on the next unblocked one
4. Document the blocker:
   `bd update {{TASK_ID}} --notes="Blocked by {{BLOCKER_ID}}: <reason>"`
5. **Notify via Thrum** (if using Thrum) so the coordinator or blocking agent is aware

### Communicating During Work (If Using Thrum)

Use Thrum to stay coordinated:

```bash
# Report progress on significant milestones
thrum send "Completed task {{TASK_ID}}, moving to next" --to @coordinator

# Ask for input when you need a decision
thrum send "Need input: should X use approach A or B? Context: ..." --to @coordinator

# Send to your team group
thrum send "Found a blocker, need help from backend team" --to @backend-team

# Update your intent when switching tasks
thrum agent set-intent "Working on {{TASK_ID}}: <description>"
```

---

## Phase 3: Verify Quality

After all tasks are complete (or after resuming to find all tasks already done),
run the full verification suite.

```bash
cd {{WORKTREE_PATH}}

# Run all tests
{{QUALITY_COMMANDS}}

# Check coverage if applicable
# Target: {{COVERAGE_TARGET}}
```

### Verification Checklist

- [ ] All tasks in the epic are closed: `bd show {{EPIC_ID}}`
- [ ] All tests pass: `{{QUALITY_COMMANDS}}`
- [ ] Lint passes
- [ ] Code coverage meets `{{COVERAGE_TARGET}}` (if applicable)
- [ ] No regressions in existing functionality
- [ ] Commit history is clean and descriptive:
      `git --no-pager log --oneline -20`

If any check fails, fix the issue before proceeding. Do not claim completion
with failing tests.

---

## Phase 4: Complete & Land

### Step 1: Update Documentation

If the epic changed public APIs, architecture, or user-facing behavior, update
relevant docs:

- Architecture docs
- API documentation
- README / quickstart guides
- Any docs referenced in `{{DESIGN_DOC}}`

### Step 2: Close the Epic

```bash
# Verify all tasks are closed
bd show {{EPIC_ID}}

# Close the epic
bd close {{EPIC_ID}} --reason="All tasks implemented and verified"
```

### Step 3: Merge to Main

```bash
# Ensure branch is up to date with main
git fetch origin main
git rebase origin/main

# If conflicts arise, resolve them, then:
# git rebase --continue

# Run quality gates one final time after rebase
{{QUALITY_COMMANDS}}

# Merge into main
cd {{PROJECT_ROOT}}
git checkout main
git merge {{BRANCH_NAME}}
```

### Step 4: Push

```bash
git push origin main
```

### Step 5: Report Completion (If Using Thrum)

```bash
thrum send "Completed {{EPIC_ID}}. All tasks done, tests passing, merged to main." --to @coordinator
```

### Step 6: Clean Up (Only If Instructed)

```bash
# Remove the worktree if no longer needed
git worktree remove {{WORKTREE_PATH}}

# Delete the feature branch
git branch -d {{BRANCH_NAME}}
```

Only clean up if explicitly instructed. The user may want to keep the worktree
for future work.

---

## Resume Quick Reference

If you're resuming after context loss (compaction, new session, etc.), here's
the minimal sequence:

```bash
# 1. Re-register with Thrum (OPTIONAL — only if using Thrum)
cd {{WORKTREE_PATH}}
thrum quickstart --name {{AGENT_NAME}} --role implementer --module {{BRANCH_NAME}} --intent "Resuming {{EPIC_ID}}"
thrum inbox --unread

# 2. Orient from beads and git
bd show {{EPIC_ID}}                    # What's done?
bd list --status=in_progress           # Anything mid-flight?
git --no-pager log --oneline -10       # What was committed?
git status                             # Any uncommitted work?
# Then pick up from the first incomplete task — DO NOT redo completed work
```
