# Implementation Agent Template

## Purpose

Guide an agent through implementing an epic's tasks in a designated worktree.
Supports both cold-start and resume scenarios. The agent works through beads
tasks in order, runs quality gates, and pushes completed work.

## Inputs Required

- `{{EPIC_ID}}` — The beads epic ID to implement
- `{{WORKTREE_PATH}}` — Absolute path to the working worktree
- `{{BRANCH_NAME}}` — Git branch for this work (e.g., `feature/auth`)
- `{{DESIGN_DOC}}` — **Absolute path** to the design spec (if applicable)
- `{{REFERENCE_CODE}}` — Paths to reference implementations (if any)
- `{{QUALITY_COMMANDS}}` — Commands for test/lint (e.g.,
  `make test && make lint`)
- `{{COVERAGE_TARGET}}` — Minimum coverage threshold (e.g., >80%)
- `{{AGENT_NAME}}` — Unique name for this agent (e.g., `impl-daemon`,
  `impl-cli`)
- `{{COORDINATOR_NAME}}` — Agent name of the coordinator (from `thrum team`,
  e.g., `coord_main`). Use for direct messages, not the role name.
- `{{PLAN_FILE}}` — **Absolute path** to the implementation plan file
- `{{PROJECT_ROOT}}` — Absolute path to the project root

**Why absolute paths?** Design docs, plan files, and prompts typically live in
gitignored directories (`dev-docs/`, `docs/plans/`). Worktrees only share
committed files, so agents in worktrees cannot resolve relative paths to these
files. All `{{DESIGN_DOC}}`, `{{PLAN_FILE}}`, and prompt references must be
absolute paths (e.g., `/Users/you/project/dev-docs/plans/file.md`).

---

## Sub-Agent Strategy

Delegate research, independent tasks, and verification to sub-agents. Your main
context should focus on task coordination, dependency management, and
cross-cutting decisions.

### Principles

1. **Parallelize independent tasks** — When multiple unblocked tasks touch
   different files/packages, implement them simultaneously via sub-agents
2. **Delegate research** — Spawn sub-agents to explore unfamiliar code before
   implementing, rather than reading it into your context
3. **Verify in background** — Run tests and lint via background sub-agents while
   you continue with the next task
4. **Focused prompts** — Give each sub-agent the full task description, worktree
   path, quality commands, and expected deliverables

### Agent Selection

| Task                        | Agent Type                  | Model  | Background? |
| --------------------------- | --------------------------- | ------ | ----------- |
| Implement a complex task    | `general-purpose`           | sonnet | no\*        |
| Implement a simple task     | `general-purpose`           | haiku  | no\*        |
| Explore unfamiliar code     | `Explore`                   | sonnet | yes         |
| Run tests / lint            | `general-purpose`           | haiku  | yes         |
| Review implementation       | `feature-dev:code-reviewer` | sonnet | no          |
| Doc updates / config tweaks | `general-purpose`           | haiku  | yes         |

\*Use foreground when you need the result before proceeding. Use background when
you can continue other work while they run.

### When to Parallelize vs. Work Sequentially

**Parallel** (sub-agents):

- Tasks touching different files/packages with no shared state
- Independent verification (tests, lint, coverage)
- Research into multiple unrelated code areas
- Doc/config updates alongside implementation

**Sequential** (direct work):

- Tasks modifying shared files or depending on prior task output
- Tasks requiring deep context from current session's changes
- Tasks needing judgment calls mid-implementation
- Single remaining task (no parallelism benefit)

### Verifier Sub-Agent Pattern

After completing a task, spawn a background sub-agent to independently verify:

- Run the test suite and confirm all pass
- Review implementation against the task description
- Check code follows project conventions
- Confirm acceptance criteria are met

This lets you start the next task immediately while verification runs in
parallel.

---

## MANDATORY: Register Before Any Work

**STOP. Run these commands before doing anything else.** Do not skip this
section.

```bash
cd {{WORKTREE_PATH}}
thrum quickstart --name {{AGENT_NAME}} --role implementer --module {{BRANCH_NAME}} --intent "Implementing {{EPIC_ID}}"
thrum inbox --unread
thrum send "Starting work on {{EPIC_ID}}" --to @{{COORDINATOR_NAME}}
```

**Verify registration succeeded** — you must see your agent name in the output
of `thrum quickstart`. If it fails, check that the daemon is running with
`thrum daemon status`.

**Finding agent names:** Run `thrum team` to see all active agents and their
names. Always send to agent names (e.g., `--to @coord_main`), not role names
(e.g., `--to @coordinator`). Sending to a role fans out to ALL agents with
that role.

**Use the message listener** — Spawn a background listener to get async
notifications. Re-arm it every time it returns (both MESSAGES_RECEIVED and
NO_MESSAGES_TIMEOUT).

When your work is complete (Phase 4), send a completion message:

```bash
thrum send "Completed {{EPIC_ID}}. All tasks done, tests passing." --to @{{COORDINATOR_NAME}}
```

If you hit a blocker from another agent's work, escalate:

```bash
thrum send "Blocked on {{TASK_ID}} by {{BLOCKER_ID}}" --to @{{COORDINATOR_NAME}}
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

Also read the full implementation code from the plan file: `{{PLAN_FILE}}`

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

### Parallel Task Implementation

When multiple tasks are unblocked and independent, implement them
simultaneously. This is the biggest context and time win.

#### Identify Parallelizable Tasks

After completing a task, check for unblocked work:

```bash
bd show {{EPIC_ID}}  # Which tasks are pending and unblocked?
```

Tasks are parallelizable when they touch different files/packages and don't
depend on each other's output.

#### Launch Pattern

Give each sub-agent everything it needs to work autonomously:

```text
# Launched in ONE message for parallel execution
Task(subagent_type="general-purpose", model="sonnet",
  prompt="Implement a task in {{WORKTREE_PATH}} on branch {{BRANCH_NAME}}.

  ## Task Details
  <paste full bd show output for task A>

  ## Instructions
  1. Read the task description — it is the source of truth
  2. Implement following existing code patterns
  3. Run tests: {{QUALITY_COMMANDS}}
  4. Commit: git commit -m '{{EPIC_TITLE}}: <task summary>'
  5. Return: what was built, test results, commit hash")

Task(subagent_type="general-purpose", model="sonnet",
  prompt="Implement a task in {{WORKTREE_PATH}} ...

  ## Task Details
  <paste full bd show output for task B>
  ...")
```

#### After Sub-Agents Complete

1. Review reported results (commit hashes, test output)
2. Check for conflicts: `git --no-pager log --oneline -5`
3. Close completed tasks: `bd close {{TASK_A}} {{TASK_B}}`
4. Run unified quality gate: `{{QUALITY_COMMANDS}}`
5. Check for newly unblocked tasks — repeat if more are available

#### Guardrails

- **2-3 concurrent sub-agents** is the sweet spot. More adds coordination
  overhead without proportional speedup.
- **Partition by file/package** to avoid git conflicts. If two tasks touch the
  same file, run them sequentially.
- **Always run a final unified test pass** after parallel work — concurrent
  sub-agents may miss interaction bugs.

---

### Tool Usage

#### auggie-mcp (Code Retrieval)

Use `auggie-mcp codebase-retrieval` for exploring existing code. It's
context-efficient — prefer it over reading files manually when you need to
understand how existing code works, find patterns, or explore unfamiliar
packages.

#### Subagents

See the **Sub-Agent Strategy** section at the top of this template for full
guidance on agent selection, parallel vs. sequential decisions, and the verifier
pattern.

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
5. **Notify via Thrum** so the coordinator or blocking agent is aware:

```bash
# Ask the blocking agent for help
thrum send "Blocked on {{TASK_ID}} — waiting for {{BLOCKER_ID}}. Can you prioritize?" --mention @implementer

# Or escalate to the coordinator
thrum send "Blocked on {{TASK_ID}} by external dependency {{BLOCKER_ID}}" --to @{{COORDINATOR_NAME}}
```

### Communicating During Work

Use Thrum to stay coordinated:

```bash
# Report progress on significant milestones
thrum send "Completed task {{TASK_ID}}, moving to next" --to @{{COORDINATOR_NAME}}

# Ask for input when you need a decision
thrum send "Need input: should X use approach A or B? Context: ..." --to @{{COORDINATOR_NAME}}

# If you realize your work affects another agent's files
thrum send "Heads up: I'm modifying internal/daemon/rpc.go which may overlap with your work" --mention @implementer

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

### Step 5: Report Completion via Thrum

```bash
thrum send "Completed {{EPIC_ID}}. All tasks done, tests passing, merged to main." --to @{{COORDINATOR_NAME}}
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
# 1. Re-register with Thrum (MANDATORY — do this first)
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
