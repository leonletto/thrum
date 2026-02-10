# Planning Agent Template

> **Note:** This is a distributable template. Fill in the `{{PLACEHOLDER}}` values with your project-specific information before using.

## Purpose

Guide an agent through the full planning lifecycle: brainstorm a feature, write
a design spec, then decompose it into beads epics and detailed tasks.

## Inputs Required

- `{{FEATURE_DESCRIPTION}}` — What the user wants to build (can be rough)
- `{{PROJECT_ROOT}}` — Absolute path to the project root
- `{{DESIGN_DOC_DIR}}` — Where design docs live (e.g., `docs/plans/`)
- `{{REFERENCE_DOCS}}` — Existing specs, designs, or reference code to consider
- `{{TECH_STACK}}` — Languages, frameworks, and tools in use

---

## Phase 1: Brainstorm & Explore

### Step 1: Understand the Codebase

Before designing anything, explore the existing project:

1. Read the project's `CLAUDE.md` and any agent instructions in `.agents/`
2. Use `auggie-mcp codebase-retrieval` to understand the current architecture
3. Review `{{REFERENCE_DOCS}}` for existing patterns and conventions
4. Check `bd list` for related or overlapping work already tracked
5. Identify existing code that this feature touches or extends

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

### Task Detail Calibration

Adjust detail level in the task description based on task nature:

| Task Type            | Detail Level             | Include in Description                        |
| -------------------- | ------------------------ | --------------------------------------------- |
| API/Interface design | Signatures + contracts   | Function signatures, types, error cases       |
| Business logic       | Algorithms + edge cases  | Pseudocode or code examples, test scenarios   |
| UI/Styling           | Full implementation code | CSS/component code, visual verification steps |
| Integration/Wiring   | Connection points        | Which modules connect, data flow, config      |
| Testing              | Test plan + scenarios    | Test categories, edge cases, coverage targets |
| Documentation        | Outline + locations      | What docs, where they go, what to cover       |

Every task description should include:

- **File paths** to create or modify
- **Acceptance criteria** — how to verify the task is done
- **Verification commands** — what to run (e.g., `make test`, `npm run test`)

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

## Output Summary

When complete, the planning agent should have produced:

1. **Design spec** at `{{DESIGN_DOC_DIR}}/YYYY-MM-DD-<topic>-design.md`
2. **Beads epics** with dependency relationships
3. **Beads tasks** under each epic with detailed descriptions
4. **Dependency DAG** showing build order

The implementing agent uses the beads tasks as its source of truth — not this
prompt template.
