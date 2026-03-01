---
name: thrum-role-config
description: Use when the user wants to create, audit, or update .thrum/role_templates by detecting repo/team context and generating role preambles with explicit autonomy and scope boundaries.
# source: claude-plugin/skills/configure-roles/SKILL.md (condensed for codex)
# last-synced: 2026-03-01
---

# Thrum Role Config

Generate role-based preamble templates customized to your project environment.
Templates auto-apply when agents register via `thrum quickstart`.

**Announce:** "Using thrum-role-config to set up preamble templates for your
team."

## Use this when
- Team roles need to be initialized for Thrum agents.
- Role templates exist but must be adjusted.
- Environment changes (worktrees, runtimes, skills, tooling) require template updates.

## Phase 1: Detect Environment

Run these commands and collect the output (suppress errors):

```bash
thrum runtime list 2>/dev/null          # Installed runtimes
git worktree list 2>/dev/null           # Worktrees and branches
bd stats 2>/dev/null                    # Beads task tracker state
thrum agent list --context 2>/dev/null  # Registered agents
thrum config show 2>/dev/null           # Thrum configuration
ls .thrum/role_templates/ 2>/dev/null   # Existing templates
```

Also check `toolkit/templates/roles/` for shipped example templates.

## Phase 2: Report Findings

Summarize what you detected:

- Runtimes installed
- Worktrees and branches in use
- Whether beads is configured
- Current team composition (agents, roles, modules)
- Available tooling and skills
- Existing role templates (if re-running)

## Phase 3: Check for Existing Templates

```bash
ls .thrum/role_templates/ 2>/dev/null
```

If templates exist, show them and ask what to change. Do NOT start from scratch.

## Phase 4: Ask Questions

Ask these in sequence:

### 4a: Team Structure

"What roles does your team need?"

Options based on detected agents, plus common defaults:

- coordinator, implementer (most common)
- coordinator, implementer, planner
- coordinator, implementer, researcher
- Custom set

### 4b: Autonomy Level Per Role

For each role selected, ask: "What autonomy level for the {role} role?"

- **Strict** — waits for coordinator instruction, limited scope
- **Autonomous** — can self-assign tasks, broader scope

### 4c: Scope Rules (optional)

If multiple worktrees detected: "Should agents be restricted to their own
worktree?"

- Yes (strict scope boundaries)
- No (can read across worktrees)

## Phase 5: Generate Templates

For each role:

1. Read the shipped example from `toolkit/templates/roles/{role}-{autonomy}.md`
2. Customize based on environment detection:
   - If beads detected: include `bd` commands in Task Tracking section
   - If MCP servers detected: add to Efficiency section
   - If specific skills detected: reference them
   - If worktree restrictions: adjust Scope Boundaries
3. Write to `.thrum/role_templates/{role}.md`

## Phase 6: Offer Deploy

If agents are already registered:

```bash
thrum roles deploy --dry-run    # Preview
thrum roles deploy              # Apply to all agents
thrum roles list                # Verify
```

## Re-run Behavior

When `.thrum/role_templates/` already has files:

1. Show existing templates with a summary of each
2. Ask what to change (add role, modify existing, remove)
3. Only regenerate requested templates
4. Offer deploy for changes

## Environment-Specific Customizations

| Detected             | Template Customization                                   |
| -------------------- | -------------------------------------------------------- |
| Codex runtime        | Add sub-agent guidance to Efficiency section             |
| Augment runtime      | Add codebase-retrieval to Efficiency section             |
| Beads installed      | Add `bd` commands to Task Tracking, disable TodoWrite    |
| Thrum MCP server     | Add MCP tool references, CLI fallback for sub-agents     |
| Multiple worktrees   | Add worktree scope rules to Scope Boundaries             |

## Output contract
- Template files in `.thrum/role_templates/<role>.md`
- Short summary of generated/updated roles and autonomy mode
- Deploy recommendation and command results when deploy is requested

