# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with
code in this repository.

## Project Overview

Thrum is a Git-backed messaging system for AI agent coordination. It enables
agents and humans to communicate persistently across sessions, worktrees, and
machines. The system consists of a Go CLI + embedded daemon with a React SPA web
UI served over WebSocket.

**Module:** `github.com/leonletto/thrum` **Go version:** 1.26 **Version:**
v0.10.1

## On Session Start

In Claude Code (and any runtime that loads the thrum plugin), the SessionStart
hook auto-injects the full `thrum prime` briefing — identity, project state,
inbox, and any restart snapshot — into your context. You do NOT need to run
`/thrum:prime` or `thrum prime` manually under normal conditions.

Only run the manual fallback when:

- The hook reported a degraded "Auto-injection failed" notice (daemon
  unreachable at session start), OR
- You see no briefing in your initial context (e.g., the runtime doesn't load
  thrum's SessionStart hook).

```bash
thrum prime         # only if auto-injection didn't fire
thrum context show  # always safe — read-only state inspection
```

## Git Rules

- **NEVER use `git add -f` or `--force`** to add files that are gitignored. If a
  file is in `.gitignore`, it stays out of git. No exceptions.
- When staging files, always use specific file names. If `git add` warns about
  ignored files, **do not override** — the file belongs outside git.
- `dev-docs/` is gitignored and synced separately to the thrumdev repo via
  `sync-dev-docs.sh`. Never commit files under `dev-docs/`.

## Commit Message Convention

Use [Conventional Commits](https://www.conventionalcommits.org/) prefixes.
GoReleaser uses these to generate release changelogs — commits with `docs:`,
`test:`, `chore:`, `ci:`, and `bd:` prefixes are excluded from release notes.

| Prefix      | When to use                                 | Example                                            |
| ----------- | ------------------------------------------- | -------------------------------------------------- |
| `feat:`     | New feature or capability                   | `feat: add backup plugin management`               |
| `fix:`      | Bug fix                                     | `fix: inbox filter matches user-prefixed refs`     |
| `refactor:` | Code restructuring, no behavior change      | `refactor: remove unused InlineReply component`    |
| `test:`     | Adding or fixing tests only                 | `test: add auto-threading test cases`              |
| `docs:`     | Documentation-only changes                  | `docs: update CLI reference for wait command`      |
| `chore:`    | Maintenance, deps, tooling, config          | `chore: update golangci-lint to v1.64.5`           |
| `ci:`       | CI/CD pipeline changes                      | `ci: add gosec to GitHub Actions workflow`         |
| `style:`    | Formatting, whitespace, linting fixes       | `style: apply gofmt -s to struct alignment`        |
| `perf:`     | Performance improvement                     | `perf: batch SQLite inserts in projection rebuild` |
| `bd:`       | Beads backup (auto-generated, never manual) | `bd: backup 2026-03-02 21:01`                      |

**Scopes** are optional but helpful: `feat(backup):`, `fix(e2e):`, `fix(ui):`,
`docs(website):`. Use the package or area name as scope.

**Breaking changes**: Add `!` after the prefix: `feat!: rename sync command`.

## Common Commands

```bash
# Build everything (UI + Go binary)
make install

# Build Go binary only
make build

# Build UI only
make build-ui

# Run all Go tests (with race detector)
make test

# Run a single Go test
go test ./internal/daemon/... -run TestFunctionName -v -race

# Run UI tests
cd ui && pnpm test -- -- --run

# Build UI
cd ui && pnpm build

# Lint Go code
make lint

# Format Go code
make fmt

# Fast pre-commit check
make quick-check

# Full CI check locally
make ci

# Sync skills/commands from claude-plugin to all runtime plugins
scripts/sync-skills.sh

# Build Open Code plugin
cd opencode-plugin && npm run build

# Restart daemon (preserves WebSocket port)
thrum daemon restart

# Check daemon status
thrum daemon status
```

## Browser Automation

Always use `playwright-cli` (the CLI skill) instead of Playwright MCP tools
(`mcp__playwright__*` / `mcp__plugin_playwright_playwright__*`). The MCP tools
send screenshots as base64 images which consume excessive tokens.
`playwright-cli` returns lightweight text snapshots and saves screenshots to
files instead.

```bash
playwright-cli open http://localhost:8080
playwright-cli snapshot                    # text-based page state
playwright-cli screenshot --filename=x.png # saves to file, not inline
playwright-cli click e3
playwright-cli close
```

## Sub-Agent Strategy

Delegate research, independent tasks, and verification to sub-agents. Your main
context should focus on task coordination, dependency management, and
cross-cutting decisions.

### Principles

1. **Parallelize independent tasks** — When multiple unblocked tasks touch
   different files/packages, implement them simultaneously via sub-agents
2. **Delegate research** — Spawn sub-agents to explore unfamiliar code before
   implementing, rather than reading it into your context
3. **Verify in background** — Run tests and lint via background sub-agents while
   you continue with the next task
4. **Focused prompts** — Give each sub-agent the full task description, worktree
   path, quality commands, and expected deliverables

### Agent Selection

| Task                        | Agent Type                  | Model  | Background? |
| --------------------------- | --------------------------- | ------ | ----------- |
| Implement a complex task    | `general-purpose`           | sonnet | no\*        |
| Implement a simple task     | `general-purpose`           | haiku  | no\*        |
| Explore unfamiliar code     | `Explore`                   | sonnet | yes         |
| Run tests / lint            | `general-purpose`           | haiku  | yes         |
| Review implementation       | `feature-dev:code-reviewer` | sonnet | no          |
| Doc updates / config tweaks | `general-purpose`           | haiku  | yes         |

\*Use foreground when you need the result before proceeding. Use background when
you can continue other work while they run.

## Role-Skills Layer

Role-discipline ships as a layered system across three locations:

- **Preambles** at
  `internal/context/roleconfig/templates/roles/<role>-{strict,autonomous}.md` —
  always-loaded role invariants (coordinator/implementer/researcher × strict/
  autonomous variants). Rendered into `.thrum/role_templates/<role>.md` per
  project by the `configure-roles` skill at agent registration time.
- **Skills** at `claude-plugin/skills/<role>-<skill>/SKILL.md` —
  description-triggered, situational deepening (3 coordinator + 4 implementer
  - 3 researcher = 10 skills). Synced to other runtime plugins via
    `scripts/sync-skills.sh`.
- **Project-local rules** via `bd remember --key <role>-rule-<slug>` — captured
  in-session, persist across restarts. Each preamble loads them with
  `bd memories <role>-rule-` and project-local rules take precedence over
  universal rules on conflict. Module-installed rules reserve the
  `<role>-rule-mod-<module>-<slug>` sub-segment.

When the user gives feedback like "stop doing X" mid-session, the right response
is: fix the behavior, capture via `bd remember --key <role>-rule-<slug>`,
acknowledge.

## Explore Existing Code

**ALWAYS:** Use `auggie-mcp codebase-retrieval` as much as possible to research
existing code which is faster and reduces token usage.

## Testing

### Go Tests

- Unit tests alongside source files (`*_test.go`) in each package
- Integration tests in `tests/integration/`
- Resilience tests in `tests/resilience/`
- Run with race detector: `go test -race ./...`
- 32 test packages, target >80% coverage

### UI Tests

- Vitest + React Testing Library
- 39 test files, 473+ tests across 2 packages (web-app, shared-logic)
- Run: `cd ui && pnpm test -- -- --run`
- Build: `cd ui && pnpm build`

### E2E Tests

- Tmux-based sessions testing CLI + Claude Code integration
- Full test plan: `dev-docs/release-testing/full_test_plan.md`
- 70 E2E scenarios across Parts A-M

## Architecture

```
cmd/thrum/           → CLI entry point (cobra commands, ~5000 lines)
internal/
  cli/               → CLI helpers (daemon.go: Start/Stop/Restart/Status)
  daemon/            → Daemon server (WebSocket + Unix socket, RPC handlers)
  daemon/rpc/        → RPC method handlers (message, agent, session, group, user)
  gitctx/            → Git context extraction (branch, commits, changed files)
  storage/           → SQLite storage + JSONL event log
  sync/              → Git-based sync across clones
ui/
  packages/
    web-app/src/     → React SPA (Vite, Tailwind, shadcn components)
      components/    → UI components (agents, groups, inbox, settings, etc.)
      pages/         → DashboardPage (single-page app)
    shared-logic/    → Hooks, stores, API client (TanStack Query + Store)
website/             → Documentation site (Hugo)
claude-plugin/       → Claude Code plugin (skills, hooks, commands — source of truth)
opencode-plugin/     → Open Code plugin (npm: opencode-thrum)
codex-plugin/        → OpenAI Codex plugin (skills)
```

**Key patterns:**

- **Embedded UI**: Go binary embeds the built React SPA, serves via daemon
  WebSocket
- **RPC over WebSocket + Unix socket**: JSON-RPC 2.0 for all daemon
  communication
- **JSONL → SQLite**: Append-only event log with SQLite projection for queries
- **Git sync**: Events sync across clones via detached worktree on `a-sync`
  branch
- **CSS variable system**: Theme-aware colors in
  `ui/packages/web-app/src/index.css` (`:root` for light, `.dark` for dark).
  Components use `var(--accent-color)` etc. instead of hardcoded Tailwind
  classes.

## Branching Strategy

- **`main`** — Release-only branch. Only updated at release time via merge from
  `thrum-dev`. Always matches the latest published release.
- **`thrum-dev`** — Active development branch. All day-to-day work happens here.
  This is the ONLY branch pushed to origin during normal sessions. The
  coordinator agent sits on this branch.
- **`feature/*` and `fix/*`** — Branches created from `thrum-dev` for isolated
  work in worktrees. **These STAY LOCAL by default.** Feature branches are many,
  temporary, and frequently reused for new tasks; pushing them clutters the
  public repo with intermediate states that may never land. Code from a feature
  branch reaches origin only by being merged into `thrum-dev` first (which then
  gets pushed at session end).
- **`website-dev`** — Long-lived branch for documentation site work. **Pushing
  `website-dev` to origin triggers `deploy-pages.yml` and deploys the website.**
  Only push `website-dev` when website changes are intentionally ready to ship.

### Branch push policy

| Branch                | Push to origin            | Why                                                           |
| --------------------- | ------------------------- | ------------------------------------------------------------- |
| `thrum-dev`           | Every session end         | Authoritative pre-release truth; protects work                |
| `feature/*` / `fix/*` | NEVER auto-push           | Local-only by design; reach origin via merge into `thrum-dev` |
| `website-dev`         | Only when ready to deploy | Push triggers website deployment workflow                     |
| `main`                | Only via release flow     | See `dev-docs/RELEASE-STEPS.md`                               |

Long-running implementer worktrees (e.g. `team-fix`) often switch what they're
working on across sessions and accumulate experimental commits. Auto-pushing
those would publish work that may never land. They reach origin via the same
merge-into-thrum-dev path as any other feature branch.

### Release workflow

**See [`dev-docs/RELEASE-STEPS.md`](dev-docs/RELEASE-STEPS.md) for the complete
release checklist.** That file is the source of truth and covers version bumps
across all 8 files, CHANGELOG entry, doc audit, sync-docs, the full local CI
gates (`make ci`, race tests, resilience tests, E2E, UI tests, optional manual
test plan), the merge sequence, tagging, GoReleaser monitoring, release notes
formatting, post-release verification, and troubleshooting.

**Critical rules from that file:**

- NEVER push to `main` without `make ci` passing locally first
- NEVER run `goreleaser release` locally — GitHub Actions handles it
- ALWAYS do version bumps before tagging
- Tag push triggers the release — only push the tag when ready
- GitHub Actions CI is a SUBSET of `make ci` (no lint/security due to Go 1.26
  compatibility) — local `make ci` is the authoritative gate

### Session close protocol

At session end, push `thrum-dev` so the work is protected. Do NOT auto-push
feature branches — they stay local per the push policy above.

```bash
# From the main repo, on thrum-dev:
git push origin thrum-dev
```

If a feature branch is genuinely complete AND ready for archival or external
collaboration, push it explicitly — but that's a deliberate decision per branch,
not a session-close routine. `website-dev` push is also explicit and only when
ready to deploy.

## Dependencies

- Go 1.26, Node 22+, pnpm 10+
- `cobra` — CLI framework
- `gorilla/websocket` — WebSocket server
- `mattn/go-sqlite3` — SQLite (CGO)
- React 19, Vite 7, TanStack Query + Store, Tailwind CSS 4, shadcn/ui

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:ca08a54f -->

## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full
workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown
  TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

<!-- END BEADS INTEGRATION -->
