# Role Config Workflow

## Goal
Create role templates that match repository reality and team operating model.

## Inputs
- Role list (for example: coordinator, implementer, planner, reviewer, tester)
- Autonomy mode per role (strict or autonomous)
- Scope boundaries (worktree-local only or cross-worktree read)

## Template generation rules
- Preserve existing templates unless explicit replacement is requested.
- Keep role directives short and behavioral.
- Include concrete command examples for routine behavior.
- Include escalation boundaries and handoff requirements.

## Suggested deployment flow
```bash
thrum roles deploy --dry-run
thrum roles deploy
thrum roles list
```
