# Agent Development Templates

These templates encode a workflow for planning and executing feature work with
AI agents. The workflow is now driven by a **skill pipeline** — brainstorming,
writing-plans, and project-setup — with the templates available as
reference/customization files for each stage.

## Process Overview

```text
1. DESIGN           2. PLAN              3. SETUP             4. IMPLEMENT
─────────────       ──────────────       ──────────────       ─────────────────────
brainstorming       writing-plans        project-setup        implementation-agent.md
  ↓                   ↓                    ↓                    ↓
Design doc          Plan file            Beads epics/tasks    Orient from beads
(interactive)       (interactive)        Worktree assignments Pick up first task
                                         Impl prompts         Implement → test → commit
                                         Create worktrees     Close task, repeat
```

---

## Skill Pipeline

All skills are invoked conversationally. The user works with Claude, invoking
each skill as the pipeline progresses.

### brainstorming

Interactive design exploration. Claude works with you to understand the problem
space, explore approaches, and reach a design decision.

Produces: a design doc at `docs/plans/YYYY-MM-DD-<topic>-design.md`

### writing-plans

Structures the approved design into phased, ordered implementation steps.
Produces a plan file that project-setup can consume.

Produces: a plan file at `docs/plans/YYYY-MM-DD-<topic>-plan.md`

### project-setup

Reads the plan and prepares everything needed for implementation:

1. Decomposes the plan into beads epics and tasks with a dependency DAG
2. Checks existing worktrees for reuse before creating new ones
3. Generates implementation prompts by filling in `implementation-agent.md` with
   feature-specific values
4. Creates worktrees and runs the setup script for each

Produces:

- Beads epics with dependency relationships
- Filled implementation prompts at `dev-docs/prompts/{feature}.md`
- Ready worktrees with thrum and beads redirects configured

### implementation-agent.md

Not a skill, but the template that project-setup fills in. The resulting filled
prompt is given to an implementation agent as its session start prompt. See the
[Implementation Phase](#implementation-phase) section below.

---

## Implementation Phase

Once project-setup has produced a filled prompt and a ready worktree, hand the
prompt to an implementation agent (a new Claude session in the worktree). The
agent runs four phases:

1. **Orient** — Reads beads status and git history to determine the starting
   point. Works identically for fresh starts and resumes after context loss.
2. **Implement** — Claims the first available task, reads its description,
   builds, tests, commits, closes the task. Repeats until all tasks are done.
3. **Verify** — Runs full quality gates after all tasks complete.
4. **Land** — Closes the epic, merges to main, pushes.

See `implementation-agent.md` for the full prompt and all details.

**Key principle:** The orient phase is always the entry point. After context
loss (compaction, new session), the agent re-runs orient and picks up exactly
where it left off. Completed work is never redone.

---

## Reference Templates

All templates live in `toolkit/templates/agent-dev-workflow/`. They can be used
directly for customization or when the skill pipeline does not fit your
workflow.

| Template                  | Status    | Purpose                                                                                     |
| ------------------------- | --------- | ------------------------------------------------------------------------------------------- |
| `planning-agent.md`       | Reference | Full planning template — superseded by brainstorming + writing-plans + project-setup skills |
| `worktree-setup.md`       | Reference | Worktree creation docs — superseded by project-setup Phase 4 + using-git-worktrees skill    |
| `implementation-agent.md` | Active    | Prompt template filled by project-setup skill, given to implementation agents               |

---

## Running Multiple Epics in Parallel

If epics are independent (no dependency between them), they can run
simultaneously in separate worktrees:

1. Create one worktree per epic (or per group of related epics)
2. Give each implementation agent its own filled-in template
3. If two epics share a worktree, define **file ownership** in the prompt to
   avoid merge conflicts (see the "Parallel Work Rules" section in
   `worktree-setup.md`)

## Resuming After Context Loss

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

Each implementation agent has two layers of context:

| Layer   | File                            | Persistence            | Content                                                                                             | Maintained By           |
| ------- | ------------------------------- | ---------------------- | --------------------------------------------------------------------------------------------------- | ----------------------- |
| Prompt  | `dev-docs/prompts/{feature}.md` | Given at session start | Feature-specific: epic IDs, owned packages, design doc, architectural constraints, quality commands | project-setup skill     |
| Context | `.thrum/context/{name}.md`      | Updated each session   | Volatile session state: current task, decisions made, blockers hit                                  | `/update-context` skill |

The **prompt** (implementation template) contains all feature-specific
instructions: which epic/tasks to implement, which packages to modify, design
doc references, feature-specific constraints, and scoped quality commands. It is
given directly to the agent at session start, not stored in thrum.

The **context** file is volatile — the `/update-context` skill rewrites it each
session with current state (active task, decisions, blockers). It is
auto-created as an empty file by `thrum quickstart` and populated at runtime by
the skill.

## Source of Truth Hierarchy

| What                               | Lives In                                 | Used By                                |
| ---------------------------------- | ---------------------------------------- | -------------------------------------- |
| Design decisions                   | Design doc (`docs/plans/*-design.md`)    | brainstorming, writing-plans           |
| Phased implementation steps        | Plan file (`docs/plans/*-plan.md`)       | project-setup skill                    |
| Task details & acceptance criteria | Beads task descriptions                  | Implementation agent                   |
| Epic structure & dependencies      | Beads epic + `bd dep` relationships      | All agents                             |
| Implementation progress            | Beads task status + git commit history   | Implementation agent (orient phase)    |
| Feature-specific instructions      | Prompt (`dev-docs/prompts/{feature}.md`) | Implementation agent (session start)   |
| Session state & decisions          | Context (`.thrum/context/{name}.md`)     | Implementation agent (current session) |
| Code                               | Git worktree                             | Implementation agent                   |

The templates themselves are guides for how to use these sources — they don't
duplicate the content.
