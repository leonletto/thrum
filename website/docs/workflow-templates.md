---
title: "Workflow Templates"
description:
  "Four-phase skill pipeline for designing, planning, setting up, and
  implementing features with AI agents"
category: "guides"
order: 3
tags:
  ["templates", "workflow", "planning", "implementation", "agents", "toolkit"]
last_updated: "2026-03-03"
---

## Workflow Templates

Thrum ships ready-to-use templates and skills that guide AI agents through
designing, planning, setting up, and implementing features. The workflow is
driven by a **skill pipeline** — brainstorming, writing-plans, and project-setup
— with templates available as reference and customization files.

Templates live in `toolkit/templates/agent-dev-workflow/` in the Thrum
repository.

## The four-phase skill pipeline

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

**Design** — Explore codebase, propose approaches, write design spec
interactively with the user.

**Plan** — Structure the approved design into phased, ordered implementation
steps with task-ID anchors.

**Setup** — Decompose the plan into beads epics and tasks, select or create git
worktrees, generate filled implementation prompts.

**Implement** — Work through tasks in dependency order: claim → read → implement
→ test → commit → close. Orient phase reads beads status and git history for
resume after context loss.

## The Two-Artifact System

Implementation agents work from two complementary artifacts per task:

| Artifact              | Contains                                       | Authoritative For                   |
| --------------------- | ---------------------------------------------- | ----------------------------------- |
| **Beads task**        | Acceptance criteria, deps, status              | What must be true to close the task |
| **Plan file section** | Step-by-step code, file paths, verify commands | How to implement the task           |

Agents read `bd show {TASK_ID}` first (what to achieve), then search the plan
file for `## Task: {BEAD_ID}` (how to get there).

Additional context layers:

| Artifact           | Purpose                                                           |
| ------------------ | ----------------------------------------------------------------- |
| **Design doc**     | Architecture decisions — WHY things are built a certain way       |
| **Philosophy doc** | Anti-patterns and red flags — what agents must NOT do             |
| **Filled prompt**  | Epic-specific overrides, scoped quality commands, cross-epic deps |

## Install the templates

Copy the template set into your project documentation:

```bash
cp toolkit/templates/agent-dev-workflow/*.md your-project/docs/templates/
```

Or reference them directly from the Thrum repo when starting a new agent
session.

## Template files

| Template                  | Status    | Purpose                                                                                       |
| ------------------------- | --------- | --------------------------------------------------------------------------------------------- |
| `implementation-agent.md` | Active    | Prompt template filled by project-setup skill, given to implementation agents                 |
| `philosophy-template.md`  | Active    | Reusable anti-patterns template — used by project-setup when a project lacks a philosophy doc |
| `planning-agent.md`       | Reference | Full planning template — superseded by brainstorming + writing-plans + project-setup skills   |
| `worktree-setup.md`       | Reference | Worktree creation docs — superseded by project-setup Phase 3 + using-git-worktrees skill      |
| `CLAUDE.md`               | Reference | Overview of the workflow and how templates fit together                                       |

## Customize the placeholders

Templates use `{{PLACEHOLDER}}` syntax for project-specific values. The
project-setup skill resolves these automatically, but you can also fill them
manually.

### Implementation agent placeholders

```bash
{{EPIC_ID}}          → bd-a3f8
{{WORKTREE_PATH}}    → ~/.workspaces/myproject/auth
{{BRANCH_NAME}}      → feature/auth
{{DESIGN_DOC}}       → /abs/path/to/dev-docs/plans/2026-02-auth-design.md
{{PLAN_FILE}}        → /abs/path/to/dev-docs/plans/2026-02-auth-plan.md
{{REFERENCE_CODE}}   → .ref/example_auth_impl/
{{QUALITY_COMMANDS}} → go test ./internal/auth/... && golangci-lint run
{{COVERAGE_TARGET}}  → >80%
{{AGENT_NAME}}       → impl-auth
{{ANTI_PATTERNS}}    → (generated from design doc + philosophy doc)
{{CROSS_EPIC_DEPS}}  → (from cross-epic dependency map, or "No cross-epic dependencies.")
```

**Important:** Use absolute paths for `{{DESIGN_DOC}}` and `{{PLAN_FILE}}` —
these files typically live in gitignored directories and worktree agents cannot
resolve relative paths to them.

### Example: Fill in for a real project

You're building authentication for a Go service:

```bash
{{EPIC_ID}}          → bd-k7m2
{{WORKTREE_PATH}}    → ~/.workspaces/myservice/auth
{{BRANCH_NAME}}      → feature/auth-jwt
{{DESIGN_DOC}}       → /home/user/myservice/dev-docs/plans/2026-02-jwt-auth.md
{{PLAN_FILE}}        → /home/user/myservice/dev-docs/plans/2026-02-jwt-auth-plan.md
{{QUALITY_COMMANDS}} → go test ./internal/auth/... -v
{{COVERAGE_TARGET}}  → >85%
{{AGENT_NAME}}       → impl-auth-jwt
```

Save the filled-in template as a file or paste it directly into your agent's
prompt.

## Use the implementation template

Give a filled `implementation-agent.md` to an implementation agent when an epic
has tasks and a worktree is ready.

**Orient** — Check beads status and git state to find starting point (works for
fresh starts and resumes).

**Implement** — Work through tasks: claim → read → implement → test → commit →
close.

**Verify** — Run full quality gates after all tasks complete.

**Land** — Close epic, push branch, notify coordinator for review and merge.

After context loss, restart with the same filled-in template. Orient re-runs and
picks up from the first incomplete task. Completed work is never redone.

## Use the philosophy template

The `philosophy-template.md` defines implementation standards for your project —
anti-patterns, red flags, and BAD/GOOD code examples. The project-setup skill
checks for a philosophy doc in Phase 1 and offers to create one from this
template if none exists.

Philosophy docs are injected into each epic's implementation prompt as the
`{{ANTI_PATTERNS}}` section, enabling the verifier sub-agent pattern to check
for architectural violations beyond just "tests pass."

## Worktree setup

The project-setup skill handles worktree selection and setup in Phase 3. The
`setup-worktree-thrum.sh` script handles full worktree bootstrapping in a single
command:

```bash
# Full worktree setup with identity
./scripts/setup-worktree-thrum.sh ~/.workspaces/myproject/auth feature/auth \
  --identity impl-auth \
  --role implementer \
  --base thrum-dev
```

This creates the worktree, sets up thrum and beads redirects, and registers the
agent identity.

## See also

- [Agent Configuration](agent-configs.md) — How to configure agents for
  autonomous operation
- [Beads and Thrum Integration](beads-and-thrum.md) — How the workflow uses
  beads for task tracking and Thrum for coordination
- [Quick Start](quickstart.md) — Set up Thrum and run your first multi-agent
  workflow
