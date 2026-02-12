# Preamble File Template

## Purpose

Create a preamble that defines the agent's **role and project conventions**. The
preamble persists in the worktree across features — it must NOT contain
feature-specific details like owned packages, design docs, or task references.
Those belong in the implementation prompt.

See `CLAUDE.md` for how preambles fit into the three-layer context model
(prompt / preamble / context).

## Key Principle: Feature Independence

The preamble must remain valid when the worktree is reused for a different
feature. Ask yourself: "If this agent switches from implementing auth to
implementing sync, would this preamble still make sense?" If not, the content
belongs in the implementation prompt instead.

## Inputs Required

- `{{PROJECT_NAME}}` — Project name (e.g., `Thrum`, `MyApp`)
- `{{AGENT_ROLE}}` — Agent role (e.g., `implementer`, `reviewer`)
- `{{TECH_STACK}}` — Languages, frameworks, tools (e.g., `Go, TypeScript, SQLite`)
- `{{PROJECT_CONVENTIONS}}` — Coding patterns, error handling, testing approach
- `{{GENERAL_QUALITY_COMMANDS}}` — Project-wide test/lint commands (e.g., `go test ./... -count=1 -race`)
- `{{COMMUNICATION_PROTOCOL}}` — When/how to use thrum messaging

## Output

Save to `docs/preambles/{{AGENT_ROLE}}-preamble.md`. This file is passed to
worktree setup:

```bash
./scripts/setup-worktree-thrum.sh <path> <branch> \
  --identity {{AGENT_NAME}} \
  --preamble docs/preambles/{{AGENT_ROLE}}-preamble.md
```

The setup script appends the custom content after the default thrum
quick-reference (which is always included automatically).

Note: preambles are per-role, not per-feature. A single `implementer-preamble.md`
can be reused across all implementer worktrees in the project.

---

## Template

Keep preambles under 80 lines. Nothing feature-specific.

```markdown
## Role

You are a {{AGENT_ROLE}} agent for the {{PROJECT_NAME}} project.

**Tech stack:** {{TECH_STACK}}

## Workflow

Follow the beads task workflow:
1. Check `bd ready` or `bd show <epic>` for available work
2. Claim: `bd update <id> --status=in_progress`
3. Read task details: `bd show <id>` — the description is the source of truth
4. Implement, test, commit
5. Close: `bd close <id>`
6. Repeat with the next unblocked task

## Project Conventions

{{PROJECT_CONVENTIONS}}

## Quality Gates

Run after every task:

\`\`\`bash
{{GENERAL_QUALITY_COMMANDS}}
\`\`\`

## Communication

{{COMMUNICATION_PROTOCOL}}
```

---

## Example: Implementer Preamble

```markdown
## Role

You are an implementer agent for the MyProject project.

**Tech stack:** Go (backend), TypeScript/React (UI), SQLite, JSONL

## Workflow

Follow the beads task workflow:
1. Check `bd ready` or `bd show <epic>` for available work
2. Claim: `bd update <id> --status=in_progress`
3. Read task details: `bd show <id>` — the description is the source of truth
4. Implement, test, commit
5. Close: `bd close <id>`
6. Repeat with the next unblocked task

## Project Conventions

- Error handling: wrap with `fmt.Errorf("context: %w", err)`, always propagate
- Testing: use `t.TempDir()` for test databases, `t.Cleanup()` for resources
- Naming: Go standard (camelCase unexported, PascalCase exported)
- Commits: descriptive messages, one logical change per commit

## Quality Gates

Run after every task:

\`\`\`bash
go test ./... -count=1 -race && go vet ./...
\`\`\`

## Communication

- Send status on significant milestones: `thrum send "Completed <task>" --to @coordinator`
- Escalate blockers: `thrum send "Blocked on <task> by <reason>" --to @coordinator`
- Update intent when switching tasks: `thrum agent set-intent "Working on <task>"`
- Check inbox periodically: `thrum inbox --unread`
```

---

## Checklist

When creating preamble files:

- [ ] One preamble per role (not per feature)
- [ ] Saved to `docs/preambles/{{AGENT_ROLE}}-preamble.md`
- [ ] Under 80 lines
- [ ] Contains ZERO feature-specific content (no epic IDs, no owned packages,
      no design doc references, no feature-specific architectural constraints)
- [ ] Would still be valid if the worktree switched to a different feature
- [ ] Includes project-wide conventions and quality gates
- [ ] Includes communication protocol
