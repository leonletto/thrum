# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with
code in this repository.

## Project Overview

Thrum is a Git-backed messaging system for AI agent coordination. It enables
agents and humans to communicate persistently across sessions, worktrees, and
machines. The system consists of a Go CLI + embedded daemon with a React SPA web
UI served over WebSocket.

**Module:** `github.com/leonletto/thrum` **Go version:** 1.26 **Version:**
v0.10.6

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

## Multi-Binary Worktree Footgun

Each worktree under `~/.thrum/worktrees/thrum/` builds its own `bin/thrum`, and
each build can support a different DB schema range. The shared daemon runs
`~/.local/bin/thrum` (installed via `make install` from whichever worktree last
ran it). When a `make install` from a worktree on a branch with a NEWER schema
(e.g. `thrum-agents` with v32 substrate work) migrates the on-disk DB up, a
later `make install` from a worktree on an OLDER schema branch (e.g.
`release/v0.10.5` at v24) ships a binary that refuses to start:

```
database schema is version 32, this binary supports up to 24 — cannot downgrade.

Recovery options:
  1. Re-install a newer binary that supports schema v32 or above:
       cd <worktree-with-newer-branch> && make install
  2. Delete the database to start fresh (LOSES local message history + spool):
       thrum daemon stop   # release file locks first
       rm /Users/<you>/dev/opensource/thrum/.thrum/var/state.db
       rm /Users/<you>/dev/opensource/thrum/.thrum/var/state.db-wal /Users/<you>/dev/opensource/thrum/.thrum/var/state.db-shm
  3. See CLAUDE.md § "Multi-binary worktree footgun" for prevention
```

**Why it happens:** The DB lives under `<repo-root>/.thrum/var/state.db` and is
shared across every worktree's binary (worktrees redirect their `.thrum/` to the
main repo's `.thrum/` via the `.thrum/redirect` file). Schema migrations are
one-way: a newer binary migrates the DB up; an older binary cannot migrate it
back down.

**How to avoid:**

- Only `make install` from a worktree on a branch whose schema `CurrentVersion`
  is `>=` what's currently on disk. The highest-schema branch you actively use
  is the safe install source.
- If you maintain release-line work (`release/v0.10.x`) alongside substrate work
  (`thrum-agents`), DO NOT cross-install: install from `thrum-agents` is one-way
  unless you're prepared to wipe the DB.
- For local dev iteration inside a single worktree, prefer `make dev`
  (per-worktree `./bin/thrum` + `./bin/thrum daemon restart`) over
  `make install` — `make dev` does not touch the shared binary.

**How to recover (the error message walks you through this):**

1. If another worktree on this machine has a binary supporting the on-disk
   schema version, `cd` there and `make install`. The shared
   `~/.local/bin/thrum` gets replaced with one that can open the DB.
2. If no such worktree is available (or you don't care about local history),
   stop the daemon, delete the DB + WAL/SHM sidecars, and restart. The daemon
   will initialize a fresh DB on next start.
3. Long-term prevention belongs in a `make install` pre-flight check
   (`thrum-quth` follow-ups) — for now the daemon's startup error is the primary
   detection point.

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

## Release Test Triage Pattern

The release-test harness (`tests/release/run.sh`, 108 scenarios) is slow: a full
gate run is ~20-30 min. When it fails, triaging scenario-by-scenario via
full-gate re-runs is unaffordable. The pattern below takes a gate with 60
failures down to a handful of real residuals in a few iterations, by isolating
clusters of similar failures and fixing root causes once.

It was validated in v0.10.6 RC1: a first full-gate run (160/220 pass, 113 min)
was reduced to 222/258 pass in 22.6 min after four small-subset iterations.

### The loop

1. **Run the full gate once** to get a fail list with bucket counts. Group the
   failures by assertion-name (last token after the final `/`):

   ```bash
   LOG=$(ls -t /tmp/reltest-*.log | head -1)
   grep -oE "/ [a-z][a-z0-9-]+$" "$LOG" | sort | uniq -c | sort -rn | head
   ```

   The biggest bucket usually has ONE root cause shared across many scenarios —
   fix it once, broadcast across the cluster.

2. **Pick the smallest representative subset** (1-3 scenarios from the biggest
   bucket) and run them in isolation:

   ```bash
   bash tests/release/run-subset.sh -g <group-name>      # named group
   bash tests/release/run-subset.sh 14 31 89             # specific IDs
   bash tests/release/run-subset.sh -l                   # list groups
   ```

   `tests/release/run-subset.sh` sources the same scenario files as `run.sh`
   (zero drift; any fix proven here applies directly to the gate). Each run
   takes ~1-5 min vs the full gate's 20+. Failure groups are catalogued in the
   script and tunable as triage progresses.

3. **When the failure surface hides the real error**, reproduce the exact
   suppressed command standalone with stderr visible. Most scenarios suppress
   stderr (`>/dev/null 2>&1`) so `(failed)` is all that emits. Find the failing
   line, build the minimal fixture, run it bare:

   ```bash
   # Example: subfixture-thrum-init was failing across 34 scenarios.
   # The harness ran `tmux-exec exec --clean -- thrum init --runtime claude`
   # and suppressed stderr. Direct repro:
   env -u TMUX -u TMUX_PANE scripts/tmux-exec exec \
     --cwd /tmp/fresh-fixture --clean --timeout 15 -- thrum init --runtime claude
   # Real error surfaced: "Agent name [...]:" — the v0.9.3 wizard hanging.
   # Root cause: missing --non-interactive. Broadcast across 17 scenarios.
   ```

   `env -u TMUX -u TMUX_PANE` mirrors what the self-isolating launcher does —
   required to reproduce harness-internal behavior from a regular agent shell.
   Short `--timeout` (5-15s) makes the diagnostic fail fast instead of burning
   the 120s/30s default.

4. **Broadcast the fix across the cluster** with `perl -i -pe` (portable on
   macOS BSD sed which mangles `-i.bak`):

   ```bash
   find tests/release/scenarios -maxdepth 1 -name '*.test.sh' \
     -not -name '10[123456]-init-wizard-*' \
     -exec perl -i -pe 's/(thrum init)( --runtime claude)/\1 --non-interactive\2/g' {} \;
   ```

   Then re-run the same subset to confirm the broadcast holds.

5. **Re-run the full gate** when several clusters are fixed. Cascade victims
   (failures that only happened because shared state was corrupted by an earlier
   failure) auto-resolve. Real residuals appear distinctly. Repeat from step 1.

### Observability prerequisites (already in place)

The pattern depends on three artifacts the harness produces:

- **`tests/release/run-subset.sh`** — the subset runner. Sources scenarios
  unmodified so the same code paths run as in the gate. Self-isolates via the
  same launcher.
- **Tee'd launcher log at `/tmp/reltest-<pid>.log`** — full harness
  stdout/stderr persisted to disk. `helpers/self-isolate.sh` builds a wrapper
  that pipes the harness through `tee`, with `${PIPESTATUS[0]}` preserving the
  harness exit code. Without this, output dies with the detached pane.
- **Per-fail pane snapshots at `/tmp/thrum-release-failures/reltest-<pid>/`** —
  `helpers/output.sh:emit_fail` captures the coord + impl panes at the moment of
  failure (`tmux capture-pane -p -S -200 -J`). For sub- fixture scenarios, the
  coord pane shows what the driver was doing; pair with direct-command repro
  (step 3) for the sub-fixture's own error.

### Pre-execute health probes (defense in depth)

Some shared infrastructure can be left in a stuck state by a prior aborted run.
The release harness's `tmux-exec` pool pane (`tmux-exec-pool-${USER}`) is the
canonical example — a stuck pool makes every fresh `tmux-exec exec` time out.
`helpers/setup-repo.sh`'s preflight sends a marker echo into the pool and waits
for it on a standalone line (the typed-input echo doesn't match — anchor with
`^[[:space:]]*${marker}[[:space:]]*$`). If the probe doesn't return within 5s,
kill the server; lazy recreate on the next call.

### Fast default timeouts

`scripts/tmux-exec --timeout` default is **30s**. Anything that legitimately
needs longer (e.g. `thrum tmux restart`) sets `--timeout 60` or higher
explicitly per-call. 30s as the default catches hangs in seconds instead of
letting them burn 2 minutes each — the difference between a 60-failure gate
finishing in 22 min vs 113.

### When to NOT use this pattern

- **A single scenario fails** intermittently across runs — small-subset
  isolation will hide the contention. Run the full gate (or multiple) to
  characterize.
- **A scenario's first-time-ever-green moment** — when something has never run
  end-to-end before, you need to walk it forward with empirical probes (matches
  the v0.10.6 RC1 scenario-29 prototyping). Small-subset is the loop _after_ the
  harness fundamentally works.

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
- **`release/vX.Y.Z`** — Created from `thrum-dev` at the start of every
  release's pre-release cycle. Carries `*-rc.N` tags, accepts bugfix commits
  during soak, gets merged to `main` at promotion (tagged `vX.Y.Z` on the merge
  commit) and back to `thrum-dev`. **Kept indefinitely post-promotion** —
  substrate for future hotfixes against the vX.Y.x line. See
  [`dev-docs/PRE-RELEASE-STEPS.md`](dev-docs/PRE-RELEASE-STEPS.md).

### Branch push policy

| Branch                | Push to origin            | Why                                                           |
| --------------------- | ------------------------- | ------------------------------------------------------------- |
| `thrum-dev`           | Every session end         | Authoritative pre-release truth; protects work                |
| `feature/*` / `fix/*` | NEVER auto-push           | Local-only by design; reach origin via merge into `thrum-dev` |
| `website-dev`         | Only when ready to deploy | Push triggers website deployment workflow                     |
| `release/vX.Y.Z`      | At rc.N tag time          | Triggers GoReleaser to publish prerelease artifacts           |
| `main`                | Only via release flow     | See `dev-docs/RELEASE-STEPS.md`                               |

Long-running implementer worktrees (e.g. `team-fix`) often switch what they're
working on across sessions and accumulate experimental commits. Auto-pushing
those would publish work that may never land. They reach origin via the same
merge-into-thrum-dev path as any other feature branch.

### Release workflow

The release flow is split across two checklists, run in order:

1. **[`dev-docs/PRE-RELEASE-STEPS.md`](dev-docs/PRE-RELEASE-STEPS.md)** —
   coordinator's RC soak cycle: cut `release/vX.Y.Z`, version bumps, CHANGELOG,
   doc audit, sync-docs, full local CI gates (`make ci`, race tests, resilience,
   E2E, UI, optional manual test plan), tag rc.N, the 48h soak (with risk-graded
   re-soak when bugs surface), and the promotion-ready checklist.
2. **[`dev-docs/RELEASE-STEPS.md`](dev-docs/RELEASE-STEPS.md)** — promotion-only
   checklist after soak passes: verify soak (step 0), merge `release/vX.Y.Z` to
   main, tag the merge commit, push, monitor GoReleaser, update release notes,
   push website, post-release verify, close beads, merge release branch back to
   `thrum-dev`.

**Critical rules from those checklists:**

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
