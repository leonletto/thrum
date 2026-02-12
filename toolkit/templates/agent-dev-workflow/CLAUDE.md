# Agent Development Templates

## Process Overview

These templates encode a three-phase workflow for planning and executing feature
work with AI agents. The process uses **beads** for issue tracking and **git
worktrees** for isolated development.

```
1. PLAN          2. PREPARE          3. IMPLEMENT
─────────────    ──────────────      ─────────────────────
Brainstorm       Select/create       Orient from beads
  ↓              worktree            Pick up first task
Write spec         ↓                 Implement → test → commit
  ↓              Setup thrum +       Close task, repeat
Create epics     beads redirect        ↓
Create tasks       ↓                 Quality gates
Write preambles  Run quickstart      Merge to main, push
Set deps         w/ --preamble
```

### Phase 1: Plan (`planning-agent.md`)

**When:** You have a feature idea (rough or detailed) and need to turn it into
actionable work.

**What it does:**

1. Explores the codebase and clarifies requirements with the user
2. Proposes 2-3 architectural approaches with trade-offs
3. Writes a design spec to `{{DESIGN_DOC_DIR}}`
4. Decomposes the spec into beads epics with detailed child tasks
5. Creates preamble files for each implementation agent (`preamble-agent.md`)
6. Sets dependency relationships between epics and between tasks

**Key principle:** Beads task descriptions are the source of truth. The planning
agent front-loads detail into task descriptions so implementing agents can work
autonomously.

### Phase 2: Prepare (`worktree-setup.md`)

**When:** An epic is ready to implement and needs an isolated workspace.

**What it does:**

The setup script (`scripts/setup-worktree-thrum.sh`) handles everything in a
single command:

1. Creates git branch + worktree (or reuses existing branch)
2. Sets up thrum redirect (shared daemon, messages, identities)
3. Sets up beads redirect (shared issue database)
4. **If `--identity` is provided:** runs `thrum quickstart` to register the
   agent identity, auto-creates empty context file (`.thrum/context/{name}.md`),
   and creates default preamble (or composes default + custom if `--preamble`
   is provided)

```bash
# Full single-command bootstrap:
./scripts/setup-worktree-thrum.sh <worktree-path> <branch> \
  --identity <name> --role <role> --module <module> \
  --preamble <preamble-file> --base <base-branch>

# Backwards-compatible modes still work:
./scripts/setup-worktree-thrum.sh                    # auto-detect all worktrees
./scripts/setup-worktree-thrum.sh <existing-path>    # redirect-only for existing worktree
```

**Key principle:** All worktrees MUST share a single beads database and thrum
instance via redirect files. Never set up redirects manually — use the script.

**Note:** `thrum quickstart` automatically creates an empty context file and a
default preamble on first registration. If the preamble already exists, it is
preserved (idempotent). If `--preamble-file` is provided, quickstart composes
`DefaultPreamble() + \n---\n\n + custom content` and **overwrites** any existing
preamble.

### Phase 3: Implement (`implementation-agent.md`)

**When:** An epic exists in beads with tasks, and a worktree is ready.

**What it does:**

1. **Orient** — Checks beads status + git state to find the starting point
   (works identically for fresh starts and resumes)
2. **Implement** — Works through tasks in dependency order: claim → read → build
   → test → commit → close
3. **Verify** — Runs full quality gates after all tasks complete
4. **Land** — Closes the epic, merges to main, pushes

**Key principle:** The orient phase is always the entry point. After context
loss (compaction, new session), the agent re-runs orient and picks up exactly
where it left off. Completed work is never redone.

---

## How to Use These Templates

### Filling in Placeholders

All templates use `{{PLACEHOLDER}}` syntax for project-specific values. Replace
these before giving a template to an agent.

**Planning agent placeholders:**

| Placeholder               | Example                                                   |
| ------------------------- | --------------------------------------------------------- |
| `{{FEATURE_DESCRIPTION}}` | "Add real-time sync between agents via WebSocket"         |
| `{{PROJECT_ROOT}}`        | `{{PROJECT_ROOT}}`                                        |
| `{{DESIGN_DOC_DIR}}`      | `docs/plans/`                                             |
| `{{REFERENCE_DOCS}}`      | `.ref/reference_impl/`, `docs/design.md`                  |
| `{{TECH_STACK}}`          | "Go backend, React/TypeScript UI, SQLite, JSONL"          |

**Worktree setup placeholders:**

| Placeholder         | Example              |
| ------------------- | -------------------- |
| `{{PROJECT_ROOT}}`  | `{{PROJECT_ROOT}}`   |
| `{{WORKTREE_BASE}}` | `{{WORKTREE_BASE}}`  |
| `{{FEATURE_NAME}}`  | `auth`               |

**Preamble placeholders** (role/project-level — no feature-specific content):

| Placeholder                    | Example                                          |
| ------------------------------ | ------------------------------------------------ |
| `{{PROJECT_NAME}}`             | `Thrum`                                          |
| `{{AGENT_ROLE}}`               | `implementer`                                    |
| `{{TECH_STACK}}`               | `Go, TypeScript/React, SQLite, JSONL`            |
| `{{PROJECT_CONVENTIONS}}`      | Coding patterns, error handling, testing approach |
| `{{GENERAL_QUALITY_COMMANDS}}` | `go test ./... -count=1 -race && go vet ./...`   |
| `{{COMMUNICATION_PROTOCOL}}`   | When/how to use thrum messaging                  |

**Implementation agent placeholders:**

| Placeholder            | Example                                      |
| ---------------------- | -------------------------------------------- |
| `{{EPIC_ID}}`          | `project-nf7`                                |
| `{{WORKTREE_PATH}}`    | `{{WORKTREE_BASE}}/foundation`               |
| `{{BRANCH_NAME}}`      | `feature/foundation`                         |
| `{{DESIGN_DOC}}`       | `docs/plans/2026-02-03-foundation-design.md` |
| `{{REFERENCE_CODE}}`   | `.ref/reference_implementation/`             |
| `{{QUALITY_COMMANDS}}` | `make test && make lint`                     |
| `{{COVERAGE_TARGET}}`  | `>80%`                                       |

### Typical Workflow

```bash
# 1. PLAN — Run in main worktree (or any worktree)
#    Give the planning-agent.md template to your planning agent
#    with placeholders filled in. It will:
#    - Brainstorm and write a spec
#    - Create beads epics and tasks
#    - Create role preamble if needed (preamble-agent.md template)
#    - Write an implementation prompt for each agent/worktree

# 2. PREPARE — Single command creates everything:
#    branch, worktree, thrum redirect, beads redirect, identity,
#    empty context file, and composed preamble.
./scripts/setup-worktree-thrum.sh {{WORKTREE_BASE}}/{{FEATURE_NAME}} \
  feature/{{FEATURE_NAME}} \
  --identity impl-{{FEATURE_NAME}} \
  --role implementer \
  --module {{FEATURE_NAME}} \
  --preamble docs/preambles/implementer-preamble.md

# The script prints a verification summary showing all created paths:
#   Path, Branch, Thrum redirect, Beads redirect,
#   Identity file, Context file, Preamble file

# 3. IMPLEMENT — Hand off to implementation agent
#    Give the implementation-agent.md template with placeholders
#    filled in. It will work through tasks autonomously.
#    If it runs out of context, restart it with the same prompt —
#    the orient phase recovers state from beads and git.
#    The preamble persists across sessions via thrum context.
```

### Running Multiple Epics in Parallel

If epics are independent (no dependency between them), they can run
simultaneously in separate worktrees:

1. Create one worktree per epic (or per group of related epics)
2. Give each implementation agent its own filled-in template
3. If two epics share a worktree, define **file ownership** in the prompt to
   avoid merge conflicts (see the "Parallel Work Rules" section in
   `worktree-setup.md`)

### Resuming After Context Loss

The implementation template is designed for resume. When an agent hits context
limits or a session ends:

1. Start a new session with the **same filled-in implementation-agent.md**
   prompt
2. The agent runs Phase 1 (Orient), which reads beads status and git history
3. It picks up from the first incomplete task — no work is duplicated

This works because beads tasks track completion status and git commits preserve
the code. The agent doesn't need conversation history to resume.

---

## Agent Context Layers

Each implementation agent has three layers of context, from most stable to most
volatile:

| Layer | File | Persistence | Content | Maintained By |
|-------|------|-------------|---------|---------------|
| Prompt | `docs/prompts/{feature}.md` | Given at session start | Feature-specific: epic IDs, owned packages, design doc, architectural constraints, quality commands scoped to this feature | Planning agent |
| Preamble | `.thrum/context/{name}_preamble.md` | Persists across features | Role and project-level: agent role, project conventions, general quality gates, communication protocol | Human (from `docs/preambles/`) |
| Context | `.thrum/context/{name}.md` | Updated each session | Volatile session state: current task, decisions made, blockers hit | `/update-context` skill |

The **preamble** is the stable base layer. It defines the agent's role and
project conventions — content that remains valid even when the worktree is reused
for a different feature. `thrum quickstart` auto-creates a default preamble
(thrum quick-reference commands) on every registration. When `--preamble-file`
is provided, the default content is composed with custom content separated by
`---`. Preambles are per-role (e.g., one `implementer-preamble.md` reused across
all implementer worktrees), stored in `docs/preambles/`. The identity file
records the source preamble path in its `context_file` field for traceability.

The **prompt** (implementation template) contains all feature-specific
instructions: which epic/tasks to implement, which packages to modify, design
doc references, feature-specific constraints, and scoped quality commands. It is
given directly to the agent at session start, not stored in thrum.

The **context** file is volatile — the `/update-context` skill rewrites it each
session with current state (active task, decisions, blockers). It is auto-created
as an empty file by `thrum quickstart` and populated at runtime by the skill.

## Source of Truth Hierarchy

| What                               | Lives In                                       | Used By                              |
| ---------------------------------- | ---------------------------------------------- | ------------------------------------ |
| Design decisions                   | Design spec (markdown in `{{DESIGN_DOC_DIR}}`) | Planning agent, implementation agent |
| Task details & acceptance criteria | Beads task descriptions                        | Implementation agent                 |
| Epic structure & dependencies      | Beads epic + `bd dep` relationships            | All agents                           |
| Implementation progress            | Beads task status + git commit history         | Implementation agent (orient phase)  |
| Role & project conventions         | Preamble (`.thrum/context/{name}_preamble.md`) | Implementation agent (every session) |
| Feature-specific instructions      | Prompt (`docs/prompts/{feature}.md`)           | Implementation agent (session start)   |
| Session state & decisions          | Context (`.thrum/context/{name}.md`)           | Implementation agent (current session) |
| Code                               | Git worktree                                   | Implementation agent                 |

The templates themselves are guides for how to use these sources — they don't
duplicate the content.

---

## Template Index

| Template | Purpose | Phase |
|----------|---------|-------|
| `planning-agent.md` | Brainstorm, spec, create epics & tasks | Plan |
| `preamble-agent.md` | Create per-role preamble files | Plan |
| `worktree-setup.md` | Create/select worktree, set up redirects | Prepare |
| `implementation-agent.md` | Implement tasks, verify, merge | Implement |
