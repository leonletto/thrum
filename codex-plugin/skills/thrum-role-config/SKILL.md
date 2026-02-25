---
name: thrum-role-config
description: Use when the user wants to create, audit, or update .thrum/role_templates by detecting repo/team context and generating role preambles with explicit autonomy and scope boundaries.
---

# Thrum Role Config

Generate or update role preambles in `.thrum/role_templates/`.

## Use this when
- Team roles need to be initialized for Thrum agents.
- Role templates exist but must be adjusted.
- Environment changes (worktrees, runtimes, skills, MCP-like tooling) require template updates.

## Workflow
1. Detect environment and current team topology.
2. Summarize findings before making changes.
3. If templates exist, diff and update only requested parts.
4. Render one template per role.
5. Optionally deploy with `thrum roles deploy --dry-run` then `thrum roles deploy`.

## Detection commands
```bash
thrum runtime list 2>/dev/null
git worktree list 2>/dev/null
bd stats 2>/dev/null
thrum agent list --context 2>/dev/null
thrum config show 2>/dev/null
ls .thrum/role_templates/ 2>/dev/null
```

## Output contract
- Template files in `.thrum/role_templates/<role>.md`
- Short summary of generated/updated roles and autonomy mode
- Deploy recommendation and command results when deploy is requested

## References
- `references/ROLE_CONFIG_WORKFLOW.md`
