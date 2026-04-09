---
name: thrum-configure-roles
description:
  Detect environment and generate customized Thrum role templates for Codex
  agents.
# source: claude-plugin/skills/configure-roles/SKILL.md (adapted for codex)
---

# Thrum Configure Roles

Generate role-based preamble templates customized to the current project
environment. Templates auto-apply when agents register via `thrum quickstart`.

## Use this when

- Team roles need to be initialized for Thrum agents.
- Existing `.thrum/role_templates/` files must be audited or updated.
- Environment changes require refreshed autonomy or scope boundaries.

## Detect environment

Run these commands and collect the output:

```bash
thrum runtime list 2>/dev/null
git worktree list 2>/dev/null
bd stats 2>/dev/null
thrum agent list --context 2>/dev/null
thrum config show 2>/dev/null
ls .thrum/role_templates/ 2>/dev/null
```

Also inspect:

- `.codex/` or repo-local skill configuration that affects agent behavior
- `toolkit/templates/roles/` for shipped examples

## Report findings

Summarize:

- runtimes installed
- worktrees and branches
- whether beads is configured
- current team composition
- installed skills and relevant tools
- existing role templates, if any

## Update flow

1. If templates already exist, summarize them before changing anything.
2. Ask the user what roles and autonomy levels they want.
3. Read the shipped template from
   `toolkit/templates/roles/{role}-{autonomy}.md`.
4. Customize it for this repo’s tools, worktree rules, and messaging patterns.
5. Write the result to `.thrum/role_templates/{role}.md`.
6. Offer `thrum roles deploy --dry-run` or `thrum roles deploy` when agents are
   already registered.

## Environment-specific customizations

| Detected           | Template customization                              |
| ------------------ | --------------------------------------------------- |
| Codex runtime      | Add Codex-specific tool guidance to Efficiency      |
| Beads installed    | Add `bd` commands and disable TodoWrite assumptions |
| Thrum daemon       | Add messaging and session lifecycle guidance        |
| Multiple worktrees | Add scope rules and branch ownership boundaries     |

## Output contract

- Updated files in `.thrum/role_templates/`
- Short summary of generated or changed roles
- Deploy recommendation and command results if deployment was requested
