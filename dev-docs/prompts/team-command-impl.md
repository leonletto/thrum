# Implementation Agent: thrum team Command

## Inputs

- **Epic ID:** `thrum-clze` (Add thrum team command)
- **Worktree Path:** `~/.workspaces/thrum/team-fix`
- **Branch Name:** `feature/team-fix`
- **Design Doc:** `dev-docs/specs/2026-02-12-thrum-team-design.md`
- **Quality Commands:** `go build ./... && go vet ./... && go test ./internal/schema/... ./internal/projection/... ./internal/daemon/rpc/... ./internal/cli/... -count=1 -race`
- **Coverage Target:** >80% for new code
- **Agent Name:** `impl_team`

---

## MANDATORY: Register Before Any Work

**STOP. Run these commands before doing anything else.** Do not skip this
section.

```bash
cd ~/.workspaces/thrum/team-fix
thrum quickstart --name impl_team --role implementer --module team_command --intent "Implementing thrum-clze: thrum team command"
thrum inbox --unread
thrum send "Starting work on thrum-clze (thrum team command)" --to @coordinator
```

When your work is complete (Phase 4), send a completion message:

```bash
thrum send "Completed thrum-clze. All tasks done, tests passing." --to @coordinator
```

---

## Scope Overview

This feature adds `thrum team` — a rich per-agent status command powered by a
single server-side SQL JOIN. It also adds hostname tracking to agent
registration.

**Tasks (in dependency order):**

1. `thrum-yp0p` — Schema migration v11: add hostname to agents table
2. `thrum-scj8` — Implement team.list RPC handler with JOIN query
3. `thrum-is0n` — Add thrum team CLI command with rich formatting
4. `thrum-42uw` — Tests for thrum team: unit + integration

Tasks 1→2→3 are sequential (each builds on the previous). Task 4 (tests) can
partially overlap with task 3 since unit tests for FormatTeam don't require the
RPC handler.

### Key Files

**Task 1 (Schema + hostname):**
- `internal/schema/schema.go` — Bump CurrentVersion to 11, add migration
- `internal/types/events.go` — Add Hostname to AgentRegisterEvent (line 112)
- `internal/projection/projector.go` — Update applyAgentRegister INSERT (line 319)
- `internal/daemon/rpc/agent.go` — Add resolveHostname(), update HandleRegister

**Task 2 (RPC handler):**
- `internal/daemon/rpc/team.go` — NEW: TeamHandler, HandleList, SQL queries
- `cmd/thrum/main.go` — Register team.list handler (around line 3840)

**Task 3 (CLI command):**
- `internal/cli/team.go` — NEW: FormatTeam(), TeamMember struct
- `cmd/thrum/main.go` — Register teamCmd Cobra command

**Task 4 (Tests):**
- `internal/cli/team_test.go` — NEW: FormatTeam unit tests
- `internal/daemon/rpc/team_test.go` — NEW: HandleList integration tests

### Design References

Read `dev-docs/specs/2026-02-12-thrum-team-design.md` for the full design,
including SQL queries, struct definitions, and output format.

### Existing Code Patterns to Follow

- **Schema migration:** `internal/schema/schema.go` line 570 (v9→v10 pattern)
- **RPC handler:** `internal/daemon/rpc/agent.go` (NewAgentHandler, HandleList)
- **Handler registration:** `cmd/thrum/main.go` line 3840 (agentHandler pattern)
- **CLI formatting:** `internal/cli/status.go:FormatStatus()` (multi-line rich output)
- **Duration/time helpers:** `internal/cli/status.go:formatDuration()`, `overview.go:formatTimeAgo()`
- **Inbox count:** `internal/daemon/rpc/message.go` line 614 (total/unread count queries)
- **Work context query:** `internal/daemon/rpc/agent.go:HandleListContext()` (JOIN with agent_work_contexts)
- **Test patterns:** `internal/daemon/rpc/agent_test.go` (temp DB, state setup)

### Architecture Decision

The daemon's SQLite is the materialized view. All agent data is already updated
incrementally via events (register, heartbeat, messages). `thrum team` is a
read-only query — no new write paths needed.

Two SQL queries (agents+sessions+contexts, then inbox counts) are merged in Go.
This is simpler and more maintainable than one massive JOIN with inbox subquery.

---

## Phase 1: Orient

```bash
cd ~/.workspaces/thrum/team-fix
bd show thrum-clze
bd list --status=in_progress
git branch --show-current
git status
git --no-pager log --oneline -10
```

Pick up from the first incomplete task.

---

## Phase 2: Implement Tasks

Work through tasks in order: thrum-yp0p → thrum-scj8 → thrum-is0n → thrum-42uw.

For each task:
1. `bd update <id> --status=in_progress`
2. `bd show <id>` — read the full description (source of truth)
3. Implement following existing code patterns
4. Run quality gates: `go build ./... && go vet ./... && go test ./internal/schema/... ./internal/projection/... ./internal/daemon/rpc/... ./internal/cli/... -count=1 -race`
5. Commit: `git add <files> && git commit -m "feat(team): <task summary>"`
6. `bd close <id>`

### Parallelization Opportunity

After task 3 (CLI command) is committed, tasks 3 and 4 can overlap:
- FormatTeam unit tests don't require the daemon
- RPC integration tests can be written once the handler exists

However, since these are all in the same worktree, sequential is simpler and
avoids git conflicts.

---

## Phase 3: Verify Quality

```bash
cd ~/.workspaces/thrum/team-fix

# Full test suite
go test ./... -count=1 -race

# Vet
go vet ./...

# Build
go build ./...

# Verify task status
bd show thrum-clze
```

All 4 tasks must be closed. All tests must pass.

---

## Phase 4: Complete & Land

```bash
# Close epic
bd close thrum-clze --force --reason="All tasks implemented and verified"

# Merge to main
cd /Users/leon/dev/opensource/thrum
git checkout main
git merge feature/team-fix --no-edit

# Verify after merge
go test ./... -count=1 -race
go build ./...
```

Update CHANGELOG.md with a team command section under [0.3.2].

---

## Resume Quick Reference

```bash
cd ~/.workspaces/thrum/team-fix
thrum quickstart --name impl_team --role implementer --module team_command --intent "Resuming thrum-clze"
thrum inbox --unread
bd show thrum-clze
bd list --status=in_progress
git --no-pager log --oneline -10
git status
```
