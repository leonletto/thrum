# Preamble File Template

## Purpose

Guide the planning agent to create a preamble file for each implementation
agent / worktree. The preamble is prepended to the agent's context on every
`thrum context show` call, providing persistent project-specific instructions
that survive context loss.

See `CLAUDE.md` for how preambles fit into the three-layer context model
(prompt / preamble / context).

## Inputs Required

- `{{AGENT_NAME}}` — Unique agent name (e.g., `impl-auth`, `impl-sync`)
- `{{AGENT_ROLE}}` — Agent role (e.g., `implementer`, `reviewer`)
- `{{AGENT_MODULE}}` — Module area (e.g., `auth`, `sync-daemon`)
- `{{WORKTREE_BRANCH}}` — Git branch (e.g., `feature/auth`)
- `{{DESIGN_DOC}}` — Path to the design spec for this work
- `{{OWNED_PACKAGES}}` — Packages/directories this agent is responsible for
- `{{QUALITY_COMMANDS}}` — Test/lint commands to run
- `{{ARCHITECTURAL_CONSTRAINTS}}` — Key design decisions and constraints
- `{{COORDINATION_NOTES}}` — How this agent interacts with other agents/epics

## Output

Save to `dev-docs/prompts/{{AGENT_NAME}}-preamble.md`. This file is passed to
worktree setup:

```bash
./scripts/setup-worktree-thrum.sh <path> <branch> \
  --identity {{AGENT_NAME}} \
  --preamble dev-docs/prompts/{{AGENT_NAME}}-preamble.md
```

The setup script appends the custom content after the default thrum
quick-reference (which is always included automatically).

---

## Template

Keep preambles under 150 lines. Reference files by path rather than inlining
content.

```markdown
## Agent Identity

**Name:** {{AGENT_NAME}}
**Role:** {{AGENT_ROLE}}
**Module:** {{AGENT_MODULE}}
**Branch:** {{WORKTREE_BRANCH}}
**Design Doc:** {{DESIGN_DOC}}

## Ownership Scope

This agent owns the following packages and files:

- {{OWNED_PACKAGES}}

Do not modify files outside this scope without coordinating via Thrum. If
another agent's files need changes, send a message:
`thrum send "Need change in <file>: <reason>" --to @coordinator`

## Architectural Constraints

{{ARCHITECTURAL_CONSTRAINTS}}

## Coding Patterns

<!-- Include project-specific patterns this agent must follow.
     Reference files by path rather than inlining code. -->

## Quality Gates

Run after every task:

\`\`\`bash
{{QUALITY_COMMANDS}}
\`\`\`

## Coordination

{{COORDINATION_NOTES}}
```

---

## Example: Auth Implementation Agent

```markdown
## Agent Identity

**Name:** impl-auth
**Role:** implementer
**Module:** auth
**Branch:** feature/auth
**Design Doc:** dev-docs/plans/2026-02-12-auth-design.md

## Ownership Scope

This agent owns the following packages and files:

- `internal/auth/` — Authentication logic, token management
- `internal/middleware/auth.go` — HTTP auth middleware
- `internal/rpc/auth_*.go` — Auth-related RPC handlers
- `tests/auth_*_test.go` — Auth tests

Do not modify files outside this scope without coordinating via Thrum. If
another agent's files need changes, send a message:
`thrum send "Need change in <file>: <reason>" --to @coordinator`

## Architectural Constraints

- Auth tokens are JWTs signed with Ed25519 keys
- Token storage uses the existing SQLite projection (not a separate database)
- All auth middleware must be composable via standard `http.Handler` wrapping
- Never store plaintext credentials — use bcrypt for passwords, encrypted
  storage for tokens
- Follow the existing `internal/daemon/` patterns for service initialization

## Coding Patterns

- Error handling: wrap with `fmt.Errorf("context: %w", err)`, always propagate
- Testing: use `t.TempDir()` for test databases, `t.Cleanup()` for resources
- See `internal/daemon/server.go` for service lifecycle patterns
- See `internal/rpc/agent.go` for RPC handler patterns

## Quality Gates

Run after every task:

\`\`\`bash
go test ./internal/auth/... ./internal/middleware/... -count=1 -race && \
go vet ./internal/auth/... ./internal/middleware/...
\`\`\`

## Coordination

- **Parallel work:** The sync agent (`impl-sync`) is working on
  `feature/sync-v2` simultaneously. The shared file `internal/daemon/server.go`
  requires coordination — message @coordinator before modifying it.
- **Dependency:** Epic 2 (sync) depends on the auth token format from this
  epic's task 3. Notify @coordinator when task 3 is complete.
```

---

## Checklist for Planning Agents

When creating preamble files during the planning phase:

- [ ] One preamble per implementation agent / worktree
- [ ] Saved to `dev-docs/prompts/{{AGENT_NAME}}-preamble.md`
- [ ] Under 150 lines
- [ ] Includes ownership scope (which packages/files this agent owns)
- [ ] Includes architectural constraints from the design spec
- [ ] Includes quality gate commands
- [ ] Includes coordination notes if parallel agents exist
- [ ] Does NOT duplicate the implementation prompt content
- [ ] References files by path, not inline code
