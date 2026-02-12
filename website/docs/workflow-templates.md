---
title: "Workflow Templates"
description: "Three-phase development templates for planning, preparing, and implementing features with AI agents"
category: "guides"
order: 3
tags: ["templates", "workflow", "planning", "implementation", "agents", "toolkit"]
last_updated: "2026-02-12"
---

# Workflow Templates

Thrum ships ready-to-use templates that guide AI agents through planning, preparing, and implementing features. You copy them into your project and customize the placeholders for your environment.

Templates live in `toolkit/templates/` in the Thrum repository.

## The three-phase workflow

```
1. PLAN              2. PREPARE            3. IMPLEMENT
─────────────        ──────────────        ─────────────────────
Brainstorm           Select/create         Orient from beads
Write spec           worktree              Implement tasks
Create epics         Setup thrum +         Test → commit → close
Create tasks         beads redirect        Quality gates
Write preambles      Run quickstart        Merge to main
Set deps             w/ --preamble
```

**Plan** — Explore codebase, propose approaches, write design spec, decompose into beads epics with detailed task descriptions. Create per-agent preamble files.

**Prepare** — Select or create git worktree, configure thrum and beads redirects so all worktrees share the same daemon and issue database. Bootstrap agent identity with preamble.

**Implement** — Work through tasks in dependency order: claim → read → implement → test → commit → close. Orient phase reads beads status and git history for resume after context loss. Preamble persists project rules across sessions.

## Install the templates

Copy the templates into your project documentation:

```bash
cp toolkit/templates/*.md your-project/docs/templates/
```

Or reference them directly from the Thrum repo when starting a new agent session.

## Customize the placeholders

Templates use `{{PLACEHOLDER}}` syntax for project-specific values. Replace these before giving a template to an agent.

### Planning agent placeholders

```bash
{{FEATURE_DESCRIPTION}} → "Add WebSocket sync between agents"
{{PROJECT_ROOT}}        → /home/user/projects/myproject
{{DESIGN_DOC_DIR}}      → docs/plans/
{{REFERENCE_DOCS}}      → .ref/example_project/, dev-docs/architecture.md
{{TECH_STACK}}          → Go backend, React/TypeScript UI, SQLite, JSONL
```

### Worktree setup placeholders

```bash
{{PROJECT_ROOT}}  → /home/user/projects/myproject
{{WORKTREE_BASE}} → ~/.workspaces/myproject
{{FEATURE_NAME}}  → auth
```

### Preamble placeholders

```bash
{{AGENT_NAME}}                → impl-auth
{{AGENT_ROLE}}                → implementer
{{AGENT_MODULE}}              → auth
{{WORKTREE_BRANCH}}           → feature/auth
{{DESIGN_DOC}}                → dev-docs/plans/2026-02-12-auth-design.md
{{OWNED_PACKAGES}}            → internal/auth/, internal/middleware/
{{QUALITY_COMMANDS}}          → go test ./internal/auth/... -count=1 -race
{{ARCHITECTURAL_CONSTRAINTS}} → Key design decisions
{{COORDINATION_NOTES}}        → Agent interaction rules
```

### Implementation agent placeholders

```bash
{{EPIC_ID}}          → bd-a3f8
{{WORKTREE_PATH}}    → ~/.workspaces/myproject/auth
{{BRANCH_NAME}}      → feature/auth
{{DESIGN_DOC}}       → docs/plans/2026-02-auth-design.md
{{REFERENCE_CODE}}   → .ref/example_auth_impl/
{{QUALITY_COMMANDS}} → make test && make lint
{{COVERAGE_TARGET}}  → >80%
{{AGENT_NAME}}       → impl-auth
```

### Example: Fill in for a real project

You're building authentication for a Go service. Here's how you customize the implementation template:

```bash
{{EPIC_ID}}          → bd-k7m2
{{WORKTREE_PATH}}    → ~/.workspaces/myservice/auth
{{BRANCH_NAME}}      → feature/auth-jwt
{{DESIGN_DOC}}       → docs/plans/2026-02-jwt-auth.md
{{REFERENCE_CODE}}   → .ref/gorilla-sessions-example/
{{QUALITY_COMMANDS}} → go test ./... && golangci-lint run
{{COVERAGE_TARGET}}  → >85%
{{AGENT_NAME}}       → impl-auth-jwt
```

Save the filled-in template as a file or paste it directly into your agent's prompt.

## Use the planning template

Give `planning-agent.md` to a planning agent when you have a feature idea and need actionable tasks.

**Produces:** Design spec, beads epics, detailed task descriptions, dependency relationships, per-agent preamble files.

Planning agents front-load detail into task descriptions so implementation agents work autonomously without conversation history.

### Use the preamble template

The planning agent uses `preamble-agent.md` to create persistent, project-specific instructions for each implementation agent. These preambles persist across all sessions, providing agent identity, ownership scope, architectural constraints, and coordination rules.

**Creates:** Per-agent preamble files saved to `dev-docs/prompts/{agent-name}-preamble.md`.

**Contains:** Agent identity, ownership scope (which files/packages the agent owns), architectural constraints, coding patterns, quality gates, and coordination notes.

Preambles are automatically prepended when showing context via `thrum context show`. They act as a stable middle layer between the session prompt (volatile) and the implementation template (given once per agent lifecycle). See [Agent Context Management](context.md) for the three-layer context model.

## Use the worktree setup guide

Follow `worktree-setup.md` when an epic needs an isolated workspace.

**Does:** Check existing worktrees for reuse, create new worktree + branch if needed, configure thrum and beads redirects, bootstrap agent identity with preamble, verify with `bd where`, `bd ready`, and `thrum context show`.

Without redirect configuration, agents in different worktrees see different tasks and different daemon instances.

### Enhanced setup script

The `setup-worktree-thrum.sh` script now supports full worktree bootstrapping with identity, role, module, and preamble in a single command:

```bash
# Full worktree setup with identity and preamble
./scripts/setup-worktree-thrum.sh ~/.workspaces/myproject/auth feature/auth \
  --identity impl-auth \
  --role implementer \
  --module auth \
  --preamble dev-docs/prompts/impl-auth-preamble.md
```

This creates the worktree, sets up redirects, registers the agent, and installs the preamble. The `thrum quickstart` command (used internally) now auto-creates empty context files and composes the default thrum preamble with custom content from `--preamble-file`.

## Use the implementation template

Give `implementation-agent.md` to an implementation agent when an epic has tasks and a worktree is ready.

**Orient** — Check beads status and git state to find starting point (works for fresh starts and resumes).

**Implement** — Work through tasks: claim → read → implement → test → commit → close.

**Verify** — Run full quality gates after all tasks complete.

**Land** — Close epic, merge to main, push.

After context loss, restart with the same filled-in template. Orient re-runs and picks up from the first incomplete task. Completed work is never redone.

## See also

- [Agent Configuration](agent-configs.md) — How to configure agents for autonomous operation
- [Beads and Thrum Integration](beads-and-thrum.md) — How the workflow uses beads for task tracking and Thrum for coordination
- [Quick Start](quickstart.md) — Set up Thrum and run your first multi-agent workflow
