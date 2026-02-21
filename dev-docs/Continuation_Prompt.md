# Thrum Project Continuation Prompt

## Agent Working Style

**Use auggie-mcp (Code Retrieval)**

Use `auggie-mcp codebase-retrieval` for exploring existing code. It's
context-efficient — prefer it over reading files manually when you need to
understand how existing code works, find patterns, or explore unfamiliar
packages.

**Use sub-agents (Task tool) for all research and exploration.** Delegate
codebase exploration, file reading, and pattern analysis to sub-agents so the
main context stays clean for decision-making and coordination.

- **Research/exploration**: Use `Task` tool with `subagent_type=Explore` or
  `subagent_type=general-purpose`
- **Planning**: Use `Task` tool with `subagent_type=Plan`
- **Code review**: Use `Task` tool with
  `subagent_type=feature-dev:code-reviewer` or
  `subagent_type=superpowers:code-reviewer`
- **Direct reads**: Only for short config files, issue tracking, or when you
  already know the exact 10-line snippet needed.

**You are the coordinator, not the implementer.** Use the template process in
`dev-docs/templates/` to plan features and create implementation prompts. Agents
are dispatched to worktrees with filled-in prompts from `dev-docs/prompts/`.

## On Session Start (MANDATORY)

**Always do these three things immediately when starting a new session:**

1. **Check Thrum** — Register, check inbox, and see who's online:

```bash
thrum quickstart --name claude_planner --role planner --module coordination --intent "Session N: <description>"
thrum inbox --unread
thrum agent list --context
```

**Verify registration succeeded** — you must see your agent name in the output
of `thrum status`. If it fails, check that the daemon is running with
`thrum daemon status`.

**Use the message listener** — Spawn a background listener to get async
notifications. Re-arm it every time it returns (both MESSAGES_RECEIVED and
NO_MESSAGES_TIMEOUT).

## Quick Resume

```bash
# Check project status (beads — local only, not in git)
bd ready
bd stats

# Check git state
git status
git --no-pager log --oneline -10

# Build, test, install (full build with UI)
make install

# Run tests
go test ./... -race

# Run E2E tests (daemon must be running)
thrum daemon start
npx playwright test --workers=1
```

## Local-Only Development Tooling

After the public release squash, the following are **gitignored and local
only**. They need to be re-initialized on a fresh clone or new machine:

| Tool                | Setup                                                       | Purpose                                                        |
| ------------------- | ----------------------------------------------------------- | -------------------------------------------------------------- |
| **Beads**           | `bd init` then `bd import < output/beads-full-export.jsonl` | Issue tracker (102 open issues)                                |
| **Thrum**           | `thrum init && thrum daemon start`                          | Agent messaging coordination                                   |
| **CLAUDE.md**       | Restore from backup or recreate                             | Agent instructions                                             |
| **AGENTS.md**       | Restore from backup or recreate                             | Beads onboarding for agents                                    |
| `.claude/agents/`   | Restore from backup                                         | Agent definitions (thrum-agent, message-listener, beads-agent) |
| `.claude/commands/` | Restore from backup                                         | Custom skills (update-context)                                 |

**Beads config**: `.beads/config.yaml` has `no-push: true` — beads works locally
without pushing the sync branch to the public remote.

**Gitignored paths**: `dev-docs/`, `.beads/`, `.claude/`, `CLAUDE.md`,
`AGENTS.md`, `.agents/`, `.ref/`, `output/`

**Backups** (in gitignored directories):

- `output/beads-full-export.jsonl` — Full export of all issues
- `dev-docs/open-issues-backlog.md` — Human-readable open issues with full
  descriptions

## Worktree Setup (as needed)

Recreate worktrees as needed for feature work:

```bash
# Create a feature worktree
git worktree add ~/.workspaces/thrum/<name> -b feature/<name>

# Set up thrum + beads redirects (share daemon and issue tracker with main repo)
scripts/setup-worktree-thrum.sh ~/.workspaces/thrum/<name>
scripts/setup-worktree-beads.sh ~/.workspaces/thrum/<name>

# Or run without args to auto-detect and set up all worktrees:
scripts/setup-worktree-thrum.sh
scripts/setup-worktree-beads.sh
```

## Development Workflow (Templates)

Feature work follows a three-phase template process:

```
1. PLAN (dev-docs/templates/planning-agent.md)
   → Brainstorm → design spec → beads epics + tasks

2. PREPARE (dev-docs/templates/worktree-setup.md)
   → Create/select worktree → beads redirect → verify

3. IMPLEMENT (dev-docs/templates/implementation-agent.md)
   → Fill in template → save to dev-docs/prompts/ → dispatch agent to worktree
```

**Active prompts** in `dev-docs/prompts/`:

- None currently (routing-fix phases 1 and 2 implemented and merged)

---

## Session Context

**Date**: 2026-02-20 (Session 41) **Project**: Thrum — Git-backed messaging for
AI agent coordination **Repository**: https://github.com/leonletto/thrum
**Visibility**: Public **Main Branch**: `main` — HEAD at 2105828 **Go Version**:
1.25.7 **Install**: `make install` (builds UI + Go binary → ~/.local/bin)
**CI**: GitHub Actions green (format, vet, golangci-lint v2, test -race,
govulncheck non-fatal, markdownlint, gosec) **Latest Release**: v0.4.4
(published), v0.4.5 ready to tag **Version String**: `v0.4.4` **Install
Script**:
`curl -fsSL https://raw.githubusercontent.com/leonletto/thrum/main/scripts/install.sh | sh`

---

## Project Status Overview

### What is Thrum?

Thrum is an offline-first, Git-backed messaging system for multi-agent
coordination in code repositories. It enables AI agents (and humans) to
communicate persistently across sessions, worktrees, and machines using Git as
the sync layer.

### Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                         Thrum                                │
├─────────────────────────────────────────────────────────────┤
│  CLI (thrum)     │  Daemon (single port)  │  Web UI (React) │
│  - send/inbox    │  - Unix socket (RPC)   │  - Embedded SPA  │
│  - agent/session │  - WebSocket at /ws    │  - Live feed     │
│  - message ops   │  - SPA at / (embedded) │  - Inbox view    │
│  - reply         │  - Sync loop (60s)     │  - Agent list    │
│  - coordination  │  - Heartbeat/context   │  - Data Terminal │
│  - user.identify │  - Auto-registration   │  - AuthProvider  │
│  - agent delete  │  - Agent cleanup       │  - Delete action │
│  - agent cleanup │  - Orphan detection    │                   │
│  - context cmds  │  - Context management  │                   │
│  - groups        │  - Agent groups        │                   │
│  - runtime       │  - Runtime presets     │                   │
│  - config        │  - Config show/manage  │                   │
│  - mcp serve     │                        │                   │
│  - team          │                        │                   │
├─────────────────────────────────────────────────────────────┤
│  MCP Server (thrum mcp serve)                               │
│    - stdio transport (JSON-RPC over stdin/stdout)            │
│    - Tools: send_message, check_messages, wait_for_message   │
│             list_agents, create_group, add_group_member      │
│             remove_group_member, list_groups, get_group      │
│    - WebSocket waiter for real-time push notifications       │
│    - Per-call daemon RPC client (thread-safe)                │
│    - Identity from .thrum/identities/{name}.json             │
├─────────────────────────────────────────────────────────────┤
│  Storage: Sharded JSONL → SQLite (projection)               │
│    - .git/thrum-sync/a-sync/events.jsonl (agent lifecycle)   │
│    - .git/thrum-sync/a-sync/messages/{agent}.jsonl (msgs)    │
│    - .thrum/identities/{name}.json (per-worktree identity)   │
│    - .thrum/context/{agent}.json (per-agent context files)   │
│    - .thrum/var/messages.db (SQLite projection cache)        │
│    - .thrum/config.json (runtime, daemon, sync settings)     │
│  Sync: Git a-sync branch worktree at .git/thrum-sync/a-sync │
│    - Sparse checkout (events.jsonl + messages/ only)         │
│    - Orphan branch via git plumbing (safe, no checkout)      │
│    - .thrum/ fully gitignored on main                        │
│  Path Resolution: .thrum/redirect for multi-worktree         │
│  Dedup: ULID event_id (globally unique, lexicographic sort)  │
│  Embed: //go:embed React SPA into Go binary (single port)   │
└─────────────────────────────────────────────────────────────┘
```

### Key Stats

| Category         |                                              Count |
| ---------------- | -------------------------------------------------: |
| Go Source Files  |                                     115 (non-test) |
| Go Test Files    |                                     117 test files |
| Go Test Packages |                  32 total (all passing with -race) |
| UI Tests         |          332 passing (web-app) + 87 (shared-logic) |
| E2E Tests        | 70 scenarios, 55 passing, 0 failing, 15 skip/fixme |

### Deployment Status

- Binary: `~/.local/bin/thrum`
- Daemon: Running
- WebSocket: `ws://localhost:9999/ws`
- UI: `http://localhost:9999` (embedded SPA via `make install`)
- Website: https://leonletto.github.io/thrum (GitHub Pages, auto-deploy)
- Identity: `.thrum/identities/{name}.json` — worktree-aware auto-selection
- Sync: 60s default interval (configurable via `--sync-interval`)
- MCP Server: `thrum mcp serve` (stdio transport, group tools, WebSocket waiter)
- CI: GitHub Actions green (`ci.yml`)
- Release: v0.4.4 published via GoReleaser (`release.yml`), 4 platform
  archives + install script, Apple codesigned

### CLI Commands (Full Suite)

```
thrum init              Initialize Thrum in a repository
thrum setup             Configure feature worktrees with .thrum/redirect
thrum migrate           Migrate old-layout repos to worktree architecture
thrum daemon            Manage the Thrum daemon (start/stop/status)
thrum quickstart        Register + start session + set intent in one step
                        Supports --name for human-readable agent names
                        Supports --runtime to detect and configure AI runtime
thrum overview          Combined status, team, inbox, and sync view
thrum team              Team roster with per-agent inbox counts

# Identity & Sessions
thrum agent             Manage agent identity (register, list, start/end aliases)
thrum agent delete      Delete an agent (removes identity, messages, SQLite record)
thrum agent cleanup     Detect and remove orphaned agents (--force, --dry-run)
thrum session           Manage sessions (start, end, list, heartbeat, set-intent/set-task)
thrum status            Show current agent status with work context

# Agent Context
thrum context save      Save agent work context (--message, auto-detect CLI vs MCP)
thrum context show      Display current work context for an agent
thrum context clear     Clear context for an agent or all agents (--all)
thrum context prime     Generate LLM preamble from git history and beads context

# Messaging
thrum send              Send a message (--to @name, --group @groupname)
thrum inbox             List messages (auto-filters to you, --all for everything)
thrum reply             Reply to a message (creates reply-to reference)
thrum message           Manage messages (get, edit, delete, read)

# Agent Groups
thrum group             Manage agent groups (create, add, remove, list, delete)
                        Groups replace broadcast for targeted multi-agent comms
                        Auto role groups created on registration

# Coordination
thrum who-has           Check which agents are editing a file
thrum ping              Check if an agent is online (active/offline + intent)

# Notifications
thrum subscribe         Subscribe to notifications
thrum subscriptions     List active subscriptions
thrum unsubscribe       Unsubscribe from notifications
thrum wait              Wait for notifications (for hooks)
                        Supports --after (time filter); --all removed

# Sync
thrum sync              Control sync operations

# Runtime Presets
thrum runtime           Manage runtime presets (list, show, edit)
                        Built-in support for Claude Code, Augment, Cursor, Windsurf

# Config Management
thrum config            Manage configuration (show, interactive init)
                        Single source of truth: .thrum/config.json
                        Runtime tiers, daemon settings, sync intervals

# MCP Server
thrum mcp serve         Start MCP stdio server for native agent messaging
                        --agent-id NAME to override identity
```

### Agent Naming System

Agents support human-readable names:

- `thrum quickstart --name furiosa --role implementer --module auth`
- Names: `[a-z0-9_]+` (lowercase alphanumeric + underscores)
- Reserved: `daemon`, `system`, `thrum`, `all`, `broadcast`
- Env vars: `THRUM_NAME` (highest priority), `THRUM_ROLE`, `THRUM_MODULE`
- Multi-agent per worktree: each agent gets own identity file
- Backward compat: legacy `agent:{role}:{hash}` IDs still work
- **Agent name cannot equal role** (prevents addressing ambiguity)
- **Auto role groups**: registering with `--role implementer` auto-creates an
  "implementer" group

### Routing System (post-v0.4.5)

Key design decisions from the routing fix (merged fda4c22):

- **Name-only routing** — routing is by agent name + groups (no role-based
  routing)
- **Recipient validation is a hard error** — message not stored if recipient
  unknown
- **`--all` removed from `thrum wait`** — was a no-op footgun; use groups
  instead
- **Auto role groups** — created on agent registration, visible and manageable
- **Reply audience** — includes original sender + uses correct group scope
  format
- **Priority removed entirely** — never stored in DB/events; -p/--priority flag
  and all Priority fields deleted

---

## What's Done (Completed Epics)

All completed epics are in the squashed initial commit. Key completed work:

| Area                     | Epics                                        | Summary                                                                                                                                           |
| ------------------------ | -------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Core**                 | 1-9                                          | Daemon, CLI, WebSocket bridge, sync protocol                                                                                                      |
| **UI**                   | 10-14, 21-24                                 | React SPA, AppShell, LiveFeed, InboxView, Data Terminal, Agent Work Context, Who-Has                                                              |
| **CLI**                  | A-E                                          | Quickstart, message lifecycle, threads, output polish, coordination                                                                               |
| **Embed**                | F                                            | React SPA embedded into Go binary (single port)                                                                                                   |
| **E2E Tests**            | E2E, E2E-S/S2, E2E-N/N2                      | 70 scenarios, Playwright, sharding + naming test suites                                                                                           |
| **Refactoring**          | JSONL Sharding, Agent Naming                 | Per-agent JSONL, ULID event_id, human-readable names                                                                                              |
| **MCP**                  | MCP Server                                   | stdio transport, 5 tools, WebSocket waiter                                                                                                        |
| **Sync**                 | Sync Worktree, Relocation                    | Git plumbing, sparse checkout, path redirect                                                                                                      |
| **Security**             | G104, Perms, Misc                            | 120 gosec findings fixed across 3 epics                                                                                                           |
| **Infra**                | CI/CD, Release Pipeline, Daemon Hardening    | GitHub Actions, GoReleaser, Homebrew cask, flock/PID                                                                                             |
| **Docs**                 | Documentation Audit, Website                 | 16 docs, GitHub Pages site, llms.txt                                                                                                              |
| **Identity**             | Identity Resolution, Wait Flags              | Auto-select, fail-fast, wait --all/--after                                                                                                        |
| **Context**              | Agent Context Management                     | Per-agent context storage with CLI detection                                                                                                      |
| **Groups**               | Agent Groups (simplified)                    | Flat groups, reply-to convention, inbox clustering                                                                                                |
| **Runtime**              | Runtime Preset Registry                      | Enhanced quickstart, template engine, context prime                                                                                               |
| **v0.3.2**               | Team Command, Per-File Tracking              | thrum team, thrum version, thrum whoami, file_changes context                                                                                     |
| **Simplification**       | Groups + Tailscale                           | Removed threads (3 commands, 486-line handler), flattened groups, deleted security layer (7 files, 1,074 lines), added pairing codes + token auth |
| **Config Consolidation** | thrum-6atk                                   | Expanded config.json schema, interactive runtime selection, daemon reads config, thrum config show, documentation                                 |
| **Bug Epic**             | thrum-iisp                                   | 6 bugs fixed and closed (thrum-pwaa, thrum-16lv, thrum-pgoc, thrum-5611, thrum-en2c, thrum-8ws1)                                                  |
| **Daemon Resilience**    | thrum-fycc (10/10), thrum-chjm (in progress) | safedb, safecmd, timeouts, lock scope, resilience test suite                                                                                      |
| **Routing Fix**          | thrum-fsvi (14/14)                           | Name-only routing, hard recipient validation, --all removal, auto role groups, reply audience fix                                                 |
| **Identity Consistency** | thrum-i0ax (12/12)                           | IdentityFile v3, AgentSummary canonical output, init full setup, quickstart enrichment, daemon whoami branch/intent                               |

---

## What's Remaining (Open Issues)

Full backlog: Run `bd ready` for current list.

### Open Epics

| Epic                      | ID         | Priority | Description                                   |
| ------------------------- | ---------- | -------- | --------------------------------------------- |
| v0.4.5 Release            | thrum-z3sl | P1       | All blockers closed — ready to tag and publish |
| Listener Identity Failure | thrum-4ski | P1       | Remove vestigial subscriptions & fix cleanup  |
| 19. Launch Marketing      | thrum-hqm  | P2       | Blog, video, README (blocked by docs/release) |
| Resilience Testing        | thrum-tvq4 | P3       | Remaining design doc coverage (13 children)   |

### Key Remaining Tasks

| ID         | Priority | Title                                                   |
| ---------- | -------- | ------------------------------------------------------- |
| thrum-z3sl | P1       | v0.4.5 Release epic — version bumps, tag, publish       |
| thrum-4ski | P1       | Listener identity failure epic                          |
| thrum-620c | P2       | Cleanup subscriptions on session end                    |
| thrum-efjv | P2       | Pass caller_agent_id in subscription RPCs               |
| thrum-tw2d | P3       | Deduplicate GetRepoName/GetWorktreeName git calls       |
| thrum-f8b8 | P3       | Wire FormatAgentSummaryCompact into team and agent list |
| thrum-vvce | P3       | Add empty-module test case for AutoDisplay              |
| thrum-u4zy | P3       | Review Source field omitempty on AgentSummary           |
| thrum-n542 | P3       | Add v1 identity round-trip write test                   |

### v0.4.5 Readiness

**Epic**: thrum-z3sl (P1) — All blockers closed, CI green, release ready

**Completed** (all closed):

- thrum-mddo: Priority removed entirely (fields, flag, docs, all references)
- thrum-0tf1: `thrum wait` redesigned — server-side `created_after` filter, DESC
  sort, seen-message tracking
- thrum-ncpx: MCP integration tests fixed (identity setup, group handlers,
  @everyone)
- thrum-yn23: Nested groups test plan F5 already correct (closed, no changes
  needed)
- thrum-wbdl: `thrum prime` shows context restore directive
- thrum-htwu: Docs update — 57 files updated across website docs, plugin,
  templates, toolkit
- UTC timezone bug fixed in cleanup/contexts.go and wait.go
- Pre-compact hook filename bug fixed (thrum-g8q2)
- Full release test plan: 59/59 PASS (Parts A-L, tmux tests with Haiku)
- CI fully green: format, golangci-lint, markdownlint, go vet, tests, gosec
- govulncheck non-fatal (Go 1.25 + x/tools SSA upstream crash)
- gosec exclusions added: G103, G115, G204, G304, G306, G404 (all accepted)
- .markdownlint.json added, ~150 pre-existing lint errors fixed across 30+ files

**Remaining work** (next session):

- Version bumps in 7 files (Makefile, plugin.json, marketplace.json, SKILL.md,
  llms.txt, llms-full.txt, README.md)
- Tag v0.4.5 and push to trigger GoReleaser
- Fast-forward worktrees (context, team-fix) to main HEAD
- Update website-dev from main after tag

**Reference**: See `dev-docs/plans/2026-02-19-routing-fix-design.md` and
`dev-docs/plans/2026-02-19-routing-fix-plan.md` for routing design context.

### Active Worktrees

| Worktree    | Branch               | Path                               | Status                            |
| ----------- | -------------------- | ---------------------------------- | --------------------------------- |
| main        | `main`               | `/Users/leon/dev/opensource/thrum` | HEAD 2105828                      |
| context     | `feature/context`    | `~/.workspaces/thrum/context`      | 74e0a96 (needs fast-forward)      |
| local-only  | `feature/local-only` | `~/.workspaces/thrum/local-only`   | d7bd6e5 (behind main)             |
| team-fix    | `feature/team-fix`   | `~/.workspaces/thrum/team-fix`     | 74e0a96 (needs fast-forward)      |
| website-dev | `website-dev`        | `~/.workspaces/thrum/website-dev`  | a5dd735 (needs update after tag)  |

---

## Recent Work (Last 2 Sessions)

### Session 41 (2026-02-20): Identity Consistency, P0 Bugfixes, Release Testing & CI Cleanup

**What was done:**

1. **Fixed P0 bugs** — thrum-ncpx (MCP integration tests) and thrum-yn23
   (nested groups test plan). Fixed identity setup in integration tests:
   non-empty agent names, Name field in registration, group handlers + @everyone
   in test daemons, recipient addresses use agent names.

2. **Added thrum prime context show directive** (thrum-wbdl) — `thrum prime`
   now shows "Run `thrum context show`" when saved context files exist in
   `.thrum/context/`.

3. **Committed v0.4.5 accumulated changes** — wait fix, priority removal,
   routing updates, doc updates (previously uncommitted from sessions 39-40).

4. **Moved docs/plans/ to dev-docs/plans/** — Internal planning docs were
   tracked in git; removed from tracking and added `docs/plans/` to
   `.gitignore`.

5. **Implemented Init, Identity & Discovery Consistency** (thrum-i0ax, 12
   tasks) — IdentityFile v3 with branch/intent/session_id, AgentSummary
   canonical output struct, `thrum init` full setup
   (prompt/daemon/register/session/intent), quickstart enrichment, set-intent
   writeback, FormatWhoami removal, all discovery commands use AgentSummary.

6. **Code review and fixes** — v3 agent register, Y/n prompt semantics,
   `cli.DaemonStart` usage, daemon whoami returns branch/intent from work
   context.

7. **Created and completed 5 P3 cleanup tasks** from code review (thrum-tw2d,
   thrum-vvce, thrum-f8b8, thrum-u4zy, thrum-n542).

8. **Full release test plan** — 59/59 PASS (Parts A-L automated, G-I tmux with
   Haiku, plugin tested on leondev).

9. **Fixed pre-compact hook filename bug** (thrum-g8q2) — script parsed wrong
   JSON field name (`name` vs `agent_id`).

10. **Docs update for v0.4.5** (thrum-htwu) — 57 files updated across website
    docs, plugin, templates, toolkit.

11. **Fixed all pre-existing markdown lint errors** (~150 across 30+ files).
    Added `.markdownlint.json` config.

12. **Fixed gosec exclusions in Makefile**. Made govulncheck non-fatal (Go 1.25
    toolchain incompatibility with x/tools SSA).

13. **CI fully green**: format, golangci-lint, markdownlint, go vet, tests,
    gosec all pass. govulncheck has upstream crash but is non-fatal.

**Key commits**: 2105828 (lint fix), 74e0a96 (docs v0.4.5), 0ca65be
(pre-compact fix), bf1babe (daemon whoami branch/intent), d7420b5 (code review
fixes), fcbaa55..85c9610 (identity consistency), 2906373 (MCP test fix),
c318a5f (prime context show)

**State at end of session**: All tests pass, CI green. v0.4.5 release epic has
all blockers closed. Ready for version bumps, tagging, and publishing.

---

### Session 40 (2026-02-20): v0.4.5 Release Testing & Bugfix

**What was done:**

1. **Full release test plan (Parts A-L)** — Ran against littleCADev test repo
   and worktrees. All tmux tests G1-G8, H1-H4 passed. Added E6-E9 (negative
   tests), J8-J12 (regression tests), Part K (MCP routing parity), Part O
   (remote VM testing on leondev). Fixed all paths to use littleCADev repo,
   agent names hyphens→underscores, removed nested group test (F5).

2. **Fixed critical `thrum wait` bug (thrum-0tf1)** — Wait never received
   messages: unread+page_size:1+ASC ordering stuck on old messages, plus inbox
   auto-mark-as-read making messages invisible. Redesigned to use server-side
   `created_after` filter, DESC sort order, seen-message tracking.

3. **Removed message priority entirely (thrum-mddo)** — Removed -p/--priority
   flag, Priority fields on all structs, isValidPriority, priority_filter, all
   docs/plugin/llms references. Priority was never stored in DB/events — removal
   was cleaner than implementing.

4. **Fixed UTC timezone bug** — Timestamps formatted without .UTC() failed
   SQLite string comparison against UTC-stored values. Fixed in
   cleanup/contexts.go and wait.go.

5. **Installed Homebrew on leondev VM** — Verified `brew install/uninstall` of
   thrum works on remote VM. Part O added to test plan for remote VM testing.

6. **Filed 4 P0 bugs under v0.4.5 epic** — thrum-mddo (closed), thrum-0tf1
   (closed), thrum-ncpx (fixed in session 41), thrum-yn23 (closed in session 41).

**State at end of session**: All unit tests pass, resilience tests pass.
Integration tests still failing (fixed in session 41).

---
