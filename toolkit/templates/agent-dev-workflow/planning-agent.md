# Planning Agent Template

## Purpose

Guide an agent through the full planning lifecycle: brainstorm a feature, write
a design spec, then decompose it into beads epics and detailed tasks.

## Inputs Required

- `{{FEATURE_DESCRIPTION}}` — What the user wants to build (can be rough)
- `{{PROJECT_ROOT}}` — Absolute path to the project root
- `{{DESIGN_DOC_DIR}}` — Where design docs live (e.g., `dev-docs/plans/`)
- `{{REFERENCE_DOCS}}` — Existing specs, designs, or reference code to consider
- `{{TECH_STACK}}` — Languages, frameworks, and tools in use

---

## Sub-Agent Strategy

The planning agent is a **coordinator**, not a researcher. Delegate codebase
exploration, document reading, and issue analysis to sub-agents. Keep your
context clean for decision-making and design writing.

### Principles

1. **Research in parallel, design sequentially** — Sub-agents gather information
   concurrently; you synthesize it into designs and plans
2. **Write to disk** — Sub-agents write findings to `output/planning/`; you read
   only the summaries, not raw code
3. **Background execution** — Use `run_in_background=true` for all investigation
   agents so you can continue working while they explore
4. **Focused prompts** — Each sub-agent gets a specific question, an output file
   path, and a format. Avoid open-ended "explore everything" prompts.

### Agent Selection

| Task                       | Agent Type                   | Model  |
| -------------------------- | ---------------------------- | ------ |
| Codebase architecture scan | `Explore`                    | sonnet |
| Read & summarize docs      | `general-purpose`            | haiku  |
| Beads issue scan           | `general-purpose`            | haiku  |
| Deep code analysis         | `feature-dev:code-explorer`  | sonnet |
| Bulk beads task creation   | `general-purpose` (parallel) | haiku  |
| Design spec writing        | Direct (main agent)          | —      |

### Parallel Research Pattern

When Phase 1 requires understanding multiple independent areas, launch
sub-agents in a single message:

```text
# All launched in ONE message for parallel execution
Task(subagent_type="Explore", run_in_background=true,
  prompt="Explore {{PROJECT_ROOT}}. Map packages, interfaces, and patterns
  relevant to: {{FEATURE_DESCRIPTION}}.
  Write findings to output/planning/codebase-scan.md")

Task(subagent_type="general-purpose", model="haiku", run_in_background=true,
  prompt="Read {{REFERENCE_DOCS}}. Summarize patterns and conventions
  relevant to {{FEATURE_DESCRIPTION}}.
  Write to output/planning/reference-summary.md")

Task(subagent_type="general-purpose", model="haiku", run_in_background=true,
  prompt="Run: bd list --status=open, bd ready, bd blocked.
  Identify work related to {{FEATURE_DESCRIPTION}}.
  Write to output/planning/beads-context.md")
```

After all complete, read the output files to inform your design work.

### Bulk Task Creation Pattern

When creating many tasks (> 6), delegate to parallel sub-agents — one per epic.
Pass the full task details (titles, descriptions, dependencies) in the prompt:

```text
Task(subagent_type="general-purpose", model="haiku",
  prompt="Create these beads tasks under epic {{EPIC_1_ID}}:
  1. bd create --title='...' --type=task --priority=2 --description='...'
  2. bd create --title='...' --type=task --priority=1 --description='...'
  Then set ordering: bd dep add <later_id> <earlier_id>
  Return the created task IDs and their titles.")

Task(subagent_type="general-purpose", model="haiku",
  prompt="Create these beads tasks under epic {{EPIC_2_ID}}:
  ...")
```

After sub-agents return IDs, set **cross-epic dependencies** yourself (requires
IDs from multiple sub-agents).

---

## Phase 1: Brainstorm & Explore

### Step 1: Understand the Codebase

Before designing anything, delegate exploration to sub-agents (see Sub-Agent
Strategy above). Do NOT read source code directly into your context.

Launch parallel background agents to:

1. **Scan the codebase** — architecture, relevant packages, existing patterns
2. **Read reference docs** — `{{REFERENCE_DOCS}}` for conventions to follow
3. **Check beads** — `bd list`, `bd ready`, `bd blocked` for related or
   overlapping work

After agents complete, read their output files in `output/planning/`. Use these
summaries — not raw source — to inform your design.

### Step 2: Clarify Requirements

Ask the user focused questions (one at a time, prefer multiple choice):

- What problem does this solve? Who benefits?
- What are the hard constraints? (performance, compatibility, etc.)
- What's explicitly out of scope?
- Are there existing patterns in the codebase to follow or break from?

Do not ask questions that can be answered by reading the codebase. Only ask when
requirements are genuinely ambiguous.

### Step 3: Propose Approaches

Present 2-3 approaches with trade-offs. For each approach, cover:

- **Architecture summary** (1-2 sentences)
- **Key files/packages affected**
- **Estimated complexity** (number of tasks/epics)
- **Trade-offs** (pros/cons)
- **Your recommendation and why**

Lead with your recommended approach. Get user buy-in before proceeding.

---

## Phase 2: Write Design Spec

Write the validated design to `{{DESIGN_DOC_DIR}}/YYYY-MM-DD-<topic>-design.md`.

### Spec Structure

The design doc should include:

1. **Overview** — What this delivers (2-3 sentences)
2. **Context** — Why this matters, what exists today
3. **Architecture** — Components, data flow, key interfaces
4. **Implementation Details** — Per-component breakdown with:
   - File paths and package structure
   - Key function signatures or interfaces
   - Data models / schemas
   - Error handling strategy
5. **Dependencies** — What this depends on, what depends on it
6. **Testing Strategy** — Unit, integration, e2e approach
7. **Out of Scope** — Explicitly excluded items

### Guidelines

- Be specific enough that an implementing agent can work from it
- Include code signatures where the interface design matters
- Reference existing code patterns by file path
- Keep it under 2000 words unless complexity demands more
- Commit the design doc to git after writing

---

## Phase 3: Create Beads Epics & Tasks

### Step 1: Identify Epics

Break the design into epics. Each epic should:

- Represent a cohesive, independently deliverable unit of work
- Be completable in 1-3 agent sessions
- Have clear boundaries (a single worktree/branch per epic, or shared with
  explicit file ownership rules)
- Map to a logical layer or component from the design spec

**Naming convention:** Descriptive, concise titles in imperative form. Examples:
"Implement Sync Protocol", "Build Agent Session Manager", "Create WebSocket
Bridge"

### Step 2: Create Epics in Beads

```bash
# Create each epic
bd epic create --title="{{EPIC_TITLE}}"

# If epics have ordering dependencies, add them
bd dep add {{LATER_EPIC_ID}} {{EARLIER_EPIC_ID}}
```

### Step 3: Create Tasks Under Each Epic

For each epic, create ordered tasks. Tasks should:

- Be sequenced so earlier tasks enable later ones (foundations first)
- Group into priority tiers when useful:
  - **P0: Foundation** — Must complete first, enables everything else
  - **P1: Core** — Main implementation work
  - **P2: Polish** — Verification, docs, cleanup
- Include enough detail in the **beads task description** for an implementing
  agent to work autonomously. The task description is the source of truth, not
  the prompt.

```bash
# Create tasks as children of the epic
bd create --title="{{TASK_TITLE}}" --type=task --priority=2 \
  --description="{{DETAILED_DESCRIPTION}}"
bd dep add {{TASK_ID}} {{EPIC_ID}}  # Link task to epic

# Set task ordering dependencies within the epic
bd dep add {{LATER_TASK_ID}} {{EARLIER_TASK_ID}}
```

**Tip:** When creating > 6 tasks, use the Bulk Task Creation Pattern from the
Sub-Agent Strategy section to parallelize creation across epics.

### Task Description Format (TDD-Quality)

Every beads task description is a **self-contained implementation guide**. An
agent reading only the task description (plus the design doc reference) should
be able to implement the task without additional context.

**Required sections in every task description:**

````markdown
## Files

- Create: `exact/path/to/new_file.go`
- Modify: `exact/path/to/existing.go` (add XyzService interface)
- Test: `exact/path/to/new_file_test.go`

## Steps

### Step 1: Write the failing test

```go
func TestSpecificBehavior(t *testing.T) {
    result := Function(input)
    assert.Equal(t, expected, result)
}
```

### Step 2: Verify test fails

Run: `go test ./path/to/package/... -run TestSpecificBehavior -v` Expected: FAIL
— "Function not defined"

### Step 3: Implement

```go
func Function(input Type) ReturnType {
    // implementation
}
```

### Step 4: Verify test passes

Run: `go test ./path/to/package/... -run TestSpecificBehavior -v` Expected: PASS

### Step 5: Commit

```bash
git add path/to/files
git commit -m "feat(module): add specific feature"
```

## Acceptance Criteria

- [ ] All tests pass: `{{QUALITY_COMMANDS}}`
- [ ] Function handles edge case X
- [ ] Error returns are typed, not generic
````

### Task Detail Calibration

Adjust **code completeness** based on task type, but always include the full
structure above:

| Task Type            | Code in Steps            | Test Detail                                   |
| -------------------- | ------------------------ | --------------------------------------------- |
| API/Interface design | Full signatures + types  | Contract tests, error case tests              |
| Business logic       | Full implementation code | TDD: failing test → implement → pass          |
| Integration/Wiring   | Connection code + config | Integration test against mock/real dependency |
| UI/Styling           | Full CSS/component code  | Visual verification steps, screenshot check   |
| Testing-only         | N/A                      | Full test code with scenarios and edge cases  |
| Documentation        | N/A (outline only)       | Verification: doc renders, links work         |

**Granularity rule:** Each task should be completable in one focused session
(30-90 minutes). If a task has more than 5 steps, split it into multiple tasks.

**Code rule:** Prefer complete code over pseudocode. "Add validation" is not a
step — `if err := validate(input); err != nil { return fmt.Errorf(...) }` is.

### Step 4: Set Dependencies Between Epics

Map the dependency DAG:

```bash
# Example: Epic 2 depends on Epic 1
bd dep add {{EPIC_2_ID}} {{EPIC_1_ID}}

# Visualize blocked work
bd blocked
```

### Step 5: Verify the Plan

Before finishing, validate:

- [ ] Every task has a clear title and detailed description
- [ ] Task ordering within each epic makes sense (foundations first)
- [ ] Epic dependencies reflect the actual build order
- [ ] No circular dependencies (`bd blocked` should be clean)
- [ ] Each epic can be assigned to one worktree/branch
- [ ] Total scope is realistic (flag if > 20 tasks per epic)
- [ ] Design doc is committed to git

---

## Phase 4: Generate Implementation Prompts

For each epic (or group of related epics sharing a worktree), generate an
implementation prompt by filling in the `implementation-agent.md` template.

### Step 1: Determine Worktree Assignments

Map epics to worktrees/branches:

```bash
# Check existing worktrees
git worktree list

# Plan assignments:
# Epic A ({{EPIC_A_ID}}) → branch: feature/{{FEATURE_A}} → worktree: {{WORKTREE_A}}
# Epic B ({{EPIC_B_ID}}) → branch: feature/{{FEATURE_B}} → worktree: {{WORKTREE_B}}
```

Independent epics get separate worktrees. Related epics sharing files can share
a worktree with file ownership rules.

### Step 2: Fill Implementation Template

For each worktree assignment, create a prompt file at
`dev-docs/prompts/{feature-name}.md` by filling in `implementation-agent.md`
placeholders:

| Placeholder            | Source                               |
| ---------------------- | ------------------------------------ |
| `{{EPIC_ID}}`          | The beads epic ID from Phase 3       |
| `{{WORKTREE_PATH}}`    | From worktree assignment above       |
| `{{BRANCH_NAME}}`      | Branch name for this epic            |
| `{{DESIGN_DOC}}`       | Path to the design spec from Phase 2 |
| `{{REFERENCE_CODE}}`   | Relevant reference implementations   |
| `{{QUALITY_COMMANDS}}` | Project-specific test/lint commands  |
| `{{COVERAGE_TARGET}}`  | Coverage threshold (e.g., >80%)      |
| `{{AGENT_NAME}}`       | Unique name (e.g., `impl-{feature}`) |

### Step 3: Write the Prompt File

Save the filled-in template to `dev-docs/prompts/{feature-name}.md`. Add a
header with context the implementing agent needs:

```markdown
# Implementation Prompt: {{FEATURE_NAME}}

> Generated by planning agent on YYYY-MM-DD Design doc: {{DESIGN_DOC}} Epic:
> {{EPIC_ID}} (N tasks)

## Quick Context

<!-- 2-3 sentences about what this epic delivers and key architectural decisions -->

## Worktree Setup

<!-- Command to create/prepare the worktree -->

./scripts/setup-worktree-thrum.sh {{WORKTREE_PATH}} {{BRANCH_NAME}} \
 --identity {{AGENT_NAME}} --role implementer

## Implementation Agent Template

<!-- The filled-in implementation-agent.md follows below -->
```

### Step 4: Commit Prompt Files

```bash
git add dev-docs/prompts/
git commit -m "plan: add implementation prompts for {{FEATURE_NAME}}"
```

---

## Output Summary

When complete, the planning agent should have produced:

1. **Design spec** at `{{DESIGN_DOC_DIR}}/YYYY-MM-DD-<topic>-design.md`
2. **Beads epics** with dependency relationships
3. **Beads tasks** under each epic with TDD-quality descriptions (file paths,
   test code, implementation code, verification commands, acceptance criteria)
4. **Dependency DAG** showing build order
5. **Implementation prompt(s)** at `dev-docs/prompts/{feature-name}.md` — filled
   from `implementation-agent.md` template with all placeholders resolved
6. **All artifacts committed** to git

The implementing agent uses the beads tasks as its source of truth. The prompt
file provides worktree setup, design doc reference, and quality commands. The
task descriptions provide the step-by-step implementation guide.
